package services

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

func TestBurnSampleCollectionOutputs(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	burn :=
		collectorBurnCandidate(
			poolAddress,
			100,
			10,
			100,
		)

	preBurn :=
		collectorPreBurnFixture(
			burn,
			1_000,
		)

	impact :=
		collectorImpactFixture(
			preBurn,
		)

	sample, err :=
		newBurnEventSample(
			preBurn,
			impact,
		)
	if err != nil {
		t.Fatalf(
			"newBurnEventSample() error = %v",
			err,
		)
	}

	skippedBefore :=
		collectorBurnCandidate(
			poolAddress,
			101,
			10,
			100,
		)

	skippedAfter :=
		collectorBurnCandidate(
			poolAddress,
			102,
			10,
			100,
		)

	lastCursor :=
		skippedAfter.Cursor

	report :=
		BurnSampleCollectionReport{
			PoolAddress: poolAddress,

			FromBlock: 100,

			ToBlock: 200,

			IndexedThrough: 250,

			Pages: 2,

			CandidateEvents: 3,

			AnalyzedEvents: 1,

			SkippedEvents: 2,

			LastProcessedCursor: &lastCursor,

			Samples: []BurnEventSample{
				sample,
			},

			Skipped: []BurnSampleSkip{
				{
					Burn: skippedBefore,

					Reason: BurnSampleSkipZeroActiveLiquidityBefore,

					Detail: "active liquidity before burn is zero",
				},
				{
					Burn: skippedAfter,

					Reason: BurnSampleSkipZeroActiveLiquidityAfter,

					Detail: "burn removes all active liquidity",
				},
			},
		}

	summary, err :=
		BuildBurnSampleCollectionSummary(
			report,
		)
	if err != nil {
		t.Fatalf(
			"BuildBurnSampleCollectionSummary() error = %v",
			err,
		)
	}

	if summary.ActiveBurnSamples != 1 ||
		summary.InactiveBurnSamples != 0 {
		t.Fatalf(
			"unexpected active counts: active=%d inactive=%d",
			summary.ActiveBurnSamples,
			summary.InactiveBurnSamples,
		)
	}

	if !summary.SampleYieldPercent.Equal(
		decimal.RequireFromString(
			"33.3333333333333333",
		),
	) {
		t.Fatalf(
			"SampleYieldPercent = %s",
			summary.SampleYieldPercent,
		)
	}

	outputDirectory :=
		t.TempDir()

	samplesPath :=
		filepath.Join(
			outputDirectory,
			"burn_samples.csv",
		)

	depthsPath :=
		filepath.Join(
			outputDirectory,
			"burn_depths.csv",
		)

	exclusionsPath :=
		filepath.Join(
			outputDirectory,
			"burn_exclusions.csv",
		)

	summaryPath :=
		filepath.Join(
			outputDirectory,
			"burn_summary.csv",
		)

	if err := WriteBurnEventSamplesCSV(
		samplesPath,
		report.Samples,
	); err != nil {
		t.Fatalf(
			"WriteBurnEventSamplesCSV() error = %v",
			err,
		)
	}

	if err := WriteBurnEventDepthsCSV(
		depthsPath,
		report.Samples,
	); err != nil {
		t.Fatalf(
			"WriteBurnEventDepthsCSV() error = %v",
			err,
		)
	}

	if err := WriteBurnSampleExclusionsCSV(
		exclusionsPath,
		report.Skipped,
	); err != nil {
		t.Fatalf(
			"WriteBurnSampleExclusionsCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteBurnSampleCollectionSummaryCSV(
			summaryPath,
			summary,
		); err != nil {
		t.Fatalf(
			"WriteBurnSampleCollectionSummaryCSV() error = %v",
			err,
		)
	}

	sampleRecords :=
		readBurnOutputCSV(
			t,
			samplesPath,
		)

	if len(sampleRecords) != 2 {
		t.Fatalf(
			"sample CSV rows = %d, want 2",
			len(sampleRecords),
		)
	}

	depthRecords :=
		readBurnOutputCSV(
			t,
			depthsPath,
		)

	expectedDepthRows :=
		1 +
			len(
				sample.
					ZeroForOne.
					DepthDeltas,
			) +
			len(
				sample.
					OneForZero.
					DepthDeltas,
			)

	if len(depthRecords) !=
		expectedDepthRows {
		t.Fatalf(
			"depth CSV rows = %d, want %d",
			len(depthRecords),
			expectedDepthRows,
		)
	}

	exclusionRecords :=
		readBurnOutputCSV(
			t,
			exclusionsPath,
		)

	if len(exclusionRecords) != 3 {
		t.Fatalf(
			"exclusion CSV rows = %d, want 3",
			len(exclusionRecords),
		)
	}

	summaryRecords :=
		readBurnOutputCSV(
			t,
			summaryPath,
		)

	if len(summaryRecords) != 2 {
		t.Fatalf(
			"summary CSV rows = %d, want 2",
			len(summaryRecords),
		)
	}
}

func TestBurnSampleCSVOutputsAllowEmptyRows(
	t *testing.T,
) {
	t.Parallel()

	outputDirectory :=
		t.TempDir()

	samplesPath :=
		filepath.Join(
			outputDirectory,
			"samples.csv",
		)

	depthsPath :=
		filepath.Join(
			outputDirectory,
			"depths.csv",
		)

	exclusionsPath :=
		filepath.Join(
			outputDirectory,
			"exclusions.csv",
		)

	if err := WriteBurnEventSamplesCSV(
		samplesPath,
		nil,
	); err != nil {
		t.Fatalf(
			"WriteBurnEventSamplesCSV() error = %v",
			err,
		)
	}

	if err := WriteBurnEventDepthsCSV(
		depthsPath,
		nil,
	); err != nil {
		t.Fatalf(
			"WriteBurnEventDepthsCSV() error = %v",
			err,
		)
	}

	if err := WriteBurnSampleExclusionsCSV(
		exclusionsPath,
		nil,
	); err != nil {
		t.Fatalf(
			"WriteBurnSampleExclusionsCSV() error = %v",
			err,
		)
	}

	for _, path := range []string{
		samplesPath,
		depthsPath,
		exclusionsPath,
	} {
		records :=
			readBurnOutputCSV(
				t,
				path,
			)

		if len(records) != 1 {
			t.Fatalf(
				"%s rows = %d, want header only",
				path,
				len(records),
			)
		}
	}
}

func TestBurnSampleCSVRejectsInvalidSampleWithoutReplacingFile(
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

	preBurn :=
		collectorPreBurnFixture(
			burn,
			1_000,
		)

	sample, err :=
		newBurnEventSample(
			preBurn,
			collectorImpactFixture(
				preBurn,
			),
		)
	if err != nil {
		t.Fatalf(
			"newBurnEventSample() error = %v",
			err,
		)
	}

	sample.RangeWidth++

	path :=
		filepath.Join(
			t.TempDir(),
			"samples.csv",
		)

	const original = "existing-file-content\n"

	if err := os.WriteFile(
		path,
		[]byte(original),
		0o644,
	); err != nil {
		t.Fatalf(
			"WriteFile() error = %v",
			err,
		)
	}

	if err := WriteBurnEventSamplesCSV(
		path,
		[]BurnEventSample{
			sample,
		},
	); err == nil {
		t.Fatal(
			"WriteBurnEventSamplesCSV() expected validation error",
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

func TestBuildBurnSampleCollectionSummaryRejectsDuplicateEvent(
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

	preBurn :=
		collectorPreBurnFixture(
			burn,
			1_000,
		)

	sample, err :=
		newBurnEventSample(
			preBurn,
			collectorImpactFixture(
				preBurn,
			),
		)
	if err != nil {
		t.Fatalf(
			"newBurnEventSample() error = %v",
			err,
		)
	}

	lastCursor :=
		burn.Cursor

	_, err =
		BuildBurnSampleCollectionSummary(
			BurnSampleCollectionReport{
				PoolAddress: burn.PoolAddress,

				FromBlock: 100,

				ToBlock: 200,

				IndexedThrough: 250,

				Pages: 1,

				CandidateEvents: 2,

				AnalyzedEvents: 1,

				SkippedEvents: 1,

				LastProcessedCursor: &lastCursor,

				Samples: []BurnEventSample{
					sample,
				},

				Skipped: []BurnSampleSkip{
					{
						Burn: burn,

						Reason: BurnSampleSkipZeroActiveLiquidityBefore,

						Detail: "duplicate fixture",
					},
				},
			},
		)

	if err == nil {
		t.Fatal(
			"BuildBurnSampleCollectionSummary() expected duplicate-event error",
		)
	}
}

func readBurnOutputCSV(
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
