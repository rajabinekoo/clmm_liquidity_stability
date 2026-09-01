package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type LiquidityImpactService struct {
	repository   *repositories.PoolStateRepository
	curveService *PriceImpactCurveService
}

func NewLiquidityImpactService(
	repository *repositories.PoolStateRepository,
	curveService *PriceImpactCurveService,
) *LiquidityImpactService {
	return &LiquidityImpactService{
		repository:   repository,
		curveService: curveService,
	}
}

type LiquidityImpactRequest struct {
	Pool          *domain.ReconstructedPool
	AmountsIn     []*big.Int
	ThresholdsBps []decimal.Decimal
	ZeroForOne    bool

	// PositionLimit is a safety cap for loading active positions.
	PositionLimit int

	// PositionCoverageBps is the minimum active-liquidity coverage
	// that the selected position prefix must reach.
	PositionCoverageBps int64
}

type LiquidityImpactReport struct {
	ZeroForOne  bool
	BaseSummary PriceImpactCurveSummary
	Positions   []PositionLiquidityImpact

	PositionLimit int

	LoadedPositionCount   int
	SelectedPositionCount int

	SelectedActiveLiquidity *big.Int

	PositionCoverageTargetBps   int64
	PositionCoverageAchievedBps decimal.Decimal
}

type PositionLiquidityImpact struct {
	Position domain.LiquidityPosition

	BaseActiveLiquidity           *big.Int
	CounterfactualActiveLiquidity *big.Int

	BaseAUCBps           decimal.Decimal
	CounterfactualAUCBps decimal.Decimal
	DeltaAUCBps          decimal.Decimal
	LSISBps              decimal.Decimal

	DepthDeltas []DepthDelta
}

type DepthDelta struct {
	ThresholdBps decimal.Decimal

	BaseDepthAmount           decimal.Decimal
	CounterfactualDepthAmount decimal.Decimal

	// Positive means removing the position reduced safe executable depth.
	DeltaDepthAmount decimal.Decimal

	BaseBreached           bool
	CounterfactualBreached bool
}

