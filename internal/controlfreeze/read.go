package controlfreeze

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type temporalMatch struct {
	MatchID                     string
	BurnEventKey                string
	BurnBlock                   uint64
	BurnLogIndex                int
	Stratum                     string
	BurnRangeActive             bool
	PlaceboAnchorBlock          uint64
	MatchDistance               float64
	BurnCurrentTick             float64
	PlaceboCurrentTick          float64
	BurnActiveLiquidity         float64
	PlaceboActiveLiquidity      float64
	BurnBaselineTotalAUCBps     float64
	PlaceboBaselineTotalAUCBps  float64
	BurnImmediateLSISBps        float64
	BurnDirectionalImbalance    float64
	PlaceboDirectionalImbalance float64
}

type temporalObservation struct {
	MatchID                         string
	BurnEventKey                    string
	Stratum                         string
	BurnRangeActive                 bool
	HorizonLabel                    string
	HorizonBlocks                   uint64
	Available                       bool
	ActualRealizedDeteriorationBps  float64
	PlaceboRealizedDeteriorationBps float64
}

type matchedPair struct {
	PairID                 string
	Stratum                string
	MatchDistance          float64
	HighEventKey           string
	LowEventKey            string
	HighImmediateLSISBps   float64
	LowImmediateLSISBps    float64
	HighBlock              float64
	LowBlock               float64
	HighCurrentTick        float64
	LowCurrentTick         float64
	HighActiveLiquidity    float64
	LowActiveLiquidity     float64
	HighLiquidityRemoved   float64
	LowLiquidityRemoved    float64
	HighRemovalFraction    float64
	LowRemovalFraction     float64
	HighActiveRemovalShare float64
	LowActiveRemovalShare  float64
	HighRangeWidth         float64
	LowRangeWidth          float64
	HighDistanceOutside    float64
	LowDistanceOutside     float64
}

type matchedOutcome struct {
	PairID                  string
	Stratum                 string
	HorizonLabel            string
	HorizonBlocks           uint64
	RealizedDifferenceBps   float64
	MechanicalAvailable     bool
	MechanicalDifferenceBps float64
}

type parsedInputs struct {
	Manifest             SourceManifest
	TemporalMatches      []temporalMatch
	TemporalObservations []temporalObservation
	MatchedPairs         []matchedPair
	MatchedOutcomes      []matchedOutcome
}

func DiscoverInputFiles(inputDir string) (InputFiles, error) {
	if strings.TrimSpace(inputDir) == "" {
		return InputFiles{}, fmt.Errorf("discover control freeze inputs: input directory is empty")
	}
	absolute, err := filepath.Abs(inputDir)
	if err != nil {
		return InputFiles{}, fmt.Errorf("discover control freeze inputs: resolve input directory: %w", err)
	}
	manifest, err := exactlyOne(absolute, "*_control_v2_manifest.csv")
	if err != nil {
		return InputFiles{}, err
	}
	stem := strings.TrimSuffix(manifest, "_control_v2_manifest.csv")
	files := InputFiles{
		Manifest:             manifest,
		TemporalMatches:      stem + "_control_v2_temporal_placebo_matches.csv",
		TemporalObservations: stem + "_control_v2_temporal_placebo_observations.csv",
		MatchedPairs:         stem + "_control_v2_matched_burn_pairs.csv",
		MatchedOutcomes:      stem + "_control_v2_matched_burn_outcomes.csv",
		Stem:                 stem,
	}
	for _, path := range []string{files.TemporalMatches, files.TemporalObservations, files.MatchedPairs, files.MatchedOutcomes} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return InputFiles{}, fmt.Errorf("discover control freeze inputs: required file %s: %w", path, statErr)
		}
		if info.IsDir() {
			return InputFiles{}, fmt.Errorf("discover control freeze inputs: required path %s is a directory", path)
		}
	}
	return files, nil
}

func OutputFilesFor(inputs InputFiles) OutputFiles {
	return OutputFiles{
		Manifest:             inputs.Stem + "_control_v21_manifest.csv",
		BalanceMetrics:       inputs.Stem + "_control_v21_balance_metrics.csv",
		BalanceGate:          inputs.Stem + "_control_v21_balance_gate.csv",
		PublicationInference: inputs.Stem + "_control_v21_publication_inference.csv",
		AnalysisValues:       inputs.Stem + "_control_v21_analysis_values.csv",
		TemporalSupport:      inputs.Stem + "_control_v21_temporal_support.csv",
		MatchedSupport:       inputs.Stem + "_control_v21_matched_support.csv",
	}
}

func exactlyOne(directory, pattern string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(directory, pattern))
	if err != nil {
		return "", fmt.Errorf("discover control freeze inputs: glob %s: %w", pattern, err)
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		return "", fmt.Errorf("discover control freeze inputs: expected exactly one %s in %s, found %d", pattern, directory, len(matches))
	}
	return matches[0], nil
}

