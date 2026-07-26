package services

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type BurnRealizedDatasetSummary struct {
	PoolAddress string

	FromBlock uint64
	ToBlock   uint64

	BurnIndexedThrough    uint64
	OutcomeIndexedThrough uint64

	BurnSamples       int
	HorizonsPerSample int

	CandidateHorizonPairs int
	ObservedHorizonPairs  int
	SkippedHorizonPairs   int

	ObservationYieldPercent decimal.Decimal
	CensoringPercent        decimal.Decimal

	FutureBlockNotIndexedSkips    int
	ZeroFutureLiquiditySkips      int
	PositiveDeteriorationPairs    int
	NonPositiveDeteriorationPairs int

	MeanImmediateTotalLSISBps decimal.Decimal
	MaxImmediateTotalLSISBps  decimal.Decimal

	MeanTotalRealizedDeltaPIAUCBps decimal.Decimal

	MeanTotalRealizedDeteriorationBps decimal.Decimal
	MaxTotalRealizedDeteriorationBps  decimal.Decimal

	MeanMaxDirectionalDeteriorationBps decimal.Decimal
	MaxMaxDirectionalDeteriorationBps  decimal.Decimal
}

func BuildBurnRealizedDatasetSummary(
	report BurnRealizedDatasetReport,
) (BurnRealizedDatasetSummary, error) {
	if err :=
		validateBurnRealizedDatasetReport(
			report,
		); err != nil {
		return BurnRealizedDatasetSummary{}, fmt.Errorf(
			"build burn realized dataset summary: %w",
			err,
		)
	}

	summary :=
		BurnRealizedDatasetSummary{
			PoolAddress: normalizeAddress(
				report.PoolAddress,
			),

			FromBlock: report.FromBlock,

			ToBlock: report.ToBlock,

			BurnIndexedThrough: report.BurnIndexedThrough,

			OutcomeIndexedThrough: report.OutcomeIndexedThrough,

			BurnSamples: report.BurnSamples,

			HorizonsPerSample: report.HorizonsPerSample,

			CandidateHorizonPairs: report.CandidateHorizonPairs,

			ObservedHorizonPairs: report.ObservedHorizonPairs,

			SkippedHorizonPairs: report.SkippedHorizonPairs,

			ObservationYieldPercent: burnCollectionPercent(
				report.ObservedHorizonPairs,
				report.CandidateHorizonPairs,
			),

			CensoringPercent: burnCollectionPercent(
				report.SkippedHorizonPairs,
				report.CandidateHorizonPairs,
			),
		}

	totalImmediateLSIS :=
		decimal.Zero

	totalRealizedDelta :=
		decimal.Zero

	totalRealizedDeterioration :=
		decimal.Zero

	totalMaximumDirectionalDeterioration :=
		decimal.Zero

	for index, observation := range report.Observations {
		if err := observation.Validate(); err != nil {
			return BurnRealizedDatasetSummary{}, fmt.Errorf(
				"build burn realized dataset summary: observation %d: %w",
				index,
				err,
			)
		}

		totalImmediateLSIS =
			totalImmediateLSIS.Add(
				observation.
					ImmediateTotalLSISBps,
			)

		totalRealizedDelta =
			totalRealizedDelta.Add(
				observation.
					TotalRealizedDeltaPIAUCBps,
			)

		totalRealizedDeterioration =
			totalRealizedDeterioration.Add(
				observation.
					TotalRealizedDeteriorationBps,
			)

		totalMaximumDirectionalDeterioration =
			totalMaximumDirectionalDeterioration.Add(
				observation.
					MaxDirectionalDeteriorationBps,
			)

		summary.MaxImmediateTotalLSISBps =
			burnMaxDecimal(
				summary.
					MaxImmediateTotalLSISBps,
				observation.
					ImmediateTotalLSISBps,
			)

		summary.MaxTotalRealizedDeteriorationBps =
			burnMaxDecimal(
				summary.
					MaxTotalRealizedDeteriorationBps,
				observation.
					TotalRealizedDeteriorationBps,
			)

		summary.MaxMaxDirectionalDeteriorationBps =
			burnMaxDecimal(
				summary.
					MaxMaxDirectionalDeteriorationBps,
				observation.
					MaxDirectionalDeteriorationBps,
			)

		if observation.
			TotalRealizedDeteriorationBps.
			GreaterThan(
				decimal.Zero,
			) {
			summary.
				PositiveDeteriorationPairs++
		} else {
			summary.
				NonPositiveDeteriorationPairs++
		}
	}

	for index, skipped := range report.Skipped {
		if strings.TrimSpace(
			skipped.Detail,
		) == "" {
			return BurnRealizedDatasetSummary{}, fmt.Errorf(
				"build burn realized dataset summary: skipped pair %d has empty detail",
				index,
			)
		}

		switch skipped.Reason {
		case BurnOutcomeSkipFutureBlockNotIndexed:
			summary.
				FutureBlockNotIndexedSkips++

		case BurnOutcomeSkipZeroFutureActiveLiquidity:
			summary.
				ZeroFutureLiquiditySkips++

		default:
			return BurnRealizedDatasetSummary{}, fmt.Errorf(
				"build burn realized dataset summary: skipped pair %d has unknown reason %q",
				index,
				skipped.Reason,
			)
		}
	}

	if report.ObservedHorizonPairs > 0 {
		count :=
			decimal.NewFromInt(
				int64(
					report.ObservedHorizonPairs,
				),
			)

		summary.MeanImmediateTotalLSISBps =
			totalImmediateLSIS.Div(
				count,
			)

		summary.MeanTotalRealizedDeltaPIAUCBps =
			totalRealizedDelta.Div(
				count,
			)

		summary.MeanTotalRealizedDeteriorationBps =
			totalRealizedDeterioration.Div(
				count,
			)

		summary.MeanMaxDirectionalDeteriorationBps =
			totalMaximumDirectionalDeterioration.Div(
				count,
			)
	}

	if err :=
		validateBurnRealizedDatasetSummary(
			summary,
		); err != nil {
		return BurnRealizedDatasetSummary{}, err
	}

	return summary, nil
}

