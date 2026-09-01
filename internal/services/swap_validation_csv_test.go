package services

import (
	"encoding/csv"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

func TestSwapValidationReportCSVOutputs(
	t *testing.T,
) {
	t.Parallel()

	report :=
		swapValidationReportFixture()

	summary, err :=
		BuildSwapValidationSummary(
			report,
		)
	if err != nil {
		t.Fatalf(
			"BuildSwapValidationSummary() error = %v",
			err,
		)
	}

	if summary.CleanSamples != 2 ||
		summary.SkippedSamples != 2 {
		t.Fatalf(
			"samples = clean:%d skipped:%d, want clean:2 skipped:2",
			summary.CleanSamples,
			summary.SkippedSamples,
		)
	}

	if summary.ExactMatches != 1 {
		t.Fatalf(
			"ExactMatches = %d, want 1",
			summary.ExactMatches,
		)
	}

	assertSwapValidationDecimalEqual(
		t,
		summary.CleanSamplePercent,
		decimal.NewFromInt(50),
	)

	assertSwapValidationDecimalEqual(
		t,
		summary.ExactMatchPercent,
		decimal.NewFromInt(50),
	)

	if summary.
		IncompleteLPIndexSkips != 1 ||
		summary.
			PriorLiquidityActionSkips != 1 {
		t.Fatalf(
			"unexpected skip counts: incomplete=%d prior_action=%d",
			summary.IncompleteLPIndexSkips,
			summary.PriorLiquidityActionSkips,
		)
	}

	if summary.TotalSwapSteps != 3 ||
		summary.TotalCrossedTicks != 1 {
		t.Fatalf(
			"unexpected simulation totals: steps=%d crossed=%d",
			summary.TotalSwapSteps,
			summary.TotalCrossedTicks,
		)
	}

	outputDir :=
		t.TempDir()

	resultsPath :=
		filepath.Join(
			outputDir,
			"results.csv",
		)

	skipsPath :=
		filepath.Join(
			outputDir,
			"skips.csv",
		)

	summaryPath :=
		filepath.Join(
			outputDir,
			"summary.csv",
		)

	if err :=
		WriteSwapValidationCSV(
			resultsPath,
			report.Results,
		); err != nil {
		t.Fatalf(
			"WriteSwapValidationCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteSwapValidationSkipsCSV(
			skipsPath,
			report.Skipped,
		); err != nil {
		t.Fatalf(
			"WriteSwapValidationSkipsCSV() error = %v",
			err,
		)
	}

	if err :=
		WriteSwapValidationSummaryCSV(
			summaryPath,
			summary,
		); err != nil {
		t.Fatalf(
			"WriteSwapValidationSummaryCSV() error = %v",
			err,
		)
	}

	results :=
		readSwapValidationCSV(
			t,
			resultsPath,
		)

	if len(results) != 3 {
		t.Fatalf(
			"results CSV records = %d, want 3",
			len(results),
		)
	}

	assertSwapValidationCSVValue(
		t,
		results,
		"sim_fee_amount_raw",
		1,
		"1",
	)

	assertSwapValidationCSVValue(
		t,
		results,
		"fee_accounting_exact",
		1,
		"true",
	)

	assertSwapValidationCSVValue(
		t,
		results,
		"sqrt_price_exact",
		2,
		"false",
	)

	assertSwapValidationCSVValue(
		t,
		results,
		"sim_swap_steps",
		2,
		"2",
	)

	skips :=
		readSwapValidationCSV(
			t,
			skipsPath,
		)

	if len(skips) != 3 {
		t.Fatalf(
			"skips CSV records = %d, want 3",
			len(skips),
		)
	}

	assertSwapValidationCSVValue(
		t,
		skips,
		"reason",
		1,
		string(
			SwapValidationSkipIncompleteLPIndex,
		),
	)

	assertSwapValidationCSVValue(
		t,
		skips,
		"reason",
		2,
		string(
			SwapValidationSkipPriorLiquidityAction,
		),
	)

	summaryRecords :=
		readSwapValidationCSV(
			t,
			summaryPath,
		)

	if len(summaryRecords) != 2 {
		t.Fatalf(
			"summary CSV records = %d, want 2",
			len(summaryRecords),
		)
	}

	assertSwapValidationCSVValue(
		t,
		summaryRecords,
		"clean_samples",
		1,
		"2",
	)

	assertSwapValidationCSVValue(
		t,
		summaryRecords,
		"exact_match_percent",
		1,
		"50",
	)
}

func TestWriteSwapValidationCSVAllowsEmptyResults(
	t *testing.T,
) {
	t.Parallel()

	path :=
		filepath.Join(
			t.TempDir(),
			"empty.csv",
		)

	if err :=
		WriteSwapValidationCSV(
			path,
			nil,
		); err != nil {
		t.Fatalf(
			"WriteSwapValidationCSV() error = %v",
			err,
		)
	}

	records :=
		readSwapValidationCSV(
			t,
			path,
		)

	if len(records) != 1 {
		t.Fatalf(
			"records = %d, want header only",
			len(records),
		)
	}
}

func TestSwapValidationOutputRejectsBrokenFeeAccountingWithoutReplacingFile(
	t *testing.T,
) {
	t.Parallel()

	report :=
		swapValidationReportFixture()

	report.Results[0].
		SimFeeAmountRaw =
		big.NewInt(2)

	path :=
		filepath.Join(
			t.TempDir(),
			"results.csv",
		)

	if err := os.WriteFile(
		path,
		[]byte("original"),
		0o644,
	); err != nil {
		t.Fatalf(
			"os.WriteFile() error = %v",
			err,
		)
	}

	if err :=
		WriteSwapValidationCSV(
			path,
			report.Results,
		); err == nil {
		t.Fatal(
			"WriteSwapValidationCSV() expected fee-accounting error",
		)
	}

	content, err :=
		os.ReadFile(path)
	if err != nil {
		t.Fatalf(
			"os.ReadFile() error = %v",
			err,
		)
	}

	if string(content) !=
		"original" {
		t.Fatalf(
			"existing file changed after failure: %q",
			content,
		)
	}
}

func TestBuildSwapValidationSummaryRejectsCandidateMismatch(
	t *testing.T,
) {
	t.Parallel()

	report :=
		swapValidationReportFixture()

	report.CandidateBlocks++

	if _, err :=
		BuildSwapValidationSummary(
			report,
		); err == nil {
		t.Fatal(
			"BuildSwapValidationSummary() expected candidate-count error",
		)
	}
}

func swapValidationReportFixture() SwapValidationReport {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	simulatedSecondPrice :=
		new(big.Int).Add(
			new(big.Int).Set(q96),
			big.NewInt(10),
		)

	amountOutDiff :=
		big.NewInt(2)

	sqrtPriceDiff :=
		big.NewInt(10)

	return SwapValidationReport{
		PoolAddress: "0x0000000000000000000000000000000000000001",

		FromBlock: 100,

		ToBlock: 103,

		GraphIndexedThrough: 110,

		ScannedWindows: 2,

		CandidateBlocks: 4,

		Results: []SwapValidationResult{
			{
				SwapID: "exact",

				TxHash: "0xexact",

				BlockNumber: 100,

				LogIndex: 1,

				ZeroForOne: true,

				SnapshotBlock: 99,

				AmountInRaw: big.NewInt(1_000),

				SimAmountInLessFeeRaw: big.NewInt(999),

				SimFeeAmountRaw: big.NewInt(1),

				ActualAmountOutRaw: big.NewInt(900),

				SimAmountOutRaw: big.NewInt(900),

				AmountOutAbsDiffRaw: big.NewInt(0),

				AmountOutDiffBps: decimal.Zero,

				AmountOutExact: true,

				ActualSqrtPriceX96After: new(big.Int).Set(q96),

				SimSqrtPriceX96After: new(big.Int).Set(q96),

				SqrtPriceAbsDiffRaw: big.NewInt(0),

				SqrtPriceDiffBps: decimal.Zero,

				SqrtPriceExact: true,

				ActualTickAfter: 0,

				SimTickAfter: 0,

				TickDelta: 0,

				TickExact: true,

				SimCrossedTicks: 0,

				SimSwapSteps: 1,

				ExactMatch: true,
			},
			{
				SwapID: "different",

				TxHash: "0xdifferent",

				BlockNumber: 101,

				LogIndex: 2,

				ZeroForOne: false,

				SnapshotBlock: 100,

				AmountInRaw: big.NewInt(2_000),

				SimAmountInLessFeeRaw: big.NewInt(1_999),

				SimFeeAmountRaw: big.NewInt(1),

				ActualAmountOutRaw: big.NewInt(1_800),

				SimAmountOutRaw: big.NewInt(1_798),

				AmountOutAbsDiffRaw: new(big.Int).Set(
					amountOutDiff,
				),

				AmountOutDiffBps: relativeDiffBps(
					amountOutDiff,
					big.NewInt(1_800),
				),

				AmountOutExact: false,

				ActualSqrtPriceX96After: new(big.Int).Set(q96),

				SimSqrtPriceX96After: simulatedSecondPrice,

				SqrtPriceAbsDiffRaw: new(big.Int).Set(
					sqrtPriceDiff,
				),

				SqrtPriceDiffBps: relativeDiffBps(
					sqrtPriceDiff,
					q96,
				),

				SqrtPriceExact: false,

				ActualTickAfter: 0,

				SimTickAfter: -1,

				TickDelta: 1,

				TickExact: false,

				SimCrossedTicks: 1,

				SimSwapSteps: 2,

				ExactMatch: false,
			},
		},

		Skipped: []SwapValidationSkip{
			{
				SwapID: "incomplete",

				TxHash: "0xincomplete",

				BlockNumber: 102,

				LogIndex: 3,

				Reason: SwapValidationSkipIncompleteLPIndex,

				Detail: "checkpoint is incomplete",
			},
			{
				SwapID: "prior-action",

				TxHash: "0xprior-action",

				BlockNumber: 103,

				LogIndex: 4,

				Reason: SwapValidationSkipPriorLiquidityAction,

				Detail: "Mint precedes Swap",
			},
		},
	}
}

func readSwapValidationCSV(
	t *testing.T,
	path string,
) [][]string {
	t.Helper()

	file, err :=
		os.Open(path)
	if err != nil {
		t.Fatalf(
			"os.Open(%q) error = %v",
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
			"csv.ReadAll() error = %v",
			err,
		)
	}

	return records
}

func assertSwapValidationCSVValue(
	t *testing.T,
	records [][]string,
	columnName string,
	rowIndex int,
	expected string,
) {
	t.Helper()

	columnIndex := -1

	for index, value := range records[0] {
		if value == columnName {
			columnIndex = index
			break
		}
	}

	if columnIndex < 0 {
		t.Fatalf(
			"CSV column %q not found",
			columnName,
		)
	}

	if rowIndex >= len(records) {
		t.Fatalf(
			"CSV row index %d out of range %d",
			rowIndex,
			len(records),
		)
	}

	if actual :=
		records[rowIndex][columnIndex]; actual != expected {
		t.Fatalf(
			"CSV row=%d column=%q value=%q, want %q",
			rowIndex,
			columnName,
			actual,
			expected,
		)
	}
}

func assertSwapValidationDecimalEqual(
	t *testing.T,
	actual decimal.Decimal,
	expected decimal.Decimal,
) {
	t.Helper()

	if !actual.Equal(expected) {
		t.Fatalf(
			"actual = %s, want %s",
			actual,
			expected,
		)
	}
}