func loadInputs(files InputFiles) (parsedInputs, error) {
	manifestRows, err := readCSVMaps(files.Manifest)
	if err != nil {
		return parsedInputs{}, err
	}
	if len(manifestRows) != 1 {
		return parsedInputs{}, fmt.Errorf("load control freeze inputs: manifest must contain exactly one row, found %d", len(manifestRows))
	}
	manifest, err := parseSourceManifest(manifestRows[0])
	if err != nil {
		return parsedInputs{}, err
	}
	if manifest.Status != "completed" {
		return parsedInputs{}, fmt.Errorf("load control freeze inputs: source control v2 manifest status is %q", manifest.Status)
	}

	matchRows, err := readCSVMaps(files.TemporalMatches)
	if err != nil {
		return parsedInputs{}, err
	}
	matches := make([]temporalMatch, 0, len(matchRows))
	for index, row := range matchRows {
		item, parseErr := parseTemporalMatch(row)
		if parseErr != nil {
			return parsedInputs{}, fmt.Errorf("load control freeze inputs: temporal match row %d: %w", index+2, parseErr)
		}
		matches = append(matches, item)
	}

	observationRows, err := readCSVMaps(files.TemporalObservations)
	if err != nil {
		return parsedInputs{}, err
	}
	observations := make([]temporalObservation, 0, len(observationRows))
	for index, row := range observationRows {
		item, parseErr := parseTemporalObservation(row)
		if parseErr != nil {
			return parsedInputs{}, fmt.Errorf("load control freeze inputs: temporal observation row %d: %w", index+2, parseErr)
		}
		observations = append(observations, item)
	}

	pairRows, err := readCSVMaps(files.MatchedPairs)
	if err != nil {
		return parsedInputs{}, err
	}
	pairs := make([]matchedPair, 0, len(pairRows))
	for index, row := range pairRows {
		if row["analysis_set"] != "full_sensitivity" {
			continue
		}
		item, parseErr := parseMatchedPair(row)
		if parseErr != nil {
			return parsedInputs{}, fmt.Errorf("load control freeze inputs: matched pair row %d: %w", index+2, parseErr)
		}
		pairs = append(pairs, item)
	}

	outcomeRows, err := readCSVMaps(files.MatchedOutcomes)
	if err != nil {
		return parsedInputs{}, err
	}
	outcomes := make([]matchedOutcome, 0, len(outcomeRows))
	for index, row := range outcomeRows {
		if row["analysis_set"] != "full_sensitivity" {
			continue
		}
		item, parseErr := parseMatchedOutcome(row)
		if parseErr != nil {
			return parsedInputs{}, fmt.Errorf("load control freeze inputs: matched outcome row %d: %w", index+2, parseErr)
		}
		outcomes = append(outcomes, item)
	}

	tickByBurn := make(map[string]float64, len(matches))
	for _, match := range matches {
		if previous, exists := tickByBurn[match.BurnEventKey]; exists && previous != match.BurnCurrentTick {
			return parsedInputs{}, fmt.Errorf("load control freeze inputs: inconsistent current tick for burn %s", match.BurnEventKey)
		}
		tickByBurn[match.BurnEventKey] = match.BurnCurrentTick
	}
	for index := range pairs {
		highTick, highExists := tickByBurn[pairs[index].HighEventKey]
		lowTick, lowExists := tickByBurn[pairs[index].LowEventKey]
		if !highExists || !lowExists {
			return parsedInputs{}, fmt.Errorf("load control freeze inputs: matched pair %s references burn missing from temporal matches", pairs[index].PairID)
		}
		pairs[index].HighCurrentTick = highTick
		pairs[index].LowCurrentTick = lowTick
	}

	if len(matches) == 0 || len(observations) == 0 || len(pairs) == 0 || len(outcomes) == 0 {
		return parsedInputs{}, fmt.Errorf("load control freeze inputs: one or more required datasets are empty")
	}
	return parsedInputs{
		Manifest:             manifest,
		TemporalMatches:      matches,
		TemporalObservations: observations,
		MatchedPairs:         pairs,
		MatchedOutcomes:      outcomes,
	}, nil
}

