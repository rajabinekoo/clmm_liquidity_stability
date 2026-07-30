package controlfreeze

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type analysisDefinition struct {
	Name            string
	Caliper         float64
	MinimumControls int
}

type temporalFeature struct {
	BlockNumber          float64
	CurrentTick          float64
	LogActiveLiquidity   float64
	LogBaselineAUC       float64
	DirectionalImbalance float64
}

type matchedFeature struct {
	BlockNumber               float64
	CurrentTick               float64
	LogActiveLiquidity        float64
	LogLiquidityRemoved       float64
	RemovalFraction           float64
	ActiveRemovalShare        float64
	LogRangeWidth             float64
	NormalizedDistanceOutside float64
}

type inferenceGroupKey struct {
	ControlType   string
	AnalysisSet   string
	Outcome       string
	Stratum       string
	HorizonLabel  string
	HorizonBlocks uint64
}

func Analyze(inputs parsedInputs, config Config) (Report, error) {
	if err := validateConfig(config); err != nil {
		return Report{}, err
	}
	if inputs.Manifest.BootstrapIterations > 0 {
		config.BootstrapIterations = inputs.Manifest.BootstrapIterations
	}
	if inputs.Manifest.PermutationIterations > 0 {
		config.PermutationIterations = inputs.Manifest.PermutationIterations
	}
	if inputs.Manifest.RandomSeed != 0 {
		config.RandomSeed = inputs.Manifest.RandomSeed
	}

	temporalDefinitions := []analysisDefinition{
		{Name: AnalysisPrimaryCommonSupport, Caliper: config.TemporalPrimaryCaliper, MinimumControls: config.MinimumTemporalControls},
		{Name: AnalysisSensitivityCaliper2, Caliper: config.TemporalSensitivityCaliper, MinimumControls: config.MinimumTemporalControls},
		{Name: AnalysisSensitivityFull, Caliper: math.Inf(1), MinimumControls: 1},
	}
	matchedDefinitions := []analysisDefinition{
		{Name: AnalysisPrimaryCommonSupport, Caliper: config.MatchedPrimaryCaliper, MinimumControls: 1},
		{Name: AnalysisSensitivityCaliper2, Caliper: config.MatchedSensitivityCaliper, MinimumControls: 1},
		{Name: AnalysisSensitivityFull, Caliper: math.Inf(1), MinimumControls: 1},
	}

	temporalMetrics, temporalValues, temporalSupport, temporalGroups, err := analyzeTemporal(inputs, temporalDefinitions, config.ExtremeTailPercentileCutoff)
	if err != nil {
		return Report{}, err
	}
	matchedMetrics, matchedValues, matchedSupport, matchedGroups, err := analyzeMatched(inputs, matchedDefinitions)
	if err != nil {
		return Report{}, err
	}
	metrics := append(temporalMetrics, matchedMetrics...)
	values := append(temporalValues, matchedValues...)
	groups := make(map[inferenceGroupKey][]float64, len(temporalGroups)+len(matchedGroups))
	for key, items := range temporalGroups {
		groups[key] = append(groups[key], items...)
	}
	for key, items := range matchedGroups {
		groups[key] = append(groups[key], items...)
	}

	gates := buildBalanceGates(metrics, config)
	inference := buildInference(groups, gates, config)
	applyHolm(inference)
	finalizeInference(inference, gates, config)

	manifest := buildManifest(inputs, config, gates, inference, temporalSupport, matchedSupport)
	return Report{
		Manifest:        manifest,
		BalanceMetrics:  metrics,
		BalanceGates:    gates,
		Inference:       inference,
		AnalysisValues:  attachValueEligibility(values, gates),
		TemporalSupport: temporalSupport,
		MatchedSupport:  matchedSupport,
	}, nil
}

func validateConfig(config Config) error {
	positiveFinite := []struct {
		name  string
		value float64
	}{
		{"temporal primary caliper", config.TemporalPrimaryCaliper},
		{"temporal sensitivity caliper", config.TemporalSensitivityCaliper},
		{"matched primary caliper", config.MatchedPrimaryCaliper},
		{"matched sensitivity caliper", config.MatchedSensitivityCaliper},
		{"maximum absolute SMD", config.MaximumAbsoluteSMD},
		{"alpha", config.Alpha},
	}
	for _, item := range positiveFinite {
		if item.value <= 0 || math.IsNaN(item.value) || math.IsInf(item.value, 0) {
			return fmt.Errorf("control freeze v2.1: %s must be finite and positive", item.name)
		}
	}
	if config.TemporalPrimaryCaliper > config.TemporalSensitivityCaliper {
		return fmt.Errorf("control freeze v2.1: temporal primary caliper exceeds sensitivity caliper")
	}
	if config.MatchedPrimaryCaliper > config.MatchedSensitivityCaliper {
		return fmt.Errorf("control freeze v2.1: matched primary caliper exceeds sensitivity caliper")
	}
	if config.MinimumTemporalControls <= 0 {
		return fmt.Errorf("control freeze v2.1: minimum temporal controls must be positive")
	}
	if config.Alpha >= 1 {
		return fmt.Errorf("control freeze v2.1: alpha must be below one")
	}
	if config.BootstrapIterations <= 0 || config.PermutationIterations <= 0 {
		return fmt.Errorf("control freeze v2.1: statistical iterations must be positive")
	}
	if config.ExtremeTailPercentileCutoff <= 0 || config.ExtremeTailPercentileCutoff >= 1 {
		return fmt.Errorf("control freeze v2.1: extreme-tail percentile cutoff must be between zero and one")
	}
	return nil
}

