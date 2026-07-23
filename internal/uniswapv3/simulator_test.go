package uniswapv3

import (
	"errors"
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestSimulatorExactInputInsideSingleRange(
	t *testing.T,
) {
	t.Parallel()

	pool := newSingleRangeTestPool(t)

	simulator, err := NewSimulator(500)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	poolBefore := cloneSimulatorTestPool(
		pool,
	)

	result, err := simulator.SimulateExactInput(
		pool,
		ExactInputRequest{
			AmountIn: big.NewInt(1_000_000),

			ZeroForOne: true,
		},
	)
	if err != nil {
		t.Fatalf(
			"SimulateExactInput() error = %v",
			err,
		)
	}

	accountedInput := new(big.Int).Add(
		new(big.Int).Set(
			result.AmountInLessFee,
		),
		result.FeeAmount,
	)

	assertBigIntEqual(
		t,
		accountedInput,
		result.AmountIn,
	)

	if result.CrossedTicks != 0 {
		t.Fatalf(
			"CrossedTicks = %d, want 0",
			result.CrossedTicks,
		)
	}

	if result.SwapSteps != 1 {
		t.Fatalf(
			"SwapSteps = %d, want 1",
			result.SwapSteps,
		)
	}

	if result.TickAfter !=
		result.TickAfterApprox {
		t.Fatalf(
			"TickAfter = %d, TickAfterApprox = %d",
			result.TickAfter,
			result.TickAfterApprox,
		)
	}

	derivedTick, err := TickAtSqrtRatio(
		result.SqrtPriceAfterX96,
	)
	if err != nil {
		t.Fatalf(
			"TickAtSqrtRatio() error = %v",
			err,
		)
	}

	if result.TickAfter != derivedTick {
		t.Fatalf(
			"TickAfter = %d, want derived tick %d",
			result.TickAfter,
			derivedTick,
		)
	}

	assertSimulatorPoolEqual(
		t,
		pool,
		poolBefore,
	)
}

func TestSimulatorAppliesFeePerSwapStep(
	t *testing.T,
) {
	t.Parallel()

	const feePips int64 = 500

	pool := newTwoRangeTestPool(t)

	simulator, err := NewSimulator(
		feePips,
	)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	firstBoundary, err :=
		SqrtRatioAtTick(-100)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(-100) error = %v",
			err,
		)
	}

	secondBoundary, err :=
		SqrtRatioAtTick(-200)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(-200) error = %v",
			err,
		)
	}

	firstAmountIn, err := getAmount0Delta(
		firstBoundary,
		pool.SqrtPriceX96,
		pool.Liquidity,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getAmount0Delta(first range) error = %v",
			err,
		)
	}

	firstFee, err := mulDivRoundingUp(
		firstAmountIn,
		big.NewInt(feePips),
		big.NewInt(
			FeeDenominator-feePips,
		),
	)
	if err != nil {
		t.Fatalf(
			"calculate first-step fee error = %v",
			err,
		)
	}

	firstGrossInput := new(big.Int).Add(
		new(big.Int).Set(
			firstAmountIn,
		),
		firstFee,
	)

	firstAmountOut, err := getAmount1Delta(
		firstBoundary,
		pool.SqrtPriceX96,
		pool.Liquidity,
		false,
	)
	if err != nil {
		t.Fatalf(
			"getAmount1Delta(first range) error = %v",
			err,
		)
	}

	liquidityAfterFirstCross :=
		new(big.Int).Sub(
			new(big.Int).Set(
				pool.Liquidity,
			),
			pool.Ticks[-100].LiquidityNet,
		)

	var (
		secondGrossInput *big.Int
		secondStep       exactInputSwapStep
		legacyGlobalFee  *big.Int
	)

	for candidate := int64(1_000); candidate <= 1_000_000; candidate++ {
		candidateInput :=
			big.NewInt(candidate)

		step, stepErr :=
			computeSwapStepExactInput(
				firstBoundary,
				secondBoundary,
				liquidityAfterFirstCross,
				candidateInput,
				feePips,
			)
		if stepErr != nil {
			t.Fatalf(
				"compute second swap step error = %v",
				stepErr,
			)
		}

		if step.SqrtPriceNextX96.Cmp(
			secondBoundary,
		) == 0 {
			continue
		}

		if step.AmountOut.Sign() <= 0 {
			continue
		}

		totalGrossInput := new(big.Int).Add(
			new(big.Int).Set(
				firstGrossInput,
			),
			candidateInput,
		)

		legacyUsableInput, legacyErr :=
			mulDivFloor(
				totalGrossInput,
				big.NewInt(
					FeeDenominator-feePips,
				),
				big.NewInt(
					FeeDenominator,
				),
			)
		if legacyErr != nil {
			t.Fatalf(
				"calculate legacy usable input error = %v",
				legacyErr,
			)
		}

		legacyFee := new(big.Int).Sub(
			new(big.Int).Set(
				totalGrossInput,
			),
			legacyUsableInput,
		)

		expectedStepFee :=
			new(big.Int).Add(
				new(big.Int).Set(
					firstFee,
				),
				step.FeeAmount,
			)

		if expectedStepFee.Cmp(
			legacyFee,
		) == 0 {
			continue
		}

		secondGrossInput =
			new(big.Int).Set(
				candidateInput,
			)

		secondStep = step
		legacyGlobalFee = legacyFee

		break
	}

	if secondGrossInput == nil {
		t.Fatal(
			"failed to find a deterministic input exposing per-step fee rounding",
		)
	}

	totalGrossInput := new(big.Int).Add(
		new(big.Int).Set(
			firstGrossInput,
		),
		secondGrossInput,
	)

	result, err := simulator.SimulateExactInput(
		pool,
		ExactInputRequest{
			AmountIn: totalGrossInput,

			ZeroForOne: true,
		},
	)
	if err != nil {
		t.Fatalf(
			"SimulateExactInput() error = %v",
			err,
		)
	}

	expectedFee := new(big.Int).Add(
		new(big.Int).Set(
			firstFee,
		),
		secondStep.FeeAmount,
	)

	expectedUsableInput := new(big.Int).Add(
		new(big.Int).Set(
			firstAmountIn,
		),
		secondStep.AmountIn,
	)

	expectedAmountOut := new(big.Int).Add(
		new(big.Int).Set(
			firstAmountOut,
		),
		secondStep.AmountOut,
	)

	assertBigIntEqual(
		t,
		result.FeeAmount,
		expectedFee,
	)

	assertBigIntEqual(
		t,
		result.AmountInLessFee,
		expectedUsableInput,
	)

	assertBigIntEqual(
		t,
		result.AmountOut,
		expectedAmountOut,
	)

	if result.FeeAmount.Cmp(
		legacyGlobalFee,
	) == 0 {
		t.Fatalf(
			"step fee %s unexpectedly equals legacy one-shot fee %s",
			result.FeeAmount,
			legacyGlobalFee,
		)
	}

	if result.CrossedTicks != 1 {
		t.Fatalf(
			"CrossedTicks = %d, want 1",
			result.CrossedTicks,
		)
	}

	if result.SwapSteps != 2 {
		t.Fatalf(
			"SwapSteps = %d, want 2",
			result.SwapSteps,
		)
	}

	expectedTick, err := TickAtSqrtRatio(
		secondStep.SqrtPriceNextX96,
	)
	if err != nil {
		t.Fatalf(
			"TickAtSqrtRatio(second step) error = %v",
			err,
		)
	}

	if result.TickAfter != expectedTick {
		t.Fatalf(
			"TickAfter = %d, want %d",
			result.TickAfter,
			expectedTick,
		)
	}
}

