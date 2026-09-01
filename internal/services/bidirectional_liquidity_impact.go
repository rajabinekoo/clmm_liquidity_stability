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
	PositionCoverageBps int64
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
			Pool:                req.Pool,
			AmountsIn:           req.ZeroForOneAmountsIn,
			ThresholdsBps:       req.ThresholdsBps,
			ZeroForOne:          true,
			PositionLimit:       req.PositionLimit,
			PositionCoverageBps: req.PositionCoverageBps,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("bidirectional impact: zero_for_one: %w", err)
	}

	oneForZeroReport, err := s.AnalyzeActivePositions(
		ctx,
		LiquidityImpactRequest{
			Pool:                req.Pool,
			AmountsIn:           req.OneForZeroAmountsIn,
			ThresholdsBps:       req.ThresholdsBps,
			ZeroForOne:          false,
			PositionLimit:       req.PositionLimit,
			PositionCoverageBps: req.PositionCoverageBps,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("bidirectional impact: one_for_zero: %w", err)
	}

	if err := validateBidirectionalPositionSelection(
		zeroForOneReport,
		oneForZeroReport,
	); err != nil {
		return nil, fmt.Errorf(
			"bidirectional impact: inconsistent position selection: %w",
			err,
		)
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

func validateBidirectionalPositionSelection(
	zeroForOneReport *LiquidityImpactReport,
	oneForZeroReport *LiquidityImpactReport,
) error {
	if zeroForOneReport == nil ||
		oneForZeroReport == nil {
		return fmt.Errorf(
			"directional report is nil",
		)
	}

	if zeroForOneReport.PositionLimit !=
		oneForZeroReport.PositionLimit {
		return fmt.Errorf(
			"position limits differ: zero_for_one=%d one_for_zero=%d",
			zeroForOneReport.PositionLimit,
			oneForZeroReport.PositionLimit,
		)
	}

	if zeroForOneReport.LoadedPositionCount !=
		oneForZeroReport.LoadedPositionCount {
		return fmt.Errorf(
			"loaded position counts differ: zero_for_one=%d one_for_zero=%d",
			zeroForOneReport.LoadedPositionCount,
			oneForZeroReport.LoadedPositionCount,
		)
	}

	if zeroForOneReport.SelectedPositionCount !=
		oneForZeroReport.SelectedPositionCount {
		return fmt.Errorf(
			"selected position counts differ: zero_for_one=%d one_for_zero=%d",
			zeroForOneReport.SelectedPositionCount,
			oneForZeroReport.SelectedPositionCount,
		)
	}

	if zeroForOneReport.PositionCoverageTargetBps !=
		oneForZeroReport.PositionCoverageTargetBps {
		return fmt.Errorf(
			"coverage targets differ: zero_for_one=%d one_for_zero=%d",
			zeroForOneReport.PositionCoverageTargetBps,
			oneForZeroReport.PositionCoverageTargetBps,
		)
	}

	if !zeroForOneReport.
		PositionCoverageAchievedBps.
		Equal(
			oneForZeroReport.
				PositionCoverageAchievedBps,
		) {
		return fmt.Errorf(
			"achieved coverage differs: zero_for_one=%s one_for_zero=%s",
			zeroForOneReport.PositionCoverageAchievedBps,
			oneForZeroReport.PositionCoverageAchievedBps,
		)
	}

	zeroForOneKeys := make(
		map[string]struct{},
		len(
			zeroForOneReport.Positions,
		),
	)

	for _, impact := range zeroForOneReport.Positions {
		key := liquidityPositionKey(
			impact.Position,
		)

		zeroForOneKeys[key] =
			struct{}{}
	}

	for _, impact := range oneForZeroReport.Positions {
		key := liquidityPositionKey(
			impact.Position,
		)

		if _, exists :=
			zeroForOneKeys[key]; !exists {
			return fmt.Errorf(
				"position %s exists only in one_for_zero selection",
				key,
			)
		}

		delete(
			zeroForOneKeys,
			key,
		)
	}

	if len(zeroForOneKeys) != 0 {
		return fmt.Errorf(
			"%d positions exist only in zero_for_one selection",
			len(zeroForOneKeys),
		)
	}

	return nil
}