func WriteBurnRegressionObservationsCSV(
	path string,
	observations []BurnRegressionObservation,
) error {
	header :=
		[]string{
			"event_id",
			"pool_address",
			"tx_hash",
			"block_number",
			"log_index",
			"timestamp",

			"horizon_label",
			"horizon_blocks",
			"future_block",
			"snapshot_block",

			"prior_liquidity_events",
			"prior_swap_events",
			"replayed_events",
			"last_replayed_block",
			"last_replayed_log_index",

			"tick_lower",
			"tick_upper",
			"current_tick",
			"future_current_tick",

			"range_location",
			"burn_range_active",
			"range_width",

			"distance_to_lower_tick",
			"distance_to_upper_tick",
			"distance_to_nearest_boundary",
			"distance_outside_range",
			"normalized_distance_outside_range",

			"sqrt_price_x96_before_burn",
			"future_sqrt_price_x96",

			"market_controls_available",

			"tick_change",
			"absolute_tick_change",

			"price_return_bps",
			"absolute_price_return_bps",

			"future_range_location",
			"future_burn_range_active",

			"future_active_liquidity_delta_from_post_burn",
			"future_active_liquidity_change_fraction_from_post_burn",
			"future_active_liquidity_change_bps_from_post_burn",

			"liquidity_removed",
			"range_liquidity_before_burn",
			"range_liquidity_after_burn",

			"active_liquidity_before_burn",
			"active_liquidity_after_burn",
			"active_liquidity_removed",
			"future_active_liquidity",

			"removal_fraction",
			"range_active_liquidity_share",
			"active_removal_share",
			"range_liquidity_density",
			"removed_liquidity_density",

			"immediate_zero_for_one_base_auc_bps",
			"immediate_zero_for_one_post_burn_auc_bps",
			"immediate_zero_for_one_delta_auc_bps",
			"immediate_zero_for_one_lsis_bps",

			"immediate_one_for_zero_base_auc_bps",
			"immediate_one_for_zero_post_burn_auc_bps",
			"immediate_one_for_zero_delta_auc_bps",
			"immediate_one_for_zero_lsis_bps",

			"immediate_total_lsis_bps",
			"immediate_max_directional_lsis_bps",

			"realized_zero_for_one_pre_event_auc_bps",
			"realized_zero_for_one_future_auc_bps",
			"realized_zero_for_one_delta_piauc_bps",
			"realized_zero_for_one_deterioration_bps",

			"realized_one_for_zero_pre_event_auc_bps",
			"realized_one_for_zero_future_auc_bps",
			"realized_one_for_zero_delta_piauc_bps",
			"realized_one_for_zero_deterioration_bps",

			"total_realized_delta_piauc_bps",
			"total_realized_deterioration_bps",
			"max_directional_deterioration_bps",
		}

	rows :=
		make(
			[][]string,
			0,
			len(observations),
		)

	for index, observation := range observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf(
				"write burn regression observations CSV: observation %d: %w",
				index,
				err,
			)
		}

		lastReplayedBlock,
			lastReplayedLogIndex :=
			burnOptionalCursorFields(
				observation.
					LastReplayedCursor,
			)

		futureActiveLiquidityDelta := ""

		if observation.
			MarketControls.
			FutureActiveLiquidityDeltaFromPostBurn != nil {
			futureActiveLiquidityDelta =
				observation.
					MarketControls.
					FutureActiveLiquidityDeltaFromPostBurn.
					String()
		}

		rows = append(
			rows,
			[]string{
				observation.Burn.ID,
				normalizeAddress(
					observation.
						Burn.
						PoolAddress,
				),
				strings.ToLower(
					strings.TrimSpace(
						observation.
							Burn.
							TxHash,
					),
				),
				strconv.FormatUint(
					observation.
						Burn.
						Cursor.
						BlockNumber,
					10,
				),
				strconv.Itoa(
					observation.
						Burn.
						Cursor.
						LogIndex,
				),
				observation.
					Burn.
					Timestamp.
					UTC().
					Format(
						time.RFC3339Nano,
					),

				observation.HorizonLabel,
				strconv.FormatUint(
					observation.HorizonBlocks,
					10,
				),
				strconv.FormatUint(
					observation.FutureBlock,
					10,
				),
				strconv.FormatUint(
					observation.SnapshotBlock,
					10,
				),

				strconv.Itoa(
					observation.
						PriorLiquidityEvents,
				),
				strconv.Itoa(
					observation.
						PriorSwapEvents,
				),
				strconv.Itoa(
					observation.
						ReplayedEvents,
				),
				lastReplayedBlock,
				lastReplayedLogIndex,

				strconv.Itoa(
					observation.
						Burn.
						TickLower,
				),
				strconv.Itoa(
					observation.
						Burn.
						TickUpper,
				),
				strconv.Itoa(
					observation.CurrentTick,
				),
				strconv.Itoa(
					observation.FutureCurrentTick,
				),

				string(
					observation.RangeLocation,
				),
				strconv.FormatBool(
					observation.BurnRangeActive,
				),
				strconv.Itoa(
					observation.RangeWidth,
				),

				strconv.Itoa(
					observation.
						DistanceToLowerTick,
				),
				strconv.Itoa(
					observation.
						DistanceToUpperTick,
				),
				strconv.Itoa(
					observation.
						DistanceToNearestBoundary,
				),
				strconv.Itoa(
					observation.
						DistanceOutsideRange,
				),
				observation.
					NormalizedDistanceOutsideRange.
					String(),

				observation.
					SqrtPriceX96BeforeBurn.
					String(),
				observation.
					FutureSqrtPriceX96.
					String(),

				strconv.FormatBool(
					observation.
						MarketControls.
						Available,
				),

				strconv.Itoa(
					observation.
						MarketControls.
						TickChange,
				),

				strconv.Itoa(
					observation.
						MarketControls.
						AbsoluteTickChange,
				),

				observation.
					MarketControls.
					PriceReturnBps.
					String(),

				observation.
					MarketControls.
					AbsolutePriceReturnBps.
					String(),

				string(
					observation.
						MarketControls.
						FutureRangeLocation,
				),

				strconv.FormatBool(
					observation.
						MarketControls.
						FutureBurnRangeActive,
				),

				futureActiveLiquidityDelta,

				observation.
					MarketControls.
					FutureActiveLiquidityChangeFractionFromPostBurn.
					String(),

				observation.
					MarketControls.
					FutureActiveLiquidityChangeBpsFromPostBurn.
					String(),

				observation.
					LiquidityRemoved.
					String(),
				observation.
					RangeLiquidityBeforeBurn.
					String(),
				observation.
					RangeLiquidityAfterBurn.
					String(),

				observation.
					ActiveLiquidityBeforeBurn.
					String(),
				observation.
					ActiveLiquidityAfterBurn.
					String(),
				observation.
					ActiveLiquidityRemoved.
					String(),
				observation.
					FutureActiveLiquidity.
					String(),

				observation.
					RemovalFraction.
					String(),
				observation.
					RangeActiveLiquidityShare.
					String(),
				observation.
					ActiveRemovalShare.
					String(),
				observation.
					RangeLiquidityDensity.
					String(),
				observation.
					RemovedLiquidityDensity.
					String(),

				observation.
					ImmediateZeroForOne.
					BaseAUCBps.
					String(),
				observation.
					ImmediateZeroForOne.
					PostBurnAUCBps.
					String(),
				observation.
					ImmediateZeroForOne.
					DeltaAUCBps.
					String(),
				observation.
					ImmediateZeroForOne.
					LSISBps.
					String(),

				observation.
					ImmediateOneForZero.
					BaseAUCBps.
					String(),
				observation.
					ImmediateOneForZero.
					PostBurnAUCBps.
					String(),
				observation.
					ImmediateOneForZero.
					DeltaAUCBps.
					String(),
				observation.
					ImmediateOneForZero.
					LSISBps.
					String(),

				observation.
					ImmediateTotalLSISBps.
					String(),
				observation.
					ImmediateMaxDirectionalLSISBps.
					String(),

				observation.
					RealizedZeroForOne.
					PreEventAUCBps.
					String(),
				observation.
					RealizedZeroForOne.
					FutureAUCBps.
					String(),
				observation.
					RealizedZeroForOne.
					RealizedDeltaPIAUCBps.
					String(),
				observation.
					RealizedZeroForOne.
					RealizedDeteriorationBps.
					String(),

				observation.
					RealizedOneForZero.
					PreEventAUCBps.
					String(),
				observation.
					RealizedOneForZero.
					FutureAUCBps.
					String(),
				observation.
					RealizedOneForZero.
					RealizedDeltaPIAUCBps.
					String(),
				observation.
					RealizedOneForZero.
					RealizedDeteriorationBps.
					String(),

				observation.
					TotalRealizedDeltaPIAUCBps.
					String(),
				observation.
					TotalRealizedDeteriorationBps.
					String(),
				observation.
					MaxDirectionalDeteriorationBps.
					String(),
			},
		)
	}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn regression observations CSV: %w",
			err,
		)
	}

	return nil
}

