package services

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type BurnSampleCollectionSummary struct {
	PoolAddress string

	FromBlock      uint64
	ToBlock        uint64
	IndexedThrough uint64

	Pages int

	CandidateEvents int
	AnalyzedEvents  int
	SkippedEvents   int

	Truncated bool

	SampleTargetReached bool

	MinimumSpacingBlocks  uint64
	SpacingRejectedEvents int

	ActiveBurnSamples   int
	InactiveBurnSamples int

	ZeroActiveLiquidityBeforeSkips int
	ZeroActiveLiquidityAfterSkips  int

	SampleYieldPercent decimal.Decimal
	SkipPercent        decimal.Decimal

	TotalPriorLiquidityEvents int
	TotalPriorSwapEvents      int
	TotalReplayedEvents       int

	MeanRemovalFraction decimal.Decimal
	MaxRemovalFraction  decimal.Decimal

	MeanActiveRemovalShare decimal.Decimal
	MaxActiveRemovalShare  decimal.Decimal

	MeanZeroForOneLSISBps decimal.Decimal
	MaxZeroForOneLSISBps  decimal.Decimal

	MeanOneForZeroLSISBps decimal.Decimal
	MaxOneForZeroLSISBps  decimal.Decimal

	MeanTotalLSISBps decimal.Decimal
	MaxTotalLSISBps  decimal.Decimal

	MeanMaxDirectionalLSISBps decimal.Decimal
	MaxMaxDirectionalLSISBps  decimal.Decimal
}

