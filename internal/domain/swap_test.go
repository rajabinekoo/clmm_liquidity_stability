package domain

import (
	"math/big"
	"testing"
)

func TestSwapEventZeroForOne(
	t *testing.T,
) {
	t.Parallel()

	swap := validDomainSwapEvent()

	swap.Amount0Raw =
		big.NewInt(1_000)

	swap.Amount1Raw =
		big.NewInt(-900)

	if err := swap.Validate(); err != nil {
		t.Fatalf(
			"Validate() error = %v",
			err,
		)
	}

	if !swap.IsZeroForOne() {
		t.Fatal(
			"IsZeroForOne() = false, want true",
		)
	}

	if swap.IsOneForZero() {
		t.Fatal(
			"IsOneForZero() = true, want false",
		)
	}

	assertDomainBigIntEqual(
		t,
		swap.AmountInRaw(),
		big.NewInt(1_000),
	)

	assertDomainBigIntEqual(
		t,
		swap.AmountOutRaw(),
		big.NewInt(900),
	)
}

func TestSwapEventOneForZero(
	t *testing.T,
) {
	t.Parallel()

	swap := validDomainSwapEvent()

	swap.Amount0Raw =
		big.NewInt(-700)

	swap.Amount1Raw =
		big.NewInt(800)

	if err := swap.Validate(); err != nil {
		t.Fatalf(
			"Validate() error = %v",
			err,
		)
	}

	if !swap.IsOneForZero() {
		t.Fatal(
			"IsOneForZero() = false, want true",
		)
	}

	if swap.IsZeroForOne() {
		t.Fatal(
			"IsZeroForOne() = true, want false",
		)
	}

	assertDomainBigIntEqual(
		t,
		swap.AmountInRaw(),
		big.NewInt(800),
	)

	assertDomainBigIntEqual(
		t,
		swap.AmountOutRaw(),
		big.NewInt(700),
	)
}

func TestSwapEventDirectionAndAmountsSupportZeroOutputObservation(
	t *testing.T,
) {
	t.Parallel()

	zeroForOne := validDomainSwapEvent()
	zeroForOne.Amount0Raw = big.NewInt(1)
	zeroForOne.Amount1Raw = big.NewInt(0)

	if err := zeroForOne.ValidateForObservation(); err != nil {
		t.Fatalf("zeroForOne ValidateForObservation() error = %v", err)
	}
	if !zeroForOne.IsZeroForOne() || zeroForOne.IsOneForZero() {
		t.Fatal("zero-output token0 input direction was not classified")
	}
	assertDomainBigIntEqual(t, zeroForOne.AmountInRaw(), big.NewInt(1))
	assertDomainBigIntEqual(t, zeroForOne.AmountOutRaw(), big.NewInt(0))

	oneForZero := validDomainSwapEvent()
	oneForZero.Amount0Raw = big.NewInt(0)
	oneForZero.Amount1Raw = big.NewInt(1)

	if err := oneForZero.ValidateForObservation(); err != nil {
		t.Fatalf("oneForZero ValidateForObservation() error = %v", err)
	}
	if !oneForZero.IsOneForZero() || oneForZero.IsZeroForOne() {
		t.Fatal("zero-output token1 input direction was not classified")
	}
	assertDomainBigIntEqual(t, oneForZero.AmountInRaw(), big.NewInt(1))
	assertDomainBigIntEqual(t, oneForZero.AmountOutRaw(), big.NewInt(0))
}

func TestSwapEventDirectionMethodsAreNilSafe(
	t *testing.T,
) {
	t.Parallel()

	swap := SwapEvent{}

	if swap.IsZeroForOne() {
		t.Fatal(
			"IsZeroForOne() = true for empty event",
		)
	}

	if swap.IsOneForZero() {
		t.Fatal(
			"IsOneForZero() = true for empty event",
		)
	}

	assertDomainBigIntEqual(
		t,
		swap.AmountInRaw(),
		big.NewInt(0),
	)

	assertDomainBigIntEqual(
		t,
		swap.AmountOutRaw(),
		big.NewInt(0),
	)
}