func analyzeTemporal(
	inputs parsedInputs,
	definitions []analysisDefinition,
	extremeTailCutoff float64,
) ([]BalanceMetric, []AnalysisValue, []TemporalSupport, map[inferenceGroupKey][]float64, error) {
	matchesByBurn := make(map[string][]temporalMatch)
	matchByID := make(map[string]temporalMatch, len(inputs.TemporalMatches))
	for _, match := range inputs.TemporalMatches {
		if _, exists := matchByID[match.MatchID]; exists {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: duplicate temporal match id %s", match.MatchID)
		}
		matchByID[match.MatchID] = match
		matchesByBurn[match.BurnEventKey] = append(matchesByBurn[match.BurnEventKey], match)
	}
	for key := range matchesByBurn {
		sort.SliceStable(matchesByBurn[key], func(i, j int) bool {
			if matchesByBurn[key][i].MatchDistance == matchesByBurn[key][j].MatchDistance {
				return matchesByBurn[key][i].MatchID < matchesByBurn[key][j].MatchID
			}
			return matchesByBurn[key][i].MatchDistance < matchesByBurn[key][j].MatchDistance
		})
	}

	type observationKey struct {
		MatchID string
		Label   string
	}
	observations := make(map[observationKey]temporalObservation, len(inputs.TemporalObservations))
	horizonBlocks := make(map[string]uint64)
	for _, observation := range inputs.TemporalObservations {
		key := observationKey{observation.MatchID, observation.HorizonLabel}
		if _, exists := observations[key]; exists {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: duplicate temporal observation %s/%s", observation.MatchID, observation.HorizonLabel)
		}
		if _, exists := matchByID[observation.MatchID]; !exists {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: temporal observation references unknown match %s", observation.MatchID)
		}
		observations[key] = observation
		if previous, exists := horizonBlocks[observation.HorizonLabel]; exists && previous != observation.HorizonBlocks {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: inconsistent horizon %s", observation.HorizonLabel)
		}
		horizonBlocks[observation.HorizonLabel] = observation.HorizonBlocks
	}
	horizons := sortedHorizons(horizonBlocks)
	burnKeys := sortedKeys(matchesByBurn)

	metrics := make([]BalanceMetric, 0)
	analysisValues := make([]AnalysisValue, 0)
	groups := make(map[inferenceGroupKey][]float64)
	for _, definition := range definitions {
		selectedByBurn := make(map[string][]temporalMatch, len(burnKeys))
		for _, burnKey := range burnKeys {
			for _, match := range matchesByBurn[burnKey] {
				if match.MatchDistance <= definition.Caliper {
					selectedByBurn[burnKey] = append(selectedByBurn[burnKey], match)
				}
			}
		}
		metrics = append(metrics, temporalBalanceMetrics(definition.Name, selectedByBurn, definition.MinimumControls)...)
		for _, burnKey := range burnKeys {
			selected := selectedByBurn[burnKey]
			if len(selected) < definition.MinimumControls {
				continue
			}
			stratum := selected[0].Stratum
			for _, horizon := range horizons {
				placeboValues := make([]float64, 0, len(selected))
				actual := 0.0
				actualSet := false
				for _, match := range selected {
					observation, exists := observations[observationKey{match.MatchID, horizon.Label}]
					if !exists || !observation.Available {
						continue
					}
					if actualSet && math.Abs(actual-observation.ActualRealizedDeteriorationBps) > 1e-9*math.Max(1, math.Abs(actual)) {
						return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: inconsistent actual deterioration for burn %s horizon %s", burnKey, horizon.Label)
					}
					actual = observation.ActualRealizedDeteriorationBps
					actualSet = true
					placeboValues = append(placeboValues, observation.PlaceboRealizedDeteriorationBps)
				}
				if len(placeboValues) < definition.MinimumControls || !actualSet {
					continue
				}
				difference := actual - mean(placeboValues)
				for _, groupStratum := range []string{"all", stratum} {
					key := inferenceGroupKey{
						ControlType: "temporal_placebo", AnalysisSet: definition.Name,
						Outcome: "excess_realized_deterioration_bps", Stratum: groupStratum,
						HorizonLabel: horizon.Label, HorizonBlocks: horizon.Blocks,
					}
					groups[key] = append(groups[key], difference)
				}
				analysisValues = append(analysisValues, AnalysisValue{
					ControlType: "temporal_placebo", AnalysisSet: definition.Name,
					AnalysisRole: roleFor(definition.Name, stratum), Stratum: stratum,
					RecordID: burnKey, HorizonLabel: horizon.Label, HorizonBlocks: horizon.Blocks,
					DifferenceBps: difference,
				})
			}
		}
	}

	support := temporalSupportRows(matchesByBurn, definitions[0].Caliper, definitions[1].Caliper, definitions[0].MinimumControls, extremeTailCutoff)
	return metrics, analysisValues, support, groups, nil
}

