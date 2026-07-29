package services

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

type BurnCounterfactualFutureSummary struct {
	PoolAddress                            string
	FromBlock                              uint64
	ToBlock                                uint64
	BurnSamples                            int
	HorizonsPerSample                      int
	ExpectedObservations                   int
	ObservedObservations                   int
	AvailableObservations                  int
	ReplayAuditRows                        int
	BothBranchesAvailable                  int
	SingleBranchAvailable                  int
	NoBranchAvailable                      int
	AmbiguousSwapModes                     int
	MeanMinTotalMechanicalEffectPIAUCBps   decimal.Decimal
	MeanMaxTotalMechanicalEffectPIAUCBps   decimal.Decimal
	MeanMinTotalMechanicalDeteriorationBps decimal.Decimal
	MeanMaxTotalMechanicalDeteriorationBps decimal.Decimal
	MaxMechanicalDeteriorationBps          decimal.Decimal
}

func BuildBurnCounterfactualFutureSummary(
	report BurnCounterfactualFutureReport,
) (BurnCounterfactualFutureSummary, error) {
	if report.ExpectedObservations <= 0 ||
		len(report.Observations) != report.ExpectedObservations {
		return BurnCounterfactualFutureSummary{}, fmt.Errorf(
			"build burn counterfactual summary: observations=%d expected=%d",
			len(report.Observations),
			report.ExpectedObservations,
		)
	}

	summary := BurnCounterfactualFutureSummary{
		PoolAddress:           normalizeAddress(report.PoolAddress),
		FromBlock:             report.FromBlock,
		ToBlock:               report.ToBlock,
		BurnSamples:           report.BurnSamples,
		HorizonsPerSample:     report.HorizonsPerSample,
		ExpectedObservations:  report.ExpectedObservations,
		ObservedObservations:  len(report.Observations),
		ReplayAuditRows:       len(report.ReplayAudits),
		BothBranchesAvailable: report.BothBranchesAvailable,
		SingleBranchAvailable: report.SingleBranchAvailable,
		NoBranchAvailable:     report.NoBranchAvailable,
		AmbiguousSwapModes:    report.AmbiguousSwapModes,
	}

	for _, observation := range report.Observations {
		if !observation.ExactInputTieBreak.Available &&
			!observation.ExactOutputTieBreak.Available {
			continue
		}

		summary.AvailableObservations++
		summary.MeanMinTotalMechanicalEffectPIAUCBps = summary.
			MeanMinTotalMechanicalEffectPIAUCBps.Add(
			observation.MinTotalMechanicalEffectPIAUCBps,
		)
		summary.MeanMaxTotalMechanicalEffectPIAUCBps = summary.
			MeanMaxTotalMechanicalEffectPIAUCBps.Add(
			observation.MaxTotalMechanicalEffectPIAUCBps,
		)
		summary.MeanMinTotalMechanicalDeteriorationBps = summary.
			MeanMinTotalMechanicalDeteriorationBps.Add(
			observation.MinTotalMechanicalDeteriorationBps,
		)
		summary.MeanMaxTotalMechanicalDeteriorationBps = summary.
			MeanMaxTotalMechanicalDeteriorationBps.Add(
			observation.MaxTotalMechanicalDeteriorationBps,
		)
		summary.MaxMechanicalDeteriorationBps = burnMaxDecimal(
			summary.MaxMechanicalDeteriorationBps,
			observation.MaxTotalMechanicalDeteriorationBps,
		)
	}

	if summary.AvailableObservations == 0 {
		return BurnCounterfactualFutureSummary{}, fmt.Errorf(
			"build burn counterfactual summary: no observation has an available counterfactual branch",
		)
	}

	count := decimal.NewFromInt(int64(summary.AvailableObservations))

	summary.MeanMinTotalMechanicalEffectPIAUCBps = summary.
		MeanMinTotalMechanicalEffectPIAUCBps.Div(count)
	summary.MeanMaxTotalMechanicalEffectPIAUCBps = summary.
		MeanMaxTotalMechanicalEffectPIAUCBps.Div(count)
	summary.MeanMinTotalMechanicalDeteriorationBps = summary.
		MeanMinTotalMechanicalDeteriorationBps.Div(count)
	summary.MeanMaxTotalMechanicalDeteriorationBps = summary.
		MeanMaxTotalMechanicalDeteriorationBps.Div(count)

	return summary, nil
}