func BuildBurnSampleCollectionSummary(
	report BurnSampleCollectionReport,
) (BurnSampleCollectionSummary, error) {
	poolAddress :=
		normalizeAddress(
			report.PoolAddress,
		)

	if poolAddress == "" {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: pool address is required",
		)
	}

	if report.FromBlock == 0 ||
		report.ToBlock == 0 ||
		report.FromBlock > report.ToBlock {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: invalid block range [%d,%d]",
			report.FromBlock,
			report.ToBlock,
		)
	}

	if report.IndexedThrough <
		report.ToBlock {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: indexed-through block %d is before to-block %d",
			report.IndexedThrough,
			report.ToBlock,
		)
	}

	if report.Pages <= 0 {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: page count must be positive",
		)
	}

	if report.CandidateEvents < 0 ||
		report.AnalyzedEvents < 0 ||
		report.SkippedEvents < 0 {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: collection counters must not be negative",
		)
	}

	if report.AnalyzedEvents !=
		len(report.Samples) {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: analyzed events=%d, samples=%d",
			report.AnalyzedEvents,
			len(report.Samples),
		)
	}

	if report.SkippedEvents !=
		len(report.Skipped) {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: skipped events=%d, exclusions=%d",
			report.SkippedEvents,
			len(report.Skipped),
		)
	}

	if report.CandidateEvents !=
		report.AnalyzedEvents+
			report.SkippedEvents {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: candidate events=%d, analyzed+skipped=%d",
			report.CandidateEvents,
			report.AnalyzedEvents+
				report.SkippedEvents,
		)
	}

	summary :=
		BurnSampleCollectionSummary{
			PoolAddress: poolAddress,

			FromBlock: report.FromBlock,

			ToBlock: report.ToBlock,

			IndexedThrough: report.IndexedThrough,

			Pages: report.Pages,

			CandidateEvents: report.CandidateEvents,

			AnalyzedEvents: report.AnalyzedEvents,

			SkippedEvents: report.SkippedEvents,

			Truncated: report.Truncated,

			SampleTargetReached: report.SampleTargetReached,

			MinimumSpacingBlocks: report.MinimumSpacingBlocks,

			SpacingRejectedEvents: 0,
		}

	seen :=
		make(
			map[string]struct{},
			report.CandidateEvents,
		)

	var latestCursor *domain.EventCursor

	var (
		totalRemovalFraction = decimal.Zero

		totalActiveRemovalShare = decimal.Zero

		totalZeroForOneLSIS = decimal.Zero

		totalOneForZeroLSIS = decimal.Zero

		totalLSIS = decimal.Zero

		totalMaximumDirectionalLSIS = decimal.Zero
	)

	for index, sample := range report.Samples {
		if err := sample.Validate(); err != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: sample %d: %w",
				index,
				err,
			)
		}

		if err :=
			validateBurnObservationScope(
				sample.Burn,
				poolAddress,
				report.FromBlock,
				report.ToBlock,
			); err != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: sample %d: %w",
				index,
				err,
			)
		}

		if err :=
			registerBurnObservation(
				seen,
				sample.Burn,
			); err != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: sample %d: %w",
				index,
				err,
			)
		}

		latestCursor =
			laterBurnCursor(
				latestCursor,
				sample.Burn.Cursor,
			)

		if sample.BurnRangeActive {
			summary.ActiveBurnSamples++
		} else {
			summary.InactiveBurnSamples++
		}

		summary.TotalPriorLiquidityEvents +=
			sample.PriorLiquidityEvents

		summary.TotalPriorSwapEvents +=
			sample.PriorSwapEvents

		summary.TotalReplayedEvents +=
			sample.ReplayedEvents

		totalRemovalFraction =
			totalRemovalFraction.Add(
				sample.RemovalFraction,
			)

		totalActiveRemovalShare =
			totalActiveRemovalShare.Add(
				sample.ActiveRemovalShare,
			)

		totalZeroForOneLSIS =
			totalZeroForOneLSIS.Add(
				sample.ZeroForOne.LSISBps,
			)

		totalOneForZeroLSIS =
			totalOneForZeroLSIS.Add(
				sample.OneForZero.LSISBps,
			)

		totalLSIS =
			totalLSIS.Add(
				sample.TotalLSISBps,
			)

		totalMaximumDirectionalLSIS =
			totalMaximumDirectionalLSIS.Add(
				sample.MaxDirectionalLSISBps,
			)

		summary.MaxRemovalFraction =
			burnMaxDecimal(
				summary.MaxRemovalFraction,
				sample.RemovalFraction,
			)

		summary.MaxActiveRemovalShare =
			burnMaxDecimal(
				summary.MaxActiveRemovalShare,
				sample.ActiveRemovalShare,
			)

		summary.MaxZeroForOneLSISBps =
			burnMaxDecimal(
				summary.MaxZeroForOneLSISBps,
				sample.ZeroForOne.LSISBps,
			)

		summary.MaxOneForZeroLSISBps =
			burnMaxDecimal(
				summary.MaxOneForZeroLSISBps,
				sample.OneForZero.LSISBps,
			)

		summary.MaxTotalLSISBps =
			burnMaxDecimal(
				summary.MaxTotalLSISBps,
				sample.TotalLSISBps,
			)

		summary.MaxMaxDirectionalLSISBps =
			burnMaxDecimal(
				summary.MaxMaxDirectionalLSISBps,
				sample.MaxDirectionalLSISBps,
			)
	}

	for index, skipped := range report.Skipped {
		if err := skipped.Burn.Validate(); err != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: exclusion %d: invalid burn: %w",
				index,
				err,
			)
		}

		if err :=
			validateBurnObservationScope(
				skipped.Burn,
				poolAddress,
				report.FromBlock,
				report.ToBlock,
			); err != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: exclusion %d: %w",
				index,
				err,
			)
		}

		if err :=
			registerBurnObservation(
				seen,
				skipped.Burn,
			); err != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: exclusion %d: %w",
				index,
				err,
			)
		}

		if strings.TrimSpace(
			skipped.Detail,
		) == "" {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: exclusion %d has empty detail",
				index,
			)
		}

		switch skipped.Reason {
		case BurnSampleSkipZeroActiveLiquidityBefore:
			summary.
				ZeroActiveLiquidityBeforeSkips++

		case BurnSampleSkipZeroActiveLiquidityAfter:
			summary.
				ZeroActiveLiquidityAfterSkips++

		case BurnSampleSkipMinimumSpacing:
			summary.
				SpacingRejectedEvents++

		default:
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: exclusion %d has unknown reason %q",
				index,
				skipped.Reason,
			)
		}

		latestCursor =
			laterBurnCursor(
				latestCursor,
				skipped.Burn.Cursor,
			)
	}

	if len(seen) !=
		report.CandidateEvents {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: unique observations=%d, candidates=%d",
			len(seen),
			report.CandidateEvents,
		)
	}

	if summary.ActiveBurnSamples+
		summary.InactiveBurnSamples !=
		report.AnalyzedEvents {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: active+inactive samples=%d, analyzed=%d",
			summary.ActiveBurnSamples+
				summary.InactiveBurnSamples,
			report.AnalyzedEvents,
		)
	}

	categorizedSkippedEvents :=
		summary.ZeroActiveLiquidityBeforeSkips +
			summary.ZeroActiveLiquidityAfterSkips +
			summary.SpacingRejectedEvents

	if categorizedSkippedEvents !=
		report.SkippedEvents {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: categorized exclusions=%d, skipped=%d",
			categorizedSkippedEvents,
			report.SkippedEvents,
		)
	}

	if summary.SpacingRejectedEvents !=
		report.SpacingRejectedEvents {
		return BurnSampleCollectionSummary{}, fmt.Errorf(
			"build burn collection summary: spacing exclusions=%d, report spacing rejections=%d",
			summary.SpacingRejectedEvents,
			report.SpacingRejectedEvents,
		)
	}

	if report.CandidateEvents == 0 {
		if report.LastProcessedCursor != nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: empty collection has last processed cursor %s",
				report.LastProcessedCursor,
			)
		}
	} else {
		if report.LastProcessedCursor == nil {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: non-empty collection has no last processed cursor",
			)
		}

		if latestCursor == nil ||
			!report.LastProcessedCursor.Equal(
				*latestCursor,
			) {
			return BurnSampleCollectionSummary{}, fmt.Errorf(
				"build burn collection summary: last processed cursor=%s, latest observation=%v",
				report.LastProcessedCursor,
				latestCursor,
			)
		}
	}

	summary.SampleYieldPercent =
		burnCollectionPercent(
			report.AnalyzedEvents,
			report.CandidateEvents,
		)

	summary.SkipPercent =
		burnCollectionPercent(
			report.SkippedEvents,
			report.CandidateEvents,
		)

	if report.AnalyzedEvents > 0 {
		count :=
			decimal.NewFromInt(
				int64(
					report.AnalyzedEvents,
				),
			)

		summary.MeanRemovalFraction =
			totalRemovalFraction.Div(
				count,
			)

		summary.MeanActiveRemovalShare =
			totalActiveRemovalShare.Div(
				count,
			)

		summary.MeanZeroForOneLSISBps =
			totalZeroForOneLSIS.Div(
				count,
			)

		summary.MeanOneForZeroLSISBps =
			totalOneForZeroLSIS.Div(
				count,
			)

		summary.MeanTotalLSISBps =
			totalLSIS.Div(
				count,
			)

		summary.MeanMaxDirectionalLSISBps =
			totalMaximumDirectionalLSIS.Div(
				count,
			)
	}

	if err :=
		validateBurnSampleCollectionSummary(
			summary,
		); err != nil {
		return BurnSampleCollectionSummary{}, err
	}

	return summary, nil
}

