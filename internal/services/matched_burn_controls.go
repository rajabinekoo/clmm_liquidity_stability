package services

import (
	"fmt"
	"math"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type MatchedBurnControlRequest struct {
	Collection     BurnSampleCollectionReport
	Realized       BurnRealizedDatasetReport
	Counterfactual BurnCounterfactualFutureReport
	Horizons       []BurnOutcomeHorizon
}

type MatchedBurnPair struct {
	PairID        string
	Stratum       BurnRangeLocation
	High          BurnEventSample
	Low           BurnEventSample
	MatchDistance float64
	BlockDistance uint64
}

type MatchedBurnOutcome struct {
	PairID                             string
	Stratum                            BurnRangeLocation
	HorizonLabel                       string
	HorizonBlocks                      uint64
	HighBurn                           domain.BurnCandidate
	LowBurn                            domain.BurnCandidate
	HighImmediateLSISBps               decimal.Decimal
	LowImmediateLSISBps                decimal.Decimal
	ImmediateLSISDifferenceBps         decimal.Decimal
	HighRealizedDeteriorationBps       decimal.Decimal
	LowRealizedDeteriorationBps        decimal.Decimal
	RealizedDeteriorationDifferenceBps decimal.Decimal
	HighMechanicalMinEffectBps         decimal.Decimal
	HighMechanicalMaxEffectBps         decimal.Decimal
	LowMechanicalMinEffectBps          decimal.Decimal
	LowMechanicalMaxEffectBps          decimal.Decimal
	MechanicalMidpointDifferenceBps    decimal.Decimal
	MechanicalBothAvailable            bool
}

type MatchedBurnBalance struct {
	Stratum              string
	Metric               string
	HighCountBefore      int
	LowCountBefore       int
	PairsAfter           int
	HighMeanBefore       float64
	LowMeanBefore        float64
	SMDBefore            float64
	HighMeanAfter        float64
	LowMeanAfter         float64
	SMDAfter             float64
	AbsoluteSMDReduction float64
}

type MatchedBurnHorizonSummary struct {
	Stratum                                  string
	HorizonLabel                             string
	HorizonBlocks                            uint64
	Pairs                                    int
	MeanHighImmediateLSISBps                 decimal.Decimal
	MeanLowImmediateLSISBps                  decimal.Decimal
	MeanImmediateLSISDifferenceBps           decimal.Decimal
	MeanHighRealizedDeteriorationBps         decimal.Decimal
	MeanLowRealizedDeteriorationBps          decimal.Decimal
	MeanRealizedDeteriorationDifferenceBps   decimal.Decimal
	MedianRealizedDeteriorationDifferenceBps decimal.Decimal
	PositiveRealizedDifferencePairs          int
	PositiveRealizedDifferenceShare          decimal.Decimal
	MechanicalAvailablePairs                 int
	MeanMechanicalMidpointDifferenceBps      decimal.Decimal
}

type MatchedBurnControlReport struct {
	PoolAddress string
	FromBlock   uint64
	ToBlock     uint64
	Pairs       []MatchedBurnPair
	Outcomes    []MatchedBurnOutcome
	Balance     []MatchedBurnBalance
	Summaries   []MatchedBurnHorizonSummary
}

type matchedBurnFeature struct {
	BlockNumber               float64
	CurrentTick               float64
	LogActiveLiquidity        float64
	LogLiquidityRemoved       float64
	RemovalFraction           float64
	ActiveRemovalShare        float64
	LogRangeWidth             float64
	NormalizedDistanceOutside float64
}

type matchedBurnScales struct {
	BlockNumber               float64
	CurrentTick               float64
	LogActiveLiquidity        float64
	LogLiquidityRemoved       float64
	RemovalFraction           float64
	ActiveRemovalShare        float64
	LogRangeWidth             float64
	NormalizedDistanceOutside float64
}

func BuildMatchedBurnControls(req MatchedBurnControlRequest) (MatchedBurnControlReport, error) {
	if err := validateBurnRealizedDatasetReport(req.Realized); err != nil {
		return MatchedBurnControlReport{}, fmt.Errorf("build matched burn controls: invalid realized dataset: %w", err)
	}
	if len(req.Collection.Samples) == 0 {
		return MatchedBurnControlReport{}, fmt.Errorf("build matched burn controls: samples are empty")
	}
	if normalizeAddress(req.Collection.PoolAddress) != normalizeAddress(req.Realized.PoolAddress) {
		return MatchedBurnControlReport{}, fmt.Errorf(
			"build matched burn controls: collection pool %s does not match realized pool %s",
			req.Collection.PoolAddress,
			req.Realized.PoolAddress,
		)
	}

	horizons, err := normalizeBurnDatasetHorizons(req.Horizons)
	if err != nil {
		return MatchedBurnControlReport{}, err
	}
	pairs, beforeGroups, err := buildMatchedBurnPairs(req.Collection.Samples)
	if err != nil {
		return MatchedBurnControlReport{}, err
	}

	actualByKey := make(map[string]BurnRegressionObservation, len(req.Realized.Observations))
	for _, observation := range req.Realized.Observations {
		key := burnCounterfactualObservationKey(observation.Burn.EventKey(), observation.HorizonLabel)
		if _, exists := actualByKey[key]; exists {
			return MatchedBurnControlReport{}, fmt.Errorf("build matched burn controls: duplicate realized observation %s", key)
		}
		actualByKey[key] = observation
	}
	counterfactualByKey := make(map[string]BurnCounterfactualObservation, len(req.Counterfactual.Observations))
	for _, observation := range req.Counterfactual.Observations {
		key := burnCounterfactualObservationKey(observation.Burn.EventKey(), observation.HorizonLabel)
		if _, exists := counterfactualByKey[key]; exists {
			return MatchedBurnControlReport{}, fmt.Errorf("build matched burn controls: duplicate counterfactual observation %s", key)
		}
		counterfactualByKey[key] = observation
	}

	outcomes := make([]MatchedBurnOutcome, 0, len(pairs)*len(horizons))
	for _, pair := range pairs {
		for _, horizon := range horizons {
			highKey := burnCounterfactualObservationKey(pair.High.Burn.EventKey(), horizon.Label)
			lowKey := burnCounterfactualObservationKey(pair.Low.Burn.EventKey(), horizon.Label)
			highActual, highOK := actualByKey[highKey]
			lowActual, lowOK := actualByKey[lowKey]
			if !highOK || !lowOK {
				return MatchedBurnControlReport{}, fmt.Errorf(
					"build matched burn controls: missing realized horizon %s for pair %s",
					horizon.Label,
					pair.PairID,
				)
			}

			row := MatchedBurnOutcome{
				PairID:                       pair.PairID,
				Stratum:                      pair.Stratum,
				HorizonLabel:                 horizon.Label,
				HorizonBlocks:                horizon.Blocks,
				HighBurn:                     cloneBurnCandidate(pair.High.Burn),
				LowBurn:                      cloneBurnCandidate(pair.Low.Burn),
				HighImmediateLSISBps:         pair.High.TotalLSISBps,
				LowImmediateLSISBps:          pair.Low.TotalLSISBps,
				ImmediateLSISDifferenceBps:   pair.High.TotalLSISBps.Sub(pair.Low.TotalLSISBps),
				HighRealizedDeteriorationBps: highActual.TotalRealizedDeteriorationBps,
				LowRealizedDeteriorationBps:  lowActual.TotalRealizedDeteriorationBps,
				RealizedDeteriorationDifferenceBps: highActual.TotalRealizedDeteriorationBps.Sub(
					lowActual.TotalRealizedDeteriorationBps,
				),
			}

			highCounterfactual, highCFOK := counterfactualByKey[highKey]
			lowCounterfactual, lowCFOK := counterfactualByKey[lowKey]
			if highCFOK && lowCFOK &&
				burnCounterfactualObservationAvailable(highCounterfactual) &&
				burnCounterfactualObservationAvailable(lowCounterfactual) {
				row.MechanicalBothAvailable = true
				row.HighMechanicalMinEffectBps = highCounterfactual.MinTotalMechanicalEffectPIAUCBps
				row.HighMechanicalMaxEffectBps = highCounterfactual.MaxTotalMechanicalEffectPIAUCBps
				row.LowMechanicalMinEffectBps = lowCounterfactual.MinTotalMechanicalEffectPIAUCBps
				row.LowMechanicalMaxEffectBps = lowCounterfactual.MaxTotalMechanicalEffectPIAUCBps
				highMidpoint := row.HighMechanicalMinEffectBps.Add(row.HighMechanicalMaxEffectBps).Div(decimal.NewFromInt(2))
				lowMidpoint := row.LowMechanicalMinEffectBps.Add(row.LowMechanicalMaxEffectBps).Div(decimal.NewFromInt(2))
				row.MechanicalMidpointDifferenceBps = highMidpoint.Sub(lowMidpoint)
			}
			outcomes = append(outcomes, row)
		}
	}

	balance := buildMatchedBurnBalance(beforeGroups, pairs)
	summaries := buildMatchedBurnSummaries(outcomes)
	return MatchedBurnControlReport{
		PoolAddress: req.Realized.PoolAddress,
		FromBlock:   req.Realized.FromBlock,
		ToBlock:     req.Realized.ToBlock,
		Pairs:       pairs,
		Outcomes:    outcomes,
		Balance:     balance,
		Summaries:   summaries,
	}, nil
}

func burnCounterfactualObservationAvailable(observation BurnCounterfactualObservation) bool {
	return observation.ExactInputTieBreak.Available || observation.ExactOutputTieBreak.Available
}

type matchedBurnBeforeGroup struct {
	Stratum BurnRangeLocation
	High    []BurnEventSample
	Low     []BurnEventSample
}

// buildMatchedBurnPairs creates covariate-nearest, non-overlapping pairs inside
// exact range-location strata. Pair formation does not use LSIS. Only after a
// pair is fixed is the member with the larger LSIS labelled "high". This keeps
// the design on common support instead of forcing extreme high-LSIS burns to be
// matched against structurally incomparable bottom-tail burns.
func buildMatchedBurnPairs(samples []BurnEventSample) ([]MatchedBurnPair, []matchedBurnBeforeGroup, error) {
	groups := make(map[BurnRangeLocation][]BurnEventSample)
	for _, sample := range samples {
		groups[sample.RangeLocation] = append(groups[sample.RangeLocation], sample)
	}

	strata := []BurnRangeLocation{
		BurnRangeActive,
		BurnRangeBelowCurrentTick,
		BurnRangeAboveCurrentTick,
	}
	pairs := make([]MatchedBurnPair, 0, len(samples)/2)
	before := make([]matchedBurnBeforeGroup, 0, len(strata))
	for _, stratum := range strata {
		items := append([]BurnEventSample(nil), groups[stratum]...)
		if len(items) < 2 {
			continue
		}
		before = append(before, matchedBurnMedianGroups(stratum, items))
		stratumPairs, err := matchBurnStratum(stratum, items)
		if err != nil {
			return nil, nil, err
		}
		pairs = append(pairs, stratumPairs...)
	}

	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].Stratum == pairs[j].Stratum {
			return pairs[i].High.Burn.Cursor.Before(pairs[j].High.Burn.Cursor)
		}
		return pairs[i].Stratum < pairs[j].Stratum
	})
	for index := range pairs {
		pairs[index].PairID = fmt.Sprintf("burn_match_%03d", index+1)
	}
	if len(pairs) == 0 {
		return nil, nil, fmt.Errorf("build matched burn controls: no covariate-matched pairs")
	}
	return pairs, before, nil
}

