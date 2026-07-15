package services

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/shopspring/decimal"
)

type ImpactCorrelation struct {
	Metric   string
	Target   string
	Count    int
	Pearson  float64
	Spearman float64
}

func BuildBidirectionalImpactCorrelations(
	report *BidirectionalLiquidityImpactReport,
) ([]ImpactCorrelation, error) {
	if report == nil {
		return nil, fmt.Errorf("impact correlation: report is nil")
	}
	if len(report.Positions) < 2 {
		return nil, fmt.Errorf("impact correlation: at least two positions are required")
	}

	metrics := map[string][]float64{
		"active_liquidity_share":       make([]float64, 0, len(report.Positions)),
		"range_width":                  make([]float64, 0, len(report.Positions)),
		"distance_to_nearest_edge":     make([]float64, 0, len(report.Positions)),
		"normalized_liquidity_density": make([]float64, 0, len(report.Positions)),
	}

	targets := map[string][]float64{
		"zero_for_one_lsis_bps":    make([]float64, 0, len(report.Positions)),
		"one_for_zero_lsis_bps":    make([]float64, 0, len(report.Positions)),
		"total_lsis_bps":           make([]float64, 0, len(report.Positions)),
		"max_directional_lsis_bps": make([]float64, 0, len(report.Positions)),
	}

	for _, impact := range report.Positions {
		metrics["active_liquidity_share"] = append(
			metrics["active_liquidity_share"],
			decimalToFloat(impact.ActiveLiquidityShare),
		)

		metrics["range_width"] = append(
			metrics["range_width"],
			float64(impact.RangeWidth),
		)

		metrics["distance_to_nearest_edge"] = append(
			metrics["distance_to_nearest_edge"],
			float64(impact.DistanceToNearestEdge),
		)

		metrics["normalized_liquidity_density"] = append(
			metrics["normalized_liquidity_density"],
			decimalToFloat(normalizedLiquidityDensity(impact)),
		)

		targets["zero_for_one_lsis_bps"] = append(
			targets["zero_for_one_lsis_bps"],
			decimalToFloat(impact.ZeroForOneLSISBps),
		)

		targets["one_for_zero_lsis_bps"] = append(
			targets["one_for_zero_lsis_bps"],
			decimalToFloat(impact.OneForZeroLSISBps),
		)

		targets["total_lsis_bps"] = append(
			targets["total_lsis_bps"],
			decimalToFloat(impact.TotalLSISBps),
		)

		targets["max_directional_lsis_bps"] = append(
			targets["max_directional_lsis_bps"],
			decimalToFloat(impact.MaxDirectionalLSISBps),
		)
	}

	result := make([]ImpactCorrelation, 0, len(metrics)*len(targets))

	for metricName, metricValues := range metrics {
		for targetName, targetValues := range targets {
			result = append(result, ImpactCorrelation{
				Metric:   metricName,
				Target:   targetName,
				Count:    len(metricValues),
				Pearson:  pearson(metricValues, targetValues),
				Spearman: spearman(metricValues, targetValues),
			})
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Target == result[j].Target {
			return result[i].Metric < result[j].Metric
		}

		return result[i].Target < result[j].Target
	})

	return result, nil
}

func WriteImpactCorrelationCSV(
	path string,
	correlations []ImpactCorrelation,
) error {
	if len(correlations) == 0 {
		return fmt.Errorf("write impact correlation csv: correlations are empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create correlation csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create correlation csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"metric",
		"target",
		"count",
		"pearson",
		"spearman",
	}); err != nil {
		return fmt.Errorf("write correlation csv header: %w", err)
	}

	for _, row := range correlations {
		if err := writer.Write([]string{
			row.Metric,
			row.Target,
			strconv.Itoa(row.Count),
			strconv.FormatFloat(row.Pearson, 'f', 6, 64),
			strconv.FormatFloat(row.Spearman, 'f', 6, 64),
		}); err != nil {
			return fmt.Errorf("write correlation csv row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush correlation csv writer: %w", err)
	}

	return nil
}

func decimalToFloat(value decimal.Decimal) float64 {
	result, _ := value.Float64()
	return result
}

func pearson(x []float64, y []float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return 0
	}

	meanX := mean(x)
	meanY := mean(y)

	var numerator float64
	var sumXSquared float64
	var sumYSquared float64

	for i := range x {
		dx := x[i] - meanX
		dy := y[i] - meanY

		numerator += dx * dy
		sumXSquared += dx * dx
		sumYSquared += dy * dy
	}

	denominator := math.Sqrt(sumXSquared * sumYSquared)
	if denominator == 0 {
		return 0
	}

	return numerator / denominator
}

func spearman(x []float64, y []float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return 0
	}

	return pearson(rankValues(x), rankValues(y))
}

func mean(values []float64) float64 {
	var total float64

	for _, value := range values {
		total += value
	}

	return total / float64(len(values))
}

type rankedValue struct {
	Value float64
	Index int
}

func rankValues(values []float64) []float64 {
	items := make([]rankedValue, 0, len(values))

	for index, value := range values {
		items = append(items, rankedValue{
			Value: value,
			Index: index,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Value < items[j].Value
	})

	ranks := make([]float64, len(values))

	for i := 0; i < len(items); {
		j := i + 1

		for j < len(items) && items[j].Value == items[i].Value {
			j++
		}

		// Average rank for ties. Ranks are 1-based.
		avgRank := (float64(i+1) + float64(j)) / 2

		for k := i; k < j; k++ {
			ranks[items[k].Index] = avgRank
		}

		i = j
	}

	return ranks
}