type horizon struct {
	Label  string
	Blocks uint64
}

func sortedHorizons(values map[string]uint64) []horizon {
	result := make([]horizon, 0, len(values))
	for label, blocks := range values {
		result = append(result, horizon{Label: label, Blocks: blocks})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Blocks == result[j].Blocks {
			return result[i].Label < result[j].Label
		}
		return result[i].Blocks < result[j].Blocks
	})
	return result
}

func temporalBalanceMetrics(analysisSet string, selected map[string][]temporalMatch, minimumControls int) []BalanceMetric {
	metrics := []struct {
		name  string
		value func(temporalFeature) float64
	}{
		{"block_number", func(value temporalFeature) float64 { return value.BlockNumber }},
		{"current_tick", func(value temporalFeature) float64 { return value.CurrentTick }},
		{"log_active_liquidity", func(value temporalFeature) float64 { return value.LogActiveLiquidity }},
		{"log_baseline_total_auc", func(value temporalFeature) float64 { return value.LogBaselineAUC }},
		{"directional_imbalance", func(value temporalFeature) float64 { return value.DirectionalImbalance }},
	}
	result := make([]BalanceMetric, 0, 4*len(metrics))
	for _, stratum := range []string{"all", "active", "below_current_tick", "above_current_tick"} {
		treated := make([]temporalFeature, 0)
		controls := make([]temporalFeature, 0)
		for _, burnKey := range sortedKeys(selected) {
			matches := selected[burnKey]
			if len(matches) < minimumControls || len(matches) == 0 {
				continue
			}
			if stratum != "all" && matches[0].Stratum != stratum {
				continue
			}
			treated = append(treated, temporalTreatedFeature(matches[0]))
			controlFeatures := make([]temporalFeature, 0, len(matches))
			for _, match := range matches {
				controlFeatures = append(controlFeatures, temporalControlFeature(match))
			}
			controls = append(controls, meanTemporalFeature(controlFeatures))
		}
		for _, metric := range metrics {
			treatedValues := featureValues(treated, metric.value)
			controlValues := featureValues(controls, metric.value)
			smd := standardizedMeanDifference(treatedValues, controlValues)
			absolute := math.Abs(smd)
			result = append(result, BalanceMetric{
				ControlType: "temporal_placebo", AnalysisSet: analysisSet, Stratum: stratum,
				Metric: metric.name, TreatedCount: len(treatedValues), ControlCount: len(controlValues),
				TreatedMean: mean(treatedValues), ControlMean: mean(controlValues),
				SMD: smd, AbsoluteSMD: absolute, Status: balanceStatus(absolute),
			})
		}
	}
	return result
}

func temporalTreatedFeature(match temporalMatch) temporalFeature {
	return temporalFeature{
		BlockNumber: float64(match.BurnBlock), CurrentTick: match.BurnCurrentTick,
		LogActiveLiquidity:   safeLog1p(match.BurnActiveLiquidity),
		LogBaselineAUC:       safeLog1p(match.BurnBaselineTotalAUCBps),
		DirectionalImbalance: match.BurnDirectionalImbalance,
	}
}

func temporalControlFeature(match temporalMatch) temporalFeature {
	return temporalFeature{
		BlockNumber:          float64(match.PlaceboAnchorBlock),
		CurrentTick:          match.PlaceboCurrentTick,
		LogActiveLiquidity:   safeLog1p(match.PlaceboActiveLiquidity),
		LogBaselineAUC:       safeLog1p(match.PlaceboBaselineTotalAUCBps),
		DirectionalImbalance: match.PlaceboDirectionalImbalance,
	}
}