func WriteBurnRegressionDepthsCSV(
	path string,
	observations []BurnRegressionObservation,
) error {
	header :=
		[]string{
			"event_id",
			"pool_address",
			"tx_hash",
			"block_number",
			"log_index",

			"horizon_label",
			"horizon_blocks",
			"future_block",

			"direction",
			"threshold_bps",

			"pre_event_depth_amount",
			"post_burn_counterfactual_depth_amount",
			"immediate_delta_depth_amount",

			"future_realized_depth_amount",
			"realized_delta_depth_amount",

			"pre_event_breached",
			"post_burn_counterfactual_breached",
			"future_realized_breached",
		}

	rows :=
		make(
			[][]string,
			0,
		)

	for index, observation := range observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf(
				"write burn regression depths CSV: observation %d: %w",
				index,
				err,
			)
		}

		rows =
			appendBurnRegressionDepthRows(
				rows,
				observation,
				"zero_for_one",
				observation.
					ImmediateZeroForOne.
					DepthDeltas,
				observation.
					RealizedZeroForOne.
					DepthDeltas,
			)

		rows =
			appendBurnRegressionDepthRows(
				rows,
				observation,
				"one_for_zero",
				observation.
					ImmediateOneForZero.
					DepthDeltas,
				observation.
					RealizedOneForZero.
					DepthDeltas,
			)
	}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn regression depths CSV: %w",
			err,
		)
	}

	return nil
}

