package uniswapv3

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/shopspring/decimal"

	"oracle/internal/domain"
)

var ErrTickCrossingRequired = errors.New("tick crossing required")
var ErrInsufficientLiquidity = errors.New("insufficient liquidity")

const maxSwapSteps = 10_000

type Simulator struct {
	feePips int64
}

type swapState struct {
	sqrtPriceX96 *big.Int
	currentTick  int
	liquidity    *big.Int
}

func NewSimulator(feePips int64) (*Simulator, error) {
	if feePips < 0 || feePips >= FeeDenominator {
		return nil, fmt.Errorf("invalid fee pips: %d", feePips)
	}

	return &Simulator{feePips: feePips}, nil
}

type ExactInputRequest struct {
	AmountIn   *big.Int
	ZeroForOne bool
}

type ExactInputResult struct {
	AmountIn           *big.Int
	AmountInLessFee    *big.Int
	AmountOut          *big.Int
	FeeAmount          *big.Int
	TickBefore         int
	TickAfterApprox    int
	CrossedTicks       int
	SqrtPriceBeforeX96 *big.Int
	SqrtPriceAfterX96  *big.Int
	SpotPriceBefore    decimal.Decimal
	SpotPriceAfter     decimal.Decimal
	ExecutionPrice     decimal.Decimal
	PriceImpactBps     decimal.Decimal
}

func (s *Simulator) SimulateExactInputNoCross(
	pool *domain.ReconstructedPool,
	req ExactInputRequest,
) (*ExactInputResult, error) {
	if pool == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	if pool.SqrtPriceX96 == nil || pool.SqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf("invalid pool sqrt price")
	}
	if pool.Liquidity == nil || pool.Liquidity.Sign() <= 0 {
		return nil, fmt.Errorf("invalid pool liquidity")
	}

	amountInLessFee, feeAmount, err := amountLessFee(req.AmountIn, s.feePips)
	if err != nil {
		return nil, err
	}

	sqrtBefore := new(big.Int).Set(pool.SqrtPriceX96)
	liquidity := new(big.Int).Set(pool.Liquidity)

	var sqrtAfter *big.Int
	var amountOut *big.Int

	if req.ZeroForOne {
		sqrtAfter = nextSqrtPriceFromAmount0In(
			sqrtBefore,
			liquidity,
			amountInLessFee,
		)

		if err := ensureNoCross(pool, sqrtAfter, true); err != nil {
			return nil, err
		}

		amountOut = amount1DeltaRoundDown(
			sqrtAfter,
			sqrtBefore,
			liquidity,
		)
	} else {
		sqrtAfter = nextSqrtPriceFromAmount1In(
			sqrtBefore,
			liquidity,
			amountInLessFee,
		)

		if err := ensureNoCross(pool, sqrtAfter, false); err != nil {
			return nil, err
		}

		amountOut = amount0DeltaRoundDown(
			sqrtBefore,
			sqrtAfter,
			liquidity,
		)
	}

	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("amount_out is zero")
	}

	spotBefore := spotPrice(sqrtBefore)
	spotAfter := spotPrice(sqrtAfter)
	executionPrice := executionPrice(
		req.ZeroForOne,
		amountInLessFee,
		amountOut,
	)

	priceImpact := executionPrice.Sub(spotBefore).Abs().
		Div(spotBefore).
		Mul(decimal.NewFromInt(10_000))

	return &ExactInputResult{
		AmountIn:           new(big.Int).Set(req.AmountIn),
		AmountInLessFee:    amountInLessFee,
		AmountOut:          amountOut,
		FeeAmount:          feeAmount,
		TickBefore:         pool.CurrentTick,
		TickAfterApprox:    approximateTickFromSqrtRatio(sqrtAfter),
		CrossedTicks:       0,
		SqrtPriceBeforeX96: sqrtBefore,
		SqrtPriceAfterX96:  sqrtAfter,
		SpotPriceBefore:    spotBefore,
		SpotPriceAfter:     spotAfter,
		ExecutionPrice:     executionPrice,
		PriceImpactBps:     priceImpact,
	}, nil
}

