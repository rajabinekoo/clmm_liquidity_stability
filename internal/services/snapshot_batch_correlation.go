package services

import (
	"fmt"
	"sort"
)

func BuildSnapshotBatchPositionCorrelations(
	results []SnapshotBatchDetailedResult,
) ([]ImpactCorrelation, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("snapshot batch position correlations: results are empty")
	}

	metrics := map[string][]float64{
		"active_liquidity_share":       {},
		"range_width":                  {},
		"distance_to_lower_tick":       {},
		"distance_to_upper_tick":       {},
		"distance_to_nearest_edge":     {},
		"normalized_liquidity_density": {},
	}

	targets := map[string][]float64{
		"zero_for_one_lsis_bps":    {},
		"one_for_zero_lsis_bps":    {},
		"total_lsis_bps":           {},
		"max_directional_lsis_bps": {},
	}

	count := 0

	for _, result := range results {
		if result.Report == nil {
			continue
		}

		for _, impact := range result.Report.Positions {
			count++

			metrics["active_liquidity_share"] = append(
				metrics["active_liquidity_share"],
				decimalToFloat(impact.ActiveLiquidityShare),
			)

			metrics["range_width"] = append(
				metrics["range_width"],
				float64(impact.RangeWidth),
			)

			metrics["distance_to_lower_tick"] = append(
				metrics["distance_to_lower_tick"],
				float64(impact.DistanceToLowerTick),
			)

			metrics["distance_to_upper_tick"] = append(
				metrics["distance_to_upper_tick"],
				float64(impact.DistanceToUpperTick),
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
	}

	if count < 2 {
		return nil, fmt.Errorf(
			"snapshot batch position correlations: at least two observations are required",
		)
	}

	correlations := make(
		[]ImpactCorrelation,
		0,
		len(metrics)*len(targets),
	)

	for metricName, metricValues := range metrics {
		for targetName, targetValues := range targets {
			correlations = append(correlations, ImpactCorrelation{
				Metric:   metricName,
				Target:   targetName,
				Count:    len(metricValues),
				Pearson:  pearson(metricValues, targetValues),
				Spearman: spearman(metricValues, targetValues),
			})
		}
	}

	sort.SliceStable(correlations, func(i, j int) bool {
		if correlations[i].Target == correlations[j].Target {
			return correlations[i].Metric < correlations[j].Metric
		}

		return correlations[i].Target < correlations[j].Target
	})

	return correlations, nil
}