func WriteBurnRegressionCensoringCSV(
	path string,
	skipped []BurnRealizedOutcomeSkip,
) error {
	header :=
		[]string{
			"event_id",
			"pool_address",
			"tx_hash",
			"block_number",
			"log_index",
			"timestamp",

			"tick_lower",
			"tick_upper",
			"liquidity_removed",

			"horizon_label",
			"horizon_blocks",
			"future_block",

			"reason",
			"detail",
		}

	rows :=
		make(
			[][]string,
			0,
			len(skipped),
		)

	for index, item := range skipped {
		if err := item.Burn.Validate(); err != nil {
			return fmt.Errorf(
				"write burn regression censoring CSV: skipped pair %d: %w",
				index,
				err,
			)
		}

		if strings.TrimSpace(
			item.HorizonLabel,
		) == "" ||
			item.HorizonBlocks == 0 ||
			strings.TrimSpace(
				item.Detail,
			) == "" {
			return fmt.Errorf(
				"write burn regression censoring CSV: invalid skipped pair %d",
				index,
			)
		}

		switch item.Reason {
		case BurnOutcomeSkipFutureBlockNotIndexed,
			BurnOutcomeSkipZeroFutureActiveLiquidity:

		default:
			return fmt.Errorf(
				"write burn regression censoring CSV: skipped pair %d has unknown reason %q",
				index,
				item.Reason,
			)
		}

		expectedFutureBlock :=
			item.
				Burn.
				Cursor.
				BlockNumber +
				item.HorizonBlocks

		if item.FutureBlock !=
			expectedFutureBlock {
			return fmt.Errorf(
				"write burn regression censoring CSV: skipped pair %d future block=%d, expected=%d",
				index,
				item.FutureBlock,
				expectedFutureBlock,
			)
		}

		rows = append(
			rows,
			[]string{
				item.Burn.ID,
				normalizeAddress(
					item.Burn.PoolAddress,
				),
				strings.ToLower(
					strings.TrimSpace(
						item.Burn.TxHash,
					),
				),
				strconv.FormatUint(
					item.
						Burn.
						Cursor.
						BlockNumber,
					10,
				),
				strconv.Itoa(
					item.
						Burn.
						Cursor.
						LogIndex,
				),
				item.
					Burn.
					Timestamp.
					UTC().
					Format(
						time.RFC3339Nano,
					),

				strconv.Itoa(
					item.Burn.TickLower,
				),
				strconv.Itoa(
					item.Burn.TickUpper,
				),
				item.
					Burn.
					LiquidityRemoved.
					String(),

				item.HorizonLabel,
				strconv.FormatUint(
					item.HorizonBlocks,
					10,
				),
				strconv.FormatUint(
					item.FutureBlock,
					10,
				),

				string(
					item.Reason,
				),
				item.Detail,
			},
		)
	}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn regression censoring CSV: %w",
			err,
		)
	}

	return nil
}

