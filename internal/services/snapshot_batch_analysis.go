package services

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type SnapshotBatchProvider interface {
	PoolSnapshotAt(
		ctx context.Context,
		poolAddress string,
		blockNumber uint64,
	) (domain.PoolSnapshot, error)
}

type SnapshotBatchAnalysisService struct {
	provider   SnapshotBatchProvider
	repository *repositories.PoolStateRepository
	impact     *LiquidityImpactService
}

func NewSnapshotBatchAnalysisService(
	provider SnapshotBatchProvider,
	repository *repositories.PoolStateRepository,
	impact *LiquidityImpactService,
) *SnapshotBatchAnalysisService {
	return &SnapshotBatchAnalysisService{
		provider:   provider,
		repository: repository,
		impact:     impact,
	}
}

type SnapshotBatchRequest struct {
	PoolAddress string

	LatestBlock    uint64
	LookbackBlocks uint64
	StepBlocks     uint64
	MaxSnapshots   int

	ZeroForOneAmountsIn []*big.Int

	AmountGridResolver AnalysisAmountGridResolver

	PositionLimit int
	ThresholdsBps []decimal.Decimal

	PositionCoverageBps int64

	JointRemovalTopCounts []int

	JointRemovalActiveLiquidityShareBps []int64
}

type SnapshotBatchResult struct {
	SnapshotIndex int

	PoolAddress string
	BlockNumber uint64
	CurrentTick int

	ActiveLiquidity string

	AmountGridStateID string
	AmountGridMode    string

	PositionCount int

	ZeroForOneBaseAUCBps decimal.Decimal
	OneForZeroBaseAUCBps decimal.Decimal

	TopPositionLabel string
	TopTickLower     int
	TopTickUpper     int

	TopActiveLiquidityShare decimal.Decimal

	TopZeroForOneLSISBps decimal.Decimal
	TopOneForZeroLSISBps decimal.Decimal
	TopTotalLSISBps      decimal.Decimal
	TopMaxDirectionalBps decimal.Decimal

	SumTotalLSISBps decimal.Decimal

	LoadedPositionCount int

	PositionCoverageTargetBps   int64
	PositionCoverageAchievedBps decimal.Decimal
}

type SnapshotBatchDetailedResult struct {
	Summary SnapshotBatchResult

	Pool *domain.ReconstructedPool

	Report *BidirectionalLiquidityImpactReport

	JointRemoval *JointRemovalReport
}

func (s *SnapshotBatchAnalysisService) Analyze(
	ctx context.Context,
	req SnapshotBatchRequest,
) ([]SnapshotBatchDetailedResult, error) {
	if req.PoolAddress == "" {
		return nil, fmt.Errorf("snapshot batch analysis: pool address is required")
	}
	if req.LatestBlock == 0 {
		return nil, fmt.Errorf("snapshot batch analysis: latest block is required")
	}
	if req.LookbackBlocks == 0 {
		req.LookbackBlocks = 50_000
	}
	if req.StepBlocks == 0 {
		req.StepBlocks = 5_000
	}
	if req.MaxSnapshots <= 0 {
		req.MaxSnapshots = 10
	}
	if req.PositionLimit <= 0 {
		req.PositionLimit = 10
	}
	if req.PositionCoverageBps == 0 {
		req.PositionCoverageBps =
			defaultPositionCoverageBps
	}
	if req.PositionCoverageBps < 1 ||
		req.PositionCoverageBps >
			positionCoverageDenominator {
		return nil, fmt.Errorf(
			"snapshot batch analysis: position coverage bps %d must be inside [1,%d]",
			req.PositionCoverageBps,
			positionCoverageDenominator,
		)
	}
	if req.AmountGridResolver == nil && len(req.ZeroForOneAmountsIn) == 0 {
		return nil, fmt.Errorf("snapshot batch analysis: zero_for_one amounts are required")
	}
	if len(req.ThresholdsBps) == 0 {
		req.ThresholdsBps = []decimal.Decimal{
			decimal.NewFromInt(10),
			decimal.NewFromInt(50),
			decimal.NewFromInt(100),
		}
	}
	if _, err :=
		buildJointRemovalScenarioSpecs(
			req.JointRemovalTopCounts,
			req.JointRemovalActiveLiquidityShareBps,
		); err != nil {
		return nil, fmt.Errorf(
			"snapshot batch analysis: invalid joint-removal configuration: %w",
			err,
		)
	}

	blocks := snapshotCandidateBlocks(
		req.LatestBlock,
		req.LookbackBlocks,
		req.StepBlocks,
		req.MaxSnapshots,
	)

	results := make([]SnapshotBatchDetailedResult, 0, len(blocks))

	for index, blockNumber := range blocks {
		result, err := s.analyzeSingleSnapshot(
			ctx,
			index+1,
			blockNumber,
			req,
		)
		if err != nil {
			slog.Warn(
				"snapshot skipped",
				"pool_address", req.PoolAddress,
				"block_number", blockNumber,
				"reason", err,
			)

			continue
		}

		results = append(results, result)
	}

	return results, nil
}