func WriteBurnCounterfactualObservationsCSV(
	path string,
	observations []BurnCounterfactualObservation,
) error {
	header := []string{
		"event_id",
		"pool_address",
		"tx_hash",
		"block_number",
		"log_index",
		"horizon_label",
		"horizon_blocks",
		"future_block",
		"future_liquidity_events",
		"future_swap_events",
		"ambiguous_swap_modes",
		"actual_future_tick",
		"actual_future_sqrt_price_x96",
		"actual_future_active_liquidity",
		"both_branches_available",
		"min_total_mechanical_effect_piauc_bps",
		"max_total_mechanical_effect_piauc_bps",
		"min_total_mechanical_deterioration_bps",
		"max_total_mechanical_deterioration_bps",
		"exact_input_available",
		"exact_input_failure_block",
		"exact_input_failure_log_index",
		"exact_input_failure_detail",
		"exact_input_future_tick",
		"exact_input_future_sqrt_price_x96",
		"exact_input_future_active_liquidity",
		"exact_input_zero_for_one_actual_future_auc_bps",
		"exact_input_zero_for_one_no_burn_future_auc_bps",
		"exact_input_zero_for_one_mechanical_effect_piauc_bps",
		"exact_input_zero_for_one_mechanical_deterioration_bps",
		"exact_input_one_for_zero_actual_future_auc_bps",
		"exact_input_one_for_zero_no_burn_future_auc_bps",
		"exact_input_one_for_zero_mechanical_effect_piauc_bps",
		"exact_input_one_for_zero_mechanical_deterioration_bps",
		"exact_input_total_mechanical_effect_piauc_bps",
		"exact_input_total_mechanical_deterioration_bps",
		"exact_input_max_directional_mechanical_deterioration_bps",
		"exact_output_available",
		"exact_output_failure_block",
		"exact_output_failure_log_index",
		"exact_output_failure_detail",
		"exact_output_future_tick",
		"exact_output_future_sqrt_price_x96",
		"exact_output_future_active_liquidity",
		"exact_output_zero_for_one_actual_future_auc_bps",
		"exact_output_zero_for_one_no_burn_future_auc_bps",
		"exact_output_zero_for_one_mechanical_effect_piauc_bps",
		"exact_output_zero_for_one_mechanical_deterioration_bps",
		"exact_output_one_for_zero_actual_future_auc_bps",
		"exact_output_one_for_zero_no_burn_future_auc_bps",
		"exact_output_one_for_zero_mechanical_effect_piauc_bps",
		"exact_output_one_for_zero_mechanical_deterioration_bps",
		"exact_output_total_mechanical_effect_piauc_bps",
		"exact_output_total_mechanical_deterioration_bps",
		"exact_output_max_directional_mechanical_deterioration_bps",
	}

	rows := make([][]string, 0, len(observations))
	for _, observation := range observations {
		inputFailureBlock, inputFailureLog := burnOptionalCursorFields(
			observation.ExactInputTieBreak.FailureCursor,
		)
		outputFailureBlock, outputFailureLog := burnOptionalCursorFields(
			observation.ExactOutputTieBreak.FailureCursor,
		)
		rows = append(rows, []string{
			observation.Burn.ID,
			normalizeAddress(observation.Burn.PoolAddress),
			strings.ToLower(strings.TrimSpace(observation.Burn.TxHash)),
			strconv.FormatUint(observation.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(observation.Burn.Cursor.LogIndex),
			observation.HorizonLabel,
			strconv.FormatUint(observation.HorizonBlocks, 10),
			strconv.FormatUint(observation.FutureBlock, 10),
			strconv.Itoa(observation.FutureLiquidityEvents),
			strconv.Itoa(observation.FutureSwapEvents),
			strconv.Itoa(observation.AmbiguousSwapModes),
			strconv.Itoa(observation.ActualFutureCurrentTick),
			burnCounterfactualOptionalBigIntString(observation.ActualFutureSqrtPriceX96),
			burnCounterfactualOptionalBigIntString(observation.ActualFutureActiveLiquidity),
			strconv.FormatBool(observation.BothBranchesAvailable),
			observation.MinTotalMechanicalEffectPIAUCBps.String(),
			observation.MaxTotalMechanicalEffectPIAUCBps.String(),
			observation.MinTotalMechanicalDeteriorationBps.String(),
			observation.MaxTotalMechanicalDeteriorationBps.String(),
			strconv.FormatBool(observation.ExactInputTieBreak.Available),
			inputFailureBlock,
			inputFailureLog,
			observation.ExactInputTieBreak.FailureDetail,
			strconv.Itoa(observation.ExactInputTieBreak.FutureCurrentTick),
			burnCounterfactualOptionalBigIntString(observation.ExactInputTieBreak.FutureSqrtPriceX96),
			burnCounterfactualOptionalBigIntString(observation.ExactInputTieBreak.FutureActiveLiquidity),
			observation.ExactInputTieBreak.ZeroForOne.ActualFutureAUCBps.String(),
			observation.ExactInputTieBreak.ZeroForOne.NoBurnFutureAUCBps.String(),
			observation.ExactInputTieBreak.ZeroForOne.MechanicalEffectPIAUCBps.String(),
			observation.ExactInputTieBreak.ZeroForOne.MechanicalDeteriorationBps.String(),
			observation.ExactInputTieBreak.OneForZero.ActualFutureAUCBps.String(),
			observation.ExactInputTieBreak.OneForZero.NoBurnFutureAUCBps.String(),
			observation.ExactInputTieBreak.OneForZero.MechanicalEffectPIAUCBps.String(),
			observation.ExactInputTieBreak.OneForZero.MechanicalDeteriorationBps.String(),
			observation.ExactInputTieBreak.TotalMechanicalEffectPIAUCBps.String(),
			observation.ExactInputTieBreak.TotalMechanicalDeteriorationBps.String(),
			observation.ExactInputTieBreak.MaxDirectionalMechanicalDeteriorationBps.String(),
			strconv.FormatBool(observation.ExactOutputTieBreak.Available),
			outputFailureBlock,
			outputFailureLog,
			observation.ExactOutputTieBreak.FailureDetail,
			strconv.Itoa(observation.ExactOutputTieBreak.FutureCurrentTick),
			burnCounterfactualOptionalBigIntString(observation.ExactOutputTieBreak.FutureSqrtPriceX96),
			burnCounterfactualOptionalBigIntString(observation.ExactOutputTieBreak.FutureActiveLiquidity),
			observation.ExactOutputTieBreak.ZeroForOne.ActualFutureAUCBps.String(),
			observation.ExactOutputTieBreak.ZeroForOne.NoBurnFutureAUCBps.String(),
			observation.ExactOutputTieBreak.ZeroForOne.MechanicalEffectPIAUCBps.String(),
			observation.ExactOutputTieBreak.ZeroForOne.MechanicalDeteriorationBps.String(),
			observation.ExactOutputTieBreak.OneForZero.ActualFutureAUCBps.String(),
			observation.ExactOutputTieBreak.OneForZero.NoBurnFutureAUCBps.String(),
			observation.ExactOutputTieBreak.OneForZero.MechanicalEffectPIAUCBps.String(),
			observation.ExactOutputTieBreak.OneForZero.MechanicalDeteriorationBps.String(),
			observation.ExactOutputTieBreak.TotalMechanicalEffectPIAUCBps.String(),
			observation.ExactOutputTieBreak.TotalMechanicalDeteriorationBps.String(),
			observation.ExactOutputTieBreak.MaxDirectionalMechanicalDeteriorationBps.String(),
		})
	}

	return writeCSVAtomically(path, header, rows)
}

func WriteBurnCounterfactualReplayAuditCSV(
	path string,
	audits []BurnCounterfactualSwapReplayAudit,
) error {
	header := []string{
		"burn_event_id",
		"pool_address",
		"burn_block_number",
		"burn_log_index",
		"swap_id",
		"swap_tx_hash",
		"swap_block_number",
		"swap_log_index",
		"zero_for_one",
		"amount_in_raw",
		"amount_out_raw",
		"actual_replay_mode",
		"actual_mode_resolved",
		"actual_ambiguous",
		"actual_replay_detail",
		"actual_tick_after",
		"actual_sqrt_price_x96_after",
		"actual_active_liquidity_after",
		"exact_input_tiebreak_mode",
		"exact_input_available",
		"exact_input_failure_detail",
		"exact_input_tick_after",
		"exact_input_sqrt_price_x96_after",
		"exact_input_active_liquidity_after",
		"exact_output_tiebreak_mode",
		"exact_output_available",
		"exact_output_failure_detail",
		"exact_output_tick_after",
		"exact_output_sqrt_price_x96_after",
		"exact_output_active_liquidity_after",
	}

	rows := make([][]string, 0, len(audits))
	for _, audit := range audits {
		rows = append(rows, []string{
			audit.Burn.ID,
			normalizeAddress(audit.Burn.PoolAddress),
			strconv.FormatUint(audit.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(audit.Burn.Cursor.LogIndex),
			audit.SwapID,
			strings.ToLower(strings.TrimSpace(audit.TxHash)),
			strconv.FormatUint(audit.Cursor.BlockNumber, 10),
			strconv.Itoa(audit.Cursor.LogIndex),
			strconv.FormatBool(audit.ZeroForOne),
			burnCounterfactualOptionalBigIntString(audit.AmountInRaw),
			burnCounterfactualOptionalBigIntString(audit.AmountOutRaw),
			string(audit.ActualReplayMode),
			strconv.FormatBool(audit.ActualModeResolved),
			strconv.FormatBool(audit.ActualAmbiguous),
			audit.ActualReplayDetail,
			strconv.Itoa(audit.ActualTickAfter),
			burnCounterfactualOptionalBigIntString(audit.ActualSqrtPriceX96After),
			burnCounterfactualOptionalBigIntString(audit.ActualActiveLiquidityAfter),
			string(audit.ExactInputTieBreakMode),
			strconv.FormatBool(audit.ExactInputAvailable),
			audit.ExactInputFailureDetail,
			strconv.Itoa(audit.ExactInputTickAfter),
			burnCounterfactualOptionalBigIntString(audit.ExactInputSqrtPriceX96After),
			burnCounterfactualOptionalBigIntString(audit.ExactInputActiveLiquidityAfter),
			string(audit.ExactOutputTieBreakMode),
			strconv.FormatBool(audit.ExactOutputAvailable),
			audit.ExactOutputFailureDetail,
			strconv.Itoa(audit.ExactOutputTickAfter),
			burnCounterfactualOptionalBigIntString(audit.ExactOutputSqrtPriceX96After),
			burnCounterfactualOptionalBigIntString(audit.ExactOutputActiveLiquidityAfter),
		})
	}

	return writeCSVAtomically(path, header, rows)
}

func WriteBurnCounterfactualDepthsCSV(
	path string,
	depths []BurnCounterfactualDepthObservation,
) error {
	header := []string{
		"event_id",
		"pool_address",
		"block_number",
		"log_index",
		"horizon_label",
		"horizon_blocks",
		"future_block",
		"tie_break",
		"available",
		"failure_detail",
		"zero_for_one",
		"threshold_bps",
		"actual_future_depth_amount",
		"no_burn_future_depth_amount",
		"mechanical_depth_loss_amount",
		"actual_breached",
		"no_burn_breached",
	}
	rows := make([][]string, 0, len(depths))
	for _, depth := range depths {
		rows = append(rows, []string{
			depth.Burn.ID,
			normalizeAddress(depth.Burn.PoolAddress),
			strconv.FormatUint(depth.Burn.Cursor.BlockNumber, 10),
			strconv.Itoa(depth.Burn.Cursor.LogIndex),
			depth.HorizonLabel,
			strconv.FormatUint(depth.HorizonBlocks, 10),
			strconv.FormatUint(depth.FutureBlock, 10),
			string(depth.TieBreak),
			strconv.FormatBool(depth.Available),
			depth.FailureDetail,
			strconv.FormatBool(depth.ZeroForOne),
			depth.ThresholdBps.String(),
			depth.ActualFutureDepthAmount.String(),
			depth.NoBurnFutureDepthAmount.String(),
			depth.MechanicalDepthLossAmount.String(),
			strconv.FormatBool(depth.ActualBreached),
			strconv.FormatBool(depth.NoBurnBreached),
		})
	}
	return writeCSVAtomically(path, header, rows)
}

func WriteBurnCounterfactualSummaryCSV(
	path string,
	summary BurnCounterfactualFutureSummary,
) error {
	header := []string{
		"pool_address",
		"from_block",
		"to_block",
		"burn_samples",
		"horizons_per_sample",
		"expected_observations",
		"observed_observations",
		"available_observations",
		"replay_audit_rows",
		"both_branches_available",
		"single_branch_available",
		"no_branch_available",
		"ambiguous_swap_modes",
		"mean_min_total_mechanical_effect_piauc_bps",
		"mean_max_total_mechanical_effect_piauc_bps",
		"mean_min_total_mechanical_deterioration_bps",
		"mean_max_total_mechanical_deterioration_bps",
		"max_mechanical_deterioration_bps",
	}
	rows := [][]string{{
		normalizeAddress(summary.PoolAddress),
		strconv.FormatUint(summary.FromBlock, 10),
		strconv.FormatUint(summary.ToBlock, 10),
		strconv.Itoa(summary.BurnSamples),
		strconv.Itoa(summary.HorizonsPerSample),
		strconv.Itoa(summary.ExpectedObservations),
		strconv.Itoa(summary.ObservedObservations),
		strconv.Itoa(summary.AvailableObservations),
		strconv.Itoa(summary.ReplayAuditRows),
		strconv.Itoa(summary.BothBranchesAvailable),
		strconv.Itoa(summary.SingleBranchAvailable),
		strconv.Itoa(summary.NoBranchAvailable),
		strconv.Itoa(summary.AmbiguousSwapModes),
		summary.MeanMinTotalMechanicalEffectPIAUCBps.String(),
		summary.MeanMaxTotalMechanicalEffectPIAUCBps.String(),
		summary.MeanMinTotalMechanicalDeteriorationBps.String(),
		summary.MeanMaxTotalMechanicalDeteriorationBps.String(),
		summary.MaxMechanicalDeteriorationBps.String(),
	}}
	return writeCSVAtomically(path, header, rows)
}

func burnCounterfactualOptionalBigIntString(
	value *big.Int,
) string {
	if value == nil {
		return ""
	}
	return value.String()
}
