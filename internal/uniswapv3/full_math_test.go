package uniswapv3

import (
	"math/big"
	"testing"
)

func TestMulDivFloor(
	t *testing.T,
) {
	t.Parallel()

	actual, err := mulDivFloor(
		big.NewInt(10),
		big.NewInt(10),
		big.NewInt(6),
	)
	if err != nil {
		t.Fatalf(
			"mulDivFloor() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		big.NewInt(16),
	)
}

func TestMulDivRoundingUp(
	t *testing.T,
) {
	t.Parallel()

	actual, err := mulDivRoundingUp(
		big.NewInt(10),
		big.NewInt(10),
		big.NewInt(6),
	)
	if err != nil {
		t.Fatalf(
			"mulDivRoundingUp() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		big.NewInt(17),
	)
}

func TestMulDivRoundingUpDoesNotRoundExactResult(
	t *testing.T,
) {
	t.Parallel()

	actual, err := mulDivRoundingUp(
		big.NewInt(12),
		big.NewInt(5),
		big.NewInt(6),
	)
	if err != nil {
		t.Fatalf(
			"mulDivRoundingUp() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		big.NewInt(10),
	)
}

func TestMulDivSupportsFullPrecisionIntermediateProduct(
	t *testing.T,
) {
	t.Parallel()

	// maxUint256 * maxUint256 exceeds uint256 as an intermediate value,
	// but division by maxUint256 produces an exact uint256 result.
	actual, err := mulDivFloor(
		maxUint256,
		maxUint256,
		maxUint256,
	)
	if err != nil {
		t.Fatalf(
			"mulDivFloor() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		maxUint256,
	)
}

func TestMulDivRejectsResultOverflow(
	t *testing.T,
) {
	t.Parallel()

	_, err := mulDivFloor(
		maxUint256,
		maxUint256,
		big.NewInt(1),
	)
	if err == nil {
		t.Fatal(
			"mulDivFloor() expected uint256 overflow error",
		)
	}
}

func TestMulDivRejectsNegativeInput(
	t *testing.T,
) {
	t.Parallel()

	_, err := mulDivFloor(
		big.NewInt(-1),
		big.NewInt(1),
		big.NewInt(1),
	)
	if err == nil {
		t.Fatal(
			"mulDivFloor() expected negative input error",
		)
	}
}

func TestDivRoundingUp(
	t *testing.T,
) {
	t.Parallel()

	actual, err := divRoundingUp(
		big.NewInt(10),
		big.NewInt(3),
	)
	if err != nil {
		t.Fatalf(
			"divRoundingUp() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		big.NewInt(4),
	)
}

func TestDivRoundingUpDoesNotRoundExactResult(
	t *testing.T,
) {
	t.Parallel()

	actual, err := divRoundingUp(
		big.NewInt(12),
		big.NewInt(3),
	)
	if err != nil {
		t.Fatalf(
			"divRoundingUp() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		big.NewInt(4),
	)
}

func TestFullMathAcceptsZeroNumerator(
	t *testing.T,
) {
	t.Parallel()

	actual, err := mulDivRoundingUp(
		big.NewInt(0),
		maxUint256,
		big.NewInt(7),
	)
	if err != nil {
		t.Fatalf(
			"mulDivRoundingUp() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		actual,
		big.NewInt(0),
	)
}

func TestFullMathRejectsZeroDenominator(
	t *testing.T,
) {
	t.Parallel()

	_, err := mulDivFloor(
		big.NewInt(1),
		big.NewInt(1),
		big.NewInt(0),
	)
	if err == nil {
		t.Fatal(
			"mulDivFloor() expected zero denominator error",
		)
	}
}

func TestFullMathRejectsNilValues(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name        string
		a           *big.Int
		b           *big.Int
		denominator *big.Int
	}{
		{
			name:        "nil a",
			a:           nil,
			b:           big.NewInt(1),
			denominator: big.NewInt(1),
		},
		{
			name:        "nil b",
			a:           big.NewInt(1),
			b:           nil,
			denominator: big.NewInt(1),
		},
		{
			name:        "nil denominator",
			a:           big.NewInt(1),
			b:           big.NewInt(1),
			denominator: nil,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				_, err := mulDivFloor(
					testCase.a,
					testCase.b,
					testCase.denominator,
				)
				if err == nil {
					t.Fatal(
						"mulDivFloor() expected validation error",
					)
				}
			},
		)
	}
}

func assertBigIntEqual(
	t *testing.T,
	actual *big.Int,
	expected *big.Int,
) {
	t.Helper()

	if actual == nil {
		t.Fatal(
			"actual big.Int is nil",
		)
	}

	if expected == nil {
		t.Fatal(
			"expected big.Int is nil",
		)
	}

	if actual.Cmp(expected) != 0 {
		t.Fatalf(
			"actual = %s, want %s",
			actual,
			expected,
		)
	}
}