func TestSimulatorZeroForOneCrossesCurrentInitializedBoundary(
	t *testing.T,
) {
	t.Parallel()

	boundary, err :=
		SqrtRatioAtTick(-100)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(-100) error = %v",
			err,
		)
	}

	pool := newTwoRangeTestPool(t)

	pool.SqrtPriceX96 =
		new(big.Int).Set(
			boundary,
		)

	pool.CurrentTick = -100

	pool.Liquidity =
		big.NewInt(
			1_000_000_000_000,
		)

	simulator, err := NewSimulator(500)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	result, err := simulator.SimulateExactInput(
		pool,
		ExactInputRequest{
			AmountIn: big.NewInt(10_000),

			ZeroForOne: true,
		},
	)
	if err != nil {
		t.Fatalf(
			"SimulateExactInput() error = %v",
			err,
		)
	}

	if result.CrossedTicks != 1 {
		t.Fatalf(
			"CrossedTicks = %d, want 1",
			result.CrossedTicks,
		)
	}

	if result.SwapSteps != 2 {
		t.Fatalf(
			"SwapSteps = %d, want 2",
			result.SwapSteps,
		)
	}

	if result.TickAfter >= -100 {
		t.Fatalf(
			"TickAfter = %d, want lower than -100",
			result.TickAfter,
		)
	}
}

