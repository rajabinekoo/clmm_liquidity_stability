package services

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func WriteSwapValidationCSV(
	path string,
	results []SwapValidationResult,
) error {
	if len(results) == 0 {
		return fmt.Errorf("write swap validation csv: results are empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create swap validation csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create swap validation csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"swap_id",
		"tx_hash",
		"block_number",
		"log_index",
		"zero_for_one",
		"amount_in_raw",
		"actual_amount_out_raw",
		"sim_amount_out_raw",
		"amount_out_abs_diff_raw",
		"amount_out_diff_bps",
		"actual_tick_after",
		"sim_tick_after",
		"tick_delta",
	}); err != nil {
		return fmt.Errorf("write swap validation csv header: %w", err)
	}

	for _, result := range results {
		if err := writer.Write([]string{
			result.SwapID,
			result.TxHash,
			strconv.FormatUint(result.BlockNumber, 10),
			strconv.Itoa(result.LogIndex),
			strconv.FormatBool(result.ZeroForOne),
			result.AmountInRaw.String(),
			result.ActualAmountOutRaw.String(),
			result.SimAmountOutRaw.String(),
			result.AmountOutAbsDiffRaw.String(),
			result.AmountOutDiffBps.String(),
			strconv.Itoa(result.ActualTickAfter),
			strconv.Itoa(result.SimTickAfter),
			strconv.Itoa(result.TickDelta),
		}); err != nil {
			return fmt.Errorf("write swap validation csv row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush swap validation csv writer: %w", err)
	}

	return nil
}
