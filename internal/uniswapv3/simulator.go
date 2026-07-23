package uniswapv3

import (
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

var (
	// ErrTickCrossingRequired is retained for backward compatibility with
	// SimulateExactInputNoCross.
	ErrTickCrossingRequired = errors.New(
		"tick crossing required",
	)

	ErrInsufficientLiquidity = errors.New(
		"insufficient liquidity",
	)
)

const (
	maxSwapSteps        = 10_000
	reportDecimalPlaces = 60
)

type Simulator struct {
	feePips int64
}

type swapState struct {
	sqrtPriceX96 *big.Int
	currentTick  int
	liquidity    *big.Int
}

type swapBoundary struct {
	Tick int

	SqrtPriceX96 *big.Int

	Initialized bool
}

func NewSimulator(
	feePips int64,
) (*Simulator, error) {
	if feePips < 0 ||
		feePips >= FeeDenominator {
		return nil, fmt.Errorf(
			"invalid fee pips %d: expected value inside [0,%d)",
			feePips,
			FeeDenominator,
		)
	}

	return &Simulator{
		feePips: feePips,
	}, nil
}

type ExactInputRequest struct {
	AmountIn *big.Int

	ZeroForOne bool
}

type ExactInputResult struct {
	// AmountIn is the total gross input supplied by the caller.
	AmountIn *big.Int

	// AmountInLessFee is the sum of the usable input from every swap step.
	//
	// It is intentionally not calculated by applying the fee once to the
	// entire input.
	AmountInLessFee *big.Int

	AmountOut *big.Int
	FeeAmount *big.Int

	TickBefore int

	// TickAfter is the exact final protocol tick.
	//
	// At a zero-for-one initialized-tick crossing, this may be one lower than
	// TickAtSqrtRatio(SqrtPriceAfterX96), matching Uniswap's crossing rule.
	TickAfter int

	// TickAfterApprox is retained for compatibility with existing analyzer,
	// CSV and swap-validation code. Its value now equals TickAfter and is no
	// longer approximate.
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

// SimulateExactInputNoCross is retained for compatibility.
//
// It uses the exact simulator and rejects the result when any initialized tick
// was crossed.
func (s *Simulator) SimulateExactInputNoCross(
	pool *domain.ReconstructedPool,
	req ExactInputRequest,
) (*ExactInputResult, error) {
	result, err := s.SimulateExactInput(
		pool,
		req,
	)
	if err != nil {
		return nil, err
	}

	if result.CrossedTicks > 0 {
		return nil, fmt.Errorf(
			"%w: crossed_ticks=%d",
			ErrTickCrossingRequired,
			result.CrossedTicks,
		)
	}

	return result, nil
}

// SimulateExactInput performs an exact-input swap over one or more initialized
// liquidity ranges.
//
// Protocol-state calculations use only integer arithmetic. Decimal is used
// after the swap solely for reporting prices and price-impact metrics.
func (s *Simulator) SimulateExactInput(
	pool *domain.ReconstructedPool,
	req ExactInputRequest,
) (*ExactInputResult, error) {
	if err := validateSimulationInput(
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

	// remaining always includes fee.
	remaining := new(big.Int).Set(
		req.AmountIn,
	)

	totalAmountInLessFee :=
		big.NewInt(0)

	totalFee :=
		big.NewInt(0)

	totalAmountOut :=
		big.NewInt(0)

	crossedTicks := 0
	executedSteps := 0

	for remaining.Sign() > 0 {
		if executedSteps >= maxSwapSteps {
			return nil, fmt.Errorf(
				"swap exceeded maximum step count %d",
				maxSwapSteps,
			)
		}

		if state.liquidity.Sign() <= 0 {
			return nil, fmt.Errorf(
				"%w: active liquidity is %s at tick %d with input %s remaining",
				ErrInsufficientLiquidity,
				state.liquidity,
				state.currentTick,
				remaining,
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

		// Fee is calculated inside this function for this price range only.
		step, err := computeSwapStepExactInput(
			state.sqrtPriceX96,
			boundary.SqrtPriceX96,
			state.liquidity,
			remaining,
			s.feePips,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"compute exact-input swap step %d: %w",
				executedSteps,
				err,
			)
		}

		consumedInput := new(big.Int).Add(
			new(big.Int).Set(
				step.AmountIn,
			),
			step.FeeAmount,
		)

		if consumedInput.Cmp(remaining) > 0 {
			return nil, fmt.Errorf(
				"swap step %d consumed %s but only %s remained",
				executedSteps,
				consumedInput,
				remaining,
			)
		}

		reachedBoundary :=
			step.SqrtPriceNextX96.Cmp(
				boundary.SqrtPriceX96,
			) == 0

		// A zero-consumption step is valid only when the current price already
		// equals an initialized boundary. Crossing that boundary changes
		// active liquidity and guarantees progress.
		if consumedInput.Sign() == 0 && !(reachedBoundary && boundary.Initialized) {
			if reachedBoundary &&
				!boundary.Initialized {
				return nil, fmt.Errorf(
					"%w: pool already reached the global price limit with %s input remaining",
					ErrInsufficientLiquidity,
					remaining,
				)
			}

			return nil, fmt.Errorf(
				"swap step %d made no progress at tick %d",
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

		remaining.Sub(
			remaining,
			consumedInput,
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

		exactTick, err :=
			TickAtSqrtRatio(
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
			remaining.Sign() > 0 {
			return nil, fmt.Errorf(
				"%w: reached global price limit with %s input remaining",
				ErrInsufficientLiquidity,
				remaining,
			)
		}

		if !reachedBoundary &&
			remaining.Sign() != 0 {
			return nil, fmt.Errorf(
				"partial swap step %d left unexpected input %s",
				executedSteps-1,
				remaining,
			)
		}
	}

	if totalAmountInLessFee.Sign() <= 0 {
		return nil, fmt.Errorf(
			"swap usable input is zero after applying step fees",
		)
	}

	if totalAmountOut.Sign() <= 0 {
		return nil, fmt.Errorf(
			"swap output is zero",
		)
	}

	accountedInput := new(big.Int).Add(
		new(big.Int).Set(
			totalAmountInLessFee,
		),
		totalFee,
	)

	if accountedInput.Cmp(req.AmountIn) != 0 {
		return nil, fmt.Errorf(
			"swap input accounting mismatch: gross=%s usable=%s fee=%s accounted=%s",
			req.AmountIn,
			totalAmountInLessFee,
			totalFee,
			accountedInput,
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

	return &ExactInputResult{
		AmountIn: new(big.Int).Set(
			req.AmountIn,
		),

		AmountInLessFee: new(big.Int).Set(
			totalAmountInLessFee,
		),

		AmountOut: new(big.Int).Set(
			totalAmountOut,
		),

		FeeAmount: new(big.Int).Set(
			totalFee,
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

func validateSimulationInput(
	pool *domain.ReconstructedPool,
	req ExactInputRequest,
) error {
	if pool == nil {
		return fmt.Errorf(
			"pool is nil",
		)
	}

	if err := validatePositiveUint160(
		"pool sqrt price",
		pool.SqrtPriceX96,
	); err != nil {
		return err
	}

	if _, err := TickAtSqrtRatio(
		pool.SqrtPriceX96,
	); err != nil {
		return fmt.Errorf(
			"pool sqrt price is outside the supported swap range: %w",
			err,
		)
	}

	if pool.CurrentTick < MinTick ||
		pool.CurrentTick > MaxTick {
		return fmt.Errorf(
			"pool current tick %d is outside [%d,%d]",
			pool.CurrentTick,
			MinTick,
			MaxTick,
		)
	}

	if err := validatePositiveUint128(
		"pool active liquidity",
		pool.Liquidity,
	); err != nil {
		return err
	}

	if err := validateUint256(
		"amount in",
		req.AmountIn,
	); err != nil {
		return err
	}

	if req.AmountIn.Sign() == 0 {
		return fmt.Errorf(
			"amount in must be greater than zero",
		)
	}

	if pool.Ticks == nil {
		return fmt.Errorf(
			"pool ticks map is nil",
		)
	}

	if len(pool.InitializedTicks) == 0 {
		return fmt.Errorf(
			"pool has no initialized ticks",
		)
	}

	if len(pool.InitializedTicks) !=
		len(pool.Ticks) {
		return fmt.Errorf(
			"initialized tick count %d does not match ticks map count %d",
			len(pool.InitializedTicks),
			len(pool.Ticks),
		)
	}

	reconstructedLiquidity :=
		big.NewInt(0)

	previousTick := 0

	for index, tickIndex := range pool.InitializedTicks {
		if tickIndex < MinTick ||
			tickIndex > MaxTick {
			return fmt.Errorf(
				"initialized tick %d is outside [%d,%d]",
				tickIndex,
				MinTick,
				MaxTick,
			)
		}

		if index > 0 &&
			tickIndex <= previousTick {
			return fmt.Errorf(
				"initialized ticks must be strictly increasing: index=%d previous=%d current=%d",
				index,
				previousTick,
				tickIndex,
			)
		}

		previousTick = tickIndex

		tickState, exists :=
			pool.Ticks[tickIndex]

		if !exists {
			return fmt.Errorf(
				"initialized tick %d is missing from ticks map",
				tickIndex,
			)
		}

		if tickState == nil {
			return fmt.Errorf(
				"tick state %d is nil",
				tickIndex,
			)
		}

		if tickState.Index != tickIndex {
			return fmt.Errorf(
				"tick map key %d does not match tick state index %d",
				tickIndex,
				tickState.Index,
			)
		}

		if err := validatePositiveUint128(
			fmt.Sprintf(
				"tick %d gross liquidity",
				tickIndex,
			),
			tickState.LiquidityGross,
		); err != nil {
			return err
		}

		if tickState.LiquidityNet == nil {
			return fmt.Errorf(
				"tick %d liquidity net is nil",
				tickIndex,
			)
		}

		absoluteNet := new(big.Int).Abs(
			new(big.Int).Set(
				tickState.LiquidityNet,
			),
		)

		if absoluteNet.Cmp(
			tickState.LiquidityGross,
		) > 0 {
			return fmt.Errorf(
				"tick %d absolute liquidity net %s exceeds gross liquidity %s",
				tickIndex,
				absoluteNet,
				tickState.LiquidityGross,
			)
		}

		if tickIndex <= pool.CurrentTick {
			reconstructedLiquidity.Add(
				reconstructedLiquidity,
				tickState.LiquidityNet,
			)
		}
	}

	if reconstructedLiquidity.Cmp(
		pool.Liquidity,
	) != 0 {
		return fmt.Errorf(
			"pool active liquidity mismatch: reconstructed=%s stored=%s current_tick=%d",
			reconstructedLiquidity,
			pool.Liquidity,
			pool.CurrentTick,
		)
	}

	return nil
}

func previousInitializedTickInclusive(
	ticks []int,
	currentTick int,
) (int, bool) {
	index := sort.Search(
		len(ticks),
		func(index int) bool {
			return ticks[index] >
				currentTick
		},
	)

	if index == 0 {
		return 0, false
	}

	return ticks[index-1], true
}

func nextInitializedTickExclusive(
	ticks []int,
	currentTick int,
) (int, bool) {
	index := sort.Search(
		len(ticks),
		func(index int) bool {
			return ticks[index] >
				currentTick
		},
	)

	if index >= len(ticks) {
		return 0, false
	}

	return ticks[index], true
}

func nextSwapBoundary(
	pool *domain.ReconstructedPool,
	currentTick int,
	zeroForOne bool,
) (swapBoundary, error) {
	if zeroForOne {
		tick, exists :=
			previousInitializedTickInclusive(
				pool.InitializedTicks,
				currentTick,
			)

		if !exists {
			return swapBoundary{
				Tick: MinTick,

				SqrtPriceX96: minSqrtPriceLimit(),

				Initialized: false,
			}, nil
		}

		sqrtPrice, err :=
			SqrtRatioAtTick(tick)
		if err != nil {
			return swapBoundary{}, fmt.Errorf(
				"calculate sqrt price for initialized tick %d: %w",
				tick,
				err,
			)
		}

		minimumLimit :=
			minSqrtPriceLimit()

		if sqrtPrice.Cmp(
			minimumLimit,
		) < 0 {
			return swapBoundary{
				Tick: MinTick,

				SqrtPriceX96: minimumLimit,

				Initialized: false,
			}, nil
		}

		return swapBoundary{
			Tick: tick,

			SqrtPriceX96: sqrtPrice,

			Initialized: true,
		}, nil
	}

	tick, exists :=
		nextInitializedTickExclusive(
			pool.InitializedTicks,
			currentTick,
		)

	if !exists {
		return swapBoundary{
			Tick: MaxTick,

			SqrtPriceX96: maxSqrtPriceLimit(),

			Initialized: false,
		}, nil
	}

	sqrtPrice, err :=
		SqrtRatioAtTick(tick)
	if err != nil {
		return swapBoundary{}, fmt.Errorf(
			"calculate sqrt price for initialized tick %d: %w",
			tick,
			err,
		)
	}

	maximumLimit :=
		maxSqrtPriceLimit()

	if sqrtPrice.Cmp(
		maximumLimit,
	) > 0 {
		return swapBoundary{
			Tick: MaxTick,

			SqrtPriceX96: maximumLimit,

			Initialized: false,
		}, nil
	}

	return swapBoundary{
		Tick: tick,

		SqrtPriceX96: sqrtPrice,

		Initialized: true,
	}, nil
}

func validateBoundaryDirection(
	currentSqrtPriceX96 *big.Int,
	targetSqrtPriceX96 *big.Int,
	zeroForOne bool,
) error {
	comparison :=
		targetSqrtPriceX96.Cmp(
			currentSqrtPriceX96,
		)

	if zeroForOne &&
		comparison > 0 {
		return fmt.Errorf(
			"zero-for-one target price %s exceeds current price %s",
			targetSqrtPriceX96,
			currentSqrtPriceX96,
		)
	}

	if !zeroForOne &&
		comparison < 0 {
		return fmt.Errorf(
			"one-for-zero target price %s is below current price %s",
			targetSqrtPriceX96,
			currentSqrtPriceX96,
		)
	}

	return nil
}

func crossInitializedTick(
	pool *domain.ReconstructedPool,
	state *swapState,
	tick int,
	zeroForOne bool,
) error {
	tickState, exists :=
		pool.Ticks[tick]

	if !exists {
		return fmt.Errorf(
			"initialized tick %d is missing from pool ticks",
			tick,
		)
	}

	if tickState == nil ||
		tickState.LiquidityNet == nil {
		return fmt.Errorf(
			"tick %d has invalid liquidity state",
			tick,
		)
	}

	nextLiquidity := new(big.Int).Set(
		state.liquidity,
	)

	if zeroForOne {
		nextLiquidity.Sub(
			nextLiquidity,
			tickState.LiquidityNet,
		)
	} else {
		nextLiquidity.Add(
			nextLiquidity,
			tickState.LiquidityNet,
		)
	}

	if err := validateUint128(
		"active liquidity after tick crossing",
		nextLiquidity,
	); err != nil {
		return fmt.Errorf(
			"cross tick %d: %w",
			tick,
			err,
		)
	}

	nextTick := tick

	if zeroForOne {
		if tick <= MinTick {
			return fmt.Errorf(
				"cannot cross below minimum tick %d",
				MinTick,
			)
		}

		nextTick = tick - 1
	}

	if nextTick < MinTick ||
		nextTick > MaxTick {
		return fmt.Errorf(
			"tick after crossing %d is outside [%d,%d]",
			nextTick,
			MinTick,
			MaxTick,
		)
	}

	state.liquidity =
		nextLiquidity

	state.currentTick =
		nextTick

	return nil
}

func minSqrtPriceLimit() *big.Int {
	return new(big.Int).Add(
		new(big.Int).Set(
			MinSqrtRatio,
		),
		big.NewInt(1),
	)
}

func maxSqrtPriceLimit() *big.Int {
	return new(big.Int).Sub(
		new(big.Int).Set(
			MaxSqrtRatio,
		),
		big.NewInt(1),
	)
}

func spotPrice(
	sqrtPriceX96 *big.Int,
) (decimal.Decimal, error) {
	if err := validatePositiveUint160(
		"sqrt price",
		sqrtPriceX96,
	); err != nil {
		return decimal.Zero, err
	}

	numerator := new(big.Int).Mul(
		sqrtPriceX96,
		sqrtPriceX96,
	)

	denominator := new(big.Int).Lsh(
		big.NewInt(1),
		192,
	)

	return decimalFromFraction(
		numerator,
		denominator,
	)
}

func executionPrice(
	zeroForOne bool,
	amountInLessFee *big.Int,
	amountOut *big.Int,
) (decimal.Decimal, error) {
	if amountInLessFee == nil ||
		amountInLessFee.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf(
			"usable input must be greater than zero",
		)
	}

	if amountOut == nil ||
		amountOut.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf(
			"amount out must be greater than zero",
		)
	}

	if zeroForOne {
		// token1 raw units per token0 raw unit
		return decimalFromFraction(
			amountOut,
			amountInLessFee,
		)
	}

	// token1 raw units per token0 raw unit
	return decimalFromFraction(
		amountInLessFee,
		amountOut,
	)
}

func decimalFromFraction(
	numerator *big.Int,
	denominator *big.Int,
) (decimal.Decimal, error) {
	if numerator == nil {
		return decimal.Zero, fmt.Errorf(
			"fraction numerator is nil",
		)
	}

	if numerator.Sign() < 0 {
		return decimal.Zero, fmt.Errorf(
			"fraction numerator must not be negative",
		)
	}

	if denominator == nil ||
		denominator.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf(
			"fraction denominator must be greater than zero",
		)
	}

	rational := new(big.Rat).SetFrac(
		numerator,
		denominator,
	)

	value, err := decimal.NewFromString(
		rational.FloatString(
			reportDecimalPlaces,
		),
	)
	if err != nil {
		return decimal.Zero, fmt.Errorf(
			"convert exact fraction to decimal: %w",
			err,
		)
	}

	return value, nil
}