func WriteBurnEventSamplesCSV(
	path string,
	samples []BurnEventSample,
) error {
	header :=
		[]string{
			"event_id",
			"pool_address",
			"tx_hash",
			"block_number",
			"log_index",
			"timestamp",
			"snapshot_block",

			"prior_liquidity_events",
			"prior_swap_events",
			"replayed_events",
			"last_replayed_block",
			"last_replayed_log_index",

			"tick_lower",
			"tick_upper",
			"current_tick",
			"range_location",
			"burn_range_active",
			"range_width",

			"distance_to_lower_tick",
			"distance_to_upper_tick",
			"distance_to_nearest_boundary",
			"distance_outside_range",
			"normalized_distance_outside_range",

			"sqrt_price_x96_before_burn",

			"liquidity_removed",
			"range_liquidity_before_burn",
			"range_liquidity_after_burn",
			"active_liquidity_before_burn",
			"active_liquidity_after_burn",
			"active_liquidity_removed",

			"removal_fraction",
			"range_active_liquidity_share",
			"active_removal_share",
			"range_liquidity_density",
			"removed_liquidity_density",

			"zero_for_one_base_auc_bps",
			"zero_for_one_post_burn_auc_bps",
			"zero_for_one_delta_auc_bps",
			"zero_for_one_lsis_bps",

			"one_for_zero_base_auc_bps",
			"one_for_zero_post_burn_auc_bps",
			"one_for_zero_delta_auc_bps",
			"one_for_zero_lsis_bps",

			"total_lsis_bps",
			"max_directional_lsis_bps",
		}

	rows :=
		make(
			[][]string,
			0,
			len(samples),
		)

	for index, sample := range samples {
		if err := sample.Validate(); err != nil {
			return fmt.Errorf(
				"write burn samples CSV: sample %d: %w",
				index,
				err,
			)
		}

		lastBlock,
			lastLog :=
			burnOptionalCursorFields(
				sample.LastReplayedCursor,
			)

		rows = append(
			rows,
			[]string{
				sample.Burn.ID,
				normalizeAddress(
					sample.Burn.PoolAddress,
				),
				strings.ToLower(
					strings.TrimSpace(
						sample.Burn.TxHash,
					),
				),
				strconv.FormatUint(
					sample.Burn.Cursor.BlockNumber,
					10,
				),
				strconv.Itoa(
					sample.Burn.Cursor.LogIndex,
				),
				sample.Burn.Timestamp.
					UTC().
					Format(
						time.RFC3339Nano,
					),
				strconv.FormatUint(
					sample.SnapshotBlock,
					10,
				),

				strconv.Itoa(
					sample.PriorLiquidityEvents,
				),
				strconv.Itoa(
					sample.PriorSwapEvents,
				),
				strconv.Itoa(
					sample.ReplayedEvents,
				),
				lastBlock,
				lastLog,

				strconv.Itoa(
					sample.Burn.TickLower,
				),
				strconv.Itoa(
					sample.Burn.TickUpper,
				),
				strconv.Itoa(
					sample.CurrentTick,
				),
				string(
					sample.RangeLocation,
				),
				strconv.FormatBool(
					sample.BurnRangeActive,
				),
				strconv.Itoa(
					sample.RangeWidth,
				),

				strconv.Itoa(
					sample.DistanceToLowerTick,
				),
				strconv.Itoa(
					sample.DistanceToUpperTick,
				),
				strconv.Itoa(
					sample.DistanceToNearestBoundary,
				),
				strconv.Itoa(
					sample.DistanceOutsideRange,
				),
				sample.
					NormalizedDistanceOutsideRange.
					String(),

				sample.
					SqrtPriceX96BeforeBurn.
					String(),

				sample.
					LiquidityRemoved.
					String(),
				sample.
					RangeLiquidityBeforeBurn.
					String(),
				sample.
					RangeLiquidityAfterBurn.
					String(),
				sample.
					ActiveLiquidityBeforeBurn.
					String(),
				sample.
					ActiveLiquidityAfterBurn.
					String(),
				sample.
					ActiveLiquidityRemoved.
					String(),

				sample.
					RemovalFraction.
					String(),
				sample.
					RangeActiveLiquidityShare.
					String(),
				sample.
					ActiveRemovalShare.
					String(),
				sample.
					RangeLiquidityDensity.
					String(),
				sample.
					RemovedLiquidityDensity.
					String(),

				sample.
					ZeroForOne.
					BaseAUCBps.
					String(),
				sample.
					ZeroForOne.
					PostBurnAUCBps.
					String(),
				sample.
					ZeroForOne.
					DeltaAUCBps.
					String(),
				sample.
					ZeroForOne.
					LSISBps.
					String(),

				sample.
					OneForZero.
					BaseAUCBps.
					String(),
				sample.
					OneForZero.
					PostBurnAUCBps.
					String(),
				sample.
					OneForZero.
					DeltaAUCBps.
					String(),
				sample.
					OneForZero.
					LSISBps.
					String(),

				sample.
					TotalLSISBps.
					String(),
				sample.
					MaxDirectionalLSISBps.
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
			"write burn samples CSV: %w",
			err,
		)
	}

	return nil
}

func WriteBurnEventDepthsCSV(
	path string,
	samples []BurnEventSample,
) error {
	header :=
		[]string{
			"event_id",
			"pool_address",
			"tx_hash",
			"block_number",
			"log_index",
			"direction",
			"threshold_bps",
			"base_depth_amount",
			"post_burn_depth_amount",
			"delta_depth_amount",
			"base_breached",
			"post_burn_breached",
		}

	rowCapacity := 0

	for _, sample := range samples {
		rowCapacity +=
			len(
				sample.
					ZeroForOne.
					DepthDeltas,
			)

		rowCapacity +=
			len(
				sample.
					OneForZero.
					DepthDeltas,
			)
	}

	rows :=
		make(
			[][]string,
			0,
			rowCapacity,
		)

	for index, sample := range samples {
		if err := sample.Validate(); err != nil {
			return fmt.Errorf(
				"write burn depths CSV: sample %d: %w",
				index,
				err,
			)
		}

		rows =
			appendBurnDepthRows(
				rows,
				sample,
				"zero_for_one",
				sample.
					ZeroForOne.
					DepthDeltas,
			)

		rows =
			appendBurnDepthRows(
				rows,
				sample,
				"one_for_zero",
				sample.
					OneForZero.
					DepthDeltas,
			)
	}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn depths CSV: %w",
			err,
		)
	}

	return nil
}