func matchedBurnMedianGroups(stratum BurnRangeLocation, samples []BurnEventSample) matchedBurnBeforeGroup {
	ordered := append([]BurnEventSample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TotalLSISBps.Equal(ordered[j].TotalLSISBps) {
			return ordered[i].Burn.Cursor.Before(ordered[j].Burn.Cursor)
		}
		return ordered[i].TotalLSISBps.LessThan(ordered[j].TotalLSISBps)
	})

	half := len(ordered) / 2
	low := append([]BurnEventSample(nil), ordered[:half]...)
	highStart := len(ordered) - half
	high := append([]BurnEventSample(nil), ordered[highStart:]...)
	return matchedBurnBeforeGroup{Stratum: stratum, High: high, Low: low}
}

type matchedBurnEdge struct {
	Distance float64
	Left     int
	Right    int
	LeftKey  string
	RightKey string
}

func matchBurnStratum(stratum BurnRangeLocation, samples []BurnEventSample) ([]MatchedBurnPair, error) {
	if len(samples) < 2 {
		return nil, fmt.Errorf("match burn stratum %s: fewer than two samples", stratum)
	}

	features := make([]matchedBurnFeature, len(samples))
	for index, sample := range samples {
		features[index] = matchedBurnFeatureFromSample(sample)
	}
	scales := matchedBurnFeatureScales(features)
	edges := make([]matchedBurnEdge, 0, len(samples)*(len(samples)-1)/2)
	for left := 0; left < len(samples); left++ {
		for right := left + 1; right < len(samples); right++ {
			// Equal-LSIS pairs contain no treatment contrast and are deliberately
			// left out of the matched outcome analysis.
			if samples[left].TotalLSISBps.Equal(samples[right].TotalLSISBps) {
				continue
			}
			leftKey := samples[left].Burn.EventKey()
			rightKey := samples[right].Burn.EventKey()
			if rightKey < leftKey {
				leftKey, rightKey = rightKey, leftKey
			}
			edges = append(edges, matchedBurnEdge{
				Distance: matchedBurnDistance(features[left], features[right], scales),
				Left:     left,
				Right:    right,
				LeftKey:  leftKey,
				RightKey: rightKey,
			})
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("match burn stratum %s: no unequal-LSIS edges", stratum)
	}

	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].Distance != edges[j].Distance {
			return edges[i].Distance < edges[j].Distance
		}
		if edges[i].LeftKey != edges[j].LeftKey {
			return edges[i].LeftKey < edges[j].LeftKey
		}
		return edges[i].RightKey < edges[j].RightKey
	})

	used := make([]bool, len(samples))
	result := make([]MatchedBurnPair, 0, len(samples)/2)
	for _, edge := range edges {
		if used[edge.Left] || used[edge.Right] {
			continue
		}
		used[edge.Left] = true
		used[edge.Right] = true

		high := samples[edge.Left]
		low := samples[edge.Right]
		if high.TotalLSISBps.LessThan(low.TotalLSISBps) ||
			(high.TotalLSISBps.Equal(low.TotalLSISBps) && high.Burn.Cursor.Before(low.Burn.Cursor)) {
			high, low = low, high
		}
		result = append(result, MatchedBurnPair{
			Stratum:       stratum,
			High:          cloneBurnEventSampleForMatch(high),
			Low:           cloneBurnEventSampleForMatch(low),
			MatchDistance: edge.Distance,
			BlockDistance: absUint64Diff(high.Burn.Cursor.BlockNumber, low.Burn.Cursor.BlockNumber),
		})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("match burn stratum %s: no pairs selected", stratum)
	}
	return result, nil
}

