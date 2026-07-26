package services

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func WriteJointRemovalImpactCSV(
	path string,
	pool *domain.ReconstructedPool,
	report *JointRemovalReport,
) error {
	if pool == nil {
		return fmt.Errorf(
			"write joint removal impact CSV: pool is nil",
		)
	}

	if report == nil {
		return fmt.Errorf(
			"write joint removal impact CSV: report is nil",
		)
	}

	header :=
		jointRemovalImpactCSVHeader(
			report.ThresholdsBps,
		)

	rows := make(
		[][]string,
		0,
		len(report.Scenarios),
	)

	for _, scenario := range report.Scenarios {
		rows =
			append(
				rows,
				jointRemovalImpactCSVRow(
					pool,
					scenario,
					report.ThresholdsBps,
				),
			)
	}

	if err :=
		writeCSVAtomically(
			path,
			header,
			rows,
		); err != nil {
		return fmt.Errorf(
			"write joint removal impact CSV: %w",
			err,
		)
	}

	return nil
}

func jointRemovalImpactCSVHeader(
	thresholds []decimal.Decimal,
) []string {
	header :=
		[]string{
			"scenario_id",
			"scenario_kind",

			"pool_address",
			"block_number",
			"current_tick",

			"requested_position_count",
			"requested_active_liquidity_share_bps",

			"removed_position_count",
			"removed_liquidity",
			"removed_active_liquidity_share",
			"counterfactual_active_liquidity",

			"position_keys",

			"skipped",
			"skip_reason",

			"zero_for_one_base_auc_bps",
			"zero_for_one_counterfactual_auc_bps",
			"zero_for_one_delta_auc_bps",
			"zero_for_one_joint_lsis_bps",
			"zero_for_one_sum_individual_lsis_bps",
			"zero_for_one_interaction_lsis_bps",
			"zero_for_one_amplification_ratio",

			"one_for_zero_base_auc_bps",
			"one_for_zero_counterfactual_auc_bps",
			"one_for_zero_delta_auc_bps",
			"one_for_zero_joint_lsis_bps",
			"one_for_zero_sum_individual_lsis_bps",
			"one_for_zero_interaction_lsis_bps",
			"one_for_zero_amplification_ratio",

			"sum_individual_total_lsis_bps",
			"joint_total_lsis_bps",
			"total_interaction_lsis_bps",
			"total_amplification_ratio",
			"max_directional_lsis_bps",
		}

	for _, threshold := range thresholds {
		suffix :=
			thresholdColumnSuffix(
				threshold,
			)

		header =
			append(
				header,

				"zero_for_one_depth_"+suffix+"_base",
				"zero_for_one_depth_"+suffix+"_counterfactual",
				"zero_for_one_depth_"+suffix+"_delta",

				"one_for_zero_depth_"+suffix+"_base",
				"one_for_zero_depth_"+suffix+"_counterfactual",
				"one_for_zero_depth_"+suffix+"_delta",
			)
	}

	return header
}

func jointRemovalImpactCSVRow(
	pool *domain.ReconstructedPool,
	scenario JointRemovalScenarioResult,
	thresholds []decimal.Decimal,
) []string {
	removedLiquidity := ""

	if scenario.RemovedLiquidity != nil {
		removedLiquidity =
			scenario.RemovedLiquidity.String()
	}

	counterfactualLiquidity := ""

	if scenario.CounterfactualActiveLiquidity != nil {
		counterfactualLiquidity =
			scenario.
				CounterfactualActiveLiquidity.
				String()
	}

	row :=
		[]string{
			scenario.ScenarioID,
			string(scenario.Kind),

			pool.PoolAddress,

			strconv.FormatUint(
				pool.BlockNumber,
				10,
			),

			strconv.Itoa(
				pool.CurrentTick,
			),

			strconv.Itoa(
				scenario.RequestedPositionCount,
			),

			strconv.FormatInt(
				scenario.
					RequestedActiveLiquidityShareBps,
				10,
			),

			strconv.Itoa(
				scenario.RemovedPositionCount,
			),

			removedLiquidity,

			scenario.
				RemovedActiveLiquidityShare.
				String(),

			counterfactualLiquidity,

			strings.Join(
				scenario.PositionKeys,
				";",
			),

			strconv.FormatBool(
				scenario.Skipped,
			),

			scenario.SkipReason,

			scenario.ZeroForOne.BaseAUCBps.String(),
			scenario.ZeroForOne.CounterfactualAUCBps.String(),
			scenario.ZeroForOne.DeltaAUCBps.String(),
			scenario.ZeroForOne.LSISBps.String(),
			scenario.ZeroForOne.SumIndividualLSISBps.String(),
			scenario.ZeroForOne.InteractionLSISBps.String(),
			scenario.ZeroForOne.AmplificationRatio.String(),

			scenario.OneForZero.BaseAUCBps.String(),
			scenario.OneForZero.CounterfactualAUCBps.String(),
			scenario.OneForZero.DeltaAUCBps.String(),
			scenario.OneForZero.LSISBps.String(),
			scenario.OneForZero.SumIndividualLSISBps.String(),
			scenario.OneForZero.InteractionLSISBps.String(),
			scenario.OneForZero.AmplificationRatio.String(),

			scenario.
				SumIndividualTotalLSISBps.
				String(),

			scenario.
				TotalLSISBps.
				String(),

			scenario.
				TotalInteractionLSISBps.
				String(),

			scenario.
				TotalAmplificationRatio.
				String(),

			scenario.
				MaxDirectionalLSISBps.
				String(),
		}

	for _, threshold := range thresholds {
		zeroForOneDepth :=
			findDepthDelta(
				scenario.
					ZeroForOne.
					DepthDeltas,
				threshold,
			)

		oneForZeroDepth :=
			findDepthDelta(
				scenario.
					OneForZero.
					DepthDeltas,
				threshold,
			)

		row =
			append(
				row,

				zeroForOneDepth.
					BaseDepthAmount.
					String(),

				zeroForOneDepth.
					CounterfactualDepthAmount.
					String(),

				zeroForOneDepth.
					DeltaDepthAmount.
					String(),

				oneForZeroDepth.
					BaseDepthAmount.
					String(),

				oneForZeroDepth.
					CounterfactualDepthAmount.
					String(),

				oneForZeroDepth.
					DeltaDepthAmount.
					String(),
			)
	}

	return row
}
