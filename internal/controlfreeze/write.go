package controlfreeze

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func WriteReport(files OutputFiles, report Report) error {
	writers := []struct {
		name  string
		path  string
		write func(*csv.Writer) error
	}{
		{"balance metrics", files.BalanceMetrics, func(writer *csv.Writer) error { return writeBalanceMetrics(writer, report.BalanceMetrics) }},
		{"balance gate", files.BalanceGate, func(writer *csv.Writer) error { return writeBalanceGates(writer, report.BalanceGates) }},
		{"publication inference", files.PublicationInference, func(writer *csv.Writer) error { return writeInference(writer, report.Inference) }},
		{"analysis values", files.AnalysisValues, func(writer *csv.Writer) error { return writeAnalysisValues(writer, report.AnalysisValues) }},
		{"temporal support", files.TemporalSupport, func(writer *csv.Writer) error { return writeTemporalSupport(writer, report.TemporalSupport) }},
		{"matched support", files.MatchedSupport, func(writer *csv.Writer) error { return writeMatchedSupport(writer, report.MatchedSupport) }},
	}
	for _, item := range writers {
		if err := writeCSVAtomic(item.path, item.write); err != nil {
			return fmt.Errorf("write control freeze v2.1 %s: %w", item.name, err)
		}
	}
	// The manifest is intentionally written last. Its completed status means all
	// preceding outputs were atomically committed.
	if err := writeCSVAtomic(files.Manifest, func(writer *csv.Writer) error {
		return writeManifest(writer, report.Manifest)
	}); err != nil {
		return fmt.Errorf("write control freeze v2.1 manifest: %w", err)
	}
	return nil
}

func writeFailureManifest(path string, manifest Manifest) error {
	manifest.Status = "failed"
	return writeCSVAtomic(path, func(writer *csv.Writer) error { return writeManifest(writer, manifest) })
}

