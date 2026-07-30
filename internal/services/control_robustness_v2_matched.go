package services

import (
	"fmt"
	"math"
	"sort"

	"github.com/shopspring/decimal"
)

func buildRobustMatchedBurnControls(
	collection BurnSampleCollectionReport,
	realized BurnRealizedDatasetReport,
	counterfactual BurnCounterfactualFutureReport,
	horizons []BurnOutcomeHorizon,
	config ControlRobustnessV2Config,
) ([]RobustMatchedBurnPair, []RobustMatchedBurnOutcome, []RobustControlBalance, []RobustControlInference, error) {
	groups := make(map[BurnRangeLocation][]BurnEventSample)
	for _, sample := range collection.Samples {
		groups[sample.RangeLocation] = append(groups[sample.RangeLocation], sample)
	}

	pairs := make([]RobustMatchedBurnPair, 0, len(collection.Samples))
	for _, stratum := range []BurnRangeLocation{BurnRangeActive, BurnRangeBelowCurrentTick, BurnRangeAboveCurrentTick} {
		items := groups[stratum]
		if len(items) < 2 {
			continue
		}
		basePairs, err := matchRobustBurnStratum(stratum, append([]BurnEventSample(nil), items...))
		if err != nil {
			// A stratum with no unequal-LSIS contrast is not an analyzer failure;
			// it simply contributes no matched-effect observations.
			continue
		}
		refined := refineMatchedBurnPairs(stratum, items, basePairs)
		for _, pair := range refined {
			robust := RobustMatchedBurnPair{
				AnalysisSet:   ControlAnalysisFullSensitivity,
				Stratum:       stratum,
				High:          cloneBurnEventSampleForMatch(pair.High),
				Low:           cloneBurnEventSampleForMatch(pair.Low),
				MatchDistance: pair.MatchDistance,
				BlockDistance: pair.BlockDistance,
				WithinCaliper: pair.MatchDistance <= config.MatchedBurnCaliper,
			}
			pairs = append(pairs, robust)
			if robust.WithinCaliper {
				common := robust
				common.AnalysisSet = ControlAnalysisCommonSupport
				pairs = append(pairs, common)
			}
		}
	}

	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].AnalysisSet != pairs[j].AnalysisSet {
			return pairs[i].AnalysisSet < pairs[j].AnalysisSet
		}
		if pairs[i].Stratum != pairs[j].Stratum {
			return pairs[i].Stratum < pairs[j].Stratum
		}
		if pairs[i].High.Burn.Cursor.Equal(pairs[j].High.Burn.Cursor) {
			return pairs[i].Low.Burn.Cursor.Before(pairs[j].Low.Burn.Cursor)
		}
		return pairs[i].High.Burn.Cursor.Before(pairs[j].High.Burn.Cursor)
	})
	counters := make(map[string]int)
	for index := range pairs {
		counters[pairs[index].AnalysisSet]++
		prefix := "full"
		if pairs[index].AnalysisSet == ControlAnalysisCommonSupport {
			prefix = "support"
		}
		pairs[index].PairID = fmt.Sprintf("robust_burn_%s_%03d", prefix, counters[pairs[index].AnalysisSet])
	}

	actualByKey := make(map[string]BurnRegressionObservation, len(realized.Observations))
	for _, observation := range realized.Observations {
		actualByKey[burnCounterfactualObservationKey(observation.Burn.EventKey(), observation.HorizonLabel)] = observation
	}
	counterfactualByKey := make(map[string]BurnCounterfactualObservation, len(counterfactual.Observations))
	for _, observation := range counterfactual.Observations {
		counterfactualByKey[burnCounterfactualObservationKey(observation.Burn.EventKey(), observation.HorizonLabel)] = observation
	}

	outcomes := make([]RobustMatchedBurnOutcome, 0, len(pairs)*len(horizons))
	for _, pair := range pairs {
		for _, horizon := range horizons {
			highKey := burnCounterfactualObservationKey(pair.High.Burn.EventKey(), horizon.Label)
			lowKey := burnCounterfactualObservationKey(pair.Low.Burn.EventKey(), horizon.Label)
			highActual, highExists := actualByKey[highKey]
			lowActual, lowExists := actualByKey[lowKey]
			if !highExists || !lowExists {
				continue
			}
			outcome := RobustMatchedBurnOutcome{
				AnalysisSet:                        pair.AnalysisSet,
				PairID:                             pair.PairID,
				Stratum:                            pair.Stratum,
				HorizonLabel:                       horizon.Label,
				HorizonBlocks:                      horizon.Blocks,
				HighBurn:                           cloneBurnCandidate(pair.High.Burn),
				LowBurn:                            cloneBurnCandidate(pair.Low.Burn),
				HighImmediateLSISBps:               pair.High.TotalLSISBps,
				LowImmediateLSISBps:                pair.Low.TotalLSISBps,
				ImmediateLSISDifferenceBps:         pair.High.TotalLSISBps.Sub(pair.Low.TotalLSISBps),
				HighRealizedDeteriorationBps:       highActual.TotalRealizedDeteriorationBps,
				LowRealizedDeteriorationBps:        lowActual.TotalRealizedDeteriorationBps,
				RealizedDeteriorationDifferenceBps: highActual.TotalRealizedDeteriorationBps.Sub(lowActual.TotalRealizedDeteriorationBps),
			}
			highCounterfactual, highCFExists := counterfactualByKey[highKey]
			lowCounterfactual, lowCFExists := counterfactualByKey[lowKey]
			if highCFExists && lowCFExists && burnCounterfactualObservationAvailable(highCounterfactual) && burnCounterfactualObservationAvailable(lowCounterfactual) {
				highMidpoint := highCounterfactual.MinTotalMechanicalEffectPIAUCBps.Add(
					highCounterfactual.MaxTotalMechanicalEffectPIAUCBps,
				).Div(decimal.NewFromInt(2))
				lowMidpoint := lowCounterfactual.MinTotalMechanicalEffectPIAUCBps.Add(
					lowCounterfactual.MaxTotalMechanicalEffectPIAUCBps,
				).Div(decimal.NewFromInt(2))
				outcome.MechanicalBothAvailable = true
				outcome.MechanicalMidpointDifferenceBps = highMidpoint.Sub(lowMidpoint)
			}
			outcomes = append(outcomes, outcome)
		}
	}

	balance := buildRobustMatchedBurnBalance(pairs)
	inference := buildRobustMatchedBurnInference(outcomes, config)
	return pairs, outcomes, balance, inference, nil
}