func cloneBurnEventSampleForMatch(value BurnEventSample) BurnEventSample {
	value.Burn = cloneBurnCandidate(value.Burn)
	value.runtime = nil
	value.SwapReplays = nil
	value.SqrtPriceX96BeforeBurn = cloneRegressionBigInt(value.SqrtPriceX96BeforeBurn)
	value.LiquidityRemoved = cloneRegressionBigInt(value.LiquidityRemoved)
	value.RangeLiquidityBeforeBurn = cloneRegressionBigInt(value.RangeLiquidityBeforeBurn)
	value.RangeLiquidityAfterBurn = cloneRegressionBigInt(value.RangeLiquidityAfterBurn)
	value.ActiveLiquidityBeforeBurn = cloneRegressionBigInt(value.ActiveLiquidityBeforeBurn)
	value.ActiveLiquidityAfterBurn = cloneRegressionBigInt(value.ActiveLiquidityAfterBurn)
	value.ActiveLiquidityRemoved = cloneRegressionBigInt(value.ActiveLiquidityRemoved)
	return value
}

func matchedBurnFeatureFromSample(sample BurnEventSample) matchedBurnFeature {
	return matchedBurnFeature{
		BlockNumber:               float64(sample.Burn.Cursor.BlockNumber),
		CurrentTick:               float64(sample.CurrentTick),
		LogActiveLiquidity:        logBigInt(sample.ActiveLiquidityBeforeBurn),
		LogLiquidityRemoved:       logBigInt(sample.LiquidityRemoved),
		RemovalFraction:           decimalToFloat(sample.RemovalFraction),
		ActiveRemovalShare:        decimalToFloat(sample.ActiveRemovalShare),
		LogRangeWidth:             math.Log1p(float64(sample.RangeWidth)),
		NormalizedDistanceOutside: decimalToFloat(sample.NormalizedDistanceOutsideRange),
	}
}

