package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type BidirectionalLiquidityImpactRequest struct {
	Pool                *domain.ReconstructedPool
	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int
	ThresholdsBps       []decimal.Decimal
	PositionLimit       int
}

type BidirectionalLiquidityImpactReport struct {
	ZeroForOneReport *LiquidityImpactReport
	OneForZeroReport *LiquidityImpactReport
	Positions        []BidirectionalPositionImpact
}

type BidirectionalPositionImpact struct {
	Position    domain.LiquidityPosition
	PositionKey string

	ActiveLiquidityShare decimal.Decimal
	RangeWidth           int

	DistanceToLowerTick   int
	DistanceToUpperTick   int
	DistanceToNearestEdge int

	LiquidityDensity decimal.Decimal

	ZeroForOneDeltaAUCBps decimal.Decimal
	OneForZeroDeltaAUCBps decimal.Decimal

	ZeroForOneLSISBps decimal.Decimal
	OneForZeroLSISBps decimal.Decimal

	TotalLSISBps          decimal.Decimal
	MaxDirectionalLSISBps decimal.Decimal

	ZeroForOneDepthDeltas []DepthDelta
	OneForZeroDepthDeltas []DepthDelta
}

func (s *LiquidityImpactService) AnalyzeBidirectionalActivePositions(
	ctx context.Context,
	req BidirectionalLiquidityImpactRequest,
) (*BidirectionalLiquidityImpactReport, error) {
	if req.Pool == nil {
		return nil, fmt.Errorf("bidirectional impact: pool is nil")
	}
	if len(req.ZeroForOneAmountsIn) == 0 {
		return nil, fmt.Errorf("bidirectional impact: zero_for_one amounts are empty")
	}
	if len(req.OneForZeroAmountsIn) == 0 {
		return nil, fmt.Errorf("bidirectional impact: one_for_zero amounts are empty")
	}

	zeroForOneReport, err := s.AnalyzeActivePositions(
		ctx,
		LiquidityImpactRequest{
			Pool:          req.Pool,
			AmountsIn:     req.ZeroForOneAmountsIn,
			ThresholdsBps: req.ThresholdsBps,
			ZeroForOne:    true,
			PositionLimit: req.PositionLimit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("bidirectional impact: zero_for_one: %w", err)
	}

	oneForZeroReport, err := s.AnalyzeActivePositions(
		ctx,
		LiquidityImpactRequest{
			Pool:          req.Pool,
			AmountsIn:     req.OneForZeroAmountsIn,
			ThresholdsBps: req.ThresholdsBps,
			ZeroForOne:    false,
			PositionLimit: req.PositionLimit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("bidirectional impact: one_for_zero: %w", err)
	}

	positions := mergeDirectionalImpacts(
		req.Pool,
		zeroForOneReport,
		oneForZeroReport,
	)

	return &BidirectionalLiquidityImpactReport{
		ZeroForOneReport: zeroForOneReport,
		OneForZeroReport: oneForZeroReport,
		Positions:        positions,
	}, nil
}

func mergeDirectionalImpacts(
	pool *domain.ReconstructedPool,
	zeroForOneReport *LiquidityImpactReport,
	oneForZeroReport *LiquidityImpactReport,
) []BidirectionalPositionImpact {
	byPosition := make(map[string]*BidirectionalPositionImpact)

	for _, impact := range zeroForOneReport.Positions {
		key := liquidityPositionKey(impact.Position)

		item, exists := byPosition[key]
		if !exists {
			item = newBidirectionalPositionImpact(
				pool,
				impact.Position,
			)
			byPosition[key] = item
		}

		item.ZeroForOneDeltaAUCBps = impact.DeltaAUCBps
		item.ZeroForOneLSISBps = impact.LSISBps
		item.ZeroForOneDepthDeltas = cloneDepthDeltas(impact.DepthDeltas)
	}

	for _, impact := range oneForZeroReport.Positions {
		key := liquidityPositionKey(impact.Position)

		item, exists := byPosition[key]
		if !exists {
			item = newBidirectionalPositionImpact(
				pool,
				impact.Position,
			)
			byPosition[key] = item
		}

		item.OneForZeroDeltaAUCBps = impact.DeltaAUCBps
		item.OneForZeroLSISBps = impact.LSISBps
		item.OneForZeroDepthDeltas = cloneDepthDeltas(impact.DepthDeltas)
	}

	result := make([]BidirectionalPositionImpact, 0, len(byPosition))

	for _, item := range byPosition {
		item.TotalLSISBps = item.ZeroForOneLSISBps.Add(
			item.OneForZeroLSISBps,
		)

		item.MaxDirectionalLSISBps = maxDecimal(
			item.ZeroForOneLSISBps,
			item.OneForZeroLSISBps,
		)

		result = append(result, *item)
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].TotalLSISBps.GreaterThan(
			result[j].TotalLSISBps,
		)
	})

	return result
}

func liquidityPositionKey(position domain.LiquidityPosition) string {
	return fmt.Sprintf(
		"%s:%d:%d",
		strings.ToLower(position.Owner),
		position.TickLower,
		position.TickUpper,
	)
}

func maxDecimal(
	a decimal.Decimal,
	b decimal.Decimal,
) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}

	return b
}

func newBidirectionalPositionImpact(
	pool *domain.ReconstructedPool,
	position domain.LiquidityPosition,
) *BidirectionalPositionImpact {
	rangeWidth := position.TickUpper - position.TickLower

	distanceToLower := pool.CurrentTick - position.TickLower
	distanceToUpper := position.TickUpper - pool.CurrentTick

	return &BidirectionalPositionImpact{
		Position:    position,
		PositionKey: liquidityPositionKey(position),

		ActiveLiquidityShare: activeLiquidityShare(
			position.Liquidity,
			pool.Liquidity,
		),

		RangeWidth:            rangeWidth,
		DistanceToLowerTick:   distanceToLower,
		DistanceToUpperTick:   distanceToUpper,
		DistanceToNearestEdge: minInt(distanceToLower, distanceToUpper),

		LiquidityDensity: liquidityDensity(
			position.Liquidity,
			rangeWidth,
		),
	}
}

func activeLiquidityShare(
	positionLiquidity *big.Int,
	poolLiquidity *big.Int,
) decimal.Decimal {
	if positionLiquidity == nil ||
		poolLiquidity == nil ||
		poolLiquidity.Sign() <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(positionLiquidity, 0).
		Div(decimal.NewFromBigInt(poolLiquidity, 0))
}

func liquidityDensity(
	liquidity *big.Int,
	rangeWidth int,
) decimal.Decimal {
	if liquidity == nil || liquidity.Sign() <= 0 || rangeWidth <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(liquidity, 0).
		Div(decimal.NewFromInt(int64(rangeWidth)))
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}

	return b
}

func cloneDepthDeltas(deltas []DepthDelta) []DepthDelta {
	if len(deltas) == 0 {
		return nil
	}

	result := make([]DepthDelta, len(deltas))
	copy(result, deltas)

	return result
}