func TestSwapEventRejectsEqualSignAmounts(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name    string
		amount0 *big.Int
		amount1 *big.Int
	}{
		{
			name:    "both positive",
			amount0: big.NewInt(100),
			amount1: big.NewInt(200),
		},
		{
			name:    "both negative",
			amount0: big.NewInt(-100),
			amount1: big.NewInt(-200),
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				swap :=
					validDomainSwapEvent()

				swap.Amount0Raw =
					testCase.amount0

				swap.Amount1Raw =
					testCase.amount1

				if err := swap.Validate(); err == nil {
					t.Fatal(
						"Validate() expected opposite-sign error",
					)
				}
			},
		)
	}
}

func TestSwapEventRejectsZeroTokenAmounts(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name    string
		amount0 *big.Int
		amount1 *big.Int
	}{
		{
			name:    "zero amount0",
			amount0: big.NewInt(0),
			amount1: big.NewInt(-1),
		},
		{
			name:    "zero amount1",
			amount0: big.NewInt(1),
			amount1: big.NewInt(0),
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				swap :=
					validDomainSwapEvent()

				swap.Amount0Raw =
					testCase.amount0

				swap.Amount1Raw =
					testCase.amount1

				if err := swap.Validate(); err == nil {
					t.Fatal(
						"Validate() expected zero-amount error",
					)
				}
			},
		)
	}
}

func TestSwapEventRejectsInvalidProtocolState(
	t *testing.T,
) {
	t.Parallel()

	t.Run(
		"sqrt price exceeds uint160",
		func(t *testing.T) {
			t.Parallel()

			swap :=
				validDomainSwapEvent()

			swap.SqrtPriceX96After =
				new(big.Int).Lsh(
					big.NewInt(1),
					160,
				)

			if err := swap.Validate(); err == nil {
				t.Fatal(
					"Validate() expected uint160 error",
				)
			}
		},
	)

	t.Run(
		"tick below minimum",
		func(t *testing.T) {
			t.Parallel()

			swap :=
				validDomainSwapEvent()

			swap.TickAfter =
				minUniswapV3Tick - 1

			if err := swap.Validate(); err == nil {
				t.Fatal(
					"Validate() expected minimum tick error",
				)
			}
		},
	)

	t.Run(
		"tick above maximum",
		func(t *testing.T) {
			t.Parallel()

			swap :=
				validDomainSwapEvent()

			swap.TickAfter =
				maxUniswapV3Tick + 1

			if err := swap.Validate(); err == nil {
				t.Fatal(
					"Validate() expected maximum tick error",
				)
			}
		},
	)
}

func validDomainSwapEvent() SwapEvent {
	return SwapEvent{
		ID: "swap-1",

		TxHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

		PoolAddress: "0x1111111111111111111111111111111111111111",

		BlockNumber: 1,

		LogIndex: 2,

		Timestamp: 1_700_000_000,

		Amount0Raw: big.NewInt(1_000),

		Amount1Raw: big.NewInt(-900),

		SqrtPriceX96After: new(big.Int).Lsh(
			big.NewInt(1),
			96,
		),

		TickAfter: 0,
	}
}

func assertDomainBigIntEqual(
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

func TestSwapEventValidateForObservationAllowsZeroOutput(
	t *testing.T,
) {
	t.Parallel()

	swap :=
		SwapEvent{
			ID: "swap-1",

			PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

			BlockNumber: 100,

			LogIndex: 1,

			Amount0Raw: big.NewInt(100),

			Amount1Raw: big.NewInt(0),

			SqrtPriceX96After: big.NewInt(1_000),
		}

	if err :=
		swap.ValidateForObservation(); err != nil {
		t.Fatalf(
			"ValidateForObservation() error = %v",
			err,
		)
	}

	if err :=
		swap.ValidateForSimulation(); err == nil {
		t.Fatal(
			"ValidateForSimulation() error = nil, want non-nil",
		)
	}
}