func meanTemporalFeature(values []temporalFeature) temporalFeature {
	var result temporalFeature
	if len(values) == 0 {
		return result
	}
	for _, value := range values {
		result.BlockNumber += value.BlockNumber
		result.CurrentTick += value.CurrentTick
		result.LogActiveLiquidity += value.LogActiveLiquidity
		result.LogBaselineAUC += value.LogBaselineAUC
		result.DirectionalImbalance += value.DirectionalImbalance
	}
	denominator := float64(len(values))
	result.BlockNumber /= denominator
	result.CurrentTick /= denominator
	result.LogActiveLiquidity /= denominator
	result.LogBaselineAUC /= denominator
	result.DirectionalImbalance /= denominator
	return result
}

func analyzeMatched(
	inputs parsedInputs,
	definitions []analysisDefinition,
) ([]BalanceMetric, []AnalysisValue, []MatchedSupport, map[inferenceGroupKey][]float64, error) {
	pairByID := make(map[string]matchedPair, len(inputs.MatchedPairs))
	for _, pair := range inputs.MatchedPairs {
		if _, exists := pairByID[pair.PairID]; exists {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: duplicate full matched pair id %s", pair.PairID)
		}
		pairByID[pair.PairID] = pair
	}
	type outcomeKey struct {
		PairID string
		Label  string
	}
	seenOutcomes := make(map[outcomeKey]struct{}, len(inputs.MatchedOutcomes))
	outcomesByPair := make(map[string][]matchedOutcome, len(inputs.MatchedPairs))
	for _, outcome := range inputs.MatchedOutcomes {
		if _, exists := pairByID[outcome.PairID]; !exists {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: matched outcome references unknown full pair %s", outcome.PairID)
		}
		key := outcomeKey{outcome.PairID, outcome.HorizonLabel}
		if _, exists := seenOutcomes[key]; exists {
			return nil, nil, nil, nil, fmt.Errorf("control freeze v2.1: duplicate matched outcome %s/%s", outcome.PairID, outcome.HorizonLabel)
		}
		seenOutcomes[key] = struct{}{}
		outcomesByPair[outcome.PairID] = append(outcomesByPair[outcome.PairID], outcome)
	}
	for pairID := range outcomesByPair {
		sort.SliceStable(outcomesByPair[pairID], func(i, j int) bool {
			if outcomesByPair[pairID][i].HorizonBlocks == outcomesByPair[pairID][j].HorizonBlocks {
				return outcomesByPair[pairID][i].HorizonLabel < outcomesByPair[pairID][j].HorizonLabel
			}
			return outcomesByPair[pairID][i].HorizonBlocks < outcomesByPair[pairID][j].HorizonBlocks
		})
	}

	metrics := make([]BalanceMetric, 0)
	analysisValues := make([]AnalysisValue, 0)
	groups := make(map[inferenceGroupKey][]float64)
	for _, definition := range definitions {
		selected := make([]matchedPair, 0)
		for _, pair := range inputs.MatchedPairs {
			if pair.MatchDistance <= definition.Caliper {
				selected = append(selected, pair)
			}
		}
		sort.SliceStable(selected, func(i, j int) bool { return selected[i].PairID < selected[j].PairID })
		metrics = append(metrics, matchedBalanceMetrics(definition.Name, selected)...)
		for _, pair := range selected {
			for _, outcome := range outcomesByPair[pair.PairID] {
				for _, stratum := range []string{"all", pair.Stratum} {
					realizedKey := inferenceGroupKey{
						ControlType: "matched_burn", AnalysisSet: definition.Name,
						Outcome: "realized_deterioration_difference_bps", Stratum: stratum,
						HorizonLabel: outcome.HorizonLabel, HorizonBlocks: outcome.HorizonBlocks,
					}
					groups[realizedKey] = append(groups[realizedKey], outcome.RealizedDifferenceBps)
					if outcome.MechanicalAvailable {
						mechanicalKey := realizedKey
						mechanicalKey.Outcome = "mechanical_midpoint_difference_bps"
						groups[mechanicalKey] = append(groups[mechanicalKey], outcome.MechanicalDifferenceBps)
					}
				}
				analysisValues = append(analysisValues, AnalysisValue{
					ControlType: "matched_burn", AnalysisSet: definition.Name,
					AnalysisRole: roleFor(definition.Name, pair.Stratum), Stratum: pair.Stratum,
					RecordID: pair.PairID, HorizonLabel: outcome.HorizonLabel, HorizonBlocks: outcome.HorizonBlocks,
					DifferenceBps: outcome.RealizedDifferenceBps,
				})
			}
		}
	}
	support := matchedSupportRows(inputs.MatchedPairs, definitions[0].Caliper, definitions[1].Caliper)
	return metrics, analysisValues, support, groups, nil
}

