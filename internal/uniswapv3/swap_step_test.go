package uniswapv3

import (
	"math/big"
	"testing"
)

func TestComputeSwapStepExactInputOfficialTargetCappedVector(
	t *testing.T,
) {
	t.Parallel()

	current :=
		encodePriceSqrtForSwapStepTest(
			1,
			1,
		)

	target :=
		encodePriceSqrtForSwapStepTest(
			101,
			100,
		)

	liquidity :=
		mustSwapStepBigInt(
			t,
			"2000000000000000000",
		)

	amountRemaining :=
		mustSwapStepBigInt(
			t,
			"1000000000000000000",
		)

	step, err := computeSwapStepExactInput(
		current,
		target,
		liquidity,
		amountRemaining,
		600,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		step.SqrtPriceNextX96,
		target,
	)

	assertBigIntEqual(
		t,
		step.AmountIn,
		mustSwapStepBigInt(
			t,
			"9975124224178055",
		),
	)

	assertBigIntEqual(
		t,
		step.FeeAmount,
		mustSwapStepBigInt(
			t,
			"5988667735148",
		),
	)

	assertBigIntEqual(
		t,
		step.AmountOut,
		mustSwapStepBigInt(
			t,
			"9925619580021728",
		),
	)

	consumed := new(big.Int).Add(
		step.AmountIn,
		step.FeeAmount,
	)

	if consumed.Cmp(amountRemaining) >= 0 {
		t.Fatalf(
			"target-capped step consumed %s, want less than %s",
			consumed,
			amountRemaining,
		)
	}
}

func TestComputeSwapStepExactInputOfficialFullySpentVector(
	t *testing.T,
) {
	t.Parallel()

	current :=
		encodePriceSqrtForSwapStepTest(
			1,
			1,
		)

	target :=
		encodePriceSqrtForSwapStepTest(
			1000,
			100,
		)

	liquidity :=
		mustSwapStepBigInt(
			t,
			"2000000000000000000",
		)

	amountRemaining :=
		mustSwapStepBigInt(
			t,
			"1000000000000000000",
		)

	step, err := computeSwapStepExactInput(
		current,
		target,
		liquidity,
		amountRemaining,
		600,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		step.AmountIn,
		mustSwapStepBigInt(
			t,
			"999400000000000000",
		),
	)

	assertBigIntEqual(
		t,
		step.FeeAmount,
		mustSwapStepBigInt(
			t,
			"600000000000000",
		),
	)

	assertBigIntEqual(
		t,
		step.AmountOut,
		mustSwapStepBigInt(
			t,
			"666399946655997866",
		),
	)

	consumed := new(big.Int).Add(
		step.AmountIn,
		step.FeeAmount,
	)

	assertBigIntEqual(
		t,
		consumed,
		amountRemaining,
	)

	if step.SqrtPriceNextX96.Cmp(target) >= 0 {
		t.Fatalf(
			"partial step next price %s reached or exceeded target %s",
			step.SqrtPriceNextX96,
			target,
		)
	}

	if step.SqrtPriceNextX96.Cmp(current) <= 0 {
		t.Fatalf(
			"one-for-zero next price %s did not increase from %s",
			step.SqrtPriceNextX96,
			current,
		)
	}
}

func TestComputeSwapStepExactInputOfficialEntireInputTakenAsFee(
	t *testing.T,
) {
	t.Parallel()

	step, err := computeSwapStepExactInput(
		big.NewInt(2413),
		mustSwapStepBigInt(
			t,
			"79887613182836312",
		),
		mustSwapStepBigInt(
			t,
			"1985041575832132834610021537970",
		),
		big.NewInt(10),
		1872,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		step.SqrtPriceNextX96,
		big.NewInt(2413),
	)

	assertBigIntEqual(
		t,
		step.AmountIn,
		big.NewInt(0),
	)

	assertBigIntEqual(
		t,
		step.AmountOut,
		big.NewInt(0),
	)

	assertBigIntEqual(
		t,
		step.FeeAmount,
		big.NewInt(10),
	)
}

func TestComputeSwapStepExactInputZeroForOneConsumesAllInput(
	t *testing.T,
) {
	t.Parallel()

	current, err := SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	target, err := SqrtRatioAtTick(-100)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(-100) error = %v",
			err,
		)
	}

	amountRemaining := big.NewInt(1_000_000)

	step, err := computeSwapStepExactInput(
		current,
		target,
		mustSwapStepBigInt(
			t,
			"1000000000000000000",
		),
		amountRemaining,
		500,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	if step.SqrtPriceNextX96.Cmp(current) >= 0 {
		t.Fatalf(
			"zero-for-one next price %s must be below current %s",
			step.SqrtPriceNextX96,
			current,
		)
	}

	if step.SqrtPriceNextX96.Cmp(target) <= 0 {
		t.Fatalf(
			"zero-for-one partial next price %s must remain above target %s",
			step.SqrtPriceNextX96,
			target,
		)
	}

	consumed := new(big.Int).Add(
		step.AmountIn,
		step.FeeAmount,
	)

	assertBigIntEqual(
		t,
		consumed,
		amountRemaining,
	)

	if step.AmountOut.Sign() <= 0 {
		t.Fatalf(
			"AmountOut = %s, want positive",
			step.AmountOut,
		)
	}
}