func matchedBurnFeatureScales(values []matchedBurnFeature) matchedBurnScales {
	field := func(value func(matchedBurnFeature) float64) []float64 {
		result := make([]float64, len(values))
		for index, item := range values {
			result[index] = value(item)
		}
		return result
	}
	return matchedBurnScales{
		BlockNumber:               safeStd(field(func(value matchedBurnFeature) float64 { return value.BlockNumber })),
		CurrentTick:               safeStd(field(func(value matchedBurnFeature) float64 { return value.CurrentTick })),
		LogActiveLiquidity:        safeStd(field(func(value matchedBurnFeature) float64 { return value.LogActiveLiquidity })),
		LogLiquidityRemoved:       safeStd(field(func(value matchedBurnFeature) float64 { return value.LogLiquidityRemoved })),
		RemovalFraction:           safeStd(field(func(value matchedBurnFeature) float64 { return value.RemovalFraction })),
		ActiveRemovalShare:        safeStd(field(func(value matchedBurnFeature) float64 { return value.ActiveRemovalShare })),
		LogRangeWidth:             safeStd(field(func(value matchedBurnFeature) float64 { return value.LogRangeWidth })),
		NormalizedDistanceOutside: safeStd(field(func(value matchedBurnFeature) float64 { return value.NormalizedDistanceOutside })),
	}
}