func TestSimulatorOneForZeroCrossesUpperBoundary(
	t *testing.T,
) {
	t.Parallel()

	const feePips int64 = 500

	pool := newSingleRangeTestPool(t)

	target, err :=
		SqrtRatioAtTick(100)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(100) error = %v",
			err,
		)
	}

	netAmountIn, err := getAmount1Delta(
		pool.SqrtPriceX96,
		target,
		pool.Liquidity,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getAmount1Delta() error = %v",
			err,
		)
	}

	feeAmount, err := mulDivRoundingUp(
		netAmountIn,
		big.NewInt(feePips),
		big.NewInt(
			FeeDenominator-feePips,
		),
	)
	if err != nil {
		t.Fatalf(
			"calculate fee error = %v",
			err,
		)
	}

	grossAmountIn := new(big.Int).Add(
		new(big.Int).Set(
			netAmountIn,
		),
		feeAmount,
	)

	simulator, err := NewSimulator(
		feePips,
	)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	result, err := simulator.SimulateExactInput(
		pool,
		ExactInputRequest{
			AmountIn: grossAmountIn,

			ZeroForOne: false,
		},
	)
	if err != nil {
		t.Fatalf(
			"SimulateExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		result.SqrtPriceAfterX96,
		target,
	)

	if result.CrossedTicks != 1 {
		t.Fatalf(
			"CrossedTicks = %d, want 1",
			result.CrossedTicks,
		)
	}

	if result.TickAfter != 100 {
		t.Fatalf(
			"TickAfter = %d, want 100",
			result.TickAfter,
		)
	}
}

func TestSimulateExactInputNoCrossRejectsCrossing(
	t *testing.T,
) {
	t.Parallel()

	const feePips int64 = 500

	pool := newSingleRangeTestPool(t)

	target, err :=
		SqrtRatioAtTick(-100)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(-100) error = %v",
			err,
		)
	}

	netAmountIn, err := getAmount0Delta(
		target,
		pool.SqrtPriceX96,
		pool.Liquidity,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getAmount0Delta() error = %v",
			err,
		)
	}

	feeAmount, err := mulDivRoundingUp(
		netAmountIn,
		big.NewInt(feePips),
		big.NewInt(
			FeeDenominator-feePips,
		),
	)
	if err != nil {
		t.Fatalf(
			"calculate fee error = %v",
			err,
		)
	}

	grossAmountIn := new(big.Int).Add(
		new(big.Int).Set(
			netAmountIn,
		),
		feeAmount,
	)

	simulator, err := NewSimulator(
		feePips,
	)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	_, err = simulator.SimulateExactInputNoCross(
		pool,
		ExactInputRequest{
			AmountIn: grossAmountIn,

			ZeroForOne: true,
		},
	)

	if !errors.Is(
		err,
		ErrTickCrossingRequired,
	) {
		t.Fatalf(
			"SimulateExactInputNoCross() error = %v, want ErrTickCrossingRequired",
			err,
		)
	}
}

func TestSimulatorRejectsUnsortedInitializedTicks(
	t *testing.T,
) {
	t.Parallel()

	pool := newSingleRangeTestPool(t)

	pool.InitializedTicks =
		[]int{100, -100}

	simulator, err := NewSimulator(500)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	_, err = simulator.SimulateExactInput(
		pool,
		ExactInputRequest{
			AmountIn: big.NewInt(1_000),

			ZeroForOne: true,
		},
	)

	if err == nil {
		t.Fatal(
			"SimulateExactInput() expected unsorted-ticks error",
		)
	}
}

func newSingleRangeTestPool(
	t *testing.T,
) *domain.ReconstructedPool {
	t.Helper()

	sqrtPrice, err :=
		SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	liquidity :=
		big.NewInt(
			1_000_000_000_000,
		)

	return &domain.ReconstructedPool{
		PoolAddress: "0x0000000000000000000000000000000000000001",

		BlockNumber: 1,

		SqrtPriceX96: sqrtPrice,

		CurrentTick: 0,

		Liquidity: new(big.Int).Set(
			liquidity,
		),

		Ticks: map[int]*domain.TickState{
			-100: {
				Index: -100,

				LiquidityGross: new(big.Int).Set(
					liquidity,
				),

				LiquidityNet: new(big.Int).Set(
					liquidity,
				),
			},

			100: {
				Index: 100,

				LiquidityGross: new(big.Int).Set(
					liquidity,
				),

				LiquidityNet: new(big.Int).Neg(
					new(big.Int).Set(
						liquidity,
					),
				),
			},
		},

		InitializedTicks: []int{-100, 100},
	}
}