func WriteBurnRealizedDatasetSummaryCSV(
	path string,
	summary BurnRealizedDatasetSummary,
) error {
	if err :=
		validateBurnRealizedDatasetSummary(
			summary,
		); err != nil {
		return fmt.Errorf(
			"write burn realized dataset summary CSV: %w",
			err,
		)
	}

	header :=
		[]string{
			"pool_address",
			"from_block",
			"to_block",

			"burn_indexed_through",
			"outcome_indexed_through",

			"burn_samples",
			"horizons_per_sample",

			"candidate_horizon_pairs",
			"observed_horizon_pairs",
			"skipped_horizon_pairs",

			"observation_yield_percent",
			"censoring_percent",

			"future_block_not_indexed_skips",
			"zero_future_liquidity_skips",

			"positive_deterioration_pairs",
			"non_positive_deterioration_pairs",

			"mean_immediate_total_lsis_bps",
			"max_immediate_total_lsis_bps",

			"mean_total_realized_delta_piauc_bps",

			"mean_total_realized_deterioration_bps",
			"max_total_realized_deterioration_bps",

			"mean_max_directional_deterioration_bps",
			"max_max_directional_deterioration_bps",
		}

	rows :=
		[][]string{
			{
				summary.PoolAddress,

				strconv.FormatUint(
					summary.FromBlock,
					10,
				),
				strconv.FormatUint(
					summary.ToBlock,
					10,
				),

				strconv.FormatUint(
					summary.
						BurnIndexedThrough,
					10,
				),
				strconv.FormatUint(
					summary.
						OutcomeIndexedThrough,
					10,
				),

				strconv.Itoa(
					summary.BurnSamples,
				),
				strconv.Itoa(
					summary.HorizonsPerSample,
				),

				strconv.Itoa(
					summary.
						CandidateHorizonPairs,
				),
				strconv.Itoa(
					summary.
						ObservedHorizonPairs,
				),
				strconv.Itoa(
					summary.
						SkippedHorizonPairs,
				),

				summary.
					ObservationYieldPercent.
					String(),
				summary.
					CensoringPercent.
					String(),

				strconv.Itoa(
					summary.
						FutureBlockNotIndexedSkips,
				),
				strconv.Itoa(
					summary.
						ZeroFutureLiquiditySkips,
				),

				strconv.Itoa(
					summary.
						PositiveDeteriorationPairs,
				),
				strconv.Itoa(
					summary.
						NonPositiveDeteriorationPairs,
				),

				summary.
					MeanImmediateTotalLSISBps.
					String(),
				summary.
					MaxImmediateTotalLSISBps.
					String(),

				summary.
					MeanTotalRealizedDeltaPIAUCBps.
					String(),

				summary.
					MeanTotalRealizedDeteriorationBps.
					String(),
				summary.
					MaxTotalRealizedDeteriorationBps.
					String(),

				summary.
					MeanMaxDirectionalDeteriorationBps.
					String(),
				summary.
					MaxMaxDirectionalDeteriorationBps.
					String(),
			},
		}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn realized dataset summary CSV: %w",
			err,
		)
	}

	return nil
}

