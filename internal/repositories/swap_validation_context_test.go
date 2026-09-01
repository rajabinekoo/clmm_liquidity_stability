package repositories

import (
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestSwapValidationContextIsCleanStart(
	t *testing.T,
) {
	t.Parallel()

	cursor := domain.EventCursor{
		BlockNumber: 100,
		LogIndex:    20,
	}

	testCases := []struct {
		name     string
		context  SwapValidationContext
		expected bool
	}{
		{
			name: "clean",

			context: SwapValidationContext{
				Cursor: cursor,

				IndexedThrough: 100,

				PriorLiquidityActionCount: 0,
			},

			expected: true,
		},
		{
			name: "checkpoint after swap is clean",

			context: SwapValidationContext{
				Cursor: cursor,

				IndexedThrough: 120,

				PriorLiquidityActionCount: 0,
			},

			expected: true,
		},
		{
			name: "checkpoint before swap",

			context: SwapValidationContext{
				Cursor: cursor,

				IndexedThrough: 99,

				PriorLiquidityActionCount: 0,
			},

			expected: false,
		},
		{
			name: "prior liquidity action",

			context: SwapValidationContext{
				Cursor: cursor,

				IndexedThrough: 100,

				PriorLiquidityActionCount: 1,
			},

			expected: false,
		},
		{
			name: "checkpoint incomplete and prior action",

			context: SwapValidationContext{
				Cursor: cursor,

				IndexedThrough: 99,

				PriorLiquidityActionCount: 2,
			},

			expected: false,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				actual :=
					testCase.context.IsCleanStart()

				if actual != testCase.expected {
					t.Fatalf(
						"IsCleanStart() = %t, want %t",
						actual,
						testCase.expected,
					)
				}
			},
		)
	}
}

func TestSwapValidationContextStateMethods(
	t *testing.T,
) {
	t.Parallel()

	context := SwapValidationContext{
		Cursor: domain.EventCursor{
			BlockNumber: 200,
			LogIndex:    15,
		},

		IndexedThrough: 199,

		PriorLiquidityActionCount: 2,
	}

	if context.IsIndexedThroughSwap() {
		t.Fatal(
			"IsIndexedThroughSwap() = true, want false",
		)
	}

	if !context.HasPriorLiquidityAction() {
		t.Fatal(
			"HasPriorLiquidityAction() = false, want true",
		)
	}
}

func TestSwapValidationContextValidate(
	t *testing.T,
) {
	t.Parallel()

	valid := SwapValidationContext{
		Cursor: domain.EventCursor{
			BlockNumber: 100,
			LogIndex:    0,
		},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf(
			"Validate() error = %v",
			err,
		)
	}

	invalid := SwapValidationContext{
		Cursor: domain.EventCursor{
			BlockNumber: 0,
			LogIndex:    0,
		},
	}

	if err := invalid.Validate(); err == nil {
		t.Fatal(
			"Validate() expected cursor error",
		)
	}
}