func (s *Simulator) SimulateExactInput(
	pool *domain.ReconstructedPool,
	req ExactInputRequest,
) (*ExactInputResult, error) {
	if pool == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	if pool.SqrtPriceX96 == nil || pool.SqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf("invalid pool sqrt price")
	}
	if pool.Liquidity == nil || pool.Liquidity.Sign() <= 0 {
		return nil, fmt.Errorf("invalid pool liquidity")
	}

	amountInLessFee, feeAmount, err := amountLessFee(req.AmountIn, s.feePips)
	if err != nil {
		return nil, err
	}

	state := &swapState{
		sqrtPriceX96: new(big.Int).Set(pool.SqrtPriceX96),
		currentTick:  pool.CurrentTick,
		liquidity:    new(big.Int).Set(pool.Liquidity),
	}

	remaining := new(big.Int).Set(amountInLessFee)
	totalOut := big.NewInt(0)
	crossedTicks := 0

	for step := 0; remaining.Sign() > 0; step++ {
		if step >= maxSwapSteps {
			return nil, fmt.Errorf("swap exceeded max steps: %d", maxSwapSteps)
		}
		if state.liquidity.Sign() <= 0 {
			return nil, fmt.Errorf("%w: active liquidity is zero", ErrInsufficientLiquidity)
		}

		targetTick, targetSqrt, hasInitializedBoundary, err := nextSwapBoundary(
			pool,
			state.currentTick,
			req.ZeroForOne,
		)
		if err != nil {
			return nil, err
		}

		amountNeeded := amountInToReachTarget(
			req.ZeroForOne,
			state.sqrtPriceX96,
			targetSqrt,
			state.liquidity,
		)

		if amountNeeded.Sign() == 0 || remaining.Cmp(amountNeeded) >= 0 {
			amountOut := outputBetweenPrices(
				req.ZeroForOne,
				state.sqrtPriceX96,
				targetSqrt,
				state.liquidity,
			)

			totalOut.Add(totalOut, amountOut)
			remaining.Sub(remaining, amountNeeded)
			state.sqrtPriceX96 = targetSqrt

			if !hasInitializedBoundary {
				if remaining.Sign() > 0 {
					return nil, fmt.Errorf(
						"%w: reached price boundary with remaining amount %s",
						ErrInsufficientLiquidity,
						remaining.String(),
					)
				}
				break
			}

			if err := crossInitializedTick(
				pool,
				state,
				targetTick,
				req.ZeroForOne,
			); err != nil {
				return nil, err
			}

			crossedTicks++
			continue
		}

		nextSqrt := nextSqrtPriceWithinRange(
			req.ZeroForOne,
			state.sqrtPriceX96,
			state.liquidity,
			remaining,
		)

		amountOut := outputBetweenPrices(
			req.ZeroForOne,
			state.sqrtPriceX96,
			nextSqrt,
			state.liquidity,
		)

		totalOut.Add(totalOut, amountOut)
		remaining.SetInt64(0)
		state.sqrtPriceX96 = nextSqrt
		state.currentTick = approximateTickFromSqrtRatio(nextSqrt)
	}

	if totalOut.Sign() <= 0 {
		return nil, fmt.Errorf("amount_out is zero")
	}

	spotBefore := spotPrice(pool.SqrtPriceX96)
	spotAfter := spotPrice(state.sqrtPriceX96)
	executionPrice := executionPrice(
		req.ZeroForOne,
		amountInLessFee,
		totalOut,
	)

	priceImpact := executionPrice.Sub(spotBefore).Abs().
		Div(spotBefore).
		Mul(decimal.NewFromInt(10_000))

	return &ExactInputResult{
		AmountIn:           new(big.Int).Set(req.AmountIn),
		AmountInLessFee:    amountInLessFee,
		AmountOut:          totalOut,
		FeeAmount:          feeAmount,
		TickBefore:         pool.CurrentTick,
		TickAfterApprox:    state.currentTick,
		CrossedTicks:       crossedTicks,
		SqrtPriceBeforeX96: new(big.Int).Set(pool.SqrtPriceX96),
		SqrtPriceAfterX96:  new(big.Int).Set(state.sqrtPriceX96),
		SpotPriceBefore:    spotBefore,
		SpotPriceAfter:     spotAfter,
		ExecutionPrice:     executionPrice,
		PriceImpactBps:     priceImpact,
	}, nil
}

func ensureNoCross(
	pool *domain.ReconstructedPool,
	nextSqrtPriceX96 *big.Int,
	zeroForOne bool,
) error {
	if zeroForOne {
		tick, ok := previousInitializedTick(
			pool.InitializedTicks,
			pool.CurrentTick,
		)
		if !ok {
			return nil
		}

		boundary, err := SqrtRatioAtTick(tick)
		if err != nil {
			return err
		}

		if nextSqrtPriceX96.Cmp(boundary) <= 0 {
			return fmt.Errorf(
				"%w: current_tick=%d boundary_tick=%d",
				ErrTickCrossingRequired,
				pool.CurrentTick,
				tick,
			)
		}

		return nil
	}

	tick, ok := nextInitializedTick(
		pool.InitializedTicks,
		pool.CurrentTick,
	)
	if !ok {
		return nil
	}

	boundary, err := SqrtRatioAtTick(tick)
	if err != nil {
		return err
	}

	if nextSqrtPriceX96.Cmp(boundary) >= 0 {
		return fmt.Errorf(
			"%w: current_tick=%d boundary_tick=%d",
			ErrTickCrossingRequired,
			pool.CurrentTick,
			tick,
		)
	}

	return nil
}

func previousInitializedTick(
	ticks []int,
	currentTick int,
) (int, bool) {
	index := sort.SearchInts(ticks, currentTick)
	if index == 0 {
		return 0, false
	}

	return ticks[index-1], true
}

