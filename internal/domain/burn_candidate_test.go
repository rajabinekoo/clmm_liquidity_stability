package domain

import (
	"math/big"
	"testing"
	"time"
)

func TestBurnCandidateValidate(
	t *testing.T,
) {
	t.Parallel()

	candidate :=
		validBurnCandidate()

	if err := candidate.Validate(); err != nil {
		t.Fatalf(
			"Validate() error = %v",
			err,
		)
	}
}

func TestBurnCandidateRejectsInvalidLiquidity(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name      string
		liquidity *big.Int
	}{
		{
			name: "nil",

			liquidity: nil,
		},
		{
			name: "zero",

			liquidity: big.NewInt(0),
		},
		{
			name: "negative",

			liquidity: big.NewInt(-1),
		},
		{
			name: "uint128 overflow",

			liquidity: new(big.Int).Lsh(
				big.NewInt(1),
				128,
			),
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				candidate :=
					validBurnCandidate()

				candidate.LiquidityRemoved =
					testCase.liquidity

				if err := candidate.Validate(); err == nil {
					t.Fatal(
						"Validate() expected liquidity error",
					)
				}
			},
		)
	}
}

func TestBurnCandidateRejectsInvalidRange(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name      string
		tickLower int
		tickUpper int
	}{
		{
			name: "equal ticks",

			tickLower: 100,

			tickUpper: 100,
		},
		{
			name: "reversed ticks",

			tickLower: 200,

			tickUpper: 100,
		},
		{
			name: "lower below minimum",

			tickLower: minUniswapV3Tick - 1,

			tickUpper: 100,
		},
		{
			name: "upper above maximum",

			tickLower: -100,

			tickUpper: maxUniswapV3Tick + 1,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				candidate :=
					validBurnCandidate()

				candidate.TickLower =
					testCase.tickLower

				candidate.TickUpper =
					testCase.tickUpper

				if err := candidate.Validate(); err == nil {
					t.Fatal(
						"Validate() expected range error",
					)
				}
			},
		)
	}
}

func TestBurnCandidateKeys(
	t *testing.T,
) {
	t.Parallel()

	candidate :=
		validBurnCandidate()

	expectedEventKey :=
		"0xcccccccccccccccccccccccccccccccccccccccc:100:12"

	if candidate.EventKey() !=
		expectedEventKey {
		t.Fatalf(
			"EventKey() = %q, want %q",
			candidate.EventKey(),
			expectedEventKey,
		)
	}

	if candidate.RangeKey() !=
		"-100:100" {
		t.Fatalf(
			"RangeKey() = %q, want %q",
			candidate.RangeKey(),
			"-100:100",
		)
	}
}

func TestBurnCandidateLiquidityRemovedCopyDoesNotAlias(
	t *testing.T,
) {
	t.Parallel()

	candidate :=
		validBurnCandidate()

	cloned :=
		candidate.LiquidityRemovedCopy()

	if cloned ==
		candidate.LiquidityRemoved {
		t.Fatal(
			"LiquidityRemovedCopy() aliases original pointer",
		)
	}

	if cloned.Cmp(
		candidate.LiquidityRemoved,
	) != 0 {
		t.Fatalf(
			"LiquidityRemovedCopy() = %s, want %s",
			cloned,
			candidate.LiquidityRemoved,
		)
	}
}

func validBurnCandidate() BurnCandidate {
	return BurnCandidate{
		ID: "burn:0xtransaction#1",

		PoolAddress: "0xcccccccccccccccccccccccccccccccccccccccc",

		TxHash: "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",

		Cursor: EventCursor{
			BlockNumber: 100,

			LogIndex: 12,
		},

		Timestamp: time.Unix(
			1_700_000_000,
			0,
		).UTC(),

		TickLower: -100,

		TickUpper: 100,

		LiquidityRemoved: big.NewInt(
			1_000_000,
		),
	}
}