func matchedBalanceMetrics(analysisSet string, pairs []matchedPair) []BalanceMetric {
	metrics := []struct {
		name  string
		value func(matchedFeature) float64
	}{
		{"block_number", func(value matchedFeature) float64 { return value.BlockNumber }},
		{"current_tick", func(value matchedFeature) float64 { return value.CurrentTick }},
		{"log_active_liquidity", func(value matchedFeature) float64 { return value.LogActiveLiquidity }},
		{"log_liquidity_removed", func(value matchedFeature) float64 { return value.LogLiquidityRemoved }},
		{"removal_fraction", func(value matchedFeature) float64 { return value.RemovalFraction }},
		{"active_removal_share", func(value matchedFeature) float64 { return value.ActiveRemovalShare }},
		{"log_range_width", func(value matchedFeature) float64 { return value.LogRangeWidth }},
		{"normalized_distance_outside", func(value matchedFeature) float64 { return value.NormalizedDistanceOutside }},
	}
	result := make([]BalanceMetric, 0, 4*len(metrics))
	for _, stratum := range []string{"all", "active", "below_current_tick", "above_current_tick"} {
		high := make([]matchedFeature, 0)
		low := make([]matchedFeature, 0)
		for _, pair := range pairs {
			if stratum != "all" && pair.Stratum != stratum {
				continue
			}
			high = append(high, highMatchedFeature(pair))
			low = append(low, lowMatchedFeature(pair))
		}
		for _, metric := range metrics {
			highValues := featureValues(high, metric.value)
			lowValues := featureValues(low, metric.value)
			smd := standardizedMeanDifference(highValues, lowValues)
			absolute := math.Abs(smd)
			result = append(result, BalanceMetric{
				ControlType: "matched_burn", AnalysisSet: analysisSet, Stratum: stratum,
				Metric: metric.name, TreatedCount: len(highValues), ControlCount: len(lowValues),
				TreatedMean: mean(highValues), ControlMean: mean(lowValues),
				SMD: smd, AbsoluteSMD: absolute, Status: balanceStatus(absolute),
			})
		}
	}
	return result
}

func highMatchedFeature(pair matchedPair) matchedFeature {
	return matchedFeature{
		BlockNumber: pair.HighBlock, CurrentTick: pair.HighCurrentTick,
		LogActiveLiquidity:  safeLog1p(pair.HighActiveLiquidity),
		LogLiquidityRemoved: safeLog1p(pair.HighLiquidityRemoved),
		RemovalFraction:     pair.HighRemovalFraction, ActiveRemovalShare: pair.HighActiveRemovalShare,
		LogRangeWidth: safeLog1p(pair.HighRangeWidth), NormalizedDistanceOutside: pair.HighDistanceOutside,
	}
}

func lowMatchedFeature(pair matchedPair) matchedFeature {
	return matchedFeature{
		BlockNumber: pair.LowBlock, CurrentTick: pair.LowCurrentTick,
		LogActiveLiquidity:  safeLog1p(pair.LowActiveLiquidity),
		LogLiquidityRemoved: safeLog1p(pair.LowLiquidityRemoved),
		RemovalFraction:     pair.LowRemovalFraction, ActiveRemovalShare: pair.LowActiveRemovalShare,
		LogRangeWidth: safeLog1p(pair.LowRangeWidth), NormalizedDistanceOutside: pair.LowDistanceOutside,
	}
}

