package services

import (
	"encoding/csv"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func WriteSwapValidationCSV(
	path string,
	results []SwapValidationResult,
) error {
	rows := make(
		[][]string,
		0,
		len(results),
	)

	for index, result := range results {
		if err :=
			validateSwapValidationResultConsistency(
				result,
			); err != nil {
			return fmt.Errorf(
				"write swap validation csv: result %d: %w",
				index,
				err,
			)
		}

		accountedInput :=
			new(big.Int).Add(
				new(big.Int).Set(
					result.
						SimAmountInLessFeeRaw,
				),
				result.SimFeeAmountRaw,
			)

		rows = append(
			rows,
			[]string{
				result.SwapID,
				result.TxHash,

				strconv.FormatUint(
					result.BlockNumber,
					10,
				),

				strconv.Itoa(
					result.LogIndex,
				),

				strconv.FormatBool(
					result.ZeroForOne,
				),

				strconv.FormatUint(
					result.SnapshotBlock,
					10,
				),

				result.AmountInRaw.String(),

				result.
					SimAmountInLessFeeRaw.
					String(),

				result.
					SimFeeAmountRaw.
					String(),

				accountedInput.String(),

				strconv.FormatBool(
					accountedInput.Cmp(
						result.AmountInRaw,
					) == 0,
				),

				result.
					ActualAmountOutRaw.
					String(),

				result.
					SimAmountOutRaw.
					String(),

				result.
					AmountOutAbsDiffRaw.
					String(),

				result.
					AmountOutDiffBps.
					String(),

				strconv.FormatBool(
					result.AmountOutExact,
				),

				result.
					ActualSqrtPriceX96After.
					String(),

				result.
					SimSqrtPriceX96After.
					String(),

				result.
					SqrtPriceAbsDiffRaw.
					String(),

				result.
					SqrtPriceDiffBps.
					String(),

				strconv.FormatBool(
					result.SqrtPriceExact,
				),

				strconv.Itoa(
					result.ActualTickAfter,
				),

				strconv.Itoa(
					result.SimTickAfter,
				),

				strconv.Itoa(
					result.TickDelta,
				),

				strconv.FormatBool(
					result.TickExact,
				),

				strconv.Itoa(
					result.SimCrossedTicks,
				),

				strconv.Itoa(
					result.SimSwapSteps,
				),

				strconv.FormatBool(
					result.ExactMatch,
				),
			},
		)
	}

	return writeCSVAtomically(
		path,
		[]string{
			"swap_id",
			"tx_hash",
			"block_number",
			"log_index",
			"zero_for_one",
			"snapshot_block",

			"amount_in_raw",
			"sim_amount_in_less_fee_raw",
			"sim_fee_amount_raw",
			"sim_accounted_input_raw",
			"fee_accounting_exact",

			"actual_amount_out_raw",
			"sim_amount_out_raw",
			"amount_out_abs_diff_raw",
			"amount_out_diff_bps",
			"amount_out_exact",

			"actual_sqrt_price_x96_after",
			"sim_sqrt_price_x96_after",
			"sqrt_price_abs_diff_raw",
			"sqrt_price_diff_bps",
			"sqrt_price_exact",

			"actual_tick_after",
			"sim_tick_after",
			"tick_delta",
			"tick_exact",

			"sim_crossed_ticks",
			"sim_swap_steps",
			"exact_match",
		},
		rows,
	)
}

func WriteSwapValidationSkipsCSV(
	path string,
	skipped []SwapValidationSkip,
) error {
	rows := make(
		[][]string,
		0,
		len(skipped),
	)

	for index, item := range skipped {
		if strings.TrimSpace(
			item.SwapID,
		) == "" {
			return fmt.Errorf(
				"write swap validation skips csv: skip %d has empty swap id",
				index,
			)
		}

		if item.BlockNumber == 0 {
			return fmt.Errorf(
				"write swap validation skips csv: skip %d has zero block number",
				index,
			)
		}

		if item.LogIndex < 0 {
			return fmt.Errorf(
				"write swap validation skips csv: skip %d has negative log index",
				index,
			)
		}

		switch item.Reason {
		case SwapValidationSkipInvalidSwap,
			SwapValidationSkipIncompleteLPIndex,
			SwapValidationSkipPriorLiquidityAction:

		default:
			return fmt.Errorf(
				"write swap validation skips csv: skip %d has unknown reason %q",
				index,
				item.Reason,
			)
		}

		rows = append(
			rows,
			[]string{
				item.SwapID,
				item.TxHash,

				strconv.FormatUint(
					item.BlockNumber,
					10,
				),

				strconv.Itoa(
					item.LogIndex,
				),

				string(item.Reason),
				item.Detail,
			},
		)
	}

	return writeCSVAtomically(
		path,
		[]string{
			"swap_id",
			"tx_hash",
			"block_number",
			"log_index",
			"reason",
			"detail",
		},
		rows,
	)
}

func WriteSwapValidationSummaryCSV(
	path string,
	summary SwapValidationSummary,
) error {
	if strings.TrimSpace(
		summary.PoolAddress,
	) == "" {
		return fmt.Errorf(
			"write swap validation summary csv: pool address is required",
		)
	}

	if summary.FromBlock == 0 ||
		summary.ToBlock == 0 ||
		summary.FromBlock >
			summary.ToBlock {
		return fmt.Errorf(
			"write swap validation summary csv: invalid block range [%d,%d]",
			summary.FromBlock,
			summary.ToBlock,
		)
	}

	return writeCSVAtomically(
		path,
		[]string{
			"pool_address",
			"from_block",
			"to_block",
			"graph_indexed_through",

			"scanned_windows",
			"candidate_blocks",
			"clean_samples",
			"skipped_samples",
			"clean_sample_percent",

			"invalid_swap_skips",
			"incomplete_lp_index_skips",
			"prior_liquidity_action_skips",

			"exact_matches",
			"exact_match_percent",

			"amount_out_exact_matches",
			"amount_out_exact_percent",

			"sqrt_price_exact_matches",
			"sqrt_price_exact_percent",

			"tick_exact_matches",
			"tick_exact_percent",

			"mean_amount_out_diff_bps",
			"max_amount_out_diff_bps",

			"mean_sqrt_price_diff_bps",
			"max_sqrt_price_diff_bps",

			"total_swap_steps",
			"total_crossed_ticks",
		},
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
					summary.GraphIndexedThrough,
					10,
				),

				strconv.Itoa(
					summary.ScannedWindows,
				),

				strconv.Itoa(
					summary.CandidateBlocks,
				),

				strconv.Itoa(
					summary.CleanSamples,
				),

				strconv.Itoa(
					summary.SkippedSamples,
				),

				summary.
					CleanSamplePercent.
					String(),

				strconv.Itoa(
					summary.InvalidSwapSkips,
				),

				strconv.Itoa(
					summary.
						IncompleteLPIndexSkips,
				),

				strconv.Itoa(
					summary.
						PriorLiquidityActionSkips,
				),

				strconv.Itoa(
					summary.ExactMatches,
				),

				summary.
					ExactMatchPercent.
					String(),

				strconv.Itoa(
					summary.
						AmountOutExactMatches,
				),

				summary.
					AmountOutExactPercent.
					String(),

				strconv.Itoa(
					summary.
						SqrtPriceExactMatches,
				),

				summary.
					SqrtPriceExactPercent.
					String(),

				strconv.Itoa(
					summary.TickExactMatches,
				),

				summary.
					TickExactPercent.
					String(),

				summary.
					MeanAmountOutDiffBps.
					String(),

				summary.
					MaxAmountOutDiffBps.
					String(),

				summary.
					MeanSqrtPriceDiffBps.
					String(),

				summary.
					MaxSqrtPriceDiffBps.
					String(),

				strconv.Itoa(
					summary.TotalSwapSteps,
				),

				strconv.Itoa(
					summary.TotalCrossedTicks,
				),
			},
		},
	)
}

