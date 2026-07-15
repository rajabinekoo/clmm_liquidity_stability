package services

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/shopspring/decimal"
)

type EmpiricalSummaryMetric struct {
	Metric string
	Mean   decimal.Decimal
	Min    decimal.Decimal
	Max    decimal.Decimal
}

func BuildSnapshotBatchEmpiricalSummary(
	diagnostics []SnapshotBatchDiagnostics,
) ([]EmpiricalSummaryMetric, error) {
	if len(diagnostics) == 0 {
		return nil, fmt.Errorf("empirical summary: diagnostics are empty")
	}

	series := map[string][]decimal.Decimal{
		"zero_for_one_base_auc_bps":     {},
		"one_for_zero_base_auc_bps":     {},
		"topk_active_share_sum":         {},
		"top1_active_share":             {},
		"top3_active_share":             {},
		"sum_total_lsis_bps":            {},
		"top1_total_lsis_bps":           {},
		"top3_total_lsis_bps":           {},
		"top1_lsis_share":               {},
		"top3_lsis_share":               {},
		"active_hhi_normalized_topk":    {},
		"total_lsis_hhi_topk":           {},
		"effective_lsis_positions_topk": {},
		"active_share_gini_topk":        {},
		"total_lsis_gini_topk":          {},
	}

	for _, diagnostic := range diagnostics {
		series["zero_for_one_base_auc_bps"] = append(
			series["zero_for_one_base_auc_bps"],
			diagnostic.ZeroForOneBaseAUCBps,
		)

		series["one_for_zero_base_auc_bps"] = append(
			series["one_for_zero_base_auc_bps"],
			diagnostic.OneForZeroBaseAUCBps,
		)

		series["topk_active_share_sum"] = append(
			series["topk_active_share_sum"],
			diagnostic.TopKActiveShareSum,
		)

		series["top1_active_share"] = append(
			series["top1_active_share"],
			diagnostic.Top1ActiveShare,
		)

		series["top3_active_share"] = append(
			series["top3_active_share"],
			diagnostic.Top3ActiveShare,
		)

		series["sum_total_lsis_bps"] = append(
			series["sum_total_lsis_bps"],
			diagnostic.SumTotalLSISBps,
		)

		series["top1_total_lsis_bps"] = append(
			series["top1_total_lsis_bps"],
			diagnostic.Top1TotalLSISBps,
		)

		series["top3_total_lsis_bps"] = append(
			series["top3_total_lsis_bps"],
			diagnostic.Top3TotalLSISBps,
		)

		series["top1_lsis_share"] = append(
			series["top1_lsis_share"],
			diagnostic.Top1LSISShare,
		)

		series["top3_lsis_share"] = append(
			series["top3_lsis_share"],
			diagnostic.Top3LSISShare,
		)

		series["active_hhi_normalized_topk"] = append(
			series["active_hhi_normalized_topk"],
			diagnostic.ActiveHHINormalizedTopK,
		)

		series["total_lsis_hhi_topk"] = append(
			series["total_lsis_hhi_topk"],
			diagnostic.TotalLSISHHITopK,
		)

		series["effective_lsis_positions_topk"] = append(
			series["effective_lsis_positions_topk"],
			diagnostic.EffectiveLSISPositionsTopK,
		)

		series["active_share_gini_topk"] = append(
			series["active_share_gini_topk"],
			diagnostic.ActiveShareGiniTopK,
		)

		series["total_lsis_gini_topk"] = append(
			series["total_lsis_gini_topk"],
			diagnostic.TotalLSISGiniTopK,
		)
	}

	order := []string{
		"zero_for_one_base_auc_bps",
		"one_for_zero_base_auc_bps",
		"topk_active_share_sum",
		"top1_active_share",
		"top3_active_share",
		"sum_total_lsis_bps",
		"top1_total_lsis_bps",
		"top3_total_lsis_bps",
		"top1_lsis_share",
		"top3_lsis_share",
		"active_hhi_normalized_topk",
		"total_lsis_hhi_topk",
		"effective_lsis_positions_topk",
		"active_share_gini_topk",
		"total_lsis_gini_topk",
	}

	result := make([]EmpiricalSummaryMetric, 0, len(order))

	for _, metric := range order {
		values := series[metric]

		result = append(result, EmpiricalSummaryMetric{
			Metric: metric,
			Mean:   meanDecimal(values),
			Min:    minDecimal(values),
			Max:    maxDecimalSlice(values),
		})
	}

	return result, nil
}

func WriteEmpiricalSummaryCSV(
	path string,
	summary []EmpiricalSummaryMetric,
	snapshotCount int,
	observationCount int,
) error {
	if len(summary) == 0 {
		return fmt.Errorf("write empirical summary csv: summary is empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create empirical summary csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create empirical summary csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"metric",
		"snapshot_count",
		"observation_count",
		"mean",
		"min",
		"max",
	}); err != nil {
		return fmt.Errorf("write empirical summary csv header: %w", err)
	}

	for _, metric := range summary {
		if err := writer.Write([]string{
			metric.Metric,
			strconv.Itoa(snapshotCount),
			strconv.Itoa(observationCount),
			metric.Mean.String(),
			metric.Min.String(),
			metric.Max.String(),
		}); err != nil {
			return fmt.Errorf("write empirical summary csv row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush empirical summary csv writer: %w", err)
	}

	return nil
}

func meanDecimal(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}

	sum := decimal.Zero

	for _, value := range values {
		sum = sum.Add(value)
	}

	return sum.Div(decimal.NewFromInt(int64(len(values))))
}

func minDecimal(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}

	min := values[0]

	for _, value := range values[1:] {
		if value.LessThan(min) {
			min = value
		}
	}

	return min
}

func maxDecimalSlice(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}

	max := values[0]

	for _, value := range values[1:] {
		if value.GreaterThan(max) {
			max = value
		}
	}

	return max
}