func buildInference(groups map[inferenceGroupKey][]float64, gates []BalanceGate, config Config) []Inference {
	keys := make([]inferenceGroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].ControlType != keys[j].ControlType {
			return keys[i].ControlType < keys[j].ControlType
		}
		if keys[i].AnalysisSet != keys[j].AnalysisSet {
			return keys[i].AnalysisSet < keys[j].AnalysisSet
		}
		if keys[i].Outcome != keys[j].Outcome {
			return keys[i].Outcome < keys[j].Outcome
		}
		if keys[i].Stratum != keys[j].Stratum {
			return keys[i].Stratum < keys[j].Stratum
		}
		if keys[i].HorizonBlocks != keys[j].HorizonBlocks {
			return keys[i].HorizonBlocks < keys[j].HorizonBlocks
		}
		return keys[i].HorizonLabel < keys[j].HorizonLabel
	})
	gateByKey := make(map[string]BalanceGate, len(gates))
	for _, gate := range gates {
		gateByKey[gateKey(gate.ControlType, gate.AnalysisSet, gate.Stratum)] = gate
	}
	result := make([]Inference, 0, len(keys))
	for _, key := range keys {
		values := groups[key]
		row := Inference{
			ControlType: key.ControlType, AnalysisSet: key.AnalysisSet,
			AnalysisRole: roleFor(key.AnalysisSet, key.Stratum), Outcome: key.Outcome,
			Stratum: key.Stratum, HorizonLabel: key.HorizonLabel, HorizonBlocks: key.HorizonBlocks,
			Pairs: len(values), MeanDifferenceBps: mean(values), MedianDifferenceBps: median(values),
		}
		for _, value := range values {
			if value > 0 {
				row.PositivePairs++
			}
		}
		if len(values) > 0 {
			row.PositiveShare = float64(row.PositivePairs) / float64(len(values))
		}
		seed := stableSeed(config.RandomSeed, key.ControlType, key.AnalysisSet, key.Outcome, key.Stratum, key.HorizonLabel)
		row.BootstrapMeanCILower, row.BootstrapMeanCIUpper = bootstrapMeanCI(values, config.BootstrapIterations, seed)
		row.WilcoxonPValue = wilcoxonSignedRankPValue(values)
		row.PermutationPValue = pairedPermutationPValue(values, config.PermutationIterations, seed^0x5f3759df)
		if gate, exists := gateByKey[gateKey(key.ControlType, key.AnalysisSet, key.Stratum)]; exists {
			row.MaximumAbsoluteSMD = gate.MaximumAbsoluteSMD
			row.BalanceGateStatus = gate.GateStatus
			row.PrimaryEligible = gate.PrimaryEligible
		}
		result = append(result, row)
	}
	return result
}

func buildBalanceGates(metrics []BalanceMetric, config Config) []BalanceGate {
	type key struct{ controlType, analysisSet, stratum string }
	groups := make(map[key][]BalanceMetric)
	for _, metric := range metrics {
		groups[key{metric.ControlType, metric.AnalysisSet, metric.Stratum}] = append(groups[key{metric.ControlType, metric.AnalysisSet, metric.Stratum}], metric)
	}
	keys := make([]key, 0, len(groups))
	for item := range groups {
		keys = append(keys, item)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].controlType != keys[j].controlType {
			return keys[i].controlType < keys[j].controlType
		}
		if keys[i].analysisSet != keys[j].analysisSet {
			return keys[i].analysisSet < keys[j].analysisSet
		}
		return keys[i].stratum < keys[j].stratum
	})
	result := make([]BalanceGate, 0, len(keys))
	for _, item := range keys {
		rows := groups[item]
		gate := BalanceGate{
			ControlType: item.controlType, AnalysisSet: item.analysisSet, Stratum: item.stratum,
			AnalysisRole: roleFor(item.analysisSet, item.stratum), MetricCount: len(rows),
			Threshold: config.MaximumAbsoluteSMD, GateStatus: GateNotApplicable,
		}
		failed := make([]string, 0)
		gate.SampleSize = math.MaxInt
		for _, row := range rows {
			if row.TreatedCount < gate.SampleSize {
				gate.SampleSize = row.TreatedCount
			}
			if row.ControlCount < gate.SampleSize {
				gate.SampleSize = row.ControlCount
			}
			if row.AbsoluteSMD > gate.MaximumAbsoluteSMD || math.IsNaN(row.AbsoluteSMD) || math.IsInf(row.AbsoluteSMD, 0) {
				gate.MaximumAbsoluteSMD = row.AbsoluteSMD
			}
			if row.Status == BalanceFailed {
				failed = append(failed, row.Metric)
			}
		}
		if gate.SampleSize == math.MaxInt {
			gate.SampleSize = 0
		}
		sort.Strings(failed)
		gate.FailedMetricCount = len(failed)
		gate.FailedMetrics = strings.Join(failed, ";")
		gate.BalanceStatus = balanceStatus(gate.MaximumAbsoluteSMD)
		if gate.AnalysisRole == RolePrimary {
			if gate.SampleSize >= 2 && gate.MaximumAbsoluteSMD <= config.MaximumAbsoluteSMD && !math.IsNaN(gate.MaximumAbsoluteSMD) && !math.IsInf(gate.MaximumAbsoluteSMD, 0) {
				gate.GateStatus = GatePassed
				gate.PrimaryEligible = true
			} else {
				gate.GateStatus = GateFailed
				gate.AnalysisRole = RolePrimaryBlock
			}
		}
		result = append(result, gate)
	}
	return result
}

