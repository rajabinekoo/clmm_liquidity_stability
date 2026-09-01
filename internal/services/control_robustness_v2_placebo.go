package services

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type ControlRobustnessV2Service struct {
	provider     TemporalPlaceboProvider
	curveService *PriceImpactCurveService
}

func NewControlRobustnessV2Service(provider TemporalPlaceboProvider, curveService *PriceImpactCurveService) *ControlRobustnessV2Service {
	return &ControlRobustnessV2Service{provider: provider, curveService: curveService}
}

func normalizeControlRobustnessV2Config(config ControlRobustnessV2Config, maximumHorizon uint64) ControlRobustnessV2Config {
	defaults := DefaultControlRobustnessV2Config(maximumHorizon)
	if config.CandidateStrideBlocks == 0 {
		config.CandidateStrideBlocks = defaults.CandidateStrideBlocks
	}
	if config.LiquidityActionExclusionBlocks == 0 {
		config.LiquidityActionExclusionBlocks = defaults.LiquidityActionExclusionBlocks
	}
	if config.BurnPlaceboSeparationBlocks <= maximumHorizon {
		config.BurnPlaceboSeparationBlocks = defaults.BurnPlaceboSeparationBlocks
	}
	if config.ControlsPerBurn <= 0 {
		config.ControlsPerBurn = defaults.ControlsPerBurn
	}
	if config.MinimumControlsForCommonSupport <= 0 || config.MinimumControlsForCommonSupport > config.ControlsPerBurn {
		config.MinimumControlsForCommonSupport = defaults.MinimumControlsForCommonSupport
		if config.MinimumControlsForCommonSupport > config.ControlsPerBurn {
			config.MinimumControlsForCommonSupport = config.ControlsPerBurn
		}
	}
	if config.MaximumCandidateReuse <= 0 {
		config.MaximumCandidateReuse = defaults.MaximumCandidateReuse
	}
	if config.TemporalPlaceboCaliper <= 0 || math.IsNaN(config.TemporalPlaceboCaliper) || math.IsInf(config.TemporalPlaceboCaliper, 0) {
		config.TemporalPlaceboCaliper = defaults.TemporalPlaceboCaliper
	}
	if config.MatchedBurnCaliper <= 0 || math.IsNaN(config.MatchedBurnCaliper) || math.IsInf(config.MatchedBurnCaliper, 0) {
		config.MatchedBurnCaliper = defaults.MatchedBurnCaliper
	}
	if config.PermutationIterations <= 0 {
		config.PermutationIterations = defaults.PermutationIterations
	}
	if config.BootstrapIterations <= 0 {
		config.BootstrapIterations = defaults.BootstrapIterations
	}
	if config.RandomSeed == 0 {
		config.RandomSeed = defaults.RandomSeed
	}
	return config
}