func validateSwapValidationResultConsistency(
	result SwapValidationResult,
) error {
	if strings.TrimSpace(
		result.SwapID,
	) == "" {
		return fmt.Errorf(
			"swap id is required",
		)
	}

	if result.BlockNumber == 0 {
		return fmt.Errorf(
			"block number must be greater than zero",
		)
	}

	if result.LogIndex < 0 {
		return fmt.Errorf(
			"log index must not be negative",
		)
	}

	if result.SnapshotBlock !=
		result.BlockNumber-1 {
		return fmt.Errorf(
			"snapshot block %d is not immediately before swap block %d",
			result.SnapshotBlock,
			result.BlockNumber,
		)
	}

	positiveValues := []struct {
		name  string
		value *big.Int
	}{
		{
			name:  "amount in",
			value: result.AmountInRaw,
		},
		{
			name: "simulated usable input",
			value: result.
				SimAmountInLessFeeRaw,
		},
		{
			name: "actual amount out",
			value: result.
				ActualAmountOutRaw,
		},
		{
			name: "simulated amount out",
			value: result.
				SimAmountOutRaw,
		},
		{
			name: "actual sqrt price",
			value: result.
				ActualSqrtPriceX96After,
		},
		{
			name: "simulated sqrt price",
			value: result.
				SimSqrtPriceX96After,
		},
	}

	for _, item := range positiveValues {
		if item.value == nil ||
			item.value.Sign() <= 0 {
			return fmt.Errorf(
				"%s must be positive",
				item.name,
			)
		}
	}

	nonNegativeValues := []struct {
		name  string
		value *big.Int
	}{
		{
			name: "simulated fee",
			value: result.
				SimFeeAmountRaw,
		},
		{
			name: "amount out absolute difference",
			value: result.
				AmountOutAbsDiffRaw,
		},
		{
			name: "sqrt price absolute difference",
			value: result.
				SqrtPriceAbsDiffRaw,
		},
	}

	for _, item := range nonNegativeValues {
		if item.value == nil ||
			item.value.Sign() < 0 {
			return fmt.Errorf(
				"%s must not be negative",
				item.name,
			)
		}
	}

	// This invariant verifies the accumulated per-step fee calculation:
	//
	// gross input =
	// sum(step usable inputs) + sum(step fees)
	accountedInput :=
		new(big.Int).Add(
			new(big.Int).Set(
				result.
					SimAmountInLessFeeRaw,
			),
			result.SimFeeAmountRaw,
		)

	if accountedInput.Cmp(
		result.AmountInRaw,
	) != 0 {
		return fmt.Errorf(
			"fee accounting mismatch: gross=%s usable=%s fee=%s accounted=%s",
			result.AmountInRaw,
			result.SimAmountInLessFeeRaw,
			result.SimFeeAmountRaw,
			accountedInput,
		)
	}

	expectedAmountOutDiff :=
		absBigIntDiff(
			result.SimAmountOutRaw,
			result.ActualAmountOutRaw,
		)

	if expectedAmountOutDiff.Cmp(
		result.AmountOutAbsDiffRaw,
	) != 0 {
		return fmt.Errorf(
			"amount out absolute difference mismatch: stored=%s expected=%s",
			result.AmountOutAbsDiffRaw,
			expectedAmountOutDiff,
		)
	}

	expectedSqrtPriceDiff :=
		absBigIntDiff(
			result.SimSqrtPriceX96After,
			result.ActualSqrtPriceX96After,
		)

	if expectedSqrtPriceDiff.Cmp(
		result.SqrtPriceAbsDiffRaw,
	) != 0 {
		return fmt.Errorf(
			"sqrt price absolute difference mismatch: stored=%s expected=%s",
			result.SqrtPriceAbsDiffRaw,
			expectedSqrtPriceDiff,
		)
	}

	expectedTickDelta :=
		absIntDiff(
			result.SimTickAfter,
			result.ActualTickAfter,
		)

	if expectedTickDelta !=
		result.TickDelta {
		return fmt.Errorf(
			"tick delta mismatch: stored=%d expected=%d",
			result.TickDelta,
			expectedTickDelta,
		)
	}

	if result.AmountOutDiffBps.
		IsNegative() ||
		result.SqrtPriceDiffBps.
			IsNegative() {
		return fmt.Errorf(
			"relative differences must not be negative",
		)
	}

	expectedAmountOutDiffBps :=
		relativeDiffBps(
			result.AmountOutAbsDiffRaw,
			result.ActualAmountOutRaw,
		)

	if !result.AmountOutDiffBps.Equal(
		expectedAmountOutDiffBps,
	) {
		return fmt.Errorf(
			"amount out difference bps mismatch: stored=%s expected=%s",
			result.AmountOutDiffBps,
			expectedAmountOutDiffBps,
		)
	}

	expectedSqrtPriceDiffBps :=
		relativeDiffBps(
			result.SqrtPriceAbsDiffRaw,
			result.ActualSqrtPriceX96After,
		)

	if !result.SqrtPriceDiffBps.Equal(
		expectedSqrtPriceDiffBps,
	) {
		return fmt.Errorf(
			"sqrt price difference bps mismatch: stored=%s expected=%s",
			result.SqrtPriceDiffBps,
			expectedSqrtPriceDiffBps,
		)
	}

	if result.SimSwapSteps <= 0 {
		return fmt.Errorf(
			"simulated swap steps must be greater than zero",
		)
	}

	if result.SimCrossedTicks < 0 ||
		result.SimCrossedTicks >
			result.SimSwapSteps {
		return fmt.Errorf(
			"invalid simulated step accounting: crossed=%d steps=%d",
			result.SimCrossedTicks,
			result.SimSwapSteps,
		)
	}

	expectedAmountOutExact :=
		result.
			AmountOutAbsDiffRaw.
			Sign() == 0

	expectedSqrtPriceExact :=
		result.
			SqrtPriceAbsDiffRaw.
			Sign() == 0

	expectedTickExact :=
		result.TickDelta == 0

	expectedExactMatch :=
		expectedAmountOutExact &&
			expectedSqrtPriceExact &&
			expectedTickExact

	if result.AmountOutExact !=
		expectedAmountOutExact {
		return fmt.Errorf(
			"amount out exact flag is inconsistent",
		)
	}

	if result.SqrtPriceExact !=
		expectedSqrtPriceExact {
		return fmt.Errorf(
			"sqrt price exact flag is inconsistent",
		)
	}

	if result.TickExact !=
		expectedTickExact {
		return fmt.Errorf(
			"tick exact flag is inconsistent",
		)
	}

	if result.ExactMatch !=
		expectedExactMatch {
		return fmt.Errorf(
			"exact match flag is inconsistent",
		)
	}

	return nil
}