func WriteBurnSampleExclusionsCSV(
	path string,
	skipped []BurnSampleSkip,
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
			"reason",
			"detail",
		}

	rows :=
		make(
			[][]string,
			0,
			len(skipped),
		)

	for index, exclusion := range skipped {
		if err :=
			exclusion.Burn.Validate(); err != nil {
			return fmt.Errorf(
				"write burn exclusions CSV: exclusion %d: %w",
				index,
				err,
			)
		}

		if strings.TrimSpace(
			exclusion.Detail,
		) == "" {
			return fmt.Errorf(
				"write burn exclusions CSV: exclusion %d has empty detail",
				index,
			)
		}

		switch exclusion.Reason {
		case BurnSampleSkipZeroActiveLiquidityBefore,
			BurnSampleSkipZeroActiveLiquidityAfter,
			BurnSampleSkipMinimumSpacing:

		default:
			return fmt.Errorf(
				"write burn exclusions CSV: exclusion %d has unknown reason %q",
				index,
				exclusion.Reason,
			)
		}

		rows = append(
			rows,
			[]string{
				exclusion.Burn.ID,
				normalizeAddress(
					exclusion.
						Burn.
						PoolAddress,
				),
				strings.ToLower(
					strings.TrimSpace(
						exclusion.
							Burn.
							TxHash,
					),
				),
				strconv.FormatUint(
					exclusion.
						Burn.
						Cursor.
						BlockNumber,
					10,
				),
				strconv.Itoa(
					exclusion.
						Burn.
						Cursor.
						LogIndex,
				),
				exclusion.
					Burn.
					Timestamp.
					UTC().
					Format(
						time.RFC3339Nano,
					),
				strconv.Itoa(
					exclusion.
						Burn.
						TickLower,
				),
				strconv.Itoa(
					exclusion.
						Burn.
						TickUpper,
				),
				exclusion.
					Burn.
					LiquidityRemoved.
					String(),
				string(
					exclusion.Reason,
				),
				exclusion.Detail,
			},
		)
	}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn exclusions CSV: %w",
			err,
		)
	}

	return nil
}

