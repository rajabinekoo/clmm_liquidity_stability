package services

import (
	"encoding/csv"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
)

func openBurnControlCSV(path string, header []string) (*os.File, *csv.Writer, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create burn control csv directory: %w", err)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("create burn control csv: %w", err)
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, writer, nil
}
func closeBurnControlCSV(file *os.File, writer *csv.Writer) error {
	writer.Flush()
	if err := writer.Error(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
func burnControlBigInt(value *big.Int) string {
	if value == nil {
		return ""
	}
	return value.String()
}
func burnControlBool(value bool) string     { return strconv.FormatBool(value) }
func burnControlFloat(value float64) string { return strconv.FormatFloat(value, 'g', 17, 64) }

func WriteTemporalPlaceboMatchesCSV(path string, rows []TemporalPlaceboMatch) error {
	file, w, err := openBurnControlCSV(path, []string{"match_id", "pool_address", "burn_block", "burn_log_index", "burn_event_key", "burn_range_location", "burn_range_active", "placebo_anchor_block", "placebo_reference_block", "match_distance", "block_distance", "burn_current_tick", "placebo_current_tick", "burn_active_liquidity", "placebo_active_liquidity", "burn_baseline_total_auc_bps", "burn_immediate_total_lsis_bps", "placebo_baseline_total_auc_bps", "burn_directional_imbalance", "placebo_directional_imbalance"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.MatchID, r.Burn.PoolAddress, strconv.FormatUint(r.Burn.Cursor.BlockNumber, 10), strconv.Itoa(r.Burn.Cursor.LogIndex), r.Burn.EventKey(), string(r.BurnRangeLocation), burnControlBool(r.BurnRangeActive), strconv.FormatUint(r.PlaceboAnchorBlock, 10), strconv.FormatUint(r.PlaceboReferenceBlock, 10), burnControlFloat(r.MatchDistance), strconv.FormatUint(r.BlockDistance, 10), strconv.Itoa(r.BurnCurrentTick), strconv.Itoa(r.PlaceboCurrentTick), burnControlBigInt(r.BurnActiveLiquidity), burnControlBigInt(r.PlaceboActiveLiquidity), r.BurnBaselineTotalAUCBps.String(), r.BurnImmediateTotalLSISBps.String(), r.PlaceboBaselineTotalAUCBps.String(), r.BurnDirectionalImbalance.String(), r.PlaceboDirectionalImbalance.String()}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteTemporalPlaceboObservationsCSV(path string, rows []TemporalPlaceboObservation) error {
	file, w, err := openBurnControlCSV(path, []string{"match_id", "pool_address", "burn_block", "burn_log_index", "burn_event_key", "burn_range_location", "burn_range_active", "burn_immediate_total_lsis_bps", "placebo_anchor_block", "placebo_reference_block", "match_distance", "block_distance", "horizon_label", "horizon_blocks", "future_block", "available", "failure_detail", "actual_total_realized_deterioration_bps", "placebo_total_realized_delta_piauc_bps", "placebo_total_realized_deterioration_bps", "excess_deterioration_bps", "actual_counterfactual_available", "actual_min_mechanical_effect_piauc_bps", "actual_max_mechanical_effect_piauc_bps", "placebo_zero_for_one_base_auc_bps", "placebo_zero_for_one_future_auc_bps", "placebo_one_for_zero_base_auc_bps", "placebo_one_for_zero_future_auc_bps", "reference_tick", "future_tick", "tick_change", "absolute_tick_change", "reference_sqrt_price_x96", "future_sqrt_price_x96", "price_return_bps", "absolute_price_return_bps", "reference_active_liquidity", "future_active_liquidity", "active_liquidity_change_bps", "swap_count", "zero_for_one_swap_count", "one_for_zero_swap_count", "liquidity_event_count", "mint_event_count", "burn_event_count", "gross_token0_volume_raw", "gross_token1_volume_raw", "gross_mint_liquidity", "gross_burn_liquidity", "net_liquidity_flow", "tick_path_total_variation", "tick_path_range", "tick_path_max_absolute_step"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		m := r.Match
		f := r.Flow
		if err := w.Write([]string{m.MatchID, m.Burn.PoolAddress, strconv.FormatUint(m.Burn.Cursor.BlockNumber, 10), strconv.Itoa(m.Burn.Cursor.LogIndex), m.Burn.EventKey(), string(m.BurnRangeLocation), burnControlBool(m.BurnRangeActive), m.BurnImmediateTotalLSISBps.String(), strconv.FormatUint(m.PlaceboAnchorBlock, 10), strconv.FormatUint(m.PlaceboReferenceBlock, 10), burnControlFloat(m.MatchDistance), strconv.FormatUint(m.BlockDistance, 10), r.HorizonLabel, strconv.FormatUint(r.HorizonBlocks, 10), strconv.FormatUint(r.FutureBlock, 10), burnControlBool(r.Available), r.FailureDetail, r.ActualTotalRealizedDeteriorationBps.String(), r.PlaceboTotalRealizedDeltaPIAUCBps.String(), r.PlaceboTotalRealizedDeteriorationBps.String(), r.ExcessDeteriorationBps.String(), burnControlBool(r.ActualCounterfactualAvailable), r.ActualMinMechanicalEffectPIAUCBps.String(), r.ActualMaxMechanicalEffectPIAUCBps.String(), r.PlaceboZeroForOneBaseAUCBps.String(), r.PlaceboZeroForOneFutureAUCBps.String(), r.PlaceboOneForZeroBaseAUCBps.String(), r.PlaceboOneForZeroFutureAUCBps.String(), strconv.Itoa(r.ReferenceTick), strconv.Itoa(r.FutureTick), strconv.Itoa(r.TickChange), strconv.Itoa(r.AbsoluteTickChange), burnControlBigInt(r.ReferenceSqrtPriceX96), burnControlBigInt(r.FutureSqrtPriceX96), r.PriceReturnBps.String(), r.AbsolutePriceReturnBps.String(), burnControlBigInt(r.ReferenceActiveLiquidity), burnControlBigInt(r.FutureActiveLiquidity), r.ActiveLiquidityChangeBps.String(), strconv.Itoa(f.SwapCount), strconv.Itoa(f.ZeroForOneSwapCount), strconv.Itoa(f.OneForZeroSwapCount), strconv.Itoa(f.LiquidityEventCount), strconv.Itoa(f.MintEventCount), strconv.Itoa(f.BurnEventCount), burnControlBigInt(f.GrossToken0VolumeRaw), burnControlBigInt(f.GrossToken1VolumeRaw), burnControlBigInt(f.GrossMintLiquidity), burnControlBigInt(f.GrossBurnLiquidity), burnControlBigInt(f.NetLiquidityFlow), strconv.FormatUint(f.TickPathTotalVariation, 10), strconv.Itoa(f.TickPathRange), strconv.Itoa(f.TickPathMaxAbsoluteStep)}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteTemporalPlaceboSummaryCSV(path string, rows []TemporalPlaceboHorizonSummary) error {
	file, w, err := openBurnControlCSV(path, []string{"stratum", "horizon_label", "horizon_blocks", "pairs", "available_pairs", "unavailable_pairs", "mean_actual_deterioration_bps", "mean_placebo_deterioration_bps", "mean_excess_deterioration_bps", "median_excess_deterioration_bps", "positive_excess_pairs", "positive_excess_share", "pearson_immediate_lsis_vs_excess", "spearman_immediate_lsis_vs_excess"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.Stratum, r.HorizonLabel, strconv.FormatUint(r.HorizonBlocks, 10), strconv.Itoa(r.Pairs), strconv.Itoa(r.AvailablePairs), strconv.Itoa(r.UnavailablePairs), r.MeanActualDeteriorationBps.String(), r.MeanPlaceboDeteriorationBps.String(), r.MeanExcessDeteriorationBps.String(), r.MedianExcessDeteriorationBps.String(), strconv.Itoa(r.PositiveExcessPairs), r.PositiveExcessShare.String(), burnControlFloat(r.PearsonImmediateLSISVsExcess), burnControlFloat(r.SpearmanImmediateLSISVsExcess)}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteTemporalPlaceboBalanceCSV(path string, rows []TemporalPlaceboBalance) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"metric",
		"actual_count",
		"candidate_count",
		"matched_count",
		"actual_mean",
		"candidate_mean",
		"smd_before",
		"matched_placebo_mean",
		"smd_after",
		"absolute_smd_reduction",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.Metric,
			strconv.Itoa(row.ActualCount),
			strconv.Itoa(row.CandidateCount),
			strconv.Itoa(row.MatchedCount),
			burnControlFloat(row.ActualMean),
			burnControlFloat(row.CandidateMean),
			burnControlFloat(row.SMDBefore),
			burnControlFloat(row.MatchedPlaceboMean),
			burnControlFloat(row.SMDAfter),
			burnControlFloat(row.AbsoluteSMDReduction),
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}

func WriteMatchedBurnPairsCSV(path string, rows []MatchedBurnPair) error {
	file, w, err := openBurnControlCSV(path, []string{"pair_id", "stratum", "match_distance", "block_distance", "high_event_key", "high_block", "high_log_index", "high_immediate_lsis_bps", "high_removal_fraction", "high_active_removal_share", "high_active_liquidity", "high_liquidity_removed", "high_range_width", "high_normalized_distance_outside", "low_event_key", "low_block", "low_log_index", "low_immediate_lsis_bps", "low_removal_fraction", "low_active_removal_share", "low_active_liquidity", "low_liquidity_removed", "low_range_width", "low_normalized_distance_outside"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.PairID, string(r.Stratum), burnControlFloat(r.MatchDistance), strconv.FormatUint(r.BlockDistance, 10), r.High.Burn.EventKey(), strconv.FormatUint(r.High.Burn.Cursor.BlockNumber, 10), strconv.Itoa(r.High.Burn.Cursor.LogIndex), r.High.TotalLSISBps.String(), r.High.RemovalFraction.String(), r.High.ActiveRemovalShare.String(), burnControlBigInt(r.High.ActiveLiquidityBeforeBurn), burnControlBigInt(r.High.LiquidityRemoved), strconv.Itoa(r.High.RangeWidth), r.High.NormalizedDistanceOutsideRange.String(), r.Low.Burn.EventKey(), strconv.FormatUint(r.Low.Burn.Cursor.BlockNumber, 10), strconv.Itoa(r.Low.Burn.Cursor.LogIndex), r.Low.TotalLSISBps.String(), r.Low.RemovalFraction.String(), r.Low.ActiveRemovalShare.String(), burnControlBigInt(r.Low.ActiveLiquidityBeforeBurn), burnControlBigInt(r.Low.LiquidityRemoved), strconv.Itoa(r.Low.RangeWidth), r.Low.NormalizedDistanceOutsideRange.String()}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteMatchedBurnOutcomesCSV(path string, rows []MatchedBurnOutcome) error {
	file, w, err := openBurnControlCSV(path, []string{"pair_id", "stratum", "horizon_label", "horizon_blocks", "high_event_key", "low_event_key", "high_immediate_lsis_bps", "low_immediate_lsis_bps", "immediate_lsis_difference_bps", "high_realized_deterioration_bps", "low_realized_deterioration_bps", "realized_deterioration_difference_bps", "mechanical_both_available", "high_mechanical_min_effect_bps", "high_mechanical_max_effect_bps", "low_mechanical_min_effect_bps", "low_mechanical_max_effect_bps", "mechanical_midpoint_difference_bps"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.PairID, string(r.Stratum), r.HorizonLabel, strconv.FormatUint(r.HorizonBlocks, 10), r.HighBurn.EventKey(), r.LowBurn.EventKey(), r.HighImmediateLSISBps.String(), r.LowImmediateLSISBps.String(), r.ImmediateLSISDifferenceBps.String(), r.HighRealizedDeteriorationBps.String(), r.LowRealizedDeteriorationBps.String(), r.RealizedDeteriorationDifferenceBps.String(), burnControlBool(r.MechanicalBothAvailable), r.HighMechanicalMinEffectBps.String(), r.HighMechanicalMaxEffectBps.String(), r.LowMechanicalMinEffectBps.String(), r.LowMechanicalMaxEffectBps.String(), r.MechanicalMidpointDifferenceBps.String()}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteMatchedBurnBalanceCSV(path string, rows []MatchedBurnBalance) error {
	file, w, err := openBurnControlCSV(path, []string{"stratum", "metric", "high_count_before", "low_count_before", "pairs_after", "high_mean_before", "low_mean_before", "smd_before", "high_mean_after", "low_mean_after", "smd_after", "absolute_smd_reduction"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.Stratum, r.Metric, strconv.Itoa(r.HighCountBefore), strconv.Itoa(r.LowCountBefore), strconv.Itoa(r.PairsAfter), burnControlFloat(r.HighMeanBefore), burnControlFloat(r.LowMeanBefore), burnControlFloat(r.SMDBefore), burnControlFloat(r.HighMeanAfter), burnControlFloat(r.LowMeanAfter), burnControlFloat(r.SMDAfter), burnControlFloat(r.AbsoluteSMDReduction)}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteMatchedBurnSummaryCSV(path string, rows []MatchedBurnHorizonSummary) error {
	file, w, err := openBurnControlCSV(path, []string{"stratum", "horizon_label", "horizon_blocks", "pairs", "mean_high_immediate_lsis_bps", "mean_low_immediate_lsis_bps", "mean_immediate_lsis_difference_bps", "mean_high_realized_deterioration_bps", "mean_low_realized_deterioration_bps", "mean_realized_deterioration_difference_bps", "median_realized_deterioration_difference_bps", "positive_realized_difference_pairs", "positive_realized_difference_share", "mechanical_available_pairs", "mean_mechanical_midpoint_difference_bps"})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.Stratum, r.HorizonLabel, strconv.FormatUint(r.HorizonBlocks, 10), strconv.Itoa(r.Pairs), r.MeanHighImmediateLSISBps.String(), r.MeanLowImmediateLSISBps.String(), r.MeanImmediateLSISDifferenceBps.String(), r.MeanHighRealizedDeteriorationBps.String(), r.MeanLowRealizedDeteriorationBps.String(), r.MeanRealizedDeteriorationDifferenceBps.String(), r.MedianRealizedDeteriorationDifferenceBps.String(), strconv.Itoa(r.PositiveRealizedDifferencePairs), r.PositiveRealizedDifferenceShare.String(), strconv.Itoa(r.MechanicalAvailablePairs), r.MeanMechanicalMidpointDifferenceBps.String()}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, w)
}

func WriteTemporalPlaceboCandidateSkipsCSV(path string, rows []TemporalPlaceboCandidateSkip) error {
	file, writer, err := openBurnControlCSV(path, []string{
		"anchor_block",
		"reference_block",
		"detail",
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			strconv.FormatUint(row.AnchorBlock, 10),
			strconv.FormatUint(row.ReferenceBlock, 10),
			row.Detail,
		}); err != nil {
			return err
		}
	}
	return closeBurnControlCSV(file, writer)
}