func readCSVMaps(path string) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read csv %s: %w", path, err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.ReuseRecord = false
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read csv %s header: %w", path, err)
	}
	seen := make(map[string]struct{}, len(header))
	for _, name := range header {
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("read csv %s: duplicate header %q", path, name)
		}
		seen[name] = struct{}{}
	}
	rows := make([]map[string]string, 0)
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read csv %s: %w", path, readErr)
		}
		if len(record) != len(header) {
			return nil, fmt.Errorf("read csv %s: row has %d columns, expected %d", path, len(record), len(header))
		}
		row := make(map[string]string, len(header))
		for index, name := range header {
			row[name] = strings.TrimSpace(record[index])
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseSourceManifest(row map[string]string) (SourceManifest, error) {
	var result SourceManifest
	var err error
	result.Status = row["status"]
	result.PoolAddress = row["pool_address"]
	if result.FromBlock, err = parseUint(row, "from_block"); err != nil {
		return result, err
	}
	if result.ToBlock, err = parseUint(row, "to_block"); err != nil {
		return result, err
	}
	if result.IndexedThrough, err = parseUint(row, "indexed_through"); err != nil {
		return result, err
	}
	if result.CandidateStates, err = parseInt(row, "candidate_states"); err != nil {
		return result, err
	}
	if result.TemporalMatches, err = parseInt(row, "temporal_matches"); err != nil {
		return result, err
	}
	if result.MatchedBurnFullPairs, err = parseInt(row, "matched_burn_full_pairs"); err != nil {
		return result, err
	}
	if result.BootstrapIterations, err = parseInt(row, "bootstrap_iterations"); err != nil {
		return result, err
	}
	if result.PermutationIterations, err = parseInt(row, "permutation_iterations"); err != nil {
		return result, err
	}
	if result.RandomSeed, err = parseInt64(row, "random_seed"); err != nil {
		return result, err
	}
	return result, nil
}

func parseTemporalMatch(row map[string]string) (temporalMatch, error) {
	var result temporalMatch
	var err error
	result.MatchID = required(row, "match_id")
	result.BurnEventKey = required(row, "burn_event_key")
	result.Stratum = required(row, "burn_range_location")
	if result.BurnBlock, err = parseUint(row, "burn_block"); err != nil {
		return result, err
	}
	if result.BurnLogIndex, err = parseInt(row, "burn_log_index"); err != nil {
		return result, err
	}
	if result.BurnRangeActive, err = parseBool(row, "burn_range_active"); err != nil {
		return result, err
	}
	if result.PlaceboAnchorBlock, err = parseUint(row, "placebo_anchor_block"); err != nil {
		return result, err
	}
	if result.MatchDistance, err = parseFloat(row, "match_distance"); err != nil {
		return result, err
	}
	if result.BurnCurrentTick, err = parseFloat(row, "burn_current_tick"); err != nil {
		return result, err
	}
	if result.PlaceboCurrentTick, err = parseFloat(row, "placebo_current_tick"); err != nil {
		return result, err
	}
	if result.BurnActiveLiquidity, err = parseFloat(row, "burn_active_liquidity"); err != nil {
		return result, err
	}
	if result.PlaceboActiveLiquidity, err = parseFloat(row, "placebo_active_liquidity"); err != nil {
		return result, err
	}
	if result.BurnBaselineTotalAUCBps, err = parseFloat(row, "burn_baseline_total_auc_bps"); err != nil {
		return result, err
	}
	if result.PlaceboBaselineTotalAUCBps, err = parseFloat(row, "placebo_baseline_total_auc_bps"); err != nil {
		return result, err
	}
	if result.BurnImmediateLSISBps, err = parseFloat(row, "burn_immediate_total_lsis_bps"); err != nil {
		return result, err
	}
	if result.BurnDirectionalImbalance, err = parseFloat(row, "burn_directional_imbalance"); err != nil {
		return result, err
	}
	if result.PlaceboDirectionalImbalance, err = parseFloat(row, "placebo_directional_imbalance"); err != nil {
		return result, err
	}
	return result, nil
}

func parseTemporalObservation(row map[string]string) (temporalObservation, error) {
	var result temporalObservation
	var err error
	result.MatchID = required(row, "match_id")
	result.BurnEventKey = required(row, "burn_event_key")
	result.Stratum = required(row, "burn_range_location")
	result.HorizonLabel = required(row, "horizon_label")
	if result.BurnRangeActive, err = parseBool(row, "burn_range_active"); err != nil {
		return result, err
	}
	if result.HorizonBlocks, err = parseUint(row, "horizon_blocks"); err != nil {
		return result, err
	}
	if result.Available, err = parseBool(row, "available"); err != nil {
		return result, err
	}
	if result.ActualRealizedDeteriorationBps, err = parseFloat(row, "actual_total_realized_deterioration_bps"); err != nil {
		return result, err
	}
	if result.PlaceboRealizedDeteriorationBps, err = parseFloat(row, "placebo_total_realized_deterioration_bps"); err != nil {
		return result, err
	}
	return result, nil
}

func parseMatchedPair(row map[string]string) (matchedPair, error) {
	var result matchedPair
	var err error
	result.PairID = required(row, "pair_id")
	result.Stratum = required(row, "stratum")
	result.HighEventKey = required(row, "high_event_key")
	result.LowEventKey = required(row, "low_event_key")
	if result.MatchDistance, err = parseFloat(row, "match_distance"); err != nil {
		return result, err
	}
	if result.HighImmediateLSISBps, err = parseFloat(row, "high_immediate_lsis_bps"); err != nil {
		return result, err
	}
	if result.LowImmediateLSISBps, err = parseFloat(row, "low_immediate_lsis_bps"); err != nil {
		return result, err
	}
	if result.HighBlock, err = parseFloat(row, "high_block"); err != nil {
		return result, err
	}
	if result.LowBlock, err = parseFloat(row, "low_block"); err != nil {
		return result, err
	}
	if result.HighCurrentTick, err = eventKeyTickFallback(row, "high_current_tick", result.HighEventKey); err != nil {
		return result, err
	}
	if result.LowCurrentTick, err = eventKeyTickFallback(row, "low_current_tick", result.LowEventKey); err != nil {
		return result, err
	}
	if result.HighActiveLiquidity, err = parseFloat(row, "high_active_liquidity"); err != nil {
		return result, err
	}
	if result.LowActiveLiquidity, err = parseFloat(row, "low_active_liquidity"); err != nil {
		return result, err
	}
	if result.HighLiquidityRemoved, err = parseFloat(row, "high_liquidity_removed"); err != nil {
		return result, err
	}
	if result.LowLiquidityRemoved, err = parseFloat(row, "low_liquidity_removed"); err != nil {
		return result, err
	}
	if result.HighRemovalFraction, err = parseFloat(row, "high_removal_fraction"); err != nil {
		return result, err
	}
	if result.LowRemovalFraction, err = parseFloat(row, "low_removal_fraction"); err != nil {
		return result, err
	}
	if result.HighActiveRemovalShare, err = parseFloat(row, "high_active_removal_share"); err != nil {
		return result, err
	}
	if result.LowActiveRemovalShare, err = parseFloat(row, "low_active_removal_share"); err != nil {
		return result, err
	}
	if result.HighRangeWidth, err = parseFloat(row, "high_range_width"); err != nil {
		return result, err
	}
	if result.LowRangeWidth, err = parseFloat(row, "low_range_width"); err != nil {
		return result, err
	}
	if result.HighDistanceOutside, err = parseFloat(row, "high_normalized_distance_outside"); err != nil {
		return result, err
	}
	if result.LowDistanceOutside, err = parseFloat(row, "low_normalized_distance_outside"); err != nil {
		return result, err
	}
	return result, nil
}

// Current-tick values are not present in the matched-pair CSV. The final freeze
// therefore reconstructs that covariate by joining the pair member to the
// temporal-match dataset before analysis. This parser leaves it as NaN.
func eventKeyTickFallback(row map[string]string, column, _ string) (float64, error) {
	if value, exists := row[column]; exists && strings.TrimSpace(value) != "" {
		return parseFiniteFloat(column, value)
	}
	return math.NaN(), nil
}

func parseMatchedOutcome(row map[string]string) (matchedOutcome, error) {
	var result matchedOutcome
	var err error
	result.PairID = required(row, "pair_id")
	result.Stratum = required(row, "stratum")
	result.HorizonLabel = required(row, "horizon_label")
	if result.HorizonBlocks, err = parseUint(row, "horizon_blocks"); err != nil {
		return result, err
	}
	if result.RealizedDifferenceBps, err = parseFloat(row, "realized_deterioration_difference_bps"); err != nil {
		return result, err
	}
	if result.MechanicalAvailable, err = parseBool(row, "mechanical_both_available"); err != nil {
		return result, err
	}
	if result.MechanicalDifferenceBps, err = parseFloat(row, "mechanical_midpoint_difference_bps"); err != nil {
		return result, err
	}
	return result, nil
}

func required(row map[string]string, column string) string {
	return strings.TrimSpace(row[column])
}

func parseUint(row map[string]string, column string) (uint64, error) {
	value := required(row, column)
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q as uint64: %w", column, value, err)
	}
	return parsed, nil
}

func parseInt(row map[string]string, column string) (int, error) {
	value := required(row, column)
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q as int: %w", column, value, err)
	}
	return parsed, nil
}

func parseInt64(row map[string]string, column string) (int64, error) {
	value := required(row, column)
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q as int64: %w", column, value, err)
	}
	return parsed, nil
}

func parseFloat(row map[string]string, column string) (float64, error) {
	return parseFiniteFloat(column, required(row, column))
}

func parseFiniteFloat(column, value string) (float64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q as float64: %w", column, value, err)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("parse %s=%q: non-finite value", column, value)
	}
	return parsed, nil
}

func parseBool(row map[string]string, column string) (bool, error) {
	value := required(row, column)
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s=%q as bool: %w", column, value, err)
	}
	return parsed, nil
}