func WriteBurnSampleCollectionSummaryCSV(
	path string,
	summary BurnSampleCollectionSummary,
) error {
	if err :=
		validateBurnSampleCollectionSummary(
			summary,
		); err != nil {
		return fmt.Errorf(
			"write burn collection summary CSV: %w",
			err,
		)
	}

	header :=
		[]string{
			"pool_address",
			"from_block",
			"to_block",
			"indexed_through",
			"pages",
			"candidate_events",
			"analyzed_events",
			"skipped_events",
			"truncated",

			"sample_target_reached",
			"minimum_spacing_blocks",
			"spacing_rejected_events",

			"active_burn_samples",
			"inactive_burn_samples",

			"zero_active_liquidity_before_skips",
			"zero_active_liquidity_after_skips",

			"sample_yield_percent",
			"skip_percent",

			"total_prior_liquidity_events",
			"total_prior_swap_events",
			"total_replayed_events",

			"mean_removal_fraction",
			"max_removal_fraction",

			"mean_active_removal_share",
			"max_active_removal_share",

			"mean_zero_for_one_lsis_bps",
			"max_zero_for_one_lsis_bps",

			"mean_one_for_zero_lsis_bps",
			"max_one_for_zero_lsis_bps",

			"mean_total_lsis_bps",
			"max_total_lsis_bps",

			"mean_max_directional_lsis_bps",
			"max_max_directional_lsis_bps",
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
					summary.IndexedThrough,
					10,
				),
				strconv.Itoa(
					summary.Pages,
				),
				strconv.Itoa(
					summary.CandidateEvents,
				),
				strconv.Itoa(
					summary.AnalyzedEvents,
				),
				strconv.Itoa(
					summary.SkippedEvents,
				),
				strconv.FormatBool(
					summary.Truncated,
				),

				strconv.FormatBool(
					summary.SampleTargetReached,
				),
				strconv.FormatUint(
					summary.MinimumSpacingBlocks,
					10,
				),
				strconv.Itoa(
					summary.SpacingRejectedEvents,
				),

				strconv.Itoa(
					summary.ActiveBurnSamples,
				),
				strconv.Itoa(
					summary.InactiveBurnSamples,
				),

				strconv.Itoa(
					summary.
						ZeroActiveLiquidityBeforeSkips,
				),
				strconv.Itoa(
					summary.
						ZeroActiveLiquidityAfterSkips,
				),

				summary.
					SampleYieldPercent.
					String(),
				summary.
					SkipPercent.
					String(),

				strconv.Itoa(
					summary.
						TotalPriorLiquidityEvents,
				),
				strconv.Itoa(
					summary.
						TotalPriorSwapEvents,
				),
				strconv.Itoa(
					summary.
						TotalReplayedEvents,
				),

				summary.
					MeanRemovalFraction.
					String(),
				summary.
					MaxRemovalFraction.
					String(),

				summary.
					MeanActiveRemovalShare.
					String(),
				summary.
					MaxActiveRemovalShare.
					String(),

				summary.
					MeanZeroForOneLSISBps.
					String(),
				summary.
					MaxZeroForOneLSISBps.
					String(),

				summary.
					MeanOneForZeroLSISBps.
					String(),
				summary.
					MaxOneForZeroLSISBps.
					String(),

				summary.
					MeanTotalLSISBps.
					String(),
				summary.
					MaxTotalLSISBps.
					String(),

				summary.
					MeanMaxDirectionalLSISBps.
					String(),
				summary.
					MaxMaxDirectionalLSISBps.
					String(),
			},
		}

	if err := writeCSVAtomically(
		path,
		header,
		rows,
	); err != nil {
		return fmt.Errorf(
			"write burn collection summary CSV: %w",
			err,
		)
	}

	return nil
}

