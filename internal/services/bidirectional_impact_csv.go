package services

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func WriteBidirectionalImpactCSV(
	path string,
	pool *domain.ReconstructedPool,
	report *BidirectionalLiquidityImpactReport,
) error {
	if pool == nil {
		return fmt.Errorf("write bidirectional impact csv: pool is nil")
	}
	if report == nil {
		return fmt.Errorf("write bidirectional impact csv: report is nil")
	}
	if report.ZeroForOneReport == nil {
		return fmt.Errorf("write bidirectional impact csv: zero_for_one report is nil")
	}
	if report.OneForZeroReport == nil {
		return fmt.Errorf("write bidirectional impact csv: one_for_zero report is nil")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	thresholds := reportDepthThresholds(report)

	if err := writer.Write(bidirectionalImpactCSVHeader(thresholds)); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	for rank, impact := range report.Positions {
		row := bidirectionalImpactCSVRow(
			rank+1,
			pool,
			report,
			impact,
			thresholds,
		)

		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write csv row rank %d: %w", rank+1, err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush csv writer: %w", err)
	}

	return nil
}

func bidirectionalImpactCSVHeader(
	thresholds []decimal.Decimal,
) []string {
	header := []string{
		"rank",
		"position_label",
		"pool_address",
		"block_number",
		"current_tick",

		"tick_lower",
		"tick_upper",
		"position_liquidity",

		"active_liquidity_share",
		"range_width",
		"distance_to_lower_tick",
		"distance_to_upper_tick",
		"distance_to_nearest_edge",
		"liquidity_density",
		"normalized_liquidity_density",

		"zero_for_one_base_auc_bps",
		"one_for_zero_base_auc_bps",

		"zero_for_one_lsis_bps",
		"one_for_zero_lsis_bps",
		"total_lsis_bps",
		"max_directional_lsis_bps",
	}

	for _, threshold := range thresholds {
		suffix := thresholdColumnSuffix(threshold)

		header = append(
			header,

			"zero_for_one_depth_"+suffix+"_base",
			"zero_for_one_depth_"+suffix+"_counterfactual",
			"zero_for_one_depth_"+suffix+"_delta",
			"zero_for_one_depth_"+suffix+"_base_breached",
			"zero_for_one_depth_"+suffix+"_counterfactual_breached",

			"one_for_zero_depth_"+suffix+"_base",
			"one_for_zero_depth_"+suffix+"_counterfactual",
			"one_for_zero_depth_"+suffix+"_delta",
			"one_for_zero_depth_"+suffix+"_base_breached",
			"one_for_zero_depth_"+suffix+"_counterfactual_breached",
		)
	}

	return header
}

func bidirectionalImpactCSVRow(
	rank int,
	pool *domain.ReconstructedPool,
	report *BidirectionalLiquidityImpactReport,
	impact BidirectionalPositionImpact,
	thresholds []decimal.Decimal,
) []string {
	row := []string{
		strconv.Itoa(rank),
		fmt.Sprintf("P%d", rank),
		pool.PoolAddress,
		strconv.FormatUint(pool.BlockNumber, 10),
		strconv.Itoa(pool.CurrentTick),

		strconv.Itoa(impact.Position.TickLower),
		strconv.Itoa(impact.Position.TickUpper),
		impact.Position.Liquidity.String(),

		impact.ActiveLiquidityShare.String(),
		strconv.Itoa(impact.RangeWidth),
		strconv.Itoa(impact.DistanceToLowerTick),
		strconv.Itoa(impact.DistanceToUpperTick),
		strconv.Itoa(impact.DistanceToNearestEdge),
		impact.LiquidityDensity.String(),
		normalizedLiquidityDensity(impact).String(),

		report.ZeroForOneReport.BaseSummary.PriceImpactAUCBps.String(),
		report.OneForZeroReport.BaseSummary.PriceImpactAUCBps.String(),

		impact.ZeroForOneLSISBps.String(),
		impact.OneForZeroLSISBps.String(),
		impact.TotalLSISBps.String(),
		impact.MaxDirectionalLSISBps.String(),
	}

	for _, threshold := range thresholds {
		zeroForOneDepth := findDepthDelta(
			impact.ZeroForOneDepthDeltas,
			threshold,
		)

		oneForZeroDepth := findDepthDelta(
			impact.OneForZeroDepthDeltas,
			threshold,
		)

		row = append(
			row,

			zeroForOneDepth.BaseDepthAmount.String(),
			zeroForOneDepth.CounterfactualDepthAmount.String(),
			zeroForOneDepth.DeltaDepthAmount.String(),
			strconv.FormatBool(zeroForOneDepth.BaseBreached),
			strconv.FormatBool(zeroForOneDepth.CounterfactualBreached),

			oneForZeroDepth.BaseDepthAmount.String(),
			oneForZeroDepth.CounterfactualDepthAmount.String(),
			oneForZeroDepth.DeltaDepthAmount.String(),
			strconv.FormatBool(oneForZeroDepth.BaseBreached),
			strconv.FormatBool(oneForZeroDepth.CounterfactualBreached),
		)
	}

	return row
}

func normalizedLiquidityDensity(
	impact BidirectionalPositionImpact,
) decimal.Decimal {
	if impact.RangeWidth <= 0 {
		return decimal.Zero
	}

	return impact.ActiveLiquidityShare.Div(
		decimal.NewFromInt(int64(impact.RangeWidth)),
	)
}

func reportDepthThresholds(
	report *BidirectionalLiquidityImpactReport,
) []decimal.Decimal {
	if report == nil ||
		report.ZeroForOneReport == nil ||
		len(report.ZeroForOneReport.BaseSummary.ThresholdDepths) == 0 {
		return nil
	}

	thresholds := make(
		[]decimal.Decimal,
		0,
		len(report.ZeroForOneReport.BaseSummary.ThresholdDepths),
	)

	for _, depth := range report.ZeroForOneReport.BaseSummary.ThresholdDepths {
		thresholds = append(thresholds, depth.ThresholdBps)
	}

	return thresholds
}

func findDepthDelta(
	deltas []DepthDelta,
	threshold decimal.Decimal,
) DepthDelta {
	for _, delta := range deltas {
		if delta.ThresholdBps.Equal(threshold) {
			return delta
		}
	}

	return DepthDelta{
		ThresholdBps: threshold,
	}
}

func thresholdColumnSuffix(
	threshold decimal.Decimal,
) string {
	value := threshold.String()
	value = strings.ReplaceAll(value, ".", "_")

	return value + "bps"
}