func writeCSVAtomic(path string, write func(*csv.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".control-freeze-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	writer := csv.NewWriter(temporary)
	if err := write(writer); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func writeManifest(writer *csv.Writer, row Manifest) error {
	header := []string{
		"status", "failure_detail", "version", "pool_address", "from_block", "to_block", "indexed_through",
		"temporal_primary_caliper", "temporal_sensitivity_caliper", "matched_primary_caliper", "matched_sensitivity_caliper",
		"minimum_temporal_controls", "maximum_absolute_smd", "alpha", "bootstrap_iterations", "permutation_iterations", "random_seed",
		"temporal_burns", "temporal_primary_support_burns", "temporal_outside_support_burns", "temporal_extreme_tail_burns",
		"matched_full_pairs", "matched_primary_support_pairs", "matched_sensitivity_pairs",
		"primary_inference_rows", "primary_eligible_rows", "primary_blocked_rows", "primary_robust_rows", "primary_supported_rows", "primary_inconclusive_rows",
		"balance_gate_rows", "balance_gate_passed", "balance_gate_failed",
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	return writer.Write([]string{
		row.Status, row.FailureDetail, row.Version, row.PoolAddress,
		strconv.FormatUint(row.FromBlock, 10), strconv.FormatUint(row.ToBlock, 10), strconv.FormatUint(row.IndexedThrough, 10),
		formatFloat(row.TemporalPrimaryCaliper), formatFloat(row.TemporalSensitivityCaliper),
		formatFloat(row.MatchedPrimaryCaliper), formatFloat(row.MatchedSensitivityCaliper),
		strconv.Itoa(row.MinimumTemporalControls), formatFloat(row.MaximumAbsoluteSMD), formatFloat(row.Alpha),
		strconv.Itoa(row.BootstrapIterations), strconv.Itoa(row.PermutationIterations), strconv.FormatInt(row.RandomSeed, 10),
		strconv.Itoa(row.TemporalBurns), strconv.Itoa(row.TemporalPrimarySupportBurns), strconv.Itoa(row.TemporalOutsideSupportBurns), strconv.Itoa(row.TemporalExtremeTailBurns),
		strconv.Itoa(row.MatchedFullPairs), strconv.Itoa(row.MatchedPrimarySupportPairs), strconv.Itoa(row.MatchedSensitivityPairs),
		strconv.Itoa(row.PrimaryInferenceRows), strconv.Itoa(row.PrimaryEligibleRows), strconv.Itoa(row.PrimaryBlockedRows),
		strconv.Itoa(row.PrimaryRobustRows), strconv.Itoa(row.PrimarySupportedRows), strconv.Itoa(row.PrimaryInconclusiveRows),
		strconv.Itoa(row.BalanceGateRows), strconv.Itoa(row.BalanceGatePassed), strconv.Itoa(row.BalanceGateFailed),
	})
}

func writeBalanceMetrics(writer *csv.Writer, rows []BalanceMetric) error {
	if err := writer.Write([]string{
		"control_type", "analysis_set", "stratum", "metric", "treated_count", "control_count",
		"treated_mean", "control_mean", "smd", "absolute_smd", "status",
	}); err != nil {
		return err
	}
	items := append([]BalanceMetric(nil), rows...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ControlType != items[j].ControlType {
			return items[i].ControlType < items[j].ControlType
		}
		if items[i].AnalysisSet != items[j].AnalysisSet {
			return items[i].AnalysisSet < items[j].AnalysisSet
		}
		if items[i].Stratum != items[j].Stratum {
			return items[i].Stratum < items[j].Stratum
		}
		return items[i].Metric < items[j].Metric
	})
	for _, row := range items {
		if err := writer.Write([]string{
			row.ControlType, row.AnalysisSet, row.Stratum, row.Metric,
			strconv.Itoa(row.TreatedCount), strconv.Itoa(row.ControlCount),
			formatFloat(row.TreatedMean), formatFloat(row.ControlMean), formatFloat(row.SMD), formatFloat(row.AbsoluteSMD), row.Status,
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeBalanceGates(writer *csv.Writer, rows []BalanceGate) error {
	if err := writer.Write([]string{
		"control_type", "analysis_set", "stratum", "analysis_role", "metric_count", "sample_size",
		"maximum_absolute_smd", "failed_metric_count", "failed_metrics", "threshold", "balance_status", "gate_status", "primary_eligible",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.ControlType, row.AnalysisSet, row.Stratum, row.AnalysisRole,
			strconv.Itoa(row.MetricCount), strconv.Itoa(row.SampleSize), formatFloat(row.MaximumAbsoluteSMD),
			strconv.Itoa(row.FailedMetricCount), row.FailedMetrics, formatFloat(row.Threshold), row.BalanceStatus, row.GateStatus, strconv.FormatBool(row.PrimaryEligible),
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeInference(writer *csv.Writer, rows []Inference) error {
	if err := writer.Write([]string{
		"control_type", "analysis_set", "analysis_role", "outcome", "stratum", "horizon_label", "horizon_blocks", "pairs",
		"mean_difference_bps", "median_difference_bps", "positive_pairs", "positive_share",
		"bootstrap_mean_ci_lower", "bootstrap_mean_ci_upper", "wilcoxon_p_value", "wilcoxon_holm_p_value",
		"permutation_p_value", "permutation_holm_p_value", "maximum_absolute_smd", "balance_gate_status",
		"primary_eligible", "ci_excludes_zero", "wilcoxon_significant", "permutation_significant", "direction", "publication_status",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.ControlType, row.AnalysisSet, row.AnalysisRole, row.Outcome, row.Stratum, row.HorizonLabel,
			strconv.FormatUint(row.HorizonBlocks, 10), strconv.Itoa(row.Pairs),
			formatFloat(row.MeanDifferenceBps), formatFloat(row.MedianDifferenceBps), strconv.Itoa(row.PositivePairs), formatFloat(row.PositiveShare),
			formatFloat(row.BootstrapMeanCILower), formatFloat(row.BootstrapMeanCIUpper),
			formatFloat(row.WilcoxonPValue), formatFloat(row.WilcoxonHolmPValue),
			formatFloat(row.PermutationPValue), formatFloat(row.PermutationHolmPValue),
			formatFloat(row.MaximumAbsoluteSMD), row.BalanceGateStatus, strconv.FormatBool(row.PrimaryEligible),
			strconv.FormatBool(row.CIExcludesZero), strconv.FormatBool(row.WilcoxonSignificant), strconv.FormatBool(row.PermutationSignificant),
			row.Direction, row.PublicationStatus,
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeAnalysisValues(writer *csv.Writer, rows []AnalysisValue) error {
	if err := writer.Write([]string{
		"control_type", "analysis_set", "analysis_role", "stratum", "record_id", "horizon_label", "horizon_blocks", "difference_bps", "primary_eligible",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.ControlType, row.AnalysisSet, row.AnalysisRole, row.Stratum, row.RecordID, row.HorizonLabel,
			strconv.FormatUint(row.HorizonBlocks, 10), formatFloat(row.DifferenceBps), strconv.FormatBool(row.PrimaryEligible),
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeTemporalSupport(writer *csv.Writer, rows []TemporalSupport) error {
	if err := writer.Write([]string{
		"burn_event_key", "burn_block", "burn_log_index", "stratum", "burn_range_active", "immediate_lsis_bps", "lsis_percentile_rank",
		"selected_controls", "within_primary_caliper_controls", "within_sensitivity_caliper_controls", "minimum_controls_required",
		"minimum_match_distance", "mean_match_distance", "maximum_match_distance", "primary_included", "support_status", "extreme_tail_event", "exclusion_reason",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.BurnEventKey, strconv.FormatUint(row.BurnBlock, 10), strconv.Itoa(row.BurnLogIndex), row.Stratum,
			strconv.FormatBool(row.BurnRangeActive), formatFloat(row.ImmediateLSISBps), formatFloat(row.LSISPercentileRank),
			strconv.Itoa(row.SelectedControls), strconv.Itoa(row.WithinPrimaryCaliperControls), strconv.Itoa(row.WithinSensitivityCaliperControls), strconv.Itoa(row.MinimumControlsRequired),
			formatFloat(row.MinimumMatchDistance), formatFloat(row.MeanMatchDistance), formatFloat(row.MaximumMatchDistance),
			strconv.FormatBool(row.PrimaryIncluded), row.SupportStatus, strconv.FormatBool(row.ExtremeTailEvent), row.ExclusionReason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeMatchedSupport(writer *csv.Writer, rows []MatchedSupport) error {
	if err := writer.Write([]string{
		"pair_id", "stratum", "high_event_key", "low_event_key", "high_immediate_lsis_bps", "low_immediate_lsis_bps",
		"match_distance", "primary_included", "sensitivity_included", "support_status",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.PairID, row.Stratum, row.HighEventKey, row.LowEventKey,
			formatFloat(row.HighImmediateLSISBps), formatFloat(row.LowImmediateLSISBps), formatFloat(row.MatchDistance),
			strconv.FormatBool(row.PrimaryIncluded), strconv.FormatBool(row.SensitivityIncluded), row.SupportStatus,
		}); err != nil {
			return err
		}
	}
	return nil
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func OutputSummary(files OutputFiles, report Report) string {
	return strings.Join([]string{
		"control freeze v2.1 completed",
		"manifest=" + files.Manifest,
		"primary_temporal_support=" + strconv.Itoa(report.Manifest.TemporalPrimarySupportBurns),
		"primary_matched_support=" + strconv.Itoa(report.Manifest.MatchedPrimarySupportPairs),
		"primary_robust_rows=" + strconv.Itoa(report.Manifest.PrimaryRobustRows),
		"primary_supported_rows=" + strconv.Itoa(report.Manifest.PrimarySupportedRows),
	}, " ")
}