func matchRobustBurnStratum(stratum BurnRangeLocation, samples []BurnEventSample) ([]MatchedBurnPair, error) {
	if len(samples) < 2 {
		return nil, fmt.Errorf("match robust burn stratum %s: fewer than two samples", stratum)
	}
	features := make([]matchedBurnFeature, len(samples))
	for index, sample := range samples {
		features[index] = matchedBurnFeatureFromSample(sample)
	}
	scales := matchedBurnFeatureScales(features)
	edges := make([]matchedBurnEdge, 0, len(samples)*(len(samples)-1)/2)
	for left := 0; left < len(samples); left++ {
		for right := left + 1; right < len(samples); right++ {
			if samples[left].TotalLSISBps.Equal(samples[right].TotalLSISBps) {
				continue
			}
			leftKey := samples[left].Burn.EventKey()
			rightKey := samples[right].Burn.EventKey()
			if rightKey < leftKey {
				leftKey, rightKey = rightKey, leftKey
			}
			edges = append(edges, matchedBurnEdge{
				Distance: robustMatchedBurnDistance(features[left], features[right], scales),
				Left:     left,
				Right:    right,
				LeftKey:  leftKey,
				RightKey: rightKey,
			})
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("match robust burn stratum %s: no unequal-LSIS edges", stratum)
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
	featureByKey := make(map[string]matchedBurnFeature, len(samples))
	for index, sample := range samples {
		featureByKey[sample.Burn.EventKey()] = features[index]
	}
	for _, edge := range edges {
		if used[edge.Left] || used[edge.Right] {
			continue
		}
		used[edge.Left] = true
		used[edge.Right] = true
		result = append(result, robustMatchedPairFromMembers(
			stratum,
			samples[edge.Left],
			samples[edge.Right],
			featureByKey,
			scales,
		))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("match robust burn stratum %s: no pairs selected", stratum)
	}
	return result, nil
}

func robustMatchedBurnDistance(left, right matchedBurnFeature, scales matchedBurnScales) float64 {
	values := []struct {
		difference float64
		weight     float64
	}{
		{(left.BlockNumber - right.BlockNumber) / scales.BlockNumber, 1},
		{(left.CurrentTick - right.CurrentTick) / scales.CurrentTick, 1},
		{(left.LogActiveLiquidity - right.LogActiveLiquidity) / scales.LogActiveLiquidity, 1},
		{(left.LogLiquidityRemoved - right.LogLiquidityRemoved) / scales.LogLiquidityRemoved, 4},
		{(left.RemovalFraction - right.RemovalFraction) / scales.RemovalFraction, 2},
		{(left.ActiveRemovalShare - right.ActiveRemovalShare) / scales.ActiveRemovalShare, 1},
		{(left.LogRangeWidth - right.LogRangeWidth) / scales.LogRangeWidth, 1},
		{(left.NormalizedDistanceOutside - right.NormalizedDistanceOutside) / scales.NormalizedDistanceOutside, 1},
	}
	var sum float64
	for _, value := range values {
		sum += value.weight * value.difference * value.difference
	}
	return math.Sqrt(sum)
}

func refineMatchedBurnPairs(
	stratum BurnRangeLocation,
	samples []BurnEventSample,
	pairs []MatchedBurnPair,
) []MatchedBurnPair {
	if len(pairs) < 2 {
		return append([]MatchedBurnPair(nil), pairs...)
	}
	features := make(map[string]matchedBurnFeature, len(samples))
	allFeatures := make([]matchedBurnFeature, 0, len(samples))
	for _, sample := range samples {
		feature := matchedBurnFeatureFromSample(sample)
		features[sample.Burn.EventKey()] = feature
		allFeatures = append(allFeatures, feature)
	}
	scales := matchedBurnFeatureScales(allFeatures)
	result := append([]MatchedBurnPair(nil), pairs...)
	for index := range result {
		result[index] = robustMatchedPairFromMembers(stratum, result[index].High, result[index].Low, features, scales)
	}

	maximumPasses := len(result) * len(result)
	for pass := 0; pass < maximumPasses; pass++ {
		improved := false
		for left := 0; left < len(result); left++ {
			for right := left + 1; right < len(result); right++ {
				a := result[left].High
				b := result[left].Low
				c := result[right].High
				d := result[right].Low
				currentCost := result[left].MatchDistance + result[right].MatchDistance

				firstLeft, firstLeftOK := robustMatchedPairCandidate(stratum, a, c, features, scales)
				firstRight, firstRightOK := robustMatchedPairCandidate(stratum, b, d, features, scales)
				secondLeft, secondLeftOK := robustMatchedPairCandidate(stratum, a, d, features, scales)
				secondRight, secondRightOK := robustMatchedPairCandidate(stratum, b, c, features, scales)

				bestCost := currentCost
				bestLeft := result[left]
				bestRight := result[right]
				if firstLeftOK && firstRightOK {
					cost := firstLeft.MatchDistance + firstRight.MatchDistance
					if cost+1e-12 < bestCost {
						bestCost, bestLeft, bestRight = cost, firstLeft, firstRight
					}
				}
				if secondLeftOK && secondRightOK {
					cost := secondLeft.MatchDistance + secondRight.MatchDistance
					if cost+1e-12 < bestCost {
						bestCost, bestLeft, bestRight = cost, secondLeft, secondRight
					}
				}
				if bestCost+1e-12 < currentCost {
					result[left], result[right] = bestLeft, bestRight
					improved = true
				}
			}
		}
		if !improved {
			break
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].High.Burn.Cursor.Equal(result[j].High.Burn.Cursor) {
			return result[i].Low.Burn.Cursor.Before(result[j].Low.Burn.Cursor)
		}
		return result[i].High.Burn.Cursor.Before(result[j].High.Burn.Cursor)
	})
	return result
}

func robustMatchedPairCandidate(
	stratum BurnRangeLocation,
	left BurnEventSample,
	right BurnEventSample,
	features map[string]matchedBurnFeature,
	scales matchedBurnScales,
) (MatchedBurnPair, bool) {
	if left.TotalLSISBps.Equal(right.TotalLSISBps) {
		return MatchedBurnPair{}, false
	}
	return robustMatchedPairFromMembers(stratum, left, right, features, scales), true
}

func robustMatchedPairFromMembers(
	stratum BurnRangeLocation,
	left BurnEventSample,
	right BurnEventSample,
	features map[string]matchedBurnFeature,
	scales matchedBurnScales,
) MatchedBurnPair {
	high, low := left, right
	if high.TotalLSISBps.LessThan(low.TotalLSISBps) ||
		(high.TotalLSISBps.Equal(low.TotalLSISBps) && high.Burn.Cursor.Before(low.Burn.Cursor)) {
		high, low = low, high
	}
	distance := robustMatchedBurnDistance(features[left.Burn.EventKey()], features[right.Burn.EventKey()], scales)
	return MatchedBurnPair{
		Stratum:       stratum,
		High:          cloneBurnEventSampleForMatch(high),
		Low:           cloneBurnEventSampleForMatch(low),
		MatchDistance: distance,
		BlockDistance: absUint64Diff(high.Burn.Cursor.BlockNumber, low.Burn.Cursor.BlockNumber),
	}
}

func buildRobustMatchedBurnBalance(pairs []RobustMatchedBurnPair) []RobustControlBalance {
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
	result := make([]RobustControlBalance, 0)
	for _, analysisSet := range []string{ControlAnalysisFullSensitivity, ControlAnalysisCommonSupport} {
		for _, stratum := range []string{"all", string(BurnRangeActive), string(BurnRangeBelowCurrentTick), string(BurnRangeAboveCurrentTick)} {
			high := make([]BurnEventSample, 0)
			low := make([]BurnEventSample, 0)
			for _, pair := range pairs {
				if pair.AnalysisSet != analysisSet || (stratum != "all" && string(pair.Stratum) != stratum) {
					continue
				}
				high = append(high, pair.High)
				low = append(low, pair.Low)
			}
			for _, metric := range metrics {
				highValues := sampleMetric(high, metric.value)
				lowValues := sampleMetric(low, metric.value)
				smd := standardizedMeanDifference(highValues, lowValues)
				status := controlBalanceStatus(math.Abs(smd))
				if len(highValues) < 2 || len(lowValues) < 2 {
					status = ControlBalanceFailed
				}
				result = append(result, RobustControlBalance{
					ControlType:  "matched_burn",
					AnalysisSet:  analysisSet,
					Stratum:      stratum,
					Metric:       metric.name,
					TreatedCount: len(highValues),
					ControlCount: len(lowValues),
					TreatedMean:  meanOrZero(highValues),
					ControlMean:  meanOrZero(lowValues),
					SMD:          smd,
					AbsoluteSMD:  math.Abs(smd),
					Status:       status,
				})
			}
		}
	}
	return result
}

func buildRobustMatchedBurnInference(outcomes []RobustMatchedBurnOutcome, config ControlRobustnessV2Config) []RobustControlInference {
	type groupKey struct {
		analysisSet string
		outcome     string
		stratum     string
		label       string
		blocks      uint64
	}
	groups := make(map[groupKey][]float64)
	for _, outcome := range outcomes {
		for _, stratum := range []string{"all", string(outcome.Stratum)} {
			realizedKey := groupKey{outcome.AnalysisSet, "realized_deterioration_difference_bps", stratum, outcome.HorizonLabel, outcome.HorizonBlocks}
			groups[realizedKey] = append(groups[realizedKey], decimalToFloat(outcome.RealizedDeteriorationDifferenceBps))
			if outcome.MechanicalBothAvailable {
				mechanicalKey := groupKey{outcome.AnalysisSet, "mechanical_midpoint_difference_bps", stratum, outcome.HorizonLabel, outcome.HorizonBlocks}
				groups[mechanicalKey] = append(groups[mechanicalKey], decimalToFloat(outcome.MechanicalMidpointDifferenceBps))
			}
		}
	}
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].analysisSet != keys[j].analysisSet {
			return keys[i].analysisSet < keys[j].analysisSet
		}
		if keys[i].outcome != keys[j].outcome {
			return keys[i].outcome < keys[j].outcome
		}
		if keys[i].stratum != keys[j].stratum {
			return keys[i].stratum < keys[j].stratum
		}
		return keys[i].blocks < keys[j].blocks
	})
	result := make([]RobustControlInference, 0, len(keys))
	for _, key := range keys {
		result = append(result, buildRobustInferenceRow(
			"matched_burn",
			key.analysisSet,
			key.outcome,
			key.stratum,
			key.label,
			key.blocks,
			groups[key],
			config,
		))
	}
	applyControlHolm(result)
	return result
}
