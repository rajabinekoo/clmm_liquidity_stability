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

func WriteSnapshotBatchPositionsCSV(
	path string,
	results []SnapshotBatchDetailedResult,
) error {
	if len(results) == 0 {
		return fmt.Errorf("write snapshot batch positions csv: results are empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create snapshot batch positions csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot batch positions csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	thresholds := snapshotBatchDepthThresholds(results)

	if err := writer.Write(snapshotBatchPositionsHeader(thresholds)); err != nil {
		return fmt.Errorf("write snapshot batch positions csv header: %w", err)
	}

	for _, result := range results {
		if result.Pool == nil || result.Report == nil {
			continue
		}

		for rank, impact := range result.Report.Positions {
			row := snapshotBatchPositionRow(
				result,
				rank+1,
				impact,
				thresholds,
			)

			if err := writer.Write(row); err != nil {
				return fmt.Errorf("write snapshot batch positions csv row: %w", err)
			}
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush snapshot batch positions csv writer: %w", err)
	}

	return nil
}

func snapshotBatchPositionsHeader(
	thresholds []decimal.Decimal,
) []string {
	header := []string{
		"snapshot_index",
		"pool_address",
		"block_number",
		"current_tick",
		"active_liquidity",
		"amount_grid_state_id",
		"amount_grid_mode",

		"rank",
		"position_label",
		"range_key",
		"position_key",
		"owner",

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

		"loaded_position_count",
		"selected_position_count",
		"position_coverage_target_bps",
		"position_coverage_achieved_bps",
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

func snapshotBatchPositionRow(
	result SnapshotBatchDetailedResult,
	rank int,
	impact BidirectionalPositionImpact,
	thresholds []decimal.Decimal,
) []string {
	row := []string{
		strconv.Itoa(result.Summary.SnapshotIndex),
		result.Pool.PoolAddress,
		strconv.FormatUint(result.Pool.BlockNumber, 10),
		strconv.Itoa(result.Pool.CurrentTick),
		result.Pool.Liquidity.String(),
		result.Summary.AmountGridStateID,
		result.Summary.AmountGridMode,

		strconv.Itoa(rank),
		fmt.Sprintf("S%d_P%d", result.Summary.SnapshotIndex, rank),
		positionRangeKey(impact),
		impact.PositionKey,
		impact.Position.Owner,

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

		result.Report.ZeroForOneReport.BaseSummary.PriceImpactAUCBps.String(),
		result.Report.OneForZeroReport.BaseSummary.PriceImpactAUCBps.String(),

		impact.ZeroForOneLSISBps.String(),
		impact.OneForZeroLSISBps.String(),
		impact.TotalLSISBps.String(),
		impact.MaxDirectionalLSISBps.String(),

		strconv.Itoa(
			result.
				Summary.
				LoadedPositionCount,
		),

		strconv.Itoa(
			result.
				Summary.
				PositionCount,
		),

		strconv.FormatInt(
			result.
				Summary.
				PositionCoverageTargetBps,
			10,
		),

		result.
			Summary.
			PositionCoverageAchievedBps.
			String(),
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

func WriteSnapshotBatchRangeAggregateCSV(
	path string,
	results []SnapshotBatchDetailedResult,
) error {
	if len(results) == 0 {
		return fmt.Errorf("write snapshot batch range aggregate csv: results are empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create snapshot batch range aggregate csv directory: %w", err)
		}
	}

	aggregates := buildRangeAggregates(results)

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot batch range aggregate csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write(snapshotBatchRangeAggregateHeader()); err != nil {
		return fmt.Errorf("write snapshot batch range aggregate csv header: %w", err)
	}

	for _, aggregate := range aggregates {
		if err := writer.Write(snapshotBatchRangeAggregateRow(aggregate)); err != nil {
			return fmt.Errorf("write snapshot batch range aggregate csv row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush snapshot batch range aggregate csv writer: %w", err)
	}

	return nil
}

type snapshotRangeAggregate struct {
	RangeKey  string
	TickLower int
	TickUpper int

	Appearances int
	Rank1Count  int
	MinRank     int
	RankSum     int

	MinBlock uint64
	MaxBlock uint64

	ActiveShareSum        decimal.Decimal
	ZeroForOneLSISSum     decimal.Decimal
	OneForZeroLSISSum     decimal.Decimal
	TotalLSISSum          decimal.Decimal
	MaxDirectionalLSISSum decimal.Decimal

	MaxTotalLSIS       decimal.Decimal
	MaxDirectionalLSIS decimal.Decimal

	initialized bool
}

func buildRangeAggregates(
	results []SnapshotBatchDetailedResult,
) []snapshotRangeAggregate {
	byRange := make(map[string]*snapshotRangeAggregate)

	for _, result := range results {
		if result.Report == nil {
			continue
		}

		for rankIndex, impact := range result.Report.Positions {
			rank := rankIndex + 1
			key := positionRangeKey(impact)

			aggregate, exists := byRange[key]
			if !exists {
				aggregate = &snapshotRangeAggregate{
					RangeKey:  key,
					TickLower: impact.Position.TickLower,
					TickUpper: impact.Position.TickUpper,
					MinRank:   rank,
					MinBlock:  result.Summary.BlockNumber,
					MaxBlock:  result.Summary.BlockNumber,
				}
				byRange[key] = aggregate
			}

			aggregate.Appearances++
			aggregate.RankSum += rank

			if rank == 1 {
				aggregate.Rank1Count++
			}

			if rank < aggregate.MinRank {
				aggregate.MinRank = rank
			}

			if result.Summary.BlockNumber < aggregate.MinBlock {
				aggregate.MinBlock = result.Summary.BlockNumber
			}
			if result.Summary.BlockNumber > aggregate.MaxBlock {
				aggregate.MaxBlock = result.Summary.BlockNumber
			}

			aggregate.ActiveShareSum = aggregate.ActiveShareSum.Add(
				impact.ActiveLiquidityShare,
			)

			aggregate.ZeroForOneLSISSum = aggregate.ZeroForOneLSISSum.Add(
				impact.ZeroForOneLSISBps,
			)

			aggregate.OneForZeroLSISSum = aggregate.OneForZeroLSISSum.Add(
				impact.OneForZeroLSISBps,
			)

			aggregate.TotalLSISSum = aggregate.TotalLSISSum.Add(
				impact.TotalLSISBps,
			)

			aggregate.MaxDirectionalLSISSum = aggregate.MaxDirectionalLSISSum.Add(
				impact.MaxDirectionalLSISBps,
			)

			if !aggregate.initialized ||
				impact.TotalLSISBps.GreaterThan(aggregate.MaxTotalLSIS) {
				aggregate.MaxTotalLSIS = impact.TotalLSISBps
			}

			if !aggregate.initialized ||
				impact.MaxDirectionalLSISBps.GreaterThan(aggregate.MaxDirectionalLSIS) {
				aggregate.MaxDirectionalLSIS = impact.MaxDirectionalLSISBps
			}

			aggregate.initialized = true
		}
	}

	aggregates := make([]snapshotRangeAggregate, 0, len(byRange))
	for _, aggregate := range byRange {
		aggregates = append(aggregates, *aggregate)
	}

	sort.SliceStable(aggregates, func(i, j int) bool {
		if aggregates[i].Appearances == aggregates[j].Appearances {
			return avgDecimal(
				aggregates[i].TotalLSISSum,
				aggregates[i].Appearances,
			).GreaterThan(
				avgDecimal(
					aggregates[j].TotalLSISSum,
					aggregates[j].Appearances,
				),
			)
		}

		return aggregates[i].Appearances > aggregates[j].Appearances
	})

	return aggregates
}

func snapshotBatchRangeAggregateHeader() []string {
	return []string{
		"range_key",
		"tick_lower",
		"tick_upper",

		"appearances",
		"rank1_count",
		"min_rank",
		"avg_rank",

		"min_block",
		"max_block",

		"avg_active_liquidity_share",

		"avg_zero_for_one_lsis_bps",
		"avg_one_for_zero_lsis_bps",
		"avg_total_lsis_bps",
		"avg_max_directional_lsis_bps",

		"max_total_lsis_bps",
		"max_directional_lsis_bps",
	}
}

func snapshotBatchRangeAggregateRow(
	aggregate snapshotRangeAggregate,
) []string {
	return []string{
		aggregate.RangeKey,
		strconv.Itoa(aggregate.TickLower),
		strconv.Itoa(aggregate.TickUpper),

		strconv.Itoa(aggregate.Appearances),
		strconv.Itoa(aggregate.Rank1Count),
		strconv.Itoa(aggregate.MinRank),
		avgRank(aggregate.RankSum, aggregate.Appearances).String(),

		strconv.FormatUint(aggregate.MinBlock, 10),
		strconv.FormatUint(aggregate.MaxBlock, 10),

		avgDecimal(
			aggregate.ActiveShareSum,
			aggregate.Appearances,
		).String(),

		avgDecimal(
			aggregate.ZeroForOneLSISSum,
			aggregate.Appearances,
		).String(),

		avgDecimal(
			aggregate.OneForZeroLSISSum,
			aggregate.Appearances,
		).String(),

		avgDecimal(
			aggregate.TotalLSISSum,
			aggregate.Appearances,
		).String(),

		avgDecimal(
			aggregate.MaxDirectionalLSISSum,
			aggregate.Appearances,
		).String(),

		aggregate.MaxTotalLSIS.String(),
		aggregate.MaxDirectionalLSIS.String(),
	}
}

func snapshotBatchDepthThresholds(
	results []SnapshotBatchDetailedResult,
) []decimal.Decimal {
	for _, result := range results {
		if result.Report == nil {
			continue
		}

		thresholds := reportDepthThresholds(result.Report)
		if len(thresholds) > 0 {
			return thresholds
		}
	}

	return nil
}

func positionRangeKey(
	impact BidirectionalPositionImpact,
) string {
	return fmt.Sprintf(
		"%d:%d",
		impact.Position.TickLower,
		impact.Position.TickUpper,
	)
}

func avgDecimal(
	sum decimal.Decimal,
	count int,
) decimal.Decimal {
	if count <= 0 {
		return decimal.Zero
	}

	return sum.Div(decimal.NewFromInt(int64(count)))
}

func avgRank(
	rankSum int,
	count int,
) decimal.Decimal {
	if count <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromInt(int64(rankSum)).
		Div(decimal.NewFromInt(int64(count)))
}
