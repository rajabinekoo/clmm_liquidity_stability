package services

import (
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
)

func controlBalanceStatus(absoluteSMD float64) string {
	switch {
	case math.IsNaN(absoluteSMD) || math.IsInf(absoluteSMD, 0):
		return ControlBalanceFailed
	case absoluteSMD <= 0.10:
		return ControlBalanceBalanced
	case absoluteSMD <= 0.20:
		return ControlBalanceWarning
	default:
		return ControlBalanceFailed
	}
}

func controlStableSeed(base int64, parts ...string) int64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return base ^ int64(h.Sum64()&math.MaxInt64)
}

func controlMedian(values []float64) float64 {
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

func controlBootstrapMeanCI(values []float64, iterations int, seed int64) (float64, float64) {
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

func controlPairedPermutationPValue(values []float64, iterations int, seed int64) float64 {
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

type controlRankedAbs struct {
	absolute float64
	sign     float64
}

func controlWilcoxonSignedRankPValue(values []float64) float64 {
	items := make([]controlRankedAbs, 0, len(values))
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) <= 1e-15 {
			continue
		}
		sign := 1.0
		if value < 0 {
			sign = -1
		}
		items = append(items, controlRankedAbs{absolute: math.Abs(value), sign: sign})
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
	continuity := 0.5
	difference := positiveRank - meanRank
	if difference > 0 {
		difference -= continuity
	} else if difference < 0 {
		difference += continuity
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

func applyControlHolm(rows []RobustControlInference) {
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

func buildRobustInferenceRow(
	controlType string,
	analysisSet string,
	outcome string,
	stratum string,
	horizonLabel string,
	horizonBlocks uint64,
	values []float64,
	config ControlRobustnessV2Config,
) RobustControlInference {
	row := RobustControlInference{
		ControlType:   controlType,
		AnalysisSet:   analysisSet,
		Outcome:       outcome,
		Stratum:       stratum,
		HorizonLabel:  horizonLabel,
		HorizonBlocks: horizonBlocks,
		Pairs:         len(values),
	}
	if len(values) == 0 {
		row.WilcoxonPValue = 1
		row.WilcoxonHolmPValue = 1
		row.PermutationPValue = 1
		row.PermutationHolmPValue = 1
		return row
	}
	row.MeanDifferenceBps = mean(values)
	row.MedianDifferenceBps = controlMedian(values)
	for _, value := range values {
		if value > 0 {
			row.PositivePairs++
		}
	}
	row.PositiveShare = float64(row.PositivePairs) / float64(len(values))
	seed := controlStableSeed(config.RandomSeed, controlType, analysisSet, outcome, stratum, horizonLabel)
	row.BootstrapMeanCILower, row.BootstrapMeanCIUpper = controlBootstrapMeanCI(values, config.BootstrapIterations, seed)
	row.WilcoxonPValue = controlWilcoxonSignedRankPValue(values)
	row.PermutationPValue = controlPairedPermutationPValue(values, config.PermutationIterations, seed^0x5f3759df)
	return row
}