func newTwoRangeTestPool(
	t *testing.T,
) *domain.ReconstructedPool {
	t.Helper()

	sqrtPrice, err :=
		SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	upperLiquidity :=
		big.NewInt(
			1_000_000_000_000,
		)

	lowerLiquidity :=
		big.NewInt(
			500_000_000_000,
		)

	sharedGross := new(big.Int).Add(
		new(big.Int).Set(
			upperLiquidity,
		),
		lowerLiquidity,
	)

	sharedNet := new(big.Int).Sub(
		new(big.Int).Set(
			upperLiquidity,
		),
		lowerLiquidity,
	)

	return &domain.ReconstructedPool{
		PoolAddress: "0x0000000000000000000000000000000000000001",

		BlockNumber: 1,

		SqrtPriceX96: sqrtPrice,

		CurrentTick: 0,

		Liquidity: new(big.Int).Set(
			upperLiquidity,
		),

		Ticks: map[int]*domain.TickState{
			-200: {
				Index: -200,

				LiquidityGross: new(big.Int).Set(
					lowerLiquidity,
				),

				LiquidityNet: new(big.Int).Set(
					lowerLiquidity,
				),
			},

			-100: {
				Index: -100,

				LiquidityGross: sharedGross,

				LiquidityNet: sharedNet,
			},

			100: {
				Index: 100,

				LiquidityGross: new(big.Int).Set(
					upperLiquidity,
				),

				LiquidityNet: new(big.Int).Neg(
					new(big.Int).Set(
						upperLiquidity,
					),
				),
			},
		},

		InitializedTicks: []int{-200, -100, 100},
	}
}

func cloneSimulatorTestPool(
	pool *domain.ReconstructedPool,
) *domain.ReconstructedPool {
	clonedTicks := make(
		map[int]*domain.TickState,
		len(pool.Ticks),
	)

	for index, tickState := range pool.Ticks {
		clonedTicks[index] =
			&domain.TickState{
				Index: tickState.Index,

				LiquidityGross: new(big.Int).Set(
					tickState.LiquidityGross,
				),

				LiquidityNet: new(big.Int).Set(
					tickState.LiquidityNet,
				),
			}
	}

	initializedTicks := append(
		[]int(nil),
		pool.InitializedTicks...,
	)

	return &domain.ReconstructedPool{
		PoolAddress: pool.PoolAddress,

		BlockNumber: pool.BlockNumber,

		SqrtPriceX96: new(big.Int).Set(
			pool.SqrtPriceX96,
		),

		CurrentTick: pool.CurrentTick,

		Liquidity: new(big.Int).Set(
			pool.Liquidity,
		),

		Ticks: clonedTicks,

		InitializedTicks: initializedTicks,
	}
}

func assertSimulatorPoolEqual(
	t *testing.T,
	actual *domain.ReconstructedPool,
	expected *domain.ReconstructedPool,
) {
	t.Helper()

	if actual.PoolAddress !=
		expected.PoolAddress {
		t.Fatalf(
			"PoolAddress = %s, want %s",
			actual.PoolAddress,
			expected.PoolAddress,
		)
	}

	if actual.BlockNumber !=
		expected.BlockNumber {
		t.Fatalf(
			"BlockNumber = %d, want %d",
			actual.BlockNumber,
			expected.BlockNumber,
		)
	}

	assertBigIntEqual(
		t,
		actual.SqrtPriceX96,
		expected.SqrtPriceX96,
	)

	if actual.CurrentTick !=
		expected.CurrentTick {
		t.Fatalf(
			"CurrentTick = %d, want %d",
			actual.CurrentTick,
			expected.CurrentTick,
		)
	}

	assertBigIntEqual(
		t,
		actual.Liquidity,
		expected.Liquidity,
	)

	if len(actual.InitializedTicks) !=
		len(expected.InitializedTicks) {
		t.Fatalf(
			"initialized tick length = %d, want %d",
			len(actual.InitializedTicks),
			len(expected.InitializedTicks),
		)
	}

	for index := range actual.InitializedTicks {
		if actual.InitializedTicks[index] !=
			expected.InitializedTicks[index] {
			t.Fatalf(
				"initialized tick %d = %d, want %d",
				index,
				actual.InitializedTicks[index],
				expected.InitializedTicks[index],
			)
		}
	}

	if len(actual.Ticks) != len(expected.Ticks) {
		t.Fatalf(
			"tick map length = %d, want %d",
			len(actual.Ticks),
			len(expected.Ticks),
		)
	}

	for index, expectedTick := range expected.Ticks {
		actualTick, exists :=
			actual.Ticks[index]

		if !exists {
			t.Fatalf(
				"tick %d is missing",
				index,
			)
		}

		if actualTick.Index !=
			expectedTick.Index {
			t.Fatalf(
				"tick %d Index = %d, want %d",
				index,
				actualTick.Index,
				expectedTick.Index,
			)
		}

		assertBigIntEqual(
			t,
			actualTick.LiquidityGross,
			expectedTick.LiquidityGross,
		)

		assertBigIntEqual(
			t,
			actualTick.LiquidityNet,
			expectedTick.LiquidityNet,
		)
	}
}