func finalizeInference(rows []Inference, gates []BalanceGate, config Config) {
	gateByKey := make(map[string]BalanceGate, len(gates))
	for _, gate := range gates {
		gateByKey[gateKey(gate.ControlType, gate.AnalysisSet, gate.Stratum)] = gate
	}
	for index := range rows {
		row := &rows[index]
		gate := gateByKey[gateKey(row.ControlType, row.AnalysisSet, row.Stratum)]
		row.AnalysisRole = gate.AnalysisRole
		row.MaximumAbsoluteSMD = gate.MaximumAbsoluteSMD
		row.BalanceGateStatus = gate.GateStatus
		row.PrimaryEligible = gate.PrimaryEligible
		row.CIExcludesZero = row.BootstrapMeanCILower > 0 || row.BootstrapMeanCIUpper < 0
		row.WilcoxonSignificant = row.WilcoxonHolmPValue < config.Alpha
		row.PermutationSignificant = row.PermutationHolmPValue < config.Alpha
		switch {
		case row.MeanDifferenceBps > 0:
			row.Direction = "positive"
		case row.MeanDifferenceBps < 0:
			row.Direction = "negative"
		default:
			row.Direction = "zero"
		}
		switch {
		case row.AnalysisRole == RolePrimaryBlock:
			row.PublicationStatus = "balance_blocked"
		case row.AnalysisRole == RoleExploratory:
			row.PublicationStatus = "exploratory"
		case row.AnalysisRole == RoleSensitivity:
			row.PublicationStatus = "sensitivity_only"
		case row.PrimaryEligible && row.CIExcludesZero && row.WilcoxonSignificant && row.PermutationSignificant:
			row.PublicationStatus = "primary_robust"
		case row.PrimaryEligible && row.CIExcludesZero && (row.WilcoxonSignificant || row.PermutationSignificant):
			row.PublicationStatus = "primary_supported"
		case row.PrimaryEligible:
			row.PublicationStatus = "primary_inconclusive"
		default:
			row.PublicationStatus = "not_primary"
		}
	}
}

func roleFor(analysisSet, stratum string) string {
	if stratum != "all" && stratum != "active" {
		return RoleExploratory
	}
	if analysisSet == AnalysisPrimaryCommonSupport {
		return RolePrimary
	}
	return RoleSensitivity
}

func gateKey(controlType, analysisSet, stratum string) string {
	return controlType + "\x00" + analysisSet + "\x00" + stratum
}

func attachValueEligibility(values []AnalysisValue, gates []BalanceGate) []AnalysisValue {
	gateByKey := make(map[string]BalanceGate, len(gates))
	for _, gate := range gates {
		gateByKey[gateKey(gate.ControlType, gate.AnalysisSet, gate.Stratum)] = gate
	}
	result := append([]AnalysisValue(nil), values...)
	for index := range result {
		gate := gateByKey[gateKey(result[index].ControlType, result[index].AnalysisSet, result[index].Stratum)]
		result[index].AnalysisRole = gate.AnalysisRole
		result[index].PrimaryEligible = gate.PrimaryEligible
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ControlType != result[j].ControlType {
			return result[i].ControlType < result[j].ControlType
		}
		if result[i].AnalysisSet != result[j].AnalysisSet {
			return result[i].AnalysisSet < result[j].AnalysisSet
		}
		if result[i].Stratum != result[j].Stratum {
			return result[i].Stratum < result[j].Stratum
		}
		if result[i].RecordID != result[j].RecordID {
			return result[i].RecordID < result[j].RecordID
		}
		return result[i].HorizonBlocks < result[j].HorizonBlocks
	})
	return result
}

func temporalSupportRows(groups map[string][]temporalMatch, primaryCaliper, sensitivityCaliper float64, minimum int, extremeTailCutoff float64) []TemporalSupport {
	keys := sortedKeys(groups)
	lsis := make([]float64, 0, len(keys))
	for _, key := range keys {
		lsis = append(lsis, groups[key][0].BurnImmediateLSISBps)
	}
	sortedLSIS := append([]float64(nil), lsis...)
	sort.Float64s(sortedLSIS)
	result := make([]TemporalSupport, 0, len(keys))
	for _, key := range keys {
		matches := groups[key]
		distances := make([]float64, 0, len(matches))
		primary := 0
		sensitivity := 0
		for _, match := range matches {
			distances = append(distances, match.MatchDistance)
			if match.MatchDistance <= primaryCaliper {
				primary++
			}
			if match.MatchDistance <= sensitivityCaliper {
				sensitivity++
			}
		}
		sort.Float64s(distances)
		included := primary >= minimum
		percentile := percentileRank(sortedLSIS, matches[0].BurnImmediateLSISBps)
		row := TemporalSupport{
			BurnEventKey: key, BurnBlock: matches[0].BurnBlock, BurnLogIndex: matches[0].BurnLogIndex,
			Stratum: matches[0].Stratum, BurnRangeActive: matches[0].BurnRangeActive,
			ImmediateLSISBps: matches[0].BurnImmediateLSISBps, LSISPercentileRank: percentile,
			SelectedControls: len(matches), WithinPrimaryCaliperControls: primary,
			WithinSensitivityCaliperControls: sensitivity, MinimumControlsRequired: minimum,
			MinimumMatchDistance: distances[0], MeanMatchDistance: mean(distances), MaximumMatchDistance: distances[len(distances)-1],
			PrimaryIncluded: included,
		}
		if included {
			row.SupportStatus = "common_support"
		} else {
			row.SupportStatus = "outside_common_support"
			row.ExclusionReason = fmt.Sprintf("within_primary_caliper_controls=%d required=%d", primary, minimum)
			row.ExtremeTailEvent = percentile >= extremeTailCutoff
		}
		result = append(result, row)
	}
	return result
}

