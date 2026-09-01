package repositories

import (
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestNormalizeRangeLiquidityBeforeCursorQuery(
	t *testing.T,
) {
	t.Parallel()

	address, cursor, err :=
		normalizeRangeLiquidityBeforeCursorQuery(
			" 0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA ",
			-100,
			100,
			domain.EventCursor{
				BlockNumber: 200,

				LogIndex: 15,
			},
		)
	if err != nil {
		t.Fatalf(
			"normalizeRangeLiquidityBeforeCursorQuery() error = %v",
			err,
		)
	}

	expectedAddress :=
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if address != expectedAddress {
		t.Fatalf(
			"address = %q, want %q",
			address,
			expectedAddress,
		)
	}

	if cursor.BlockNumber != 200 ||
		cursor.LogIndex != 15 {
		t.Fatalf(
			"cursor = %s, want 200:15",
			cursor,
		)
	}
}

func TestNormalizeRangeLiquidityBeforeCursorQueryRejectsInvalidRange(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name      string
		tickLower int
		tickUpper int
	}{
		{
			name: "equal",

			tickLower: 100,

			tickUpper: 100,
		},
		{
			name: "reversed",

			tickLower: 200,

			tickUpper: 100,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				_, _, err :=
					normalizeRangeLiquidityBeforeCursorQuery(
						"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						testCase.tickLower,
						testCase.tickUpper,
						domain.EventCursor{
							BlockNumber: 200,

							LogIndex: 15,
						},
					)

				if err == nil {
					t.Fatal(
						"expected invalid-range error",
					)
				}
			},
		)
	}
}
