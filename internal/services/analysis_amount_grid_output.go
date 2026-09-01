package services

import (
	"encoding/csv"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"

	"github.com/shopspring/decimal"
)

func WriteAnalysisAmountGridAuditCSV(
	path string,
	records []AnalysisAmountGridAuditRecord,
	token0Symbol string,
	token1Symbol string,
	token0Decimals int,
	token1Decimals int,
) error {
	if len(records) == 0 {
		return fmt.Errorf("write analysis amount grid audit: records are empty")
	}
	if token0Decimals < 0 || token1Decimals < 0 {
		return fmt.Errorf("write analysis amount grid audit: token decimals must not be negative")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("write analysis amount grid audit: create directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write analysis amount grid audit: create file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"state_id",
		"grid_mode",
		"pool_address",
		"block_number",
		"current_tick",
		"sqrt_price_x96",
		"active_liquidity",
		"direction",
		"input_token",
		"input_decimals",
		"point_index",
		"target_impact_bps",
		"amount_in_raw",
		"amount_in_human",
		"output_token",
		"output_decimals",
		"amount_out_raw",
		"amount_out_human",
		"output_quantization_bound_bps",
		"achieved_impact_bps",
		"overshoot_bps",
		"available",
		"status",
		"expansion_steps",
		"bisection_steps",
		"swap_steps",
		"crossed_ticks",
		"detail",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("write analysis amount grid audit: write header: %w", err)
	}

	for _, record := range records {
		direction := "one_for_zero"
		inputToken := token1Symbol
		inputDecimals := token1Decimals
		outputToken := token0Symbol
		outputDecimals := token0Decimals
		if record.ZeroForOne {
			direction = "zero_for_one"
			inputToken = token0Symbol
			inputDecimals = token0Decimals
			outputToken = token1Symbol
			outputDecimals = token1Decimals
		}

		amountRaw := ""
		amountHuman := ""
		if record.Point.AmountInRaw != nil {
			amountRaw = record.Point.AmountInRaw.String()
			amountHuman = rawAmountToHumanString(
				record.Point.AmountInRaw,
				inputDecimals,
			)
		}

		amountOutRaw := ""
		amountOutHuman := ""
		if record.Point.AmountOutRaw != nil {
			amountOutRaw = record.Point.AmountOutRaw.String()
			amountOutHuman = rawAmountToHumanString(
				record.Point.AmountOutRaw,
				outputDecimals,
			)
		}

		row := []string{
			record.StateID,
			record.Mode,
			record.PoolAddress,
			strconv.FormatUint(record.BlockNumber, 10),
			strconv.Itoa(record.CurrentTick),
			bigIntString(record.SqrtPriceX96),
			bigIntString(record.ActiveLiquidity),
			direction,
			inputToken,
			strconv.Itoa(inputDecimals),
			strconv.Itoa(record.PointIndex),
			record.Point.TargetImpactBps.String(),
			amountRaw,
			amountHuman,
			outputToken,
			strconv.Itoa(outputDecimals),
			amountOutRaw,
			amountOutHuman,
			record.Point.OutputQuantizationBoundBps.String(),
			record.Point.AchievedImpactBps.String(),
			record.Point.OvershootBps.String(),
			strconv.FormatBool(record.Point.Available),
			record.Point.Status,
			strconv.Itoa(record.Point.ExpansionSteps),
			strconv.Itoa(record.Point.BisectionSteps),
			strconv.Itoa(record.Point.SwapSteps),
			strconv.Itoa(record.Point.CrossedTicks),
			record.Point.Detail,
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write analysis amount grid audit: write row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("write analysis amount grid audit: flush writer: %w", err)
	}

	return nil
}

func WriteAnalysisAmountGridSummaryCSV(
	path string,
	summary AnalysisAmountGridAuditSummary,
	targets []decimal.Decimal,
) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("write analysis amount grid summary: create directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write analysis amount grid summary: create file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"grid_mode",
		"target_impacts_bps",
		"max_output_quantization_bps",
		"minimum_output_raw",
		"resolve_requests",
		"cache_hits",
		"unique_states",
		"complete_states",
		"incomplete_states",
		"resolved_points",
		"unavailable_points",
	}); err != nil {
		return fmt.Errorf("write analysis amount grid summary: write header: %w", err)
	}

	if err := writer.Write([]string{
		summary.Mode,
		decimalSliceCSV(targets),
		summary.MaxOutputQuantizationBps.String(),
		bigIntString(summary.MinimumOutputRaw),
		strconv.Itoa(summary.ResolveRequests),
		strconv.Itoa(summary.CacheHits),
		strconv.Itoa(summary.UniqueStates),
		strconv.Itoa(summary.CompleteStates),
		strconv.Itoa(summary.IncompleteStates),
		strconv.Itoa(summary.ResolvedPoints),
		strconv.Itoa(summary.UnavailablePoints),
	}); err != nil {
		return fmt.Errorf("write analysis amount grid summary: write row: %w", err)
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("write analysis amount grid summary: flush writer: %w", err)
	}

	return nil
}

func rawAmountToHumanString(amount *big.Int, decimals int) string {
	if amount == nil {
		return ""
	}
	if decimals <= 0 {
		return amount.String()
	}

	return decimal.NewFromBigInt(amount, -int32(decimals)).String()
}

func decimalSliceCSV(values []decimal.Decimal) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ","
		}
		result += value.String()
	}
	return result
}

func bigIntString(value *big.Int) string {
	if value == nil {
		return ""
	}
	return value.String()
}
