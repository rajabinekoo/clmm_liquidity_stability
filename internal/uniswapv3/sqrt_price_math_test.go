package uniswapv3

import (
	"math/big"
	"testing"
)

func TestGetNextSqrtPriceFromInputOfficialVectors(
	t *testing.T,
) {
	t.Parallel()

	price := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	liquidity := mustSqrtPriceMathBigInt(
		t,
		"1000000000000000000",
	)

	amountIn := mustSqrtPriceMathBigInt(
		t,
		"100000000000000000",
	)

	token1InputResult, err :=
		getNextSqrtPriceFromInput(
			price,
			liquidity,
			amountIn,
			false,
		)
	if err != nil {
		t.Fatalf(
			"getNextSqrtPriceFromInput(token1) error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		token1InputResult,
		mustSqrtPriceMathBigInt(
			t,
			"87150978765690771352898345369",
		),
	)

	token0InputResult, err :=
		getNextSqrtPriceFromInput(
			price,
			liquidity,
			amountIn,
			true,
		)
	if err != nil {
		t.Fatalf(
			"getNextSqrtPriceFromInput(token0) error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		token0InputResult,
		mustSqrtPriceMathBigInt(
			t,
			"72025602285694852357767227579",
		),
	)
}

func TestGetNextSqrtPriceFromInputReturnsCurrentPriceForZeroAmount(
	t *testing.T,
) {
	t.Parallel()

	price := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	liquidity := mustSqrtPriceMathBigInt(
		t,
		"1000000000000000000",
	)

	for _, zeroForOne := range []bool{
		false,
		true,
	} {
		actual, err :=
			getNextSqrtPriceFromInput(
				price,
				liquidity,
				big.NewInt(0),
				zeroForOne,
			)
		if err != nil {
			t.Fatalf(
				"getNextSqrtPriceFromInput(zeroForOne=%t) error = %v",
				zeroForOne,
				err,
			)
		}

		assertBigIntEqual(
			t,
			actual,
			price,
		)

		if actual == price {
			t.Fatal(
				"result aliases the input price pointer",
			)
		}
	}
}

func TestGetAmount0DeltaOfficialVector(
	t *testing.T,
) {
	t.Parallel()

	lower := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	// sqrt(1.21) = 1.1.
	//
	// The official encodePriceSqrt helper floors the Q64.96 value.
	upper := new(big.Int).Mul(
		lower,
		big.NewInt(11),
	)

	upper.Quo(
		upper,
		big.NewInt(10),
	)

	liquidity := mustSqrtPriceMathBigInt(
		t,
		"1000000000000000000",
	)

	roundedUp, err := getAmount0Delta(
		lower,
		upper,
		liquidity,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getAmount0Delta(roundUp=true) error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		roundedUp,
		mustSqrtPriceMathBigInt(
			t,
			"90909090909090910",
		),
	)

	roundedDown, err := getAmount0Delta(
		lower,
		upper,
		liquidity,
		false,
	)
	if err != nil {
		t.Fatalf(
			"getAmount0Delta(roundUp=false) error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		roundedDown,
		mustSqrtPriceMathBigInt(
			t,
			"90909090909090909",
		),
	)
}

func TestGetAmount1DeltaOfficialVector(
	t *testing.T,
) {
	t.Parallel()

	lower := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	upper := new(big.Int).Mul(
		lower,
		big.NewInt(11),
	)

	upper.Quo(
		upper,
		big.NewInt(10),
	)

	liquidity := mustSqrtPriceMathBigInt(
		t,
		"1000000000000000000",
	)

	roundedUp, err := getAmount1Delta(
		lower,
		upper,
		liquidity,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getAmount1Delta(roundUp=true) error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		roundedUp,
		mustSqrtPriceMathBigInt(
			t,
			"100000000000000000",
		),
	)

	roundedDown, err := getAmount1Delta(
		lower,
		upper,
		liquidity,
		false,
	)
	if err != nil {
		t.Fatalf(
			"getAmount1Delta(roundUp=false) error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		roundedDown,
		mustSqrtPriceMathBigInt(
			t,
			"99999999999999999",
		),
	)
}

func TestGetNextSqrtPriceFromInputOverflowFallbackVector(
	t *testing.T,
) {
	t.Parallel()

	sqrtPrice := mustSqrtPriceMathBigInt(
		t,
		"1025574284609383690408304870162715216695788925244",
	)

	liquidity := mustSqrtPriceMathBigInt(
		t,
		"50015962439936049619261659728067971248",
	)

	amountIn := big.NewInt(406)

	nextPrice, err :=
		getNextSqrtPriceFromInput(
			sqrtPrice,
			liquidity,
			amountIn,
			true,
		)
	if err != nil {
		t.Fatalf(
			"getNextSqrtPriceFromInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		nextPrice,
		mustSqrtPriceMathBigInt(
			t,
			"1025574284609383582644711336373707553698163132913",
		),
	)

	amount0Delta, err := getAmount0Delta(
		nextPrice,
		sqrtPrice,
		liquidity,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getAmount0Delta() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		amount0Delta,
		big.NewInt(406),
	)
}

func TestSqrtPriceMathDoesNotMutateInputs(
	t *testing.T,
) {
	t.Parallel()

	price := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	liquidity := mustSqrtPriceMathBigInt(
		t,
		"1000000000000000000",
	)

	amount := mustSqrtPriceMathBigInt(
		t,
		"100000000000000000",
	)

	priceBefore := new(big.Int).Set(price)
	liquidityBefore := new(big.Int).Set(liquidity)
	amountBefore := new(big.Int).Set(amount)

	_, err := getNextSqrtPriceFromInput(
		price,
		liquidity,
		amount,
		true,
	)
	if err != nil {
		t.Fatalf(
			"getNextSqrtPriceFromInput() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		price,
		priceBefore,
	)

	assertBigIntEqual(
		t,
		liquidity,
		liquidityBefore,
	)

	assertBigIntEqual(
		t,
		amount,
		amountBefore,
	)
}

func TestGetNextSqrtPriceFromInputRejectsInvalidArguments(
	t *testing.T,
) {
	t.Parallel()

	validPrice := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	validLiquidity := big.NewInt(1)

	testCases := []struct {
		name      string
		price     *big.Int
		liquidity *big.Int
		amount    *big.Int
	}{
		{
			name:      "nil price",
			price:     nil,
			liquidity: validLiquidity,
			amount:    big.NewInt(1),
		},
		{
			name:      "zero price",
			price:     big.NewInt(0),
			liquidity: validLiquidity,
			amount:    big.NewInt(1),
		},
		{
			name:      "nil liquidity",
			price:     validPrice,
			liquidity: nil,
			amount:    big.NewInt(1),
		},
		{
			name:      "zero liquidity",
			price:     validPrice,
			liquidity: big.NewInt(0),
			amount:    big.NewInt(1),
		},
		{
			name:      "nil amount",
			price:     validPrice,
			liquidity: validLiquidity,
			amount:    nil,
		},
		{
			name:      "negative amount",
			price:     validPrice,
			liquidity: validLiquidity,
			amount:    big.NewInt(-1),
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				_, err := getNextSqrtPriceFromInput(
					testCase.price,
					testCase.liquidity,
					testCase.amount,
					true,
				)

				if err == nil {
					t.Fatal(
						"getNextSqrtPriceFromInput() expected validation error",
					)
				}
			},
		)
	}
}

func mustSqrtPriceMathBigInt(
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
