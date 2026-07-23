package uniswapv3

import (
	"math/big"
	"testing"
)

func TestSqrtRatioAtTickKnownBounds(
	t *testing.T,
) {
	t.Parallel()

	minimum, err :=
		SqrtRatioAtTick(MinTick)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(MinTick) error = %v",
			err,
		)
	}

	if minimum.Cmp(MinSqrtRatio) != 0 {
		t.Fatalf(
			"SqrtRatioAtTick(MinTick) = %s, want %s",
			minimum,
			MinSqrtRatio,
		)
	}

	maximum, err :=
		SqrtRatioAtTick(MaxTick)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(MaxTick) error = %v",
			err,
		)
	}

	if maximum.Cmp(MaxSqrtRatio) != 0 {
		t.Fatalf(
			"SqrtRatioAtTick(MaxTick) = %s, want %s",
			maximum,
			MaxSqrtRatio,
		)
	}
}

func TestSqrtRatioAtTickZeroEqualsQ96(
	t *testing.T,
) {
	t.Parallel()

	actual, err := SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(0) error = %v",
			err,
		)
	}

	expected := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	if actual.Cmp(expected) != 0 {
		t.Fatalf(
			"SqrtRatioAtTick(0) = %s, want %s",
			actual,
			expected,
		)
	}
}

func TestSqrtRatioAtTickRejectsOutOfRangeTicks(
	t *testing.T,
) {
	t.Parallel()

	testCases := []int{
		MinTick - 1,
		MaxTick + 1,
	}

	for _, tick := range testCases {
		tick := tick

		t.Run(
			big.NewInt(int64(tick)).String(),
			func(t *testing.T) {
				t.Parallel()

				if _, err :=
					SqrtRatioAtTick(tick); err == nil {
					t.Fatalf(
						"SqrtRatioAtTick(%d) expected error",
						tick,
					)
				}
			},
		)
	}
}

func TestTickAtSqrtRatioRoundTrip(
	t *testing.T,
) {
	t.Parallel()

	testTicks := []int{
		MinTick,
		-800000,
		-500000,
		-100000,
		-1,
		0,
		1,
		100000,
		500000,
		800000,
		MaxTick - 1,
	}

	for _, expectedTick := range testTicks {
		expectedTick := expectedTick

		t.Run(
			big.NewInt(
				int64(expectedTick),
			).String(),
			func(t *testing.T) {
				t.Parallel()

				ratio, err :=
					SqrtRatioAtTick(expectedTick)
				if err != nil {
					t.Fatalf(
						"SqrtRatioAtTick(%d) error = %v",
						expectedTick,
						err,
					)
				}

				actualTick, err :=
					TickAtSqrtRatio(ratio)
				if err != nil {
					t.Fatalf(
						"TickAtSqrtRatio() error = %v",
						err,
					)
				}

				if actualTick != expectedTick {
					t.Fatalf(
						"TickAtSqrtRatio(SqrtRatioAtTick(%d)) = %d",
						expectedTick,
						actualTick,
					)
				}
			},
		)
	}
}

func TestTickAtSqrtRatioBetweenAdjacentTicks(
	t *testing.T,
) {
	t.Parallel()

	const expectedTick = 120

	lower, err :=
		SqrtRatioAtTick(expectedTick)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(%d) error = %v",
			expectedTick,
			err,
		)
	}

	upper, err :=
		SqrtRatioAtTick(expectedTick + 1)
	if err != nil {
		t.Fatalf(
			"SqrtRatioAtTick(%d) error = %v",
			expectedTick+1,
			err,
		)
	}

	middle := new(big.Int).Add(
		lower,
		upper,
	)

	middle.Quo(
		middle,
		big.NewInt(2),
	)

	actualTick, err :=
		TickAtSqrtRatio(middle)
	if err != nil {
		t.Fatalf(
			"TickAtSqrtRatio() error = %v",
			err,
		)
	}

	if actualTick != expectedTick {
		t.Fatalf(
			"TickAtSqrtRatio(midpoint) = %d, want %d",
			actualTick,
			expectedTick,
		)
	}
}

func TestTickAtSqrtRatioRejectsInvalidBounds(
	t *testing.T,
) {
	t.Parallel()

	belowMinimum := new(big.Int).Sub(
		MinSqrtRatio,
		big.NewInt(1),
	)

	if _, err :=
		TickAtSqrtRatio(belowMinimum); err == nil {
		t.Fatal(
			"TickAtSqrtRatio(below minimum) expected error",
		)
	}

	if _, err :=
		TickAtSqrtRatio(MaxSqrtRatio); err == nil {
		t.Fatal(
			"TickAtSqrtRatio(maximum) expected exclusive-bound error",
		)
	}

	if _, err :=
		TickAtSqrtRatio(nil); err == nil {
		t.Fatal(
			"TickAtSqrtRatio(nil) expected error",
		)
	}
}
