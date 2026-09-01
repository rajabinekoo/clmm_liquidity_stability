package services

import (
	"fmt"
	"strconv"
)

func WriteControlRobustnessV2ManifestCSV(path string, row ControlRobustnessV2Manifest) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"status", "failure_detail", "pool_address", "from_block", "to_block", "indexed_through",
		"candidate_stride_blocks", "candidate_blocks", "candidate_states", "candidate_state_skips",
		"controls_per_burn", "minimum_controls_for_common_support", "maximum_candidate_reuse",
		"temporal_placebo_caliper", "burn_placebo_separation_blocks", "temporal_matches",
		"temporal_common_support_burns", "temporal_excluded_burns", "matched_burn_caliper",
		"matched_burn_full_pairs", "matched_burn_common_support_pairs", "permutation_iterations",
		"bootstrap_iterations", "random_seed",
	})
	if err != nil {
		return err
	}
	if err := writer.Write([]string{
		row.Status,
		row.FailureDetail,
		row.PoolAddress,
		strconv.FormatUint(row.FromBlock, 10),
		strconv.FormatUint(row.ToBlock, 10),
		strconv.FormatUint(row.IndexedThrough, 10),
		strconv.FormatUint(row.CandidateStrideBlocks, 10),
		strconv.Itoa(row.CandidateBlocks),
		strconv.Itoa(row.CandidateStates),
		strconv.Itoa(row.CandidateStateSkips),
		strconv.Itoa(row.ControlsPerBurn),
		strconv.Itoa(row.MinimumControlsForCommonSupport),
		strconv.Itoa(row.MaximumCandidateReuse),
		burnControlFloat(row.TemporalPlaceboCaliper),
		strconv.FormatUint(row.BurnPlaceboSeparationBlocks, 10),
		strconv.Itoa(row.TemporalMatches),
		strconv.Itoa(row.TemporalCommonSupportBurns),
		strconv.Itoa(row.TemporalExcludedBurns),
		burnControlFloat(row.MatchedBurnCaliper),
		strconv.Itoa(row.MatchedBurnFullPairs),
		strconv.Itoa(row.MatchedBurnCommonSupportPairs),
		strconv.Itoa(row.PermutationIterations),
		strconv.Itoa(row.BootstrapIterations),
		strconv.FormatInt(row.RandomSeed, 10),
	}); err != nil {
		return err
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustTemporalPlaceboMatchesCSV(path string, rows []RobustTemporalPlaceboMatch) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"match_id", "rank", "within_caliper", "candidate_final_use_count", "pool_address",
		"burn_block", "burn_log_index", "burn_event_key", "burn_range_location", "burn_range_active",
		"placebo_anchor_block", "placebo_reference_block", "match_distance", "block_distance",
		"burn_current_tick", "placebo_current_tick", "burn_active_liquidity", "placebo_active_liquidity",
		"burn_baseline_total_auc_bps", "burn_immediate_total_lsis_bps", "placebo_baseline_total_auc_bps",
		"burn_directional_imbalance", "placebo_directional_imbalance",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		match := row.Match
		if err := writer.Write([]string{
			match.MatchID,
			strconv.Itoa(row.Rank),
			burnControlBool(row.WithinCaliper),
			strconv.Itoa(row.CandidateFinalUseCount),
			match.Burn.PoolAddress,
			strconv.FormatUint(match.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(match.Burn.Cursor.LogIndex),
			match.Burn.EventKey(),
			string(match.BurnRangeLocation),
			burnControlBool(match.BurnRangeActive),
			strconv.FormatUint(match.PlaceboAnchorBlock, 10),
			strconv.FormatUint(match.PlaceboReferenceBlock, 10),
			burnControlFloat(match.MatchDistance),
			strconv.FormatUint(match.BlockDistance, 10),
			strconv.Itoa(match.BurnCurrentTick),
			strconv.Itoa(match.PlaceboCurrentTick),
			burnControlBigInt(match.BurnActiveLiquidity),
			burnControlBigInt(match.PlaceboActiveLiquidity),
			match.BurnBaselineTotalAUCBps.String(),
			match.BurnImmediateTotalLSISBps.String(),
			match.PlaceboBaselineTotalAUCBps.String(),
			match.BurnDirectionalImbalance.String(),
			match.PlaceboDirectionalImbalance.String(),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustTemporalPlaceboObservationsCSV(path string, rows []TemporalPlaceboObservation, matches []RobustTemporalPlaceboMatch) error {
	metadata := make(map[string]RobustTemporalPlaceboMatch, len(matches))
	for _, match := range matches {
		metadata[match.Match.MatchID] = match
	}
	file, writer, err := openBurnControlCSV(path, []string{
		"match_id", "rank", "within_caliper", "burn_event_key", "burn_range_location", "burn_range_active",
		"placebo_anchor_block", "match_distance", "horizon_label", "horizon_blocks", "future_block", "available",
		"failure_detail", "burn_immediate_total_lsis_bps", "actual_total_realized_deterioration_bps",
		"placebo_total_realized_deterioration_bps", "excess_deterioration_bps",
		"actual_counterfactual_available", "actual_min_mechanical_effect_piauc_bps",
		"actual_max_mechanical_effect_piauc_bps", "reference_tick", "future_tick", "absolute_tick_change",
		"absolute_price_return_bps", "reference_active_liquidity", "future_active_liquidity",
		"active_liquidity_change_bps", "swap_count", "liquidity_event_count", "net_liquidity_flow",
		"tick_path_total_variation", "tick_path_range",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		meta := metadata[row.Match.MatchID]
		if err := writer.Write([]string{
			row.Match.MatchID,
			strconv.Itoa(meta.Rank),
			burnControlBool(meta.WithinCaliper),
			row.Match.Burn.EventKey(),
			string(row.Match.BurnRangeLocation),
			burnControlBool(row.Match.BurnRangeActive),
			strconv.FormatUint(row.Match.PlaceboAnchorBlock, 10),
			burnControlFloat(row.Match.MatchDistance),
			row.HorizonLabel,
			strconv.FormatUint(row.HorizonBlocks, 10),
			strconv.FormatUint(row.FutureBlock, 10),
			burnControlBool(row.Available),
			row.FailureDetail,
			row.Match.BurnImmediateTotalLSISBps.String(),
			row.ActualTotalRealizedDeteriorationBps.String(),
			row.PlaceboTotalRealizedDeteriorationBps.String(),
			row.ExcessDeteriorationBps.String(),
			burnControlBool(row.ActualCounterfactualAvailable),
			row.ActualMinMechanicalEffectPIAUCBps.String(),
			row.ActualMaxMechanicalEffectPIAUCBps.String(),
			strconv.Itoa(row.ReferenceTick),
			strconv.Itoa(row.FutureTick),
			strconv.Itoa(row.AbsoluteTickChange),
			row.AbsolutePriceReturnBps.String(),
			burnControlBigInt(row.ReferenceActiveLiquidity),
			burnControlBigInt(row.FutureActiveLiquidity),
			row.ActiveLiquidityChangeBps.String(),
			strconv.Itoa(row.Flow.SwapCount),
			strconv.Itoa(row.Flow.LiquidityEventCount),
			burnControlBigInt(row.Flow.NetLiquidityFlow),
			strconv.FormatUint(row.Flow.TickPathTotalVariation, 10),
			strconv.Itoa(row.Flow.TickPathRange),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustTemporalPlaceboAggregatesCSV(path string, rows []RobustTemporalPlaceboAggregate) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"analysis_set", "pool_address", "burn_block", "burn_log_index", "burn_event_key",
		"burn_range_location", "burn_range_active", "horizon_label", "horizon_blocks", "included",
		"exclusion_reason", "requested_controls", "selected_controls", "available_controls",
		"within_caliper_controls", "mean_match_distance", "maximum_match_distance",
		"immediate_total_lsis_bps", "actual_realized_deterioration_bps",
		"mean_placebo_realized_deterioration_bps", "excess_deterioration_bps",
		"actual_counterfactual_available", "actual_mechanical_midpoint_effect_bps",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.AnalysisSet,
			row.Burn.PoolAddress,
			strconv.FormatUint(row.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(row.Burn.Cursor.LogIndex),
			row.Burn.EventKey(),
			string(row.BurnRangeLocation),
			burnControlBool(row.BurnRangeActive),
			row.HorizonLabel,
			strconv.FormatUint(row.HorizonBlocks, 10),
			burnControlBool(row.Included),
			row.ExclusionReason,
			strconv.Itoa(row.RequestedControls),
			strconv.Itoa(row.SelectedControls),
			strconv.Itoa(row.AvailableControls),
			strconv.Itoa(row.WithinCaliperControls),
			burnControlFloat(row.MeanMatchDistance),
			burnControlFloat(row.MaximumMatchDistance),
			row.ImmediateTotalLSISBps.String(),
			row.ActualRealizedDeteriorationBps.String(),
			row.MeanPlaceboRealizedDeteriorationBps.String(),
			row.ExcessDeteriorationBps.String(),
			burnControlBool(row.ActualCounterfactualAvailable),
			row.ActualMechanicalMidpointEffectBps.String(),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustControlBalanceCSV(path string, rows []RobustControlBalance) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"control_type", "analysis_set", "stratum", "metric", "treated_count", "control_count",
		"treated_mean", "control_mean", "smd", "absolute_smd", "status",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.ControlType,
			row.AnalysisSet,
			row.Stratum,
			row.Metric,
			strconv.Itoa(row.TreatedCount),
			strconv.Itoa(row.ControlCount),
			burnControlFloat(row.TreatedMean),
			burnControlFloat(row.ControlMean),
			burnControlFloat(row.SMD),
			burnControlFloat(row.AbsoluteSMD),
			row.Status,
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustControlInferenceCSV(path string, rows []RobustControlInference) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"control_type", "analysis_set", "outcome", "stratum", "horizon_label", "horizon_blocks",
		"pairs", "mean_difference_bps", "median_difference_bps", "positive_pairs", "positive_share",
		"bootstrap_mean_ci_lower", "bootstrap_mean_ci_upper", "wilcoxon_p_value", "wilcoxon_holm_p_value",
		"permutation_p_value", "permutation_holm_p_value",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.ControlType,
			row.AnalysisSet,
			row.Outcome,
			row.Stratum,
			row.HorizonLabel,
			strconv.FormatUint(row.HorizonBlocks, 10),
			strconv.Itoa(row.Pairs),
			burnControlFloat(row.MeanDifferenceBps),
			burnControlFloat(row.MedianDifferenceBps),
			strconv.Itoa(row.PositivePairs),
			burnControlFloat(row.PositiveShare),
			burnControlFloat(row.BootstrapMeanCILower),
			burnControlFloat(row.BootstrapMeanCIUpper),
			burnControlFloat(row.WilcoxonPValue),
			burnControlFloat(row.WilcoxonHolmPValue),
			burnControlFloat(row.PermutationPValue),
			burnControlFloat(row.PermutationHolmPValue),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustTemporalPlaceboExclusionsCSV(path string, candidateSkips []TemporalPlaceboCandidateSkip, exclusions []RobustTemporalPlaceboExclusion) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"record_type", "burn_event_key", "burn_block", "candidate_anchor_block", "candidate_reference_block", "reason", "detail",
	})
	if err != nil {
		return err
	}
	for _, row := range candidateSkips {
		if err := writer.Write([]string{
			"candidate_state_skip", "", "", strconv.FormatUint(row.AnchorBlock, 10), strconv.FormatUint(row.ReferenceBlock, 10), "candidate_state_unavailable", row.Detail,
		}); err != nil {
			return err
		}
	}
	for _, row := range exclusions {
		if err := writer.Write([]string{
			"burn_support_exclusion", row.BurnEventKey, strconv.FormatUint(row.BurnBlock, 10), "", "", row.Reason, row.Detail,
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustMatchedBurnPairsCSV(path string, rows []RobustMatchedBurnPair) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"analysis_set", "pair_id", "stratum", "within_caliper", "match_distance", "block_distance",
		"high_event_key", "high_block", "high_log_index", "high_immediate_lsis_bps", "high_removal_fraction",
		"high_active_removal_share", "high_active_liquidity", "high_liquidity_removed", "high_range_width",
		"high_normalized_distance_outside", "low_event_key", "low_block", "low_log_index",
		"low_immediate_lsis_bps", "low_removal_fraction", "low_active_removal_share", "low_active_liquidity",
		"low_liquidity_removed", "low_range_width", "low_normalized_distance_outside",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.AnalysisSet,
			row.PairID,
			string(row.Stratum),
			burnControlBool(row.WithinCaliper),
			burnControlFloat(row.MatchDistance),
			strconv.FormatUint(row.BlockDistance, 10),
			row.High.Burn.EventKey(),
			strconv.FormatUint(row.High.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(row.High.Burn.Cursor.LogIndex),
			row.High.TotalLSISBps.String(),
			row.High.RemovalFraction.String(),
			row.High.ActiveRemovalShare.String(),
			burnControlBigInt(row.High.ActiveLiquidityBeforeBurn),
			burnControlBigInt(row.High.LiquidityRemoved),
			strconv.Itoa(row.High.RangeWidth),
			row.High.NormalizedDistanceOutsideRange.String(),
			row.Low.Burn.EventKey(),
			strconv.FormatUint(row.Low.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(row.Low.Burn.Cursor.LogIndex),
			row.Low.TotalLSISBps.String(),
			row.Low.RemovalFraction.String(),
			row.Low.ActiveRemovalShare.String(),
			burnControlBigInt(row.Low.ActiveLiquidityBeforeBurn),
			burnControlBigInt(row.Low.LiquidityRemoved),
			strconv.Itoa(row.Low.RangeWidth),
			row.Low.NormalizedDistanceOutsideRange.String(),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteRobustMatchedBurnOutcomesCSV(path string, rows []RobustMatchedBurnOutcome) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"analysis_set", "pair_id", "stratum", "horizon_label", "horizon_blocks", "high_event_key", "low_event_key",
		"high_immediate_lsis_bps", "low_immediate_lsis_bps", "immediate_lsis_difference_bps",
		"high_realized_deterioration_bps", "low_realized_deterioration_bps",
		"realized_deterioration_difference_bps", "mechanical_both_available", "mechanical_midpoint_difference_bps",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.AnalysisSet,
			row.PairID,
			string(row.Stratum),
			row.HorizonLabel,
			strconv.FormatUint(row.HorizonBlocks, 10),
			row.HighBurn.EventKey(),
			row.LowBurn.EventKey(),
			row.HighImmediateLSISBps.String(),
			row.LowImmediateLSISBps.String(),
			row.ImmediateLSISDifferenceBps.String(),
			row.HighRealizedDeteriorationBps.String(),
			row.LowRealizedDeteriorationBps.String(),
			row.RealizedDeteriorationDifferenceBps.String(),
			burnControlBool(row.MechanicalBothAvailable),
			row.MechanicalMidpointDifferenceBps.String(),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func ValidateControlRobustnessV2Report(report ControlRobustnessV2Report) error {
	if report.Manifest.Status != "completed" {
		return fmt.Errorf("control robustness v2 manifest status is %q", report.Manifest.Status)
	}
	if len(report.TemporalMatches) == 0 {
		return fmt.Errorf("control robustness v2 has no temporal placebo matches")
	}
	if len(report.TemporalAggregates) == 0 {
		return fmt.Errorf("control robustness v2 has no temporal placebo aggregates")
	}
	if len(report.TemporalInference) == 0 {
		return fmt.Errorf("control robustness v2 has no temporal placebo inference rows")
	}
	if len(report.MatchedBurnPairs) == 0 {
		return fmt.Errorf("control robustness v2 has no matched burn pairs")
	}
	return nil
}