func (s *SnapshotBatchAnalysisService) analyzeSingleSnapshot(
	ctx context.Context,
	snapshotIndex int,
	blockNumber uint64,
	req SnapshotBatchRequest,
) (SnapshotBatchDetailedResult, error) {
	pool, err := loadHistoricalPoolAt(
		ctx,
		s.provider,
		s.repository,
		req.PoolAddress,
		blockNumber,
	)
	if err != nil {
		return SnapshotBatchDetailedResult{}, fmt.Errorf(
			"load local reconstructed pool: %w",
			err,
		)
	}

	zeroForOneAmountsIn := req.ZeroForOneAmountsIn
	oneForZeroAmountsIn := []*big.Int(nil)
	amountGridStateID := ""
	amountGridMode := AnalysisAmountGridModeRaw

	if req.AmountGridResolver != nil {
		grid, gridErr := req.AmountGridResolver.Resolve(ctx, pool)
		if gridErr != nil {
			return SnapshotBatchDetailedResult{}, fmt.Errorf(
				"resolve analysis amount grid: %w",
				gridErr,
			)
		}
		if gridErr = grid.RequireComplete(); gridErr != nil {
			return SnapshotBatchDetailedResult{}, gridErr
		}

		zeroForOneAmountsIn, gridErr = grid.ZeroForOneAmountsIn()
		if gridErr != nil {
			return SnapshotBatchDetailedResult{}, gridErr
		}
		oneForZeroAmountsIn, gridErr = grid.OneForZeroAmountsIn()
		if gridErr != nil {
			return SnapshotBatchDetailedResult{}, gridErr
		}

		amountGridStateID = grid.StateID
		amountGridMode = grid.Mode
	} else {
		var gridErr error
		oneForZeroAmountsIn, gridErr = batchToken1EquivalentAmounts(
			req.ZeroForOneAmountsIn,
			pool.SqrtPriceX96,
		)
		if gridErr != nil {
			return SnapshotBatchDetailedResult{}, gridErr
		}
	}

	report, err := s.impact.AnalyzeBidirectionalActivePositions(
		ctx,
		BidirectionalLiquidityImpactRequest{
			Pool:                pool,
			ZeroForOneAmountsIn: zeroForOneAmountsIn,
			OneForZeroAmountsIn: oneForZeroAmountsIn,
			PositionLimit:       req.PositionLimit,
			ThresholdsBps:       req.ThresholdsBps,
			PositionCoverageBps: req.PositionCoverageBps,
		},
	)
	if err != nil {
		return SnapshotBatchDetailedResult{}, err
	}

	jointRemovalReport, err :=
		s.impact.AnalyzeJointRemovalScenarios(
			ctx,
			JointRemovalRequest{
				Pool: pool,

				BaseReport: report,

				ZeroForOneAmountsIn: zeroForOneAmountsIn,

				OneForZeroAmountsIn: oneForZeroAmountsIn,

				ThresholdsBps: req.ThresholdsBps,

				TopCounts: append(
					[]int(nil),
					req.
						JointRemovalTopCounts...,
				),

				TargetActiveLiquidityShareBps: append(
					[]int64(nil),
					req.
						JointRemovalActiveLiquidityShareBps...,
				),
			},
		)
	if err != nil {
		return SnapshotBatchDetailedResult{}, fmt.Errorf(
			"analyze joint removal scenarios: %w",
			err,
		)
	}

	summary := summarizeSnapshotBatchResult(
		snapshotIndex,
		pool,
		report,
	)
	summary.AmountGridStateID = amountGridStateID
	summary.AmountGridMode = amountGridMode

	return SnapshotBatchDetailedResult{
		Summary: summary,

		Pool: pool,

		Report: report,

		JointRemoval: jointRemovalReport,
	}, nil
}

func summarizeSnapshotBatchResult(
	snapshotIndex int,
	pool *domain.ReconstructedPool,
	report *BidirectionalLiquidityImpactReport,
) SnapshotBatchResult {
	result := SnapshotBatchResult{
		SnapshotIndex: snapshotIndex,

		PoolAddress: pool.PoolAddress,
		BlockNumber: pool.BlockNumber,
		CurrentTick: pool.CurrentTick,

		ActiveLiquidity: pool.Liquidity.String(),

		LoadedPositionCount: report.
			ZeroForOneReport.
			LoadedPositionCount,

		PositionCount: len(report.Positions),

		PositionCoverageTargetBps: report.
			ZeroForOneReport.
			PositionCoverageTargetBps,

		PositionCoverageAchievedBps: report.
			ZeroForOneReport.
			PositionCoverageAchievedBps,

		ZeroForOneBaseAUCBps: report.ZeroForOneReport.BaseSummary.PriceImpactAUCBps,
		OneForZeroBaseAUCBps: report.OneForZeroReport.BaseSummary.PriceImpactAUCBps,
	}

	sumTotal := decimal.Zero

	for _, position := range report.Positions {
		sumTotal = sumTotal.Add(position.TotalLSISBps)
	}

	result.SumTotalLSISBps = sumTotal

	if len(report.Positions) == 0 {
		return result
	}

	top := report.Positions[0]

	result.TopPositionLabel = "P1"
	result.TopTickLower = top.Position.TickLower
	result.TopTickUpper = top.Position.TickUpper
	result.TopActiveLiquidityShare = top.ActiveLiquidityShare
	result.TopZeroForOneLSISBps = top.ZeroForOneLSISBps
	result.TopOneForZeroLSISBps = top.OneForZeroLSISBps
	result.TopTotalLSISBps = top.TotalLSISBps
	result.TopMaxDirectionalBps = top.MaxDirectionalLSISBps

	return result
}