func (s *ControlRobustnessV2Service) Build(ctx context.Context, req ControlRobustnessV2Request) (ControlRobustnessV2Report, error) {
	if s == nil || s.provider == nil || s.curveService == nil || s.curveService.simulator == nil {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: service is incomplete")
	}
	if err := validateBurnRealizedDatasetReport(req.Realized); err != nil {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: invalid realized dataset: %w", err)
	}
	if len(req.Collection.Samples) == 0 {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: burn samples are empty")
	}
	if normalizeAddress(req.Collection.PoolAddress) != normalizeAddress(req.Realized.PoolAddress) {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: collection and realized pools differ")
	}

	horizons, err := normalizeBurnOutcomeHorizons(req.Collection.FromBlock, req.Horizons)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}
	zeroForOneAmounts, err := normalizeBurnAmountGrid("control robustness v2 zero_for_one", req.ZeroForOneAmountsIn)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}
	oneForZeroAmounts, err := normalizeBurnAmountGrid("control robustness v2 one_for_zero", req.OneForZeroAmountsIn)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}
	thresholds, err := normalizeBurnThresholds(req.ThresholdsBps)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}
	maximumHorizon := horizons[len(horizons)-1].Blocks
	config := normalizeControlRobustnessV2Config(req.Config, maximumHorizon)

	report := ControlRobustnessV2Report{}
	report.Manifest = ControlRobustnessV2Manifest{
		Status:                          "completed",
		PoolAddress:                     req.Realized.PoolAddress,
		FromBlock:                       req.Realized.FromBlock,
		ToBlock:                         req.Realized.ToBlock,
		CandidateStrideBlocks:           config.CandidateStrideBlocks,
		ControlsPerBurn:                 config.ControlsPerBurn,
		MinimumControlsForCommonSupport: config.MinimumControlsForCommonSupport,
		MaximumCandidateReuse:           config.MaximumCandidateReuse,
		TemporalPlaceboCaliper:          config.TemporalPlaceboCaliper,
		BurnPlaceboSeparationBlocks:     config.BurnPlaceboSeparationBlocks,
		MatchedBurnCaliper:              config.MatchedBurnCaliper,
		PermutationIterations:           config.PermutationIterations,
		BootstrapIterations:             config.BootstrapIterations,
		RandomSeed:                      config.RandomSeed,
	}

	head, err := s.provider.IndexedHead(ctx)
	if err != nil {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: indexed head: %w", err)
	}
	report.Manifest.IndexedThrough = head.BlockNumber
	if req.Realized.ToBlock > math.MaxUint64-maximumHorizon || head.BlockNumber < req.Realized.ToBlock+maximumHorizon {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: local index does not cover all horizons")
	}

	actualByKey, err := indexTemporalPlaceboActualObservations(req.Collection.Samples, horizons, req.Realized.Observations)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}
	counterfactualByKey, err := indexTemporalPlaceboCounterfactualObservations(req.Counterfactual.Observations)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}

	startCursor := domain.EventCursor{BlockNumber: req.Realized.FromBlock - 1, LogIndex: math.MaxInt32}
	studyEvents, err := s.provider.PoolEventsAfterCursorThroughBlock(ctx, req.Realized.PoolAddress, startCursor, req.Realized.ToBlock)
	if err != nil {
		return ControlRobustnessV2Report{}, fmt.Errorf("build control robustness v2: load local study events: %w", err)
	}
	actionBlocks := temporalPlaceboLiquidityActionBlocks(studyEvents, req.Collection.Samples)
	candidateBlocks := buildDenseTemporalPlaceboCandidateBlocks(
		req.Realized.FromBlock,
		req.Realized.ToBlock,
		config.CandidateStrideBlocks,
		config.LiquidityActionExclusionBlocks,
		actionBlocks,
	)
	report.Manifest.CandidateBlocks = len(candidateBlocks)

	legacyService := NewTemporalPlaceboService(s.provider, s.curveService)
	states, stateSkips := legacyService.buildCandidateStates(
		ctx,
		req.Realized.PoolAddress,
		candidateBlocks,
		zeroForOneAmounts,
		oneForZeroAmounts,
		thresholds,
		req.AmountGridResolver,
	)
	report.TemporalCandidateSkips = stateSkips
	report.Manifest.CandidateStates = len(states)
	report.Manifest.CandidateStateSkips = len(stateSkips)

	matches, exclusions := matchRobustTemporalPlacebos(req.Collection.Samples, states, config)
	report.TemporalMatches = matches
	report.TemporalExclusions = exclusions
	report.Manifest.TemporalMatches = len(matches)

	legacyMatches := make([]TemporalPlaceboMatch, len(matches))
	matchMetadata := make(map[string]RobustTemporalPlaceboMatch, len(matches))
	for index, match := range matches {
		legacyMatches[index] = cloneTemporalPlaceboMatch(match.Match)
		matchMetadata[match.Match.MatchID] = match
	}
	observations := legacyService.buildObservations(
		ctx,
		req.Realized.PoolAddress,
		legacyMatches,
		states,
		horizons,
		zeroForOneAmounts,
		oneForZeroAmounts,
		thresholds,
		actualByKey,
		counterfactualByKey,
	)
	report.TemporalObservations = observations
	report.TemporalAggregates = buildRobustTemporalPlaceboAggregates(
		req.Collection.Samples,
		horizons,
		observations,
		matchMetadata,
		config,
	)
	report.TemporalBalance = buildRobustTemporalPlaceboBalance(req.Collection.Samples, matches, config)
	report.TemporalInference = buildRobustTemporalPlaceboInference(report.TemporalAggregates, config)

	commonSupportBurns := make(map[string]struct{})
	excludedBurns := make(map[string]struct{})
	for _, aggregate := range report.TemporalAggregates {
		if aggregate.AnalysisSet != ControlAnalysisCommonSupport || aggregate.HorizonLabel != horizons[0].Label {
			continue
		}
		if aggregate.Included {
			commonSupportBurns[aggregate.Burn.EventKey()] = struct{}{}
		} else {
			excludedBurns[aggregate.Burn.EventKey()] = struct{}{}
		}
	}
	report.Manifest.TemporalCommonSupportBurns = len(commonSupportBurns)
	report.Manifest.TemporalExcludedBurns = len(excludedBurns)

	matchedPairs, matchedOutcomes, matchedBalance, matchedInference, err := buildRobustMatchedBurnControls(
		req.Collection,
		req.Realized,
		req.Counterfactual,
		horizons,
		config,
	)
	if err != nil {
		return ControlRobustnessV2Report{}, err
	}
	report.MatchedBurnPairs = matchedPairs
	report.MatchedBurnOutcomes = matchedOutcomes
	report.MatchedBurnBalance = matchedBalance
	report.MatchedBurnInference = matchedInference
	for _, pair := range matchedPairs {
		switch pair.AnalysisSet {
		case ControlAnalysisFullSensitivity:
			report.Manifest.MatchedBurnFullPairs++
		case ControlAnalysisCommonSupport:
			report.Manifest.MatchedBurnCommonSupportPairs++
		}
	}
	return report, nil
}

