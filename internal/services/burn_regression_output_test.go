package services

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestBurnRegressionOutputs(
	t *testing.T,
) {
	t.Parallel()

	burn :=
		collectorBurnCandidate(
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			100,
			10,
			100,
		)

	sample :=
		regressionSampleFixture(
			t,
			burn,
		)

	horizon :=
		BurnOutcomeHorizon{
			Label: "h10",

			Blocks: 10,
		}

	outcome :=
		regressionOutcomeFixture(
			sample,
			horizon,
			900,
		)

	observation, err :=
		newBurnRegressionObservation(
			sample,
			outcome,
		)
	if err != nil {
		t.Fatalf(
			"newBurnRegressionObservation() error = %v",
			err,
		)
	}

	skipped :=
		BurnRealizedOutcomeSkip{
			Burn: cloneBurnCandidate(
				burn,
			),

			HorizonLabel: "h20",

			HorizonBlocks: 20,

			FutureBlock: burn.Cursor.BlockNumber +
				20,

			Reason: BurnOutcomeSkipFutureBlockNotIndexed,

			Detail: "future block is not indexed",
		}

	report :=
		BurnRealizedDatasetReport{
			PoolAddress: burn.PoolAddress,

			FromBlock: 100,

			ToBlock: 200,

			BurnIndexedThrough: 500,

			OutcomeIndexedThrough: 1_000,

			BurnSamples: 1,

			HorizonsPerSample: 2,

			CandidateHorizonPairs: 2,

			ObservedHorizonPairs: 1,

			SkippedHorizonPairs: 1,

			Observations: []BurnRegressionObservation{
				observation,
			},

			Skipped: []BurnRealizedOutcomeSkip{
				skipped,
			},
		}

	summary, err :=
		BuildBurnRealizedDatasetSummary(
			report,
		)
	if err != nil {
		t.Fatalf(
			"BuildBurnRealizedDatasetSummary() error = %v",
			err,
		)
	}

	if summary.ObservedHorizonPairs != 1 ||
		summary.SkippedHorizonPairs != 1 {
		t.Fatalf(
			"unexpected summary counts: observed=%d skipped=%d",
			summary.ObservedHorizonPairs,
			summary.SkippedHorizonPairs,
		)
	}

	outputDirectory :=
		t.TempDir()

	observationsPath :=
		filepath.Join(
			outputDirectory,
			"observations.csv",
		)

	depthsPath :=
		filepath.Join(
			outputDirectory,
			"depths.csv",
		)

	censoringPath :=
		filepath.Join(
			outputDirectory,
			"censoring.csv",
		)

	summaryPath :=
		filepath.Join(
			outputDirectory,
			"summary.csv",
		)

	if err :=
		WriteBurnRegressionObservationsCSV(
			observationsPath,
			report.Observations,
		); err != nil {
		t.Fatalf(
			"WriteBurnRegressionObservationsCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnRegressionDepthsCSV(
			depthsPath,
			report.Observations,
		); err != nil {
		t.Fatalf(
			"WriteBurnRegressionDepthsCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnRegressionCensoringCSV(
			censoringPath,
			report.Skipped,
		); err != nil {
		t.Fatalf(
			"WriteBurnRegressionCensoringCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnRealizedDatasetSummaryCSV(
			summaryPath,
			summary,
		); err != nil {
		t.Fatalf(
			"WriteBurnRealizedDatasetSummaryCSV() error = %v",
			err,
		)
	}

	observationRecords :=
		readBurnRegressionCSV(
			t,
			observationsPath,
		)

	if len(observationRecords) != 2 {
		t.Fatalf(
			"observation CSV rows=%d, want 2",
			len(observationRecords),
		)
	}

	depthRecords :=
		readBurnRegressionCSV(
			t,
			depthsPath,
		)

	expectedDepthRows :=
		1 +
			len(
				observation.
					ImmediateZeroForOne.
					DepthDeltas,
			) +
			len(
				observation.
					ImmediateOneForZero.
					DepthDeltas,
			)

	if len(depthRecords) !=
		expectedDepthRows {
		t.Fatalf(
			"depth CSV rows=%d, want %d",
			len(depthRecords),
			expectedDepthRows,
		)
	}

	censoringRecords :=
		readBurnRegressionCSV(
			t,
			censoringPath,
		)

	if len(censoringRecords) != 2 {
		t.Fatalf(
			"censoring CSV rows=%d, want 2",
			len(censoringRecords),
		)
	}

	summaryRecords :=
		readBurnRegressionCSV(
			t,
			summaryPath,
		)

	if len(summaryRecords) != 2 {
		t.Fatalf(
			"summary CSV rows=%d, want 2",
			len(summaryRecords),
		)
	}
}

func TestBurnRegressionOutputsAllowEmptyRows(
	t *testing.T,
) {
	t.Parallel()

	outputDirectory :=
		t.TempDir()

	observationsPath :=
		filepath.Join(
			outputDirectory,
			"observations.csv",
		)

	depthsPath :=
		filepath.Join(
			outputDirectory,
			"depths.csv",
		)

	censoringPath :=
		filepath.Join(
			outputDirectory,
			"censoring.csv",
		)

	if err :=
		WriteBurnRegressionObservationsCSV(
			observationsPath,
			nil,
		); err != nil {
		t.Fatalf(
			"WriteBurnRegressionObservationsCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnRegressionDepthsCSV(
			depthsPath,
			nil,
		); err != nil {
		t.Fatalf(
			"WriteBurnRegressionDepthsCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnRegressionCensoringCSV(
			censoringPath,
			nil,
		); err != nil {
		t.Fatalf(
			"WriteBurnRegressionCensoringCSV() error = %v",
			err,
		)
	}

	for _, path := range []string{
		observationsPath,
		depthsPath,
		censoringPath,
	} {
		records :=
			readBurnRegressionCSV(
				t,
				path,
			)

		if len(records) != 1 {
			t.Fatalf(
				"%s rows=%d, want header only",
				path,
				len(records),
			)
		}
	}
}

func TestBurnRegressionObservationsRejectInvalidDataWithoutReplacingFile(
	t *testing.T,
) {
	t.Parallel()

	burn :=
		collectorBurnCandidate(
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			100,
			10,
			100,
		)

	sample :=
		regressionSampleFixture(
			t,
			burn,
		)

	outcome :=
		regressionOutcomeFixture(
			sample,
			BurnOutcomeHorizon{
				Label: "h10",

				Blocks: 10,
			},
			900,
		)

	observation, err :=
		newBurnRegressionObservation(
			sample,
			outcome,
		)
	if err != nil {
		t.Fatalf(
			"newBurnRegressionObservation() error = %v",
			err,
		)
	}

	observation.FutureBlock++

	path :=
		filepath.Join(
			t.TempDir(),
			"observations.csv",
		)

	const original = "existing-file-content\n"

	if err :=
		os.WriteFile(
			path,
			[]byte(original),
			0o644,
		); err != nil {
		t.Fatalf(
			"WriteFile() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnRegressionObservationsCSV(
			path,
			[]BurnRegressionObservation{
				observation,
			},
		); err == nil {
		t.Fatal(
			"WriteBurnRegressionObservationsCSV() expected validation error",
		)
	}

	content, err :=
		os.ReadFile(
			path,
		)
	if err != nil {
		t.Fatalf(
			"ReadFile() error = %v",
			err,
		)
	}

	if string(content) != original {
		t.Fatalf(
			"existing file was replaced: %q",
			content,
		)
	}
}

func readBurnRegressionCSV(
	t *testing.T,
	path string,
) [][]string {
	t.Helper()

	file, err :=
		os.Open(
			path,
		)
	if err != nil {
		t.Fatalf(
			"Open(%s) error = %v",
			path,
			err,
		)
	}
	defer file.Close()

	records, err :=
		csv.NewReader(
			file,
		).ReadAll()
	if err != nil {
		t.Fatalf(
			"ReadAll(%s) error = %v",
			path,
			err,
		)
	}

	return records
}
