package uniswapv3

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type ExactOutputRequest struct {
	AmountOut *big.Int

	ZeroForOne bool
}

type ExactOutputResult struct {
	AmountOut *big.Int

	AmountIn *big.Int

	AmountInLessFee *big.Int

	FeeAmount *big.Int

	LiquidityAfter *big.Int

	TickBefore int
	TickAfter  int

	TickAfterApprox int

	CrossedTicks int
	SwapSteps    int

	SqrtPriceBeforeX96 *big.Int
	SqrtPriceAfterX96  *big.Int

	SpotPriceBefore decimal.Decimal
	SpotPriceAfter  decimal.Decimal
	ExecutionPrice  decimal.Decimal
	PriceImpactBps  decimal.Decimal
}

func (s *Simulator) SimulateExactOutput(
	pool *domain.ReconstructedPool,
	req ExactOutputRequest,
) (*ExactOutputResult, error) {
	if s == nil {
		return nil, fmt.Errorf(
			"simulator is nil",
		)
	}

	if err := validateExactOutputSimulationInput(
		pool,
		req,
	); err != nil {
		return nil, err
	}

	state := &swapState{
		sqrtPriceX96: new(big.Int).Set(
			pool.SqrtPriceX96,
		),

		currentTick: pool.CurrentTick,

		liquidity: new(big.Int).Set(
			pool.Liquidity,
		),
	}

	remainingOut := new(big.Int).Set(
		req.AmountOut,
	)

	totalAmountInLessFee := big.NewInt(0)
	totalFee := big.NewInt(0)
	totalAmountOut := big.NewInt(0)

	crossedTicks := 0
	executedSteps := 0

	for remainingOut.Sign() > 0 {
		if executedSteps >= maxSwapSteps {
			return nil, fmt.Errorf(
				"swap exceeded maximum step count %d",
				maxSwapSteps,
			)
		}

		if state.liquidity.Sign() <= 0 {
			return nil, fmt.Errorf(
				"%w: active liquidity is %s at tick %d with output %s remaining",
				ErrInsufficientLiquidity,
				state.liquidity,
				state.currentTick,
				remainingOut,
			)
		}

		boundary, err := nextSwapBoundary(
			pool,
			state.currentTick,
			req.ZeroForOne,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve swap boundary at step %d: %w",
				executedSteps,
				err,
			)
		}

		if err := validateBoundaryDirection(
			state.sqrtPriceX96,
			boundary.SqrtPriceX96,
			req.ZeroForOne,
		); err != nil {
			return nil, fmt.Errorf(
				"invalid swap boundary at step %d: %w",
				executedSteps,
				err,
			)
		}

		step, err := computeSwapStepExactOutput(
			state.sqrtPriceX96,
			boundary.SqrtPriceX96,
			state.liquidity,
			remainingOut,
			s.feePips,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"compute exact-output swap step %d: %w",
				executedSteps,
				err,
			)
		}

		if step.AmountOut.Cmp(
			remainingOut,
		) > 0 {
			return nil, fmt.Errorf(
				"swap step %d produced %s output but only %s remained",
				executedSteps,
				step.AmountOut,
				remainingOut,
			)
		}

		reachedBoundary :=
			step.SqrtPriceNextX96.Cmp(
				boundary.SqrtPriceX96,
			) == 0

		grossInput := new(big.Int).Add(
			new(big.Int).Set(
				step.AmountIn,
			),
			step.FeeAmount,
		)

		if step.AmountOut.Sign() == 0 &&
			!(reachedBoundary && boundary.Initialized) {
			if reachedBoundary &&
				!boundary.Initialized {
				return nil, fmt.Errorf(
					"%w: pool already reached the global price limit with %s output remaining",
					ErrInsufficientLiquidity,
					remainingOut,
				)
			}

			return nil, fmt.Errorf(
				"swap step %d made no output progress at tick %d",
				executedSteps,
				state.currentTick,
			)
		}

		totalAmountInLessFee.Add(
			totalAmountInLessFee,
			step.AmountIn,
		)

		totalFee.Add(
			totalFee,
			step.FeeAmount,
		)

		totalAmountOut.Add(
			totalAmountOut,
			step.AmountOut,
		)

		remainingOut.Sub(
			remainingOut,
			step.AmountOut,
		)

		state.sqrtPriceX96 =
			new(big.Int).Set(
				step.SqrtPriceNextX96,
			)

		executedSteps++

		if reachedBoundary &&
			boundary.Initialized {
			if err := crossInitializedTick(
				pool,
				state,
				boundary.Tick,
				req.ZeroForOne,
			); err != nil {
				return nil, fmt.Errorf(
					"cross initialized tick %d at step %d: %w",
					boundary.Tick,
					executedSteps-1,
					err,
				)
			}

			crossedTicks++

			continue
		}

		exactTick, err := TickAtSqrtRatio(
			state.sqrtPriceX96,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"derive exact tick after swap step %d: %w",
				executedSteps-1,
				err,
			)
		}

		state.currentTick = exactTick

		if reachedBoundary &&
			!boundary.Initialized &&
			remainingOut.Sign() > 0 {
			return nil, fmt.Errorf(
				"%w: reached global price limit with %s output remaining",
				ErrInsufficientLiquidity,
				remainingOut,
			)
		}

		if !reachedBoundary &&
			remainingOut.Sign() != 0 {
			return nil, fmt.Errorf(
				"partial swap step %d left unexpected output %s",
				executedSteps-1,
				remainingOut,
			)
		}

		if grossInput.Sign() <= 0 {
			return nil, fmt.Errorf(
				"swap step %d produced non-positive gross input %s",
				executedSteps-1,
				grossInput,
			)
		}
	}

	if totalAmountOut.Cmp(
		req.AmountOut,
	) != 0 {
		return nil, fmt.Errorf(
			"swap output accounting mismatch: requested=%s delivered=%s",
			req.AmountOut,
			totalAmountOut,
		)
	}

	if totalAmountInLessFee.Sign() <= 0 {
		return nil, fmt.Errorf(
			"swap usable input is zero",
		)
	}

	totalAmountIn := new(big.Int).Add(
		new(big.Int).Set(
			totalAmountInLessFee,
		),
		totalFee,
	)

	if totalAmountIn.Sign() <= 0 {
		return nil, fmt.Errorf(
			"swap gross input is zero",
		)
	}

	spotBefore, err := spotPrice(
		pool.SqrtPriceX96,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate spot price before swap: %w",
			err,
		)
	}

	spotAfter, err := spotPrice(
		state.sqrtPriceX96,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate spot price after swap: %w",
			err,
		)
	}

	execution, err := executionPrice(
		req.ZeroForOne,
		totalAmountInLessFee,
		totalAmountOut,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate execution price: %w",
			err,
		)
	}

	if spotBefore.IsZero() {
		return nil, fmt.Errorf(
			"spot price before swap is zero",
		)
	}

	priceImpact := execution.
		Sub(spotBefore).
		Abs().
		Div(spotBefore).
		Mul(
			decimal.NewFromInt(10_000),
		)

	return &ExactOutputResult{
		AmountOut: new(big.Int).Set(
			totalAmountOut,
		),

		AmountIn: new(big.Int).Set(
			totalAmountIn,
		),

		AmountInLessFee: new(big.Int).Set(
			totalAmountInLessFee,
		),

		FeeAmount: new(big.Int).Set(
			totalFee,
		),

		LiquidityAfter: new(big.Int).Set(
			state.liquidity,
		),

		TickBefore: pool.CurrentTick,

		TickAfter: state.currentTick,

		TickAfterApprox: state.currentTick,

		CrossedTicks: crossedTicks,

		SwapSteps: executedSteps,

		SqrtPriceBeforeX96: new(big.Int).Set(
			pool.SqrtPriceX96,
		),

		SqrtPriceAfterX96: new(big.Int).Set(
			state.sqrtPriceX96,
		),

		SpotPriceBefore: spotBefore,

		SpotPriceAfter: spotAfter,

		ExecutionPrice: execution,

		PriceImpactBps: priceImpact,
	}, nil
}