func (s *LiquidityImpactService) AnalyzeActivePositions(
	ctx context.Context,
	req LiquidityImpactRequest,
) (*LiquidityImpactReport, error) {
	if req.Pool == nil {
		return nil, fmt.Errorf("liquidity impact: pool is nil")
	}
	if len(req.AmountsIn) == 0 {
		return nil, fmt.Errorf("liquidity impact: amounts_in is empty")
	}
	if len(req.ThresholdsBps) == 0 {
		return nil, fmt.Errorf("liquidity impact: thresholds_bps is empty")
	}
	if req.PositionLimit <= 0 {
		return nil, fmt.Errorf("liquidity impact: position_limit must be positive")
	}

	if req.PositionCoverageBps == 0 {
		req.PositionCoverageBps =
			defaultPositionCoverageBps
	}

	if req.PositionCoverageBps < 1 ||
		req.PositionCoverageBps >
			positionCoverageDenominator {
		return nil, fmt.Errorf(
			"liquidity impact: position_coverage_bps %d must be inside [1,%d]",
			req.PositionCoverageBps,
			positionCoverageDenominator,
		)
	}

	baseCurve, err := s.curveService.Build(ctx, PriceImpactCurveRequest{
		Pool:       req.Pool,
		AmountsIn:  req.AmountsIn,
		ZeroForOne: req.ZeroForOne,
	})
	if err != nil {
		return nil, fmt.Errorf("liquidity impact: build base curve: %w", err)
	}

	baseSummary, err := s.curveService.Summarize(
		baseCurve,
		req.ThresholdsBps,
	)
	if err != nil {
		return nil, fmt.Errorf("liquidity impact: summarize base curve: %w", err)
	}

	loadedPositions, err :=
		s.repository.LoadActivePositionsAt(
			ctx,
			req.Pool.PoolAddress,
			req.Pool.BlockNumber,
			req.Pool.CurrentTick,
			req.PositionLimit,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"liquidity impact: load active positions: %w",
			err,
		)
	}

	selection, err :=
		selectActivePositionsByCoverage(
			loadedPositions,
			req.Pool.Liquidity,
			req.PositionCoverageBps,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"liquidity impact: select positions by coverage: %w",
			err,
		)
	}

	impacts := make(
		[]PositionLiquidityImpact,
		0,
		len(selection.Positions),
	)

	for _, position := range selection.Positions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		counterfactualPool, err := uniswapv3.RemoveLiquidity(
			req.Pool,
			position,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"liquidity impact: remove liquidity range [%d,%d]: %w",
				position.TickLower,
				position.TickUpper,
				err,
			)
		}

		counterfactualCurve, err := s.curveService.Build(ctx, PriceImpactCurveRequest{
			Pool:       counterfactualPool,
			AmountsIn:  req.AmountsIn,
			ZeroForOne: req.ZeroForOne,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"liquidity impact: build counterfactual curve range [%d,%d]: %w",
				position.TickLower,
				position.TickUpper,
				err,
			)
		}

		counterfactualSummary, err := s.curveService.Summarize(
			counterfactualCurve,
			req.ThresholdsBps,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"liquidity impact: summarize counterfactual curve range [%d,%d]: %w",
				position.TickLower,
				position.TickUpper,
				err,
			)
		}

		deltaAUC := counterfactualSummary.PriceImpactAUCBps.
			Sub(baseSummary.PriceImpactAUCBps)

		impact := PositionLiquidityImpact{
			Position: position,

			BaseActiveLiquidity: new(big.Int).Set(req.Pool.Liquidity),
			CounterfactualActiveLiquidity: new(big.Int).Set(
				counterfactualPool.Liquidity,
			),

			BaseAUCBps:           baseSummary.PriceImpactAUCBps,
			CounterfactualAUCBps: counterfactualSummary.PriceImpactAUCBps,
			DeltaAUCBps:          deltaAUC,
			LSISBps:              positiveOnly(deltaAUC),

			DepthDeltas: compareDepths(
				baseSummary.ThresholdDepths,
				counterfactualSummary.ThresholdDepths,
			),
		}

		impacts = append(impacts, impact)
	}

	sort.SliceStable(impacts, func(i, j int) bool {
		return impacts[i].LSISBps.GreaterThan(impacts[j].LSISBps)
	})

	return &LiquidityImpactReport{
		ZeroForOne:  req.ZeroForOne,
		BaseSummary: *baseSummary,
		Positions:   impacts,

		PositionLimit: req.PositionLimit,

		LoadedPositionCount: selection.LoadedPositionCount,

		SelectedPositionCount: selection.SelectedPositionCount,

		SelectedActiveLiquidity: new(big.Int).Set(
			selection.SelectedLiquidity,
		),

		PositionCoverageTargetBps: selection.TargetCoverageBps,

		PositionCoverageAchievedBps: selection.AchievedCoverageBps,
	}, nil
}

func positiveOnly(value decimal.Decimal) decimal.Decimal {
	if value.IsNegative() {
		return decimal.Zero
	}

	return value
}

func compareDepths(
	base []ThresholdDepth,
	counterfactual []ThresholdDepth,
) []DepthDelta {
	count := len(base)
	if len(counterfactual) < count {
		count = len(counterfactual)
	}

	result := make([]DepthDelta, 0, count)

	for i := 0; i < count; i++ {
		baseDepth := base[i]
		counterfactualDepth := counterfactual[i]

		result = append(result, DepthDelta{
			ThresholdBps: baseDepth.ThresholdBps,

			BaseDepthAmount:           baseDepth.DepthAmount,
			CounterfactualDepthAmount: counterfactualDepth.DepthAmount,

			DeltaDepthAmount: baseDepth.DepthAmount.
				Sub(counterfactualDepth.DepthAmount),

			BaseBreached:           baseDepth.Breached,
			CounterfactualBreached: counterfactualDepth.Breached,
		})
	}

	return result
}