func nextInitializedTick(
	ticks []int,
	currentTick int,
) (int, bool) {
	index := sort.Search(
		len(ticks),
		func(i int) bool {
			return ticks[i] > currentTick
		},
	)

	if index >= len(ticks) {
		return 0, false
	}

	return ticks[index], true
}

func spotPrice(sqrtPriceX96 *big.Int) decimal.Decimal {
	numerator := new(big.Int).Mul(sqrtPriceX96, sqrtPriceX96)
	denominator := new(big.Int).Lsh(big.NewInt(1), 192)

	return decimalFromFraction(numerator, denominator)
}

func executionPrice(
	zeroForOne bool,
	amountInLessFee *big.Int,
	amountOut *big.Int,
) decimal.Decimal {
	if zeroForOne {
		// token1 per token0
		return decimalFromFraction(amountOut, amountInLessFee)
	}

	// token1 per token0
	return decimalFromFraction(amountInLessFee, amountOut)
}

func decimalFromFraction(
	numerator *big.Int,
	denominator *big.Int,
) decimal.Decimal {
	rat := new(big.Rat).SetFrac(numerator, denominator)

	value, err := decimal.NewFromString(rat.FloatString(60))
	if err != nil {
		return decimal.Zero
	}

	return value
}

func approximateTickFromSqrtRatio(sqrtPriceX96 *big.Int) int {
	sqrtFloat, _ := new(big.Float).
		SetPrec(256).
		SetInt(sqrtPriceX96).
		Float64()

	q96Float, _ := new(big.Float).
		SetPrec(256).
		SetInt(q96).
		Float64()

	ratio := sqrtFloat / q96Float
	price := ratio * ratio

	return int(math.Floor(math.Log(price) / math.Log(1.0001)))
}

func nextSwapBoundary(
	pool *domain.ReconstructedPool,
	currentTick int,
	zeroForOne bool,
) (int, *big.Int, bool, error) {
	if zeroForOne {
		tick, ok := previousInitializedTick(
			pool.InitializedTicks,
			currentTick,
		)
		if !ok {
			sqrt, err := SqrtRatioAtTick(MinTick)
			return MinTick, sqrt, false, err
		}

		sqrt, err := SqrtRatioAtTick(tick)
		return tick, sqrt, true, err
	}

	tick, ok := nextInitializedTick(
		pool.InitializedTicks,
		currentTick,
	)
	if !ok {
		sqrt, err := SqrtRatioAtTick(MaxTick)
		return MaxTick, sqrt, false, err
	}

	sqrt, err := SqrtRatioAtTick(tick)
	return tick, sqrt, true, err
}

func amountInToReachTarget(
	zeroForOne bool,
	currentSqrt *big.Int,
	targetSqrt *big.Int,
	liquidity *big.Int,
) *big.Int {
	if zeroForOne {
		return amount0DeltaRoundUp(
			targetSqrt,
			currentSqrt,
			liquidity,
		)
	}

	return amount1DeltaRoundUp(
		currentSqrt,
		targetSqrt,
		liquidity,
	)
}

func outputBetweenPrices(
	zeroForOne bool,
	currentSqrt *big.Int,
	targetSqrt *big.Int,
	liquidity *big.Int,
) *big.Int {
	if zeroForOne {
		return amount1DeltaRoundDown(
			targetSqrt,
			currentSqrt,
			liquidity,
		)
	}

	return amount0DeltaRoundDown(
		currentSqrt,
		targetSqrt,
		liquidity,
	)
}

func nextSqrtPriceWithinRange(
	zeroForOne bool,
	currentSqrt *big.Int,
	liquidity *big.Int,
	amountIn *big.Int,
) *big.Int {
	if zeroForOne {
		return nextSqrtPriceFromAmount0In(
			currentSqrt,
			liquidity,
			amountIn,
		)
	}

	return nextSqrtPriceFromAmount1In(
		currentSqrt,
		liquidity,
		amountIn,
	)
}

func crossInitializedTick(
	pool *domain.ReconstructedPool,
	state *swapState,
	tick int,
	zeroForOne bool,
) error {
	tickState, exists := pool.Ticks[tick]
	if !exists {
		return fmt.Errorf("initialized tick %d not found in pool ticks", tick)
	}
	if tickState.LiquidityNet == nil {
		return fmt.Errorf("tick %d has nil liquidity net", tick)
	}

	if zeroForOne {
		state.liquidity.Sub(
			state.liquidity,
			tickState.LiquidityNet,
		)
		state.currentTick = tick - 1
	} else {
		state.liquidity.Add(
			state.liquidity,
			tickState.LiquidityNet,
		)
		state.currentTick = tick
	}

	if state.liquidity.Sign() < 0 {
		return fmt.Errorf(
			"active liquidity became negative after crossing tick %d: %s",
			tick,
			state.liquidity.String(),
		)
	}

	return nil
}
