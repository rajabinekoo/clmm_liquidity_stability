package services

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/shopspring/decimal"
)

type SnapshotBatchDiagnostics struct {
	SnapshotIndex int

	PoolAddress string
	BlockNumber uint64
	CurrentTick int

	PositionCount int

	ZeroForOneBaseAUCBps decimal.Decimal
	OneForZeroBaseAUCBps decimal.Decimal

	TopKActiveShareSum decimal.Decimal
	Top1ActiveShare    decimal.Decimal
	Top3ActiveShare    decimal.Decimal

	SumTotalLSISBps  decimal.Decimal
	Top1TotalLSISBps decimal.Decimal
	Top3TotalLSISBps decimal.Decimal

	Top1LSISShare decimal.Decimal
	Top3LSISShare decimal.Decimal

	ActiveHHIRawTopK        decimal.Decimal
	ActiveHHINormalizedTopK decimal.Decimal

	TotalLSISHHITopK           decimal.Decimal
	EffectiveLSISPositionsTopK decimal.Decimal
	ActiveShareGiniTopK        decimal.Decimal
	TotalLSISGiniTopK          decimal.Decimal
}

func BuildSnapshotBatchDiagnostics(
	results []SnapshotBatchDetailedResult,
) ([]SnapshotBatchDiagnostics, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("snapshot batch diagnostics: results are empty")
	}

	diagnostics := make([]SnapshotBatchDiagnostics, 0, len(results))

	for _, result := range results {
		if result.Pool == nil || result.Report == nil {
			continue
		}

		diagnostic := buildSingleSnapshotDiagnostics(result)
		diagnostics = append(diagnostics, diagnostic)
	}

	if len(diagnostics) == 0 {
		return nil, fmt.Errorf("snapshot batch diagnostics: no valid snapshots")
	}

	return diagnostics, nil
}

func buildSingleSnapshotDiagnostics(
	result SnapshotBatchDetailedResult,
) SnapshotBatchDiagnostics {
	positions := result.Report.Positions

	diagnostic := SnapshotBatchDiagnostics{
		SnapshotIndex: result.Summary.SnapshotIndex,

		PoolAddress: result.Pool.PoolAddress,
		BlockNumber: result.Pool.BlockNumber,
		CurrentTick: result.Pool.CurrentTick,

		PositionCount: len(positions),

		ZeroForOneBaseAUCBps: result.Report.ZeroForOneReport.BaseSummary.PriceImpactAUCBps,
		OneForZeroBaseAUCBps: result.Report.OneForZeroReport.BaseSummary.PriceImpactAUCBps,
	}

	activeShares := make([]decimal.Decimal, 0, len(positions))
	totalLSISValues := make([]decimal.Decimal, 0, len(positions))

	for rank, position := range positions {
		activeShare := position.ActiveLiquidityShare
		totalLSIS := position.TotalLSISBps

		activeShares = append(activeShares, activeShare)
		totalLSISValues = append(totalLSISValues, totalLSIS)

		diagnostic.TopKActiveShareSum = diagnostic.TopKActiveShareSum.Add(activeShare)
		diagnostic.SumTotalLSISBps = diagnostic.SumTotalLSISBps.Add(totalLSIS)

		diagnostic.ActiveHHIRawTopK = diagnostic.ActiveHHIRawTopK.Add(
			activeShare.Mul(activeShare),
		)

		if rank == 0 {
			diagnostic.Top1ActiveShare = activeShare
			diagnostic.Top1TotalLSISBps = totalLSIS
		}

		if rank < 3 {
			diagnostic.Top3ActiveShare = diagnostic.Top3ActiveShare.Add(activeShare)
			diagnostic.Top3TotalLSISBps = diagnostic.Top3TotalLSISBps.Add(totalLSIS)
		}
	}

	diagnostic.Top1LSISShare = safeDecimalRatio(
		diagnostic.Top1TotalLSISBps,
		diagnostic.SumTotalLSISBps,
	)

	diagnostic.Top3LSISShare = safeDecimalRatio(
		diagnostic.Top3TotalLSISBps,
		diagnostic.SumTotalLSISBps,
	)

	diagnostic.ActiveHHINormalizedTopK = normalizedHHI(activeShares)
	diagnostic.TotalLSISHHITopK = normalizedHHI(totalLSISValues)

	diagnostic.EffectiveLSISPositionsTopK = safeDecimalInverse(
		diagnostic.TotalLSISHHITopK,
	)

	diagnostic.ActiveShareGiniTopK = giniDecimal(activeShares)
	diagnostic.TotalLSISGiniTopK = giniDecimal(totalLSISValues)

	return diagnostic
}