func appendBurnDepthRows(
	rows [][]string,
	sample BurnEventSample,
	direction string,
	depths []DepthDelta,
) [][]string {
	for _, depth := range depths {
		rows = append(
			rows,
			[]string{
				sample.Burn.ID,
				normalizeAddress(
					sample.Burn.PoolAddress,
				),
				strings.ToLower(
					strings.TrimSpace(
						sample.Burn.TxHash,
					),
				),
				strconv.FormatUint(
					sample.Burn.Cursor.BlockNumber,
					10,
				),
				strconv.Itoa(
					sample.Burn.Cursor.LogIndex,
				),
				direction,
				depth.
					ThresholdBps.
					String(),
				depth.
					BaseDepthAmount.
					String(),
				depth.
					CounterfactualDepthAmount.
					String(),
				depth.
					DeltaDepthAmount.
					String(),
				strconv.FormatBool(
					depth.BaseBreached,
				),
				strconv.FormatBool(
					depth.
						CounterfactualBreached,
				),
			},
		)
	}

	return rows
}

func validateBurnSampleCollectionSummary(
	summary BurnSampleCollectionSummary,
) error {
	if normalizeAddress(
		summary.PoolAddress,
	) == "" {
		return fmt.Errorf(
			"burn collection summary: pool address is required",
		)
	}

	if summary.FromBlock == 0 ||
		summary.ToBlock == 0 ||
		summary.FromBlock >
			summary.ToBlock {
		return fmt.Errorf(
			"burn collection summary: invalid block range [%d,%d]",
			summary.FromBlock,
			summary.ToBlock,
		)
	}

	if summary.IndexedThrough <
		summary.ToBlock {
		return fmt.Errorf(
			"burn collection summary: indexed-through block %d is before to-block %d",
			summary.IndexedThrough,
			summary.ToBlock,
		)
	}

	if summary.Pages <= 0 {
		return fmt.Errorf(
			"burn collection summary: pages must be positive",
		)
	}

	if summary.CandidateEvents < 0 ||
		summary.AnalyzedEvents < 0 ||
		summary.SkippedEvents < 0 ||
		summary.SpacingRejectedEvents < 0 {
		return fmt.Errorf(
			"burn collection summary: collection counters must not be negative",
		)
	}

	if summary.CandidateEvents !=
		summary.AnalyzedEvents+
			summary.SkippedEvents {
		return fmt.Errorf(
			"burn collection summary: candidate events=%d, analyzed+skipped=%d",
			summary.CandidateEvents,
			summary.AnalyzedEvents+
				summary.SkippedEvents,
		)
	}

	if summary.ActiveBurnSamples+
		summary.InactiveBurnSamples !=
		summary.AnalyzedEvents {
		return fmt.Errorf(
			"burn collection summary: active+inactive=%d, analyzed=%d",
			summary.ActiveBurnSamples+
				summary.InactiveBurnSamples,
			summary.AnalyzedEvents,
		)
	}

	categorizedSkippedEvents :=
		summary.ZeroActiveLiquidityBeforeSkips +
			summary.ZeroActiveLiquidityAfterSkips +
			summary.SpacingRejectedEvents

	if categorizedSkippedEvents !=
		summary.SkippedEvents {
		return fmt.Errorf(
			"burn collection summary: categorized skips=%d, skipped=%d",
			categorizedSkippedEvents,
			summary.SkippedEvents,
		)
	}

	expectedYield :=
		burnCollectionPercent(
			summary.AnalyzedEvents,
			summary.CandidateEvents,
		)

	expectedSkip :=
		burnCollectionPercent(
			summary.SkippedEvents,
			summary.CandidateEvents,
		)

	if !summary.SampleYieldPercent.Equal(
		expectedYield,
	) {
		return fmt.Errorf(
			"burn collection summary: sample yield=%s, expected=%s",
			summary.SampleYieldPercent,
			expectedYield,
		)
	}

	if !summary.SkipPercent.Equal(
		expectedSkip,
	) {
		return fmt.Errorf(
			"burn collection summary: skip percent=%s, expected=%s",
			summary.SkipPercent,
			expectedSkip,
		)
	}

	nonNegativeDecimals :=
		[]struct {
			name  string
			value decimal.Decimal
		}{
			{
				name: "mean removal fraction",

				value: summary.MeanRemovalFraction,
			},
			{
				name: "max removal fraction",

				value: summary.MaxRemovalFraction,
			},
			{
				name: "mean active removal share",

				value: summary.MeanActiveRemovalShare,
			},
			{
				name: "max active removal share",

				value: summary.MaxActiveRemovalShare,
			},
			{
				name: "mean zero_for_one LSIS",

				value: summary.MeanZeroForOneLSISBps,
			},
			{
				name: "max zero_for_one LSIS",

				value: summary.MaxZeroForOneLSISBps,
			},
			{
				name: "mean one_for_zero LSIS",

				value: summary.MeanOneForZeroLSISBps,
			},
			{
				name: "max one_for_zero LSIS",

				value: summary.MaxOneForZeroLSISBps,
			},
			{
				name: "mean total LSIS",

				value: summary.MeanTotalLSISBps,
			},
			{
				name: "max total LSIS",

				value: summary.MaxTotalLSISBps,
			},
			{
				name: "mean maximum directional LSIS",

				value: summary.
					MeanMaxDirectionalLSISBps,
			},
			{
				name: "max maximum directional LSIS",

				value: summary.
					MaxMaxDirectionalLSISBps,
			},
		}

	for _, item := range nonNegativeDecimals {
		if item.value.IsNegative() {
			return fmt.Errorf(
				"burn collection summary: %s must not be negative",
				item.name,
			)
		}
	}

	if summary.MeanRemovalFraction.
		GreaterThan(
			summary.MaxRemovalFraction,
		) {
		return fmt.Errorf(
			"burn collection summary: mean removal fraction exceeds maximum",
		)
	}

	if summary.MeanActiveRemovalShare.
		GreaterThan(
			summary.MaxActiveRemovalShare,
		) {
		return fmt.Errorf(
			"burn collection summary: mean active removal share exceeds maximum",
		)
	}

	if summary.MeanZeroForOneLSISBps.
		GreaterThan(
			summary.MaxZeroForOneLSISBps,
		) {
		return fmt.Errorf(
			"burn collection summary: mean zero_for_one LSIS exceeds maximum",
		)
	}

	if summary.MeanOneForZeroLSISBps.
		GreaterThan(
			summary.MaxOneForZeroLSISBps,
		) {
		return fmt.Errorf(
			"burn collection summary: mean one_for_zero LSIS exceeds maximum",
		)
	}

	if summary.MeanTotalLSISBps.
		GreaterThan(
			summary.MaxTotalLSISBps,
		) {
		return fmt.Errorf(
			"burn collection summary: mean total LSIS exceeds maximum",
		)
	}

	if summary.MeanMaxDirectionalLSISBps.
		GreaterThan(
			summary.MaxMaxDirectionalLSISBps,
		) {
		return fmt.Errorf(
			"burn collection summary: mean maximum directional LSIS exceeds maximum",
		)
	}

	return nil
}