func matchedSupportRows(pairs []matchedPair, primaryCaliper, sensitivityCaliper float64) []MatchedSupport {
	result := make([]MatchedSupport, 0, len(pairs))
	for _, pair := range pairs {
		primary := pair.MatchDistance <= primaryCaliper
		row := MatchedSupport{
			PairID: pair.PairID, Stratum: pair.Stratum, HighEventKey: pair.HighEventKey, LowEventKey: pair.LowEventKey,
			HighImmediateLSISBps: pair.HighImmediateLSISBps, LowImmediateLSISBps: pair.LowImmediateLSISBps,
			MatchDistance: pair.MatchDistance, PrimaryIncluded: primary,
			SensitivityIncluded: pair.MatchDistance <= sensitivityCaliper,
		}
		if primary {
			row.SupportStatus = "common_support"
		} else {
			row.SupportStatus = "outside_common_support"
		}
		result = append(result, row)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].PairID < result[j].PairID })
	return result
}

func buildManifest(inputs parsedInputs, config Config, gates []BalanceGate, inference []Inference, temporal []TemporalSupport, matched []MatchedSupport) Manifest {
	manifest := Manifest{
		Status: "completed", Version: Version, PoolAddress: inputs.Manifest.PoolAddress,
		FromBlock: inputs.Manifest.FromBlock, ToBlock: inputs.Manifest.ToBlock, IndexedThrough: inputs.Manifest.IndexedThrough,
		TemporalPrimaryCaliper: config.TemporalPrimaryCaliper, TemporalSensitivityCaliper: config.TemporalSensitivityCaliper,
		MatchedPrimaryCaliper: config.MatchedPrimaryCaliper, MatchedSensitivityCaliper: config.MatchedSensitivityCaliper,
		MinimumTemporalControls: config.MinimumTemporalControls, MaximumAbsoluteSMD: config.MaximumAbsoluteSMD,
		Alpha: config.Alpha, BootstrapIterations: config.BootstrapIterations,
		PermutationIterations: config.PermutationIterations, RandomSeed: config.RandomSeed,
		TemporalBurns: len(temporal), MatchedFullPairs: len(matched), BalanceGateRows: len(gates),
	}
	for _, row := range temporal {
		if row.PrimaryIncluded {
			manifest.TemporalPrimarySupportBurns++
		} else {
			manifest.TemporalOutsideSupportBurns++
		}
		if row.ExtremeTailEvent {
			manifest.TemporalExtremeTailBurns++
		}
	}
	for _, row := range matched {
		if row.PrimaryIncluded {
			manifest.MatchedPrimarySupportPairs++
		}
		if row.SensitivityIncluded {
			manifest.MatchedSensitivityPairs++
		}
	}
	for _, gate := range gates {
		if gate.GateStatus == GatePassed {
			manifest.BalanceGatePassed++
		}
		if gate.GateStatus == GateFailed {
			manifest.BalanceGateFailed++
		}
	}
	for _, row := range inference {
		if row.AnalysisRole == RolePrimary || row.AnalysisRole == RolePrimaryBlock {
			manifest.PrimaryInferenceRows++
		}
		if row.PrimaryEligible {
			manifest.PrimaryEligibleRows++
		}
		if row.AnalysisRole == RolePrimaryBlock {
			manifest.PrimaryBlockedRows++
		}
		switch row.PublicationStatus {
		case "primary_robust":
			manifest.PrimaryRobustRows++
		case "primary_supported":
			manifest.PrimarySupportedRows++
		case "primary_inconclusive":
			manifest.PrimaryInconclusiveRows++
		}
	}
	return manifest
}

func safeLog1p(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Log1p(value)
}

func featureValues[T any](items []T, value func(T) float64) []float64 {
	result := make([]float64, len(items))
	for index, item := range items {
		result[index] = value(item)
	}
	return result
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func percentileRank(sorted []float64, value float64) float64 {
	if len(sorted) <= 1 {
		return 1
	}
	upper := sort.Search(len(sorted), func(index int) bool { return sorted[index] > value })
	return float64(upper-1) / float64(len(sorted)-1)
}