func snapshotCandidateBlocks(
	latestBlock uint64,
	lookbackBlocks uint64,
	stepBlocks uint64,
	maxSnapshots int,
) []uint64 {
	if maxSnapshots <= 0 {
		return nil
	}
	if stepBlocks == 0 {
		stepBlocks = 1
	}

	earliest := uint64(1)
	if latestBlock > lookbackBlocks {
		earliest = latestBlock - lookbackBlocks
	}

	blocks := make([]uint64, 0, maxSnapshots)

	for block := latestBlock; block >= earliest; {
		blocks = append(blocks, block)

		if len(blocks) >= maxSnapshots {
			break
		}
		if block <= stepBlocks || block-stepBlocks < earliest {
			break
		}

		block -= stepBlocks
	}

	return blocks
}

func batchToken1EquivalentAmounts(
	token0Amounts []*big.Int,
	sqrtPriceX96 *big.Int,
) ([]*big.Int, error) {
	result := make([]*big.Int, 0, len(token0Amounts))

	for _, amount0 := range token0Amounts {
		amount1, err := uniswapv3.QuoteToken1ForToken0Raw(
			sqrtPriceX96,
			amount0,
		)
		if err != nil {
			return nil, err
		}

		result = append(result, amount1)
	}

	return result, nil
}

func WriteSnapshotBatchSummaryCSV(
	path string,
	results []SnapshotBatchDetailedResult,
) error {
	if len(results) == 0 {
		return fmt.Errorf("write snapshot batch summary csv: results are empty")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create snapshot batch csv directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot batch csv file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write(snapshotBatchCSVHeader()); err != nil {
		return fmt.Errorf("write snapshot batch csv header: %w", err)
	}

	for _, result := range results {
		if err := writer.Write(snapshotBatchCSVRow(result.Summary)); err != nil {
			return fmt.Errorf("write snapshot batch csv row: %w", err)
		}
	}

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush snapshot batch csv writer: %w", err)
	}

	return nil
}

func snapshotBatchCSVHeader() []string {
	return []string{
		"snapshot_index",
		"pool_address",
		"block_number",
		"current_tick",
		"active_liquidity",
		"amount_grid_state_id",
		"amount_grid_mode",
		"position_count",

		"zero_for_one_base_auc_bps",
		"one_for_zero_base_auc_bps",

		"top_position_label",
		"top_tick_lower",
		"top_tick_upper",
		"top_active_liquidity_share",

		"top_zero_for_one_lsis_bps",
		"top_one_for_zero_lsis_bps",
		"top_total_lsis_bps",
		"top_max_directional_lsis_bps",

		"sum_total_lsis_bps",

		"loaded_position_count",
		"selected_position_count",

		"position_coverage_target_bps",
		"position_coverage_achieved_bps",
	}
}

func snapshotBatchCSVRow(
	result SnapshotBatchResult,
) []string {
	return []string{
		strconv.Itoa(result.SnapshotIndex),
		result.PoolAddress,
		strconv.FormatUint(result.BlockNumber, 10),
		strconv.Itoa(result.CurrentTick),
		result.ActiveLiquidity,
		result.AmountGridStateID,
		result.AmountGridMode,
		strconv.Itoa(result.PositionCount),

		result.ZeroForOneBaseAUCBps.String(),
		result.OneForZeroBaseAUCBps.String(),

		result.TopPositionLabel,
		strconv.Itoa(result.TopTickLower),
		strconv.Itoa(result.TopTickUpper),
		result.TopActiveLiquidityShare.String(),

		result.TopZeroForOneLSISBps.String(),
		result.TopOneForZeroLSISBps.String(),
		result.TopTotalLSISBps.String(),
		result.TopMaxDirectionalBps.String(),

		result.SumTotalLSISBps.String(),

		strconv.Itoa(
			result.LoadedPositionCount,
		),

		strconv.Itoa(
			result.PositionCount,
		),

		strconv.FormatInt(
			result.PositionCoverageTargetBps,
			10,
		),

		result.
			PositionCoverageAchievedBps.
			String(),
	}
}