func TestComputeSwapStepExactInputTargetReachedLeavesInput(
	t *testing.T,
) {
	t.Parallel()

	current, err := SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	target, err := SqrtRatioAtTick(-10)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(-10) error = %v",
			err,
		)
	}

	amountRemaining :=
		mustSwapStepBigInt(
			t,
			"1000000000000000000000000000000",
		)

	step, err := computeSwapStepExactInput(
		current,
		target,
		big.NewInt(1_000_000_000_000),
		amountRemaining,
		500,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		step.SqrtPriceNextX96,
		target,
	)

	consumed := new(big.Int).Add(
		step.AmountIn,
		step.FeeAmount,
	)

	if consumed.Cmp(amountRemaining) >= 0 {
		t.Fatalf(
			"target-reaching step consumed %s, want less than %s",
			consumed,
			amountRemaining,
		)
	}
}

func TestComputeSwapStepExactInputEqualPricesProduceNoMovement(
	t *testing.T,
) {
	t.Parallel()

	price, err := SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	step, err := computeSwapStepExactInput(
		price,
		price,
		big.NewInt(1_000_000),
		big.NewInt(1_000),
		3_000,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		step.SqrtPriceNextX96,
		price,
	)

	assertBigIntEqual(
		t,
		step.AmountIn,
		big.NewInt(0),
	)

	assertBigIntEqual(
		t,
		step.AmountOut,
		big.NewInt(0),
	)

	assertBigIntEqual(
		t,
		step.FeeAmount,
		big.NewInt(0),
	)
}

func TestComputeSwapStepExactInputDoesNotMutateInputs(
	t *testing.T,
) {
	t.Parallel()

	current, err := SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	target, err := SqrtRatioAtTick(100)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(100) error = %v",
			err,
		)
	}

	liquidity :=
		mustSwapStepBigInt(
			t,
			"1000000000000000000",
		)

	amountRemaining :=
		mustSwapStepBigInt(
			t,
			"1000000000000000",
		)

	currentBefore :=
		new(big.Int).Set(current)

	targetBefore :=
		new(big.Int).Set(target)

	liquidityBefore :=
		new(big.Int).Set(liquidity)

	amountBefore :=
		new(big.Int).Set(
			amountRemaining,
		)

	_, err = computeSwapStepExactInput(
		current,
		target,
		liquidity,
		amountRemaining,
		3_000,
	)
	if err != nil {
		t.Fatalf(
			"computeSwapStepExactInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		current,
		currentBefore,
	)

	assertBigIntEqual(
		t,
		target,
		targetBefore,
	)

	assertBigIntEqual(
		t,
		liquidity,
		liquidityBefore,
	)

	assertBigIntEqual(
		t,
		amountRemaining,
		amountBefore,
	)
}

func TestComputeSwapStepExactInputRejectsInvalidArguments(
	t *testing.T,
) {
	t.Parallel()

	validPrice, err := SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	testCases := []struct {
		name string

		current   *big.Int
		target    *big.Int
		liquidity *big.Int
		amount    *big.Int
		feePips   int64
	}{
		{
			name: "nil current price",

			current:   nil,
			target:    validPrice,
			liquidity: big.NewInt(1),
			amount:    big.NewInt(1),
			feePips:   500,
		},
		{
			name: "zero target price",

			current:   validPrice,
			target:    big.NewInt(0),
			liquidity: big.NewInt(1),
			amount:    big.NewInt(1),
			feePips:   500,
		},
		{
			name: "zero liquidity",

			current:   validPrice,
			target:    validPrice,
			liquidity: big.NewInt(0),
			amount:    big.NewInt(1),
			feePips:   500,
		},
		{
			name: "zero amount remaining",

			current:   validPrice,
			target:    validPrice,
			liquidity: big.NewInt(1),
			amount:    big.NewInt(0),
			feePips:   500,
		},
		{
			name: "negative fee",

			current:   validPrice,
			target:    validPrice,
			liquidity: big.NewInt(1),
			amount:    big.NewInt(1),
			feePips:   -1,
		},
		{
			name: "fee denominator",

			current:   validPrice,
			target:    validPrice,
			liquidity: big.NewInt(1),
			amount:    big.NewInt(1),
			feePips:   FeeDenominator,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				_, err :=
					computeSwapStepExactInput(
						testCase.current,
						testCase.target,
						testCase.liquidity,
						testCase.amount,
						testCase.feePips,
					)

				if err == nil {
					t.Fatal(
						"computeSwapStepExactInput() expected validation error",
					)
				}
			},
		)
	}
}

func encodePriceSqrtForSwapStepTest(
	numerator int64,
	denominator int64,
) *big.Int {
	scaledNumerator := new(big.Int).Lsh(
		big.NewInt(numerator),
		192,
	)

	scaledNumerator.Quo(
		scaledNumerator,
		big.NewInt(denominator),
	)

	return new(big.Int).Sqrt(
		scaledNumerator,
	)
}

func mustSwapStepBigInt(
	t *testing.T,
	value string,
) *big.Int {
	t.Helper()

	result, ok := new(big.Int).SetString(
		value,
		10,
	)
	if !ok {
		t.Fatalf(
			"invalid decimal big.Int %q",
			value,
		)
	}

	return result
}