func appendBurnRegressionDepthRows(
	rows [][]string,
	observation BurnRegressionObservation,
	direction string,
	immediate []DepthDelta,
	realized []DepthDelta,
) [][]string {
	for index := range immediate {
		immediateDepth :=
			immediate[index]

		realizedDepth :=
			realized[index]

		rows = append(
			rows,
			[]string{
				observation.Burn.ID,
				normalizeAddress(
					observation.
						Burn.
						PoolAddress,
				),
				strings.ToLower(
					strings.TrimSpace(
						observation.
							Burn.
							TxHash,
					),
				),
				strconv.FormatUint(
					observation.
						Burn.
						Cursor.
						BlockNumber,
					10,
				),
				strconv.Itoa(
					observation.
						Burn.
						Cursor.
						LogIndex,
				),

				observation.HorizonLabel,
				strconv.FormatUint(
					observation.HorizonBlocks,
					10,
				),
				strconv.FormatUint(
					observation.FutureBlock,
					10,
				),

				direction,
				immediateDepth.
					ThresholdBps.
					String(),

				immediateDepth.
					BaseDepthAmount.
					String(),
				immediateDepth.
					CounterfactualDepthAmount.
					String(),
				immediateDepth.
					DeltaDepthAmount.
					String(),

				realizedDepth.
					CounterfactualDepthAmount.
					String(),
				realizedDepth.
					DeltaDepthAmount.
					String(),

				strconv.FormatBool(
					immediateDepth.
						BaseBreached,
				),
				strconv.FormatBool(
					immediateDepth.
						CounterfactualBreached,
				),
				strconv.FormatBool(
					realizedDepth.
						CounterfactualBreached,
				),
			},
		)
	}

	return rows
}