func validateBurnObservationScope(
	burn domain.BurnCandidate,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
) error {
	if normalizeAddress(
		burn.PoolAddress,
	) != poolAddress {
		return fmt.Errorf(
			"burn pool=%s, expected=%s",
			burn.PoolAddress,
			poolAddress,
		)
	}

	if burn.Cursor.BlockNumber <
		fromBlock ||
		burn.Cursor.BlockNumber >
			toBlock {
		return fmt.Errorf(
			"burn cursor %s is outside range [%d,%d]",
			burn.Cursor,
			fromBlock,
			toBlock,
		)
	}

	return nil
}

func registerBurnObservation(
	seen map[string]struct{},
	burn domain.BurnCandidate,
) error {
	key :=
		burn.EventKey()

	if _, exists :=
		seen[key]; exists {
		return fmt.Errorf(
			"duplicate burn event %s",
			key,
		)
	}

	seen[key] =
		struct{}{}

	return nil
}

func laterBurnCursor(
	current *domain.EventCursor,
	candidate domain.EventCursor,
) *domain.EventCursor {
	if current == nil ||
		current.Before(
			candidate,
		) {
		copy :=
			candidate

		return &copy
	}

	return current
}

func burnCollectionPercent(
	numerator int,
	denominator int,
) decimal.Decimal {
	if numerator < 0 ||
		denominator <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromInt(
		int64(numerator),
	).Mul(
		decimal.NewFromInt(100),
	).Div(
		decimal.NewFromInt(
			int64(denominator),
		),
	)
}