func writeCSVAtomically(
	path string,
	header []string,
	rows [][]string,
) error {
	normalizedPath :=
		strings.TrimSpace(path)

	if normalizedPath == "" {
		return fmt.Errorf(
			"write csv atomically: path is required",
		)
	}

	if len(header) == 0 {
		return fmt.Errorf(
			"write csv atomically: header is empty",
		)
	}

	directory :=
		filepath.Dir(
			normalizedPath,
		)

	if err := os.MkdirAll(
		directory,
		0o755,
	); err != nil {
		return fmt.Errorf(
			"write csv atomically: create directory %s: %w",
			directory,
			err,
		)
	}

	temporary, err :=
		os.CreateTemp(
			directory,
			"."+filepath.Base(
				normalizedPath,
			)+".tmp-*",
		)
	if err != nil {
		return fmt.Errorf(
			"write csv atomically: create temporary file: %w",
			err,
		)
	}

	temporaryPath :=
		temporary.Name()

	committed := false

	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(
				temporaryPath,
			)
		}
	}()

	writer :=
		csv.NewWriter(
			temporary,
		)

	if err := writer.Write(
		header,
	); err != nil {
		return fmt.Errorf(
			"write csv atomically: write header: %w",
			err,
		)
	}

	for index, row := range rows {
		if len(row) !=
			len(header) {
			return fmt.Errorf(
				"write csv atomically: row %d has %d columns, want %d",
				index,
				len(row),
				len(header),
			)
		}

		if err := writer.Write(
			row,
		); err != nil {
			return fmt.Errorf(
				"write csv atomically: write row %d: %w",
				index,
				err,
			)
		}
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return fmt.Errorf(
			"write csv atomically: flush writer: %w",
			err,
		)
	}

	if err := temporary.Sync(); err != nil {
		return fmt.Errorf(
			"write csv atomically: sync temporary file: %w",
			err,
		)
	}

	if err := temporary.Chmod(
		0o644,
	); err != nil {
		return fmt.Errorf(
			"write csv atomically: chmod temporary file: %w",
			err,
		)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf(
			"write csv atomically: close temporary file: %w",
			err,
		)
	}

	if err := os.Rename(
		temporaryPath,
		normalizedPath,
	); err != nil {
		return fmt.Errorf(
			"write csv atomically: replace %s: %w",
			normalizedPath,
			err,
		)
	}

	committed = true

	return nil
}