func matchedBurnDistance(left, right matchedBurnFeature, scales matchedBurnScales) float64 {
	values := []float64{
		(left.BlockNumber - right.BlockNumber) / scales.BlockNumber,
		(left.CurrentTick - right.CurrentTick) / scales.CurrentTick,
		(left.LogActiveLiquidity - right.LogActiveLiquidity) / scales.LogActiveLiquidity,
		(left.LogLiquidityRemoved - right.LogLiquidityRemoved) / scales.LogLiquidityRemoved,
		(left.RemovalFraction - right.RemovalFraction) / scales.RemovalFraction,
		(left.ActiveRemovalShare - right.ActiveRemovalShare) / scales.ActiveRemovalShare,
		(left.LogRangeWidth - right.LogRangeWidth) / scales.LogRangeWidth,
		(left.NormalizedDistanceOutside - right.NormalizedDistanceOutside) / scales.NormalizedDistanceOutside,
	}
	var sum float64
	for _, value := range values {
		sum += value * value
	}
	return math.Sqrt(sum)
}

func buildMatchedBurnBalance(before []matchedBurnBeforeGroup, pairs []MatchedBurnPair) []MatchedBurnBalance {
	metrics := []struct {
		name  string
		value func(BurnEventSample) float64
	}{
		{"block_number", func(sample BurnEventSample) float64 { return float64(sample.Burn.Cursor.BlockNumber) }},
		{"current_tick", func(sample BurnEventSample) float64 { return float64(sample.CurrentTick) }},
		{"log_active_liquidity", func(sample BurnEventSample) float64 { return logBigInt(sample.ActiveLiquidityBeforeBurn) }},
		{"log_liquidity_removed", func(sample BurnEventSample) float64 { return logBigInt(sample.LiquidityRemoved) }},
		{"removal_fraction", func(sample BurnEventSample) float64 { return decimalToFloat(sample.RemovalFraction) }},
		{"active_removal_share", func(sample BurnEventSample) float64 { return decimalToFloat(sample.ActiveRemovalShare) }},
		{"log_range_width", func(sample BurnEventSample) float64 { return math.Log1p(float64(sample.RangeWidth)) }},
		{"normalized_distance_outside", func(sample BurnEventSample) float64 { return decimalToFloat(sample.NormalizedDistanceOutsideRange) }},
	}

	groups := append([]matchedBurnBeforeGroup(nil), before...)
	allHigh := make([]BurnEventSample, 0)
	allLow := make([]BurnEventSample, 0)
	for _, group := range before {
		allHigh = append(allHigh, group.High...)
		allLow = append(allLow, group.Low...)
	}
	groups = append(groups, matchedBurnBeforeGroup{Stratum: "", High: allHigh, Low: allLow})

	result := make([]MatchedBurnBalance, 0, len(groups)*len(metrics))
	for _, group := range groups {
		stratum := "all"
		if group.Stratum != "" {
			stratum = string(group.Stratum)
		}
		matchedHigh := make([]BurnEventSample, 0)
		matchedLow := make([]BurnEventSample, 0)
		for _, pair := range pairs {
			if stratum == "all" || pair.Stratum == group.Stratum {
				matchedHigh = append(matchedHigh, pair.High)
				matchedLow = append(matchedLow, pair.Low)
			}
		}

		for _, metric := range metrics {
			highBefore := sampleMetric(group.High, metric.value)
			lowBefore := sampleMetric(group.Low, metric.value)
			highAfter := sampleMetric(matchedHigh, metric.value)
			lowAfter := sampleMetric(matchedLow, metric.value)
			beforeSMD := standardizedMeanDifference(highBefore, lowBefore)
			afterSMD := standardizedMeanDifference(highAfter, lowAfter)
			result = append(result, MatchedBurnBalance{
				Stratum:              stratum,
				Metric:               metric.name,
				HighCountBefore:      len(highBefore),
				LowCountBefore:       len(lowBefore),
				PairsAfter:           len(highAfter),
				HighMeanBefore:       meanOrZero(highBefore),
				LowMeanBefore:        meanOrZero(lowBefore),
				SMDBefore:            beforeSMD,
				HighMeanAfter:        meanOrZero(highAfter),
				LowMeanAfter:         meanOrZero(lowAfter),
				SMDAfter:             afterSMD,
				AbsoluteSMDReduction: math.Abs(beforeSMD) - math.Abs(afterSMD),
			})
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Stratum == result[j].Stratum {
			return result[i].Metric < result[j].Metric
		}
		return result[i].Stratum < result[j].Stratum
	})
	return result
}