func buildDenseTemporalPlaceboCandidateBlocks(fromBlock, toBlock, stride, exclusion uint64, actionBlocks []uint64) []uint64 {
	if fromBlock <= 1 || toBlock < fromBlock || stride == 0 {
		return nil
	}
	offset := stride / 2
	if offset == 0 {
		offset = 1
	}
	start := fromBlock
	if offset <= math.MaxUint64-fromBlock {
		start = fromBlock + offset
	}
	result := make([]uint64, 0, int((toBlock-fromBlock)/stride)+1)
	for block := start; block <= toBlock; {
		if block > 1 && !temporalPlaceboNearLiquidityAction(block, actionBlocks, exclusion) {
			result = append(result, block)
		}
		if stride > toBlock-block {
			break
		}
		block += stride
	}
	return result
}

type robustTemporalCandidate struct {
	index    int
	distance float64
}

type robustTemporalBurnOrder struct {
	index              int
	withinCaliperCount int
	nearestDistance    float64
	key                string
}

func matchRobustTemporalPlacebos(samples []BurnEventSample, states []temporalPlaceboState, config ControlRobustnessV2Config) ([]RobustTemporalPlaceboMatch, []RobustTemporalPlaceboExclusion) {
	if len(samples) == 0 {
		return nil, nil
	}
	if len(states) == 0 {
		exclusions := make([]RobustTemporalPlaceboExclusion, 0, len(samples))
		for _, sample := range samples {
			exclusions = append(exclusions, RobustTemporalPlaceboExclusion{
				BurnEventKey: sample.Burn.EventKey(),
				BurnBlock:    sample.Burn.Cursor.BlockNumber,
				Reason:       "no_candidate_states",
				Detail:       "no valid dense temporal placebo candidate states were available",
			})
		}
		return nil, exclusions
	}

	actualFeatures := make([]temporalPlaceboFeature, len(samples))
	candidateFeatures := make([]temporalPlaceboFeature, len(states))
	allFeatures := make([]temporalPlaceboFeature, 0, len(samples)+len(states))
	for index, sample := range samples {
		actualFeatures[index] = temporalPlaceboFeatureFromBurn(sample)
		allFeatures = append(allFeatures, actualFeatures[index])
	}
	for index, state := range states {
		candidateFeatures[index] = temporalPlaceboFeatureFromState(state)
		allFeatures = append(allFeatures, candidateFeatures[index])
	}
	scales := temporalPlaceboFeatureScales(allFeatures)

	candidatesByBurn := make([][]robustTemporalCandidate, len(samples))
	order := make([]robustTemporalBurnOrder, len(samples))
	for sampleIndex, sample := range samples {
		candidates := make([]robustTemporalCandidate, 0, len(states))
		within := 0
		nearest := math.Inf(1)
		for stateIndex, state := range states {
			blockDistance := absUint64Diff(sample.Burn.Cursor.BlockNumber, state.AnchorBlock)
			if blockDistance < config.BurnPlaceboSeparationBlocks {
				continue
			}
			distance := temporalPlaceboDistance(actualFeatures[sampleIndex], candidateFeatures[stateIndex], scales)
			if distance <= config.TemporalPlaceboCaliper {
				within++
			}
			if distance < nearest {
				nearest = distance
			}
			candidates = append(candidates, robustTemporalCandidate{index: stateIndex, distance: distance})
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].distance == candidates[j].distance {
				return states[candidates[i].index].AnchorBlock < states[candidates[j].index].AnchorBlock
			}
			return candidates[i].distance < candidates[j].distance
		})
		candidatesByBurn[sampleIndex] = candidates
		order[sampleIndex] = robustTemporalBurnOrder{
			index:              sampleIndex,
			withinCaliperCount: within,
			nearestDistance:    nearest,
			key:                sample.Burn.EventKey(),
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].withinCaliperCount != order[j].withinCaliperCount {
			return order[i].withinCaliperCount < order[j].withinCaliperCount
		}
		if order[i].nearestDistance != order[j].nearestDistance {
			return order[i].nearestDistance > order[j].nearestDistance
		}
		return order[i].key < order[j].key
	})

	usage := make([]int, len(states))
	selectedByBurn := make([][]robustTemporalCandidate, len(samples))
	for _, burnOrder := range order {
		selected := make([]robustTemporalCandidate, 0, config.ControlsPerBurn)
		for _, candidate := range candidatesByBurn[burnOrder.index] {
			if usage[candidate.index] >= config.MaximumCandidateReuse {
				continue
			}
			selected = append(selected, candidate)
			usage[candidate.index]++
			if len(selected) == config.ControlsPerBurn {
				break
			}
		}
		selectedByBurn[burnOrder.index] = selected
	}

	matches := make([]RobustTemporalPlaceboMatch, 0, len(samples)*config.ControlsPerBurn)
	exclusions := make([]RobustTemporalPlaceboExclusion, 0)
	for sampleIndex, sample := range samples {
		selected := selectedByBurn[sampleIndex]
		if len(selected) == 0 {
			exclusions = append(exclusions, RobustTemporalPlaceboExclusion{
				BurnEventKey: sample.Burn.EventKey(),
				BurnBlock:    sample.Burn.Cursor.BlockNumber,
				Reason:       "no_eligible_control",
				Detail:       "all candidates were inside the burn-overlap exclusion window or exhausted their reuse capacity",
			})
			continue
		}
		within := 0
		for rank, candidate := range selected {
			state := states[candidate.index]
			if candidate.distance <= config.TemporalPlaceboCaliper {
				within++
			}
			matchID := fmt.Sprintf("robust_placebo_%03d_%02d", sampleIndex+1, rank+1)
			legacy := TemporalPlaceboMatch{
				MatchID:                     matchID,
				Burn:                        cloneBurnCandidate(sample.Burn),
				BurnRangeActive:             sample.BurnRangeActive,
				BurnRangeLocation:           sample.RangeLocation,
				PlaceboAnchorBlock:          state.AnchorBlock,
				PlaceboReferenceBlock:       state.ReferenceBlock,
				MatchDistance:               candidate.distance,
				BlockDistance:               absUint64Diff(sample.Burn.Cursor.BlockNumber, state.AnchorBlock),
				BurnCurrentTick:             sample.CurrentTick,
				PlaceboCurrentTick:          state.Pool.CurrentTick,
				BurnActiveLiquidity:         new(big.Int).Set(sample.ActiveLiquidityBeforeBurn),
				PlaceboActiveLiquidity:      new(big.Int).Set(state.Pool.Liquidity),
				BurnBaselineTotalAUCBps:     sample.ZeroForOne.BaseAUCBps.Add(sample.OneForZero.BaseAUCBps),
				BurnImmediateTotalLSISBps:   sample.TotalLSISBps,
				PlaceboBaselineTotalAUCBps:  state.TotalAUCBps,
				BurnDirectionalImbalance:    temporalBurnDirectionalImbalance(sample),
				PlaceboDirectionalImbalance: state.DirectionalImbalance,
			}
			matches = append(matches, RobustTemporalPlaceboMatch{
				Match:                  legacy,
				Rank:                   rank + 1,
				WithinCaliper:          candidate.distance <= config.TemporalPlaceboCaliper,
				CandidateFinalUseCount: usage[candidate.index],
			})
		}
		if len(selected) < config.ControlsPerBurn {
			exclusions = append(exclusions, RobustTemporalPlaceboExclusion{
				BurnEventKey: sample.Burn.EventKey(),
				BurnBlock:    sample.Burn.Cursor.BlockNumber,
				Reason:       "fewer_than_requested_controls",
				Detail:       fmt.Sprintf("selected %d of %d requested controls", len(selected), config.ControlsPerBurn),
			})
		}
		if within < config.MinimumControlsForCommonSupport {
			exclusions = append(exclusions, RobustTemporalPlaceboExclusion{
				BurnEventKey: sample.Burn.EventKey(),
				BurnBlock:    sample.Burn.Cursor.BlockNumber,
				Reason:       "outside_common_support",
				Detail:       fmt.Sprintf("only %d selected controls are within caliper %.6g; minimum is %d", within, config.TemporalPlaceboCaliper, config.MinimumControlsForCommonSupport),
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := matches[i].Match.Burn.Cursor
		right := matches[j].Match.Burn.Cursor
		if left.Equal(right) {
			return matches[i].Rank < matches[j].Rank
		}
		return left.Before(right)
	})
	sort.SliceStable(exclusions, func(i, j int) bool {
		if exclusions[i].BurnBlock == exclusions[j].BurnBlock {
			if exclusions[i].BurnEventKey == exclusions[j].BurnEventKey {
				return exclusions[i].Reason < exclusions[j].Reason
			}
			return exclusions[i].BurnEventKey < exclusions[j].BurnEventKey
		}
		return exclusions[i].BurnBlock < exclusions[j].BurnBlock
	})
	return matches, exclusions
}

func buildRobustTemporalPlaceboAggregates(
	samples []BurnEventSample,
	horizons []BurnOutcomeHorizon,
	observations []TemporalPlaceboObservation,
	metadata map[string]RobustTemporalPlaceboMatch,
	config ControlRobustnessV2Config,
) []RobustTemporalPlaceboAggregate {
	type key struct {
		burnKey string
		label   string
	}
	groups := make(map[key][]TemporalPlaceboObservation)
	for _, observation := range observations {
		groups[key{observation.Match.Burn.EventKey(), observation.HorizonLabel}] = append(
			groups[key{observation.Match.Burn.EventKey(), observation.HorizonLabel}],
			observation,
		)
	}
	result := make([]RobustTemporalPlaceboAggregate, 0, len(samples)*len(horizons)*2)
	for _, sample := range samples {
		for _, horizon := range horizons {
			rows := groups[key{sample.Burn.EventKey(), horizon.Label}]
			for _, analysisSet := range []string{ControlAnalysisFullSensitivity, ControlAnalysisCommonSupport} {
				aggregate := RobustTemporalPlaceboAggregate{
					AnalysisSet:           analysisSet,
					Burn:                  cloneBurnCandidate(sample.Burn),
					BurnRangeLocation:     sample.RangeLocation,
					BurnRangeActive:       sample.BurnRangeActive,
					HorizonLabel:          horizon.Label,
					HorizonBlocks:         horizon.Blocks,
					RequestedControls:     config.ControlsPerBurn,
					ImmediateTotalLSISBps: sample.TotalLSISBps,
				}
				selected := make([]TemporalPlaceboObservation, 0, len(rows))
				for _, row := range rows {
					meta, exists := metadata[row.Match.MatchID]
					if !exists {
						continue
					}
					aggregate.SelectedControls++
					if meta.WithinCaliper {
						aggregate.WithinCaliperControls++
					}
					if analysisSet == ControlAnalysisCommonSupport && !meta.WithinCaliper {
						continue
					}
					if row.Available {
						selected = append(selected, row)
					}
				}
				aggregate.AvailableControls = len(selected)
				required := 1
				if analysisSet == ControlAnalysisCommonSupport {
					required = config.MinimumControlsForCommonSupport
				}
				if len(selected) < required {
					aggregate.ExclusionReason = fmt.Sprintf("available_controls=%d required=%d", len(selected), required)
					result = append(result, aggregate)
					continue
				}
				distances := make([]float64, 0, len(selected))
				placeboValues := make([]decimal.Decimal, 0, len(selected))
				for _, row := range selected {
					distances = append(distances, row.Match.MatchDistance)
					placeboValues = append(placeboValues, row.PlaceboTotalRealizedDeteriorationBps)
					if row.Match.MatchDistance > aggregate.MaximumMatchDistance {
						aggregate.MaximumMatchDistance = row.Match.MatchDistance
					}
				}
				aggregate.MeanMatchDistance = mean(distances)
				aggregate.MeanPlaceboRealizedDeteriorationBps = decimalMean(placeboValues)
				aggregate.ActualRealizedDeteriorationBps = selected[0].ActualTotalRealizedDeteriorationBps
				aggregate.ExcessDeteriorationBps = aggregate.ActualRealizedDeteriorationBps.Sub(aggregate.MeanPlaceboRealizedDeteriorationBps)
				aggregate.ActualCounterfactualAvailable = selected[0].ActualCounterfactualAvailable
				if aggregate.ActualCounterfactualAvailable {
					aggregate.ActualMechanicalMidpointEffectBps = selected[0].ActualMinMechanicalEffectPIAUCBps.Add(
						selected[0].ActualMaxMechanicalEffectPIAUCBps,
					).Div(decimal.NewFromInt(2))
				}
				aggregate.Included = true
				result = append(result, aggregate)
			}
		}
	}
	return result
}

func robustTemporalControlFeature(match RobustTemporalPlaceboMatch) temporalPlaceboFeature {
	return temporalPlaceboFeature{
		BlockNumber:          float64(match.Match.PlaceboAnchorBlock),
		Tick:                 float64(match.Match.PlaceboCurrentTick),
		LogLiquidity:         logBigInt(match.Match.PlaceboActiveLiquidity),
		LogAUC:               logPositiveDecimal(match.Match.PlaceboBaselineTotalAUCBps),
		DirectionalImbalance: decimalToFloat(match.Match.PlaceboDirectionalImbalance),
	}
}

func buildRobustTemporalPlaceboBalance(samples []BurnEventSample, matches []RobustTemporalPlaceboMatch, config ControlRobustnessV2Config) []RobustControlBalance {
	sampleByKey := make(map[string]BurnEventSample, len(samples))
	for _, sample := range samples {
		sampleByKey[sample.Burn.EventKey()] = sample
	}
	matchesByBurn := make(map[string][]RobustTemporalPlaceboMatch)
	for _, match := range matches {
		matchesByBurn[match.Match.Burn.EventKey()] = append(matchesByBurn[match.Match.Burn.EventKey()], match)
	}
	metrics := []struct {
		name  string
		value func(temporalPlaceboFeature) float64
	}{
		{"block_number", func(value temporalPlaceboFeature) float64 { return value.BlockNumber }},
		{"current_tick", func(value temporalPlaceboFeature) float64 { return value.Tick }},
		{"log_active_liquidity", func(value temporalPlaceboFeature) float64 { return value.LogLiquidity }},
		{"log_baseline_total_auc", func(value temporalPlaceboFeature) float64 { return value.LogAUC }},
		{"directional_imbalance", func(value temporalPlaceboFeature) float64 { return value.DirectionalImbalance }},
	}
	result := make([]RobustControlBalance, 0, 2*4*len(metrics))
	for _, analysisSet := range []string{ControlAnalysisFullSensitivity, ControlAnalysisCommonSupport} {
		for _, stratum := range []string{"all", string(BurnRangeActive), string(BurnRangeBelowCurrentTick), string(BurnRangeAboveCurrentTick)} {
			treated := make([]temporalPlaceboFeature, 0)
			controls := make([]temporalPlaceboFeature, 0)
			keys := make([]string, 0, len(matchesByBurn))
			for key := range matchesByBurn {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				sample, exists := sampleByKey[key]
				if !exists || (stratum != "all" && string(sample.RangeLocation) != stratum) {
					continue
				}
				selected := make([]temporalPlaceboFeature, 0)
				for _, match := range matchesByBurn[key] {
					if analysisSet == ControlAnalysisCommonSupport && !match.WithinCaliper {
						continue
					}
					selected = append(selected, robustTemporalControlFeature(match))
				}
				required := 1
				if analysisSet == ControlAnalysisCommonSupport {
					required = config.MinimumControlsForCommonSupport
				}
				if len(selected) < required {
					continue
				}
				treated = append(treated, temporalPlaceboFeatureFromBurn(sample))
				controls = append(controls, meanTemporalPlaceboFeature(selected))
			}
			for _, metric := range metrics {
				treatedValues := make([]float64, len(treated))
				controlValues := make([]float64, len(controls))
				for index := range treated {
					treatedValues[index] = metric.value(treated[index])
					controlValues[index] = metric.value(controls[index])
				}
				smd := standardizedMeanDifference(treatedValues, controlValues)
				status := controlBalanceStatus(math.Abs(smd))
				if len(treatedValues) < 2 || len(controlValues) < 2 {
					status = ControlBalanceFailed
				}
				result = append(result, RobustControlBalance{
					ControlType:  "temporal_placebo",
					AnalysisSet:  analysisSet,
					Stratum:      stratum,
					Metric:       metric.name,
					TreatedCount: len(treatedValues),
					ControlCount: len(controlValues),
					TreatedMean:  meanOrZero(treatedValues),
					ControlMean:  meanOrZero(controlValues),
					SMD:          smd,
					AbsoluteSMD:  math.Abs(smd),
					Status:       status,
				})
			}
		}
	}
	return result
}

func meanTemporalPlaceboFeature(values []temporalPlaceboFeature) temporalPlaceboFeature {
	if len(values) == 0 {
		return temporalPlaceboFeature{}
	}
	var result temporalPlaceboFeature
	for _, value := range values {
		result.BlockNumber += value.BlockNumber
		result.Tick += value.Tick
		result.LogLiquidity += value.LogLiquidity
		result.LogAUC += value.LogAUC
		result.DirectionalImbalance += value.DirectionalImbalance
	}
	denominator := float64(len(values))
	result.BlockNumber /= denominator
	result.Tick /= denominator
	result.LogLiquidity /= denominator
	result.LogAUC /= denominator
	result.DirectionalImbalance /= denominator
	return result
}

func buildRobustTemporalPlaceboInference(aggregates []RobustTemporalPlaceboAggregate, config ControlRobustnessV2Config) []RobustControlInference {
	type groupKey struct {
		analysisSet string
		stratum     string
		label       string
		blocks      uint64
	}
	groups := make(map[groupKey][]float64)
	for _, aggregate := range aggregates {
		if !aggregate.Included {
			continue
		}
		for _, stratum := range []string{"all", string(aggregate.BurnRangeLocation)} {
			key := groupKey{aggregate.AnalysisSet, stratum, aggregate.HorizonLabel, aggregate.HorizonBlocks}
			groups[key] = append(groups[key], decimalToFloat(aggregate.ExcessDeteriorationBps))
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
		if keys[i].stratum != keys[j].stratum {
			return keys[i].stratum < keys[j].stratum
		}
		return keys[i].blocks < keys[j].blocks
	})
	result := make([]RobustControlInference, 0, len(keys))
	for _, key := range keys {
		result = append(result, buildRobustInferenceRow(
			"temporal_placebo",
			key.analysisSet,
			"excess_realized_deterioration_bps",
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

func controlRobustnessFailureManifest(req ControlRobustnessV2Request, detail string) ControlRobustnessV2Manifest {
	maximumHorizon := uint64(0)
	for _, horizon := range req.Horizons {
		if horizon.Blocks > maximumHorizon {
			maximumHorizon = horizon.Blocks
		}
	}
	config := normalizeControlRobustnessV2Config(req.Config, maximumHorizon)
	return ControlRobustnessV2Manifest{
		Status:                          "failed",
		FailureDetail:                   strings.TrimSpace(detail),
		PoolAddress:                     req.Realized.PoolAddress,
		FromBlock:                       req.Realized.FromBlock,
		ToBlock:                         req.Realized.ToBlock,
		CandidateStrideBlocks:           config.CandidateStrideBlocks,
		ControlsPerBurn:                 config.ControlsPerBurn,
		MinimumControlsForCommonSupport: config.MinimumControlsForCommonSupport,
		MaximumCandidateReuse:           config.MaximumCandidateReuse,
		TemporalPlaceboCaliper:          config.TemporalPlaceboCaliper,
		BurnPlaceboSeparationBlocks:     config.BurnPlaceboSeparationBlocks,
		MatchedBurnCaliper:              config.MatchedBurnCaliper,
		PermutationIterations:           config.PermutationIterations,
		BootstrapIterations:             config.BootstrapIterations,
		RandomSeed:                      config.RandomSeed,
	}
}
