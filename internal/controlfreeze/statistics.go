package controlfreeze

import (
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
)

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	items := append([]float64(nil), values...)
	sort.Float64s(items)
	middle := len(items) / 2
	if len(items)%2 == 1 {
		return items[middle]
	}
	return (items[middle-1] + items[middle]) / 2
}

func sampleVariance(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	m := mean(values)
	var sum float64
	for _, value := range values {
		delta := value - m
		sum += delta * delta
	}
	return sum / float64(len(values)-1)
}

func standardizedMeanDifference(treated, control []float64) float64 {
	if len(treated) < 2 || len(control) < 2 {
		return math.NaN()
	}
	pooled := (sampleVariance(treated) + sampleVariance(control)) / 2
	if pooled <= 0 {
		if math.Abs(mean(treated)-mean(control)) <= 1e-15 {
			return 0
		}
		return math.Inf(1)
	}
	return (mean(treated) - mean(control)) / math.Sqrt(pooled)
}

func balanceStatus(absoluteSMD float64) string {
	switch {
	case math.IsNaN(absoluteSMD) || math.IsInf(absoluteSMD, 0):
		return BalanceFailed
	case absoluteSMD <= 0.10:
		return BalanceBalanced
	case absoluteSMD <= 0.20:
		return BalanceWarning
	default:
		return BalanceFailed
	}
}

func stableSeed(base int64, parts ...string) int64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return base ^ int64(h.Sum64()&math.MaxInt64)
}

func bootstrapMeanCI(values []float64, iterations int, seed int64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	if len(values) == 1 || iterations <= 0 {
		return values[0], values[0]
	}
	rng := rand.New(rand.NewSource(seed))
	means := make([]float64, iterations)
	for iteration := 0; iteration < iterations; iteration++ {
		var sum float64
		for sample := 0; sample < len(values); sample++ {
			sum += values[rng.Intn(len(values))]
		}
		means[iteration] = sum / float64(len(values))
	}
	sort.Float64s(means)
	lowerIndex := int(math.Floor(0.025 * float64(iterations-1)))
	upperIndex := int(math.Ceil(0.975 * float64(iterations-1)))
	if lowerIndex < 0 {
		lowerIndex = 0
	}
	if upperIndex >= len(means) {
		upperIndex = len(means) - 1
	}
	return means[lowerIndex], means[upperIndex]
}

func pairedPermutationPValue(values []float64, iterations int, seed int64) float64 {
	filtered := make([]float64, 0, len(values))
	for _, value := range values {
		if math.Abs(value) > 1e-15 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			filtered = append(filtered, value)
		}
	}
	if len(filtered) == 0 {
		return 1
	}
	if iterations <= 0 {
		iterations = 1
	}
	observed := math.Abs(mean(filtered))
	rng := rand.New(rand.NewSource(seed))
	extreme := 0
	for iteration := 0; iteration < iterations; iteration++ {
		var sum float64
		for _, value := range filtered {
			if rng.Intn(2) == 0 {
				sum -= value
			} else {
				sum += value
			}
		}
		if math.Abs(sum/float64(len(filtered)))+1e-15 >= observed {
			extreme++
		}
	}
	return float64(extreme+1) / float64(iterations+1)
}

type rankedAbs struct {
	absolute float64
	sign     float64
}

func wilcoxonSignedRankPValue(values []float64) float64 {
	items := make([]rankedAbs, 0, len(values))
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) <= 1e-15 {
			continue
		}
		sign := 1.0
		if value < 0 {
			sign = -1
		}
		items = append(items, rankedAbs{absolute: math.Abs(value), sign: sign})
	}
	if len(items) == 0 {
		return 1
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].absolute < items[j].absolute })
	var positiveRank float64
	var tieCorrection float64
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && math.Abs(items[end].absolute-items[start].absolute) <= 1e-12*math.Max(1, items[start].absolute) {
			end++
		}
		averageRank := (float64(start+1) + float64(end)) / 2
		for index := start; index < end; index++ {
			if items[index].sign > 0 {
				positiveRank += averageRank
			}
		}
		tieSize := float64(end - start)
		if tieSize > 1 {
			tieCorrection += tieSize*tieSize*tieSize - tieSize
		}
		start = end
	}
	n := float64(len(items))
	meanRank := n * (n + 1) / 4
	variance := n*(n+1)*(2*n+1)/24 - tieCorrection/48
	if variance <= 0 {
		return 1
	}
	difference := positiveRank - meanRank
	if difference > 0 {
		difference -= 0.5
	} else if difference < 0 {
		difference += 0.5
	}
	z := math.Abs(difference) / math.Sqrt(variance)
	p := math.Erfc(z / math.Sqrt2)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func applyHolm(rows []Inference) {
	type groupKey struct {
		ControlType string
		AnalysisSet string
		Outcome     string
		Stratum     string
	}
	groups := make(map[groupKey][]int)
	for index, row := range rows {
		key := groupKey{row.ControlType, row.AnalysisSet, row.Outcome, row.Stratum}
		groups[key] = append(groups[key], index)
	}
	for _, indexes := range groups {
		apply := func(raw func(int) float64, set func(int, float64)) {
			ordered := append([]int(nil), indexes...)
			sort.SliceStable(ordered, func(i, j int) bool {
				left := raw(ordered[i])
				right := raw(ordered[j])
				if left == right {
					return rows[ordered[i]].HorizonBlocks < rows[ordered[j]].HorizonBlocks
				}
				return left < right
			})
			previous := 0.0
			m := len(ordered)
			for rank, index := range ordered {
				adjusted := float64(m-rank) * raw(index)
				if adjusted < previous {
					adjusted = previous
				}
				if adjusted > 1 {
					adjusted = 1
				}
				previous = adjusted
				set(index, adjusted)
			}
		}
		apply(
			func(index int) float64 { return rows[index].WilcoxonPValue },
			func(index int, value float64) { rows[index].WilcoxonHolmPValue = value },
		)
		apply(
			func(index int) float64 { return rows[index].PermutationPValue },
			func(index int, value float64) { rows[index].PermutationHolmPValue = value },
		)
	}
}