func burnOptionalCursorFields(
	cursor *domain.EventCursor,
) (string, string) {
	if cursor == nil {
		return "", ""
	}

	return strconv.FormatUint(
			cursor.BlockNumber,
			10,
		),
		strconv.Itoa(
			cursor.LogIndex,
		)
}

func WriteBurnSwapReplayAuditCSV(
	path string,
	samples []BurnEventSample,
) error {
	header :=
		[]string{
			"burn_event_id",
			"burn_event_key",
			"pool_address",
			"burn_block_number",
			"burn_log_index",

			"swap_id",
			"swap_tx_hash",
			"swap_block_number",
			"swap_log_index",

			"zero_for_one",
			"replay_mode",

			"amount_in_raw",
			"amount_out_raw",

			"sqrt_price_x96_after",
			"simulated_sqrt_price_x96_after",
			"sqrt_price_abs_diff_raw",
			"sqrt_price_exact",
			"sqrt_price_within_tolerance",

			"tick_after",

			"swap_steps",
			"crossed_ticks",
		}

	rowCount := 0

	for _, sample := range samples {
		rowCount +=
			len(sample.SwapReplays)
	}

	rows := make(
		[][]string,
		0,
		rowCount,
	)

	for sampleIndex, sample := range samples {
		if err := sample.Validate(); err != nil {
			return fmt.Errorf(
				"write burn swap replay audit CSV: sample %d: %w",
				sampleIndex,
				err,
			)
		}

		for replayIndex, replay := range sample.SwapReplays {
			if err := replay.Validate(); err != nil {
				return fmt.Errorf(
					"write burn swap replay audit CSV: sample=%d replay=%d: %w",
					sampleIndex,
					replayIndex,
					err,
				)
			}

			rows =
				append(
					rows,
					[]string{
						sample.Burn.ID,

						sample.Burn.EventKey(),

						normalizeAddress(
							sample.Burn.PoolAddress,
						),

						strconv.FormatUint(
							sample.
								Burn.
								Cursor.
								BlockNumber,
							10,
						),

						strconv.Itoa(
							sample.
								Burn.
								Cursor.
								LogIndex,
						),

						replay.SwapID,

						strings.ToLower(
							strings.TrimSpace(
								replay.TxHash,
							),
						),

						strconv.FormatUint(
							replay.
								Cursor.
								BlockNumber,
							10,
						),

						strconv.Itoa(
							replay.
								Cursor.
								LogIndex,
						),

						strconv.FormatBool(
							replay.ZeroForOne,
						),

						string(replay.Mode),

						replay.
							AmountInRaw.
							String(),

						replay.
							AmountOutRaw.
							String(),

						replay.
							SqrtPriceX96After.
							String(),

						replay.
							SimulatedSqrtPriceX96After.
							String(),

						replay.
							SqrtPriceAbsDiffRaw.
							String(),

						strconv.FormatBool(
							replay.SqrtPriceExact,
						),

						strconv.FormatBool(
							replay.SqrtPriceWithinTolerance,
						),

						strconv.Itoa(
							replay.TickAfter,
						),

						strconv.Itoa(
							replay.SwapSteps,
						),

						strconv.Itoa(
							replay.CrossedTicks,
						),
					},
				)
		}
	}

	if err :=
		writeCSVAtomically(
			path,
			header,
			rows,
		); err != nil {
		return fmt.Errorf(
			"write burn swap replay audit CSV: %w",
			err,
		)
	}

	return nil
}