func validateBurnRealizedDatasetSummary(
	summary BurnRealizedDatasetSummary,
) error {
	if normalizeAddress(
		summary.PoolAddress,
	) == "" {
		return fmt.Errorf(
			"burn realized dataset summary: pool address is required",
		)
	}

	if summary.FromBlock == 0 ||
		summary.ToBlock == 0 ||
		summary.FromBlock >
			summary.ToBlock {
		return fmt.Errorf(
			"burn realized dataset summary: invalid block range [%d,%d]",
			summary.FromBlock,
			summary.ToBlock,
		)
	}

	if summary.BurnIndexedThrough <
		summary.ToBlock {
		return fmt.Errorf(
			"burn realized dataset summary: burn checkpoint %d is before to-block %d",
			summary.BurnIndexedThrough,
			summary.ToBlock,
		)
	}

	if summary.BurnSamples > 0 &&
		summary.OutcomeIndexedThrough == 0 {
		return fmt.Errorf(
			"burn realized dataset summary: non-empty dataset has zero outcome indexed head",
		)
	}

	if summary.BurnSamples < 0 ||
		summary.HorizonsPerSample <= 0 ||
		summary.CandidateHorizonPairs < 0 ||
		summary.ObservedHorizonPairs < 0 ||
		summary.SkippedHorizonPairs < 0 {
		return fmt.Errorf(
			"burn realized dataset summary: invalid counters",
		)
	}

	expectedPairs :=
		summary.BurnSamples *
			summary.HorizonsPerSample

	if summary.CandidateHorizonPairs !=
		expectedPairs {
		return fmt.Errorf(
			"burn realized dataset summary: candidate pairs=%d, expected=%d",
			summary.CandidateHorizonPairs,
			expectedPairs,
		)
	}

	if summary.CandidateHorizonPairs !=
		summary.ObservedHorizonPairs+
			summary.SkippedHorizonPairs {
		return fmt.Errorf(
			"burn realized dataset summary: candidate pairs=%d, observed+skipped=%d",
			summary.CandidateHorizonPairs,
			summary.ObservedHorizonPairs+
				summary.SkippedHorizonPairs,
		)
	}

	if summary.
		FutureBlockNotIndexedSkips+
		summary.
			ZeroFutureLiquiditySkips !=
		summary.SkippedHorizonPairs {
		return fmt.Errorf(
			"burn realized dataset summary: categorized skips=%d, skipped=%d",
			summary.
				FutureBlockNotIndexedSkips+
				summary.
					ZeroFutureLiquiditySkips,
			summary.SkippedHorizonPairs,
		)
	}

	if summary.
		PositiveDeteriorationPairs+
		summary.
			NonPositiveDeteriorationPairs !=
		summary.ObservedHorizonPairs {
		return fmt.Errorf(
			"burn realized dataset summary: categorized observations=%d, observed=%d",
			summary.
				PositiveDeteriorationPairs+
				summary.
					NonPositiveDeteriorationPairs,
			summary.ObservedHorizonPairs,
		)
	}

	expectedYield :=
		burnCollectionPercent(
			summary.ObservedHorizonPairs,
			summary.CandidateHorizonPairs,
		)

	expectedCensoring :=
		burnCollectionPercent(
			summary.SkippedHorizonPairs,
			summary.CandidateHorizonPairs,
		)

	if !summary.
		ObservationYieldPercent.
		Equal(
			expectedYield,
		) {
		return fmt.Errorf(
			"burn realized dataset summary: observation yield=%s, expected=%s",
			summary.ObservationYieldPercent,
			expectedYield,
		)
	}

	if !summary.
		CensoringPercent.
		Equal(
			expectedCensoring,
		) {
		return fmt.Errorf(
			"burn realized dataset summary: censoring percent=%s, expected=%s",
			summary.CensoringPercent,
			expectedCensoring,
		)
	}

	nonNegativeValues :=
		[]struct {
			name  string
			value decimal.Decimal
		}{
			{
				name: "mean immediate LSIS",

				value: summary.
					MeanImmediateTotalLSISBps,
			},
			{
				name: "max immediate LSIS",

				value: summary.
					MaxImmediateTotalLSISBps,
			},
			{
				name: "mean realized deterioration",

				value: summary.
					MeanTotalRealizedDeteriorationBps,
			},
			{
				name: "max realized deterioration",

				value: summary.
					MaxTotalRealizedDeteriorationBps,
			},
			{
				name: "mean maximum directional deterioration",

				value: summary.
					MeanMaxDirectionalDeteriorationBps,
			},
			{
				name: "max maximum directional deterioration",

				value: summary.
					MaxMaxDirectionalDeteriorationBps,
			},
		}

	for _, item := range nonNegativeValues {
		if item.value.IsNegative() {
			return fmt.Errorf(
				"burn realized dataset summary: %s must not be negative",
				item.name,
			)
		}
	}

	if summary.MeanImmediateTotalLSISBps.
		GreaterThan(
			summary.MaxImmediateTotalLSISBps,
		) {
		return fmt.Errorf(
			"burn realized dataset summary: mean immediate LSIS exceeds maximum",
		)
	}

	if summary.
		MeanTotalRealizedDeteriorationBps.
		GreaterThan(
			summary.
				MaxTotalRealizedDeteriorationBps,
		) {
		return fmt.Errorf(
			"burn realized dataset summary: mean realized deterioration exceeds maximum",
		)
	}

	if summary.
		MeanMaxDirectionalDeteriorationBps.
		GreaterThan(
			summary.
				MaxMaxDirectionalDeteriorationBps,
		) {
		return fmt.Errorf(
			"burn realized dataset summary: mean maximum directional deterioration exceeds maximum",
		)
	}

	return nil
}
