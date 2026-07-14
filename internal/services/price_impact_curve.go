package services

import (
	"context"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"oracle/internal/domain"
	"oracle/internal/uniswapv3"
)

type PriceImpactCurveService struct {
	simulator *uniswapv3.Simulator
}

type PriceImpactCurveSummary struct {
	PriceImpactAUCBps decimal.Decimal
	MaxAmountIn       *big.Int
	ThresholdDepths   []ThresholdDepth
}

type ThresholdDepth struct {
	ThresholdBps decimal.Decimal
	DepthAmount  decimal.Decimal
	Breached     bool
}

func NewPriceImpactCurveService(
	simulator *uniswapv3.Simulator,
) *PriceImpactCurveService {
	return &PriceImpactCurveService{
		simulator: simulator,
	}
}

type PriceImpactCurveRequest struct {
	Pool       *domain.ReconstructedPool
	AmountsIn  []*big.Int
	ZeroForOne bool
}

type PriceImpactCurvePoint struct {
	AmountIn        *big.Int
	AmountOut       *big.Int
	TickAfterApprox int
	CrossedTicks    int
	ExecutionPrice  decimal.Decimal
	SpotPriceAfter  decimal.Decimal
	PriceImpactBps  decimal.Decimal
}

type PriceImpactCurve struct {
	ZeroForOne bool
	Points     []PriceImpactCurvePoint
}

func (s *PriceImpactCurveService) Build(
	ctx context.Context,
	req PriceImpactCurveRequest,
) (*PriceImpactCurve, error) {
	if req.Pool == nil {
		return nil, fmt.Errorf("price impact curve: pool is nil")
	}
	if len(req.AmountsIn) == 0 {
		return nil, fmt.Errorf("price impact curve: amounts_in is empty")
	}

	points := make([]PriceImpactCurvePoint, 0, len(req.AmountsIn))

	for _, amountIn := range req.AmountsIn {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if amountIn == nil || amountIn.Sign() <= 0 {
			return nil, fmt.Errorf(
				"price impact curve: invalid amount_in %v",
				amountIn,
			)
		}

		result, err := s.simulator.SimulateExactInput(
			req.Pool,
			uniswapv3.ExactInputRequest{
				AmountIn:   amountIn,
				ZeroForOne: req.ZeroForOne,
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"simulate amount_in=%s: %w",
				amountIn.String(),
				err,
			)
		}

		points = append(points, PriceImpactCurvePoint{
			AmountIn:        new(big.Int).Set(result.AmountIn),
			AmountOut:       new(big.Int).Set(result.AmountOut),
			TickAfterApprox: result.TickAfterApprox,
			CrossedTicks:    result.CrossedTicks,
			ExecutionPrice:  result.ExecutionPrice,
			SpotPriceAfter:  result.SpotPriceAfter,
			PriceImpactBps:  result.PriceImpactBps,
		})
	}

	return &PriceImpactCurve{
		ZeroForOne: req.ZeroForOne,
		Points:     points,
	}, nil
}

func (s *PriceImpactCurveService) Summarize(
	curve *PriceImpactCurve,
	thresholds []decimal.Decimal,
) (*PriceImpactCurveSummary, error) {
	if curve == nil {
		return nil, fmt.Errorf("price impact summary: curve is nil")
	}
	if len(curve.Points) == 0 {
		return nil, fmt.Errorf("price impact summary: curve has no points")
	}

	if err := validateCurvePoints(curve.Points); err != nil {
		return nil, err
	}

	auc := normalizedTrapezoidalAUC(curve.Points)

	depths := make([]ThresholdDepth, 0, len(thresholds))
	for _, threshold := range thresholds {
		depths = append(
			depths,
			depthAtThreshold(curve.Points, threshold),
		)
	}

	return &PriceImpactCurveSummary{
		PriceImpactAUCBps: auc,
		MaxAmountIn:       new(big.Int).Set(curve.Points[len(curve.Points)-1].AmountIn),
		ThresholdDepths:   depths,
	}, nil
}

func validateCurvePoints(points []PriceImpactCurvePoint) error {
	var previous *big.Int

	for _, point := range points {
		if point.AmountIn == nil || point.AmountIn.Sign() <= 0 {
			return fmt.Errorf("price impact curve: invalid amount_in")
		}

		if previous != nil && point.AmountIn.Cmp(previous) <= 0 {
			return fmt.Errorf(
				"price impact curve: amounts must be strictly increasing",
			)
		}

		if point.PriceImpactBps.IsNegative() {
			return fmt.Errorf(
				"price impact curve: negative price impact for amount_in=%s",
				point.AmountIn.String(),
			)
		}

		previous = point.AmountIn
	}

	return nil
}

func normalizedTrapezoidalAUC(
	points []PriceImpactCurvePoint,
) decimal.Decimal {
	area := decimal.Zero

	prevAmount := decimal.Zero
	prevImpact := decimal.Zero

	for _, point := range points {
		amount := decimal.NewFromBigInt(point.AmountIn, 0)
		impact := point.PriceImpactBps

		width := amount.Sub(prevAmount)
		avgHeight := prevImpact.Add(impact).Div(decimal.NewFromInt(2))

		area = area.Add(width.Mul(avgHeight))

		prevAmount = amount
		prevImpact = impact
	}

	maxAmount := decimal.NewFromBigInt(
		points[len(points)-1].AmountIn,
		0,
	)

	if maxAmount.IsZero() {
		return decimal.Zero
	}

	return area.Div(maxAmount)
}

func depthAtThreshold(
	points []PriceImpactCurvePoint,
	threshold decimal.Decimal,
) ThresholdDepth {
	if threshold.IsNegative() || threshold.IsZero() {
		return ThresholdDepth{
			ThresholdBps: threshold,
			DepthAmount:  decimal.Zero,
			Breached:     true,
		}
	}

	prevAmount := decimal.Zero
	prevImpact := decimal.Zero

	for _, point := range points {
		currentAmount := decimal.NewFromBigInt(point.AmountIn, 0)
		currentImpact := point.PriceImpactBps

		if currentImpact.GreaterThan(threshold) {
			depth := interpolateThresholdAmount(
				prevAmount,
				prevImpact,
				currentAmount,
				currentImpact,
				threshold,
			)

			return ThresholdDepth{
				ThresholdBps: threshold,
				DepthAmount:  depth,
				Breached:     true,
			}
		}

		prevAmount = currentAmount
		prevImpact = currentImpact
	}

	return ThresholdDepth{
		ThresholdBps: threshold,
		DepthAmount: decimal.NewFromBigInt(
			points[len(points)-1].AmountIn,
			0,
		),
		Breached: false,
	}
}

func interpolateThresholdAmount(
	prevAmount decimal.Decimal,
	prevImpact decimal.Decimal,
	currentAmount decimal.Decimal,
	currentImpact decimal.Decimal,
	threshold decimal.Decimal,
) decimal.Decimal {
	impactDelta := currentImpact.Sub(prevImpact)
	if impactDelta.IsZero() {
		return prevAmount
	}

	progress := threshold.Sub(prevImpact).Div(impactDelta)
	if progress.IsNegative() {
		return prevAmount
	}
	if progress.GreaterThan(decimal.NewFromInt(1)) {
		return currentAmount
	}

	return prevAmount.Add(
		currentAmount.Sub(prevAmount).Mul(progress),
	)
}