func sampleMetric(samples []BurnEventSample, value func(BurnEventSample) float64) []float64 {
	result := make([]float64, len(samples))
	for index, sample := range samples {
		result[index] = value(sample)
	}
	return result
}

func meanOrZero(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return mean(values)
}

func standardizedMeanDifference(left, right []float64) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	leftMean := mean(left)
	rightMean := mean(right)
	leftVariance := sampleVariance(left)
	rightVariance := sampleVariance(right)
	pooled := math.Sqrt((leftVariance + rightVariance) / 2)
	if pooled == 0 {
		return 0
	}
	return (leftMean - rightMean) / pooled
}

func sampleVariance(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	average := mean(values)
	var sum float64
	for _, value := range values {
		difference := value - average
		sum += difference * difference
	}
	return sum / float64(len(values)-1)
}

func buildMatchedBurnSummaries(outcomes []MatchedBurnOutcome) []MatchedBurnHorizonSummary {
	type summaryKey struct {
		Stratum string
		Label   string
		Blocks  uint64
	}
	groups := make(map[summaryKey][]MatchedBurnOutcome)
	for _, outcome := range outcomes {
		for _, stratum := range []string{"all", string(outcome.Stratum)} {
			key := summaryKey{Stratum: stratum, Label: outcome.HorizonLabel, Blocks: outcome.HorizonBlocks}
			groups[key] = append(groups[key], outcome)
		}
	}
	keys := make([]summaryKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Blocks == keys[j].Blocks {
			return keys[i].Stratum < keys[j].Stratum
		}
		return keys[i].Blocks < keys[j].Blocks
	})

	result := make([]MatchedBurnHorizonSummary, 0, len(keys))
	for _, key := range keys {
		rows := groups[key]
		summary := MatchedBurnHorizonSummary{
			Stratum:       key.Stratum,
			HorizonLabel:  key.Label,
			HorizonBlocks: key.Blocks,
			Pairs:         len(rows),
		}
		highLSIS := make([]decimal.Decimal, 0, len(rows))
		lowLSIS := make([]decimal.Decimal, 0, len(rows))
		differenceLSIS := make([]decimal.Decimal, 0, len(rows))
		highRealized := make([]decimal.Decimal, 0, len(rows))
		lowRealized := make([]decimal.Decimal, 0, len(rows))
		differenceRealized := make([]decimal.Decimal, 0, len(rows))
		mechanicalDifferences := make([]decimal.Decimal, 0, len(rows))
		for _, outcome := range rows {
			highLSIS = append(highLSIS, outcome.HighImmediateLSISBps)
			lowLSIS = append(lowLSIS, outcome.LowImmediateLSISBps)
			differenceLSIS = append(differenceLSIS, outcome.ImmediateLSISDifferenceBps)
			highRealized = append(highRealized, outcome.HighRealizedDeteriorationBps)
			lowRealized = append(lowRealized, outcome.LowRealizedDeteriorationBps)
			differenceRealized = append(differenceRealized, outcome.RealizedDeteriorationDifferenceBps)
			if outcome.RealizedDeteriorationDifferenceBps.IsPositive() {
				summary.PositiveRealizedDifferencePairs++
			}
			if outcome.MechanicalBothAvailable {
				summary.MechanicalAvailablePairs++
				mechanicalDifferences = append(mechanicalDifferences, outcome.MechanicalMidpointDifferenceBps)
			}
		}
		summary.MeanHighImmediateLSISBps = decimalMean(highLSIS)
		summary.MeanLowImmediateLSISBps = decimalMean(lowLSIS)
		summary.MeanImmediateLSISDifferenceBps = decimalMean(differenceLSIS)
		summary.MeanHighRealizedDeteriorationBps = decimalMean(highRealized)
		summary.MeanLowRealizedDeteriorationBps = decimalMean(lowRealized)
		summary.MeanRealizedDeteriorationDifferenceBps = decimalMean(differenceRealized)
		summary.MedianRealizedDeteriorationDifferenceBps = decimalMedian(differenceRealized)
		if len(rows) > 0 {
			summary.PositiveRealizedDifferenceShare = decimal.NewFromInt(
				int64(summary.PositiveRealizedDifferencePairs),
			).Div(decimal.NewFromInt(int64(len(rows))))
		}
		summary.MeanMechanicalMidpointDifferenceBps = decimalMean(mechanicalDifferences)
		result = append(result, summary)
	}
	return result
}
