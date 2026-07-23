package providers

import (
	"math/big"
	"testing"
)

func TestTokenDecimalToRawExactConversion(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name     string
		value    string
		decimals int
		expected string
	}{
		{
			name:     "positive six decimals",
			value:    "1.250001",
			decimals: 6,
			expected: "1250001",
		},
		{
			name:     "negative six decimals",
			value:    "-2000.125",
			decimals: 6,
			expected: "-2000125000",
		},
		{
			name:     "integer token amount",
			value:    "42",
			decimals: 18,
			expected: "42000000000000000000",
		},
		{
			name:     "smallest six-decimal unit",
			value:    "0.000001",
			decimals: 6,
			expected: "1",
		},
		{
			name:     "negative smallest unit",
			value:    "-0.000001",
			decimals: 6,
			expected: "-1",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				actual, err :=
					tokenDecimalToRaw(
						testCase.value,
						testCase.decimals,
					)
				if err != nil {
					t.Fatalf(
						"tokenDecimalToRaw() error = %v",
						err,
					)
				}

				expected, ok :=
					new(big.Int).SetString(
						testCase.expected,
						10,
					)
				if !ok {
					t.Fatalf(
						"invalid expected integer %q",
						testCase.expected,
					)
				}

				assertProviderBigIntEqual(
					t,
					actual,
					expected,
				)
			},
		)
	}
}

func TestTokenDecimalToRawRejectsExcessPrecision(
	t *testing.T,
) {
	t.Parallel()

	_, err := tokenDecimalToRaw(
		"1.0000001",
		6,
	)
	if err == nil {
		t.Fatal(
			"tokenDecimalToRaw() expected excess-precision error",
		)
	}
}

func TestTokenDecimalToRawRejectsInvalidInput(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name     string
		value    string
		decimals int
	}{
		{
			name:     "empty value",
			value:    "",
			decimals: 6,
		},
		{
			name:     "invalid decimal",
			value:    "not-a-number",
			decimals: 6,
		},
		{
			name:     "negative decimals",
			value:    "1",
			decimals: -1,
		},
		{
			name:     "decimals over uint8 domain",
			value:    "1",
			decimals: 256,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				if _, err := tokenDecimalToRaw(
					testCase.value,
					testCase.decimals,
				); err == nil {
					t.Fatal(
						"tokenDecimalToRaw() expected validation error",
					)
				}
			},
		)
	}
}

func TestMapRawSwapZeroForOne(
	t *testing.T,
) {
	t.Parallel()

	swap, err := mapRawSwap(
		" 0xABCDEFabcdefABCDEFabcdefABCDEFabcdefABCD ",
		rawSwap{
			ID: "swap-1",

			Amount0: "1.250001",

			Amount1: "-0.5",

			SqrtPriceX96: "79228162514264337593543950336",

			Tick: "0",

			LogIndex: "15",

			Timestamp: "1700000000",

			Transaction: rawTransaction{
				ID: "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",

				BlockNumber: graphNumber("12345678"),
			},
		},
		6,
		18,
	)
	if err != nil {
		t.Fatalf(
			"mapRawSwap() error = %v",
			err,
		)
	}

	if err := swap.Validate(); err != nil {
		t.Fatalf(
			"mapped swap Validate() error = %v",
			err,
		)
	}

	if !swap.IsZeroForOne() {
		t.Fatal(
			"mapped swap IsZeroForOne() = false",
		)
	}

	if swap.PoolAddress !=
		"0xabcdefabcdefabcdefabcdefabcdefabcdefabcd" {
		t.Fatalf(
			"PoolAddress = %q",
			swap.PoolAddress,
		)
	}

	if swap.TxHash !=
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf(
			"TxHash = %q",
			swap.TxHash,
		)
	}

	if swap.BlockNumber != 12_345_678 {
		t.Fatalf(
			"BlockNumber = %d, want 12345678",
			swap.BlockNumber,
		)
	}

	if swap.LogIndex != 15 {
		t.Fatalf(
			"LogIndex = %d, want 15",
			swap.LogIndex,
		)
	}

	assertProviderBigIntEqual(
		t,
		swap.Amount0Raw,
		big.NewInt(1_250_001),
	)

	expectedAmount1, ok :=
		new(big.Int).SetString(
			"-500000000000000000",
			10,
		)
	if !ok {
		t.Fatal(
			"parse expected amount1",
		)
	}

	assertProviderBigIntEqual(
		t,
		swap.Amount1Raw,
		expectedAmount1,
	)
}

func TestMapRawSwapRejectsInvalidDirection(
	t *testing.T,
) {
	t.Parallel()

	_, err := mapRawSwap(
		"0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		rawSwap{
			ID: "swap-1",

			Amount0: "1",

			Amount1: "2",

			SqrtPriceX96: "79228162514264337593543950336",

			Tick: "0",

			LogIndex: "1",

			Timestamp: "1700000000",

			Transaction: rawTransaction{
				ID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

				BlockNumber: graphNumber("123"),
			},
		},
		6,
		18,
	)
	if err == nil {
		t.Fatal(
			"mapRawSwap() expected invalid-direction error",
		)
	}
}

func TestMapRawSwapRejectsFractionalRawUnit(
	t *testing.T,
) {
	t.Parallel()

	_, err := mapRawSwap(
		"0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		rawSwap{
			ID: "swap-1",

			Amount0: "1.0000001",

			Amount1: "-1",

			SqrtPriceX96: "79228162514264337593543950336",

			Tick: "0",

			LogIndex: "1",

			Timestamp: "1700000000",

			Transaction: rawTransaction{
				ID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

				BlockNumber: graphNumber("123"),
			},
		},
		6,
		18,
	)
	if err == nil {
		t.Fatal(
			"mapRawSwap() expected fractional-raw-unit error",
		)
	}
}

func assertProviderBigIntEqual(
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