func normalizedHHI(
	values []decimal.Decimal,
) decimal.Decimal {
	sum := decimal.Zero

	for _, value := range values {
		if value.IsNegative() {
			continue
		}

		sum = sum.Add(value)
	}

	if sum.IsZero() {
		return decimal.Zero
	}

	hhi := decimal.Zero

	for _, value := range values {
		if value.IsNegative() {
			continue
		}

		share := value.Div(sum)
		hhi = hhi.Add(share.Mul(share))
	}

	return hhi
}

func giniDecimal(
	values []decimal.Decimal,
) decimal.Decimal {
	filtered := make([]decimal.Decimal, 0, len(values))

	for _, value := range values {
		if value.IsNegative() {
			continue
		}

		filtered = append(filtered, value)
	}

	n := len(filtered)
	if n == 0 {
		return decimal.Zero
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].LessThan(filtered[j])
	})

	sum := decimal.Zero
	weightedSum := decimal.Zero

	for index, value := range filtered {
		sum = sum.Add(value)

		rank := decimal.NewFromInt(int64(index + 1))
		weightedSum = weightedSum.Add(rank.Mul(value))
	}

	if sum.IsZero() {
		return decimal.Zero
	}

	nDec := decimal.NewFromInt(int64(n))

	return decimal.NewFromInt(2).
		Mul(weightedSum).
		Div(nDec.Mul(sum)).
		Sub(
			decimal.NewFromInt(int64(n + 1)).
				Div(nDec),
		)
}

func safeDecimalRatio(
	numerator decimal.Decimal,
	denominator decimal.Decimal,
) decimal.Decimal {
	if denominator.IsZero() {
		return decimal.Zero
	}

	return numerator.Div(denominator)
}

func safeDecimalInverse(
	value decimal.Decimal,
) decimal.Decimal {
	if value.IsZero() {
		return decimal.Zero
	}

	return decimal.NewFromInt(1).Div(value)
}

func WriteSnapshotBatchDiagnosticsCSV(
	path string,
	diagnostics []SnapshotBatchDiagnostics,
) error {
	if len(diagnostics) == 0 {
		return fmt.Errorf("write snapshot batch diagnostics csv: diagnostics are empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create snapshot batch diagnostics csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot batch diagnostics csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write(snapshotBatchDiagnosticsHeader()); err != nil {
		return fmt.Errorf("write snapshot batch diagnostics csv header: %w", err)
	}

	for _, diagnostic := range diagnostics {
		if err := writer.Write(snapshotBatchDiagnosticsRow(diagnostic)); err != nil {
			return fmt.Errorf("write snapshot batch diagnostics csv row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush snapshot batch diagnostics csv writer: %w", err)
	}

	return nil
}

func snapshotBatchDiagnosticsHeader() []string {
	return []string{
		"snapshot_index",
		"pool_address",
		"block_number",
		"current_tick",
		"position_count",

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

		"active_hhi_raw_topk",
		"active_hhi_normalized_topk",

		"total_lsis_hhi_topk",
		"effective_lsis_positions_topk",
		"active_share_gini_topk",
		"total_lsis_gini_topk",
	}
}

func snapshotBatchDiagnosticsRow(
	diagnostic SnapshotBatchDiagnostics,
) []string {
	return []string{
		strconv.Itoa(diagnostic.SnapshotIndex),
		diagnostic.PoolAddress,
		strconv.FormatUint(diagnostic.BlockNumber, 10),
		strconv.Itoa(diagnostic.CurrentTick),
		strconv.Itoa(diagnostic.PositionCount),

		diagnostic.ZeroForOneBaseAUCBps.String(),
		diagnostic.OneForZeroBaseAUCBps.String(),

		diagnostic.TopKActiveShareSum.String(),
		diagnostic.Top1ActiveShare.String(),
		diagnostic.Top3ActiveShare.String(),

		diagnostic.SumTotalLSISBps.String(),
		diagnostic.Top1TotalLSISBps.String(),
		diagnostic.Top3TotalLSISBps.String(),

		diagnostic.Top1LSISShare.String(),
		diagnostic.Top3LSISShare.String(),

		diagnostic.ActiveHHIRawTopK.String(),
		diagnostic.ActiveHHINormalizedTopK.String(),

		diagnostic.TotalLSISHHITopK.String(),
		diagnostic.EffectiveLSISPositionsTopK.String(),
		diagnostic.ActiveShareGiniTopK.String(),
		diagnostic.TotalLSISGiniTopK.String(),
	}
}
