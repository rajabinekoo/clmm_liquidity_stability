package uniswapv3

import (
	"fmt"
	"math/big"
)

var (
	// sqrtPriceMathQ96 is the Q64.96 scaling factor used by Uniswap v3.
	sqrtPriceMathQ96 = new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	maxUint160 = new(big.Int).Sub(
		new(big.Int).Lsh(
			big.NewInt(1),
			160,
		),
		big.NewInt(1),
	)

	maxUint128 = new(big.Int).Sub(
		new(big.Int).Lsh(
			big.NewInt(1),
			128,
		),
		big.NewInt(1),
	)
)

// getNextSqrtPriceFromInput calculates the next square-root price after an
// exact-input swap.
//
// When zeroForOne is true, token0 enters the pool and the price decreases.
// Otherwise token1 enters the pool and the price increases.
//
// The rounding direction follows Uniswap v3:
//
//   - token0 input: round the next price upward;
//   - token1 input: round the next price downward.
//
// This prevents an exact-input swap from sending more output than the input
// amount permits.
func getNextSqrtPriceFromInput(
	sqrtPX96 *big.Int,
	liquidity *big.Int,
	amountIn *big.Int,
	zeroForOne bool,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		"sqrt price",
		sqrtPX96,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return nil, err
	}

	if err := validateUint256(
		"amount in",
		amountIn,
	); err != nil {
		return nil, err
	}

	if zeroForOne {
		return getNextSqrtPriceFromAmount0RoundingUp(
			sqrtPX96,
			liquidity,
			amountIn,
			true,
		)
	}

	return getNextSqrtPriceFromAmount1RoundingDown(
		sqrtPX96,
		liquidity,
		amountIn,
		true,
	)
}

// getNextSqrtPriceFromOutput calculates the next square-root price after an
// exact-output swap.
//
// The rounding direction follows Uniswap v3 and intentionally moves the price
// far enough to deliver the requested output:
//
//   - zero-for-one removes token1;
//   - one-for-zero removes token0.
func getNextSqrtPriceFromOutput(
	sqrtPX96 *big.Int,
	liquidity *big.Int,
	amountOut *big.Int,
	zeroForOne bool,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		"sqrt price",
		sqrtPX96,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return nil, err
	}

	if err := validateUint256(
		"amount out",
		amountOut,
	); err != nil {
		return nil, err
	}

	if zeroForOne {
		return getNextSqrtPriceFromAmount1RoundingDown(
			sqrtPX96,
			liquidity,
			amountOut,
			false,
		)
	}

	return getNextSqrtPriceFromAmount0RoundingUp(
		sqrtPX96,
		liquidity,
		amountOut,
		false,
	)
}

// getNextSqrtPriceFromAmount0RoundingUp calculates the next price after
// adding or removing token0 from the pool's virtual reserves.
//
// The lossless expression is:
//
//	liquidity * sqrtP
//	────────────────────────────────
//	liquidity ± amount * sqrtP
//
// Values are represented in Q64.96, therefore liquidity is shifted by 96
// bits before applying the expression.
//
// The implementation reproduces Uniswap v3's uint256 overflow fallback. This
// matters because using unlimited big.Int precision without reproducing the
// Solidity branch could produce a slightly different rounded result.
func getNextSqrtPriceFromAmount0RoundingUp(
	sqrtPX96 *big.Int,
	liquidity *big.Int,
	amount *big.Int,
	add bool,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		"sqrt price",
		sqrtPX96,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return nil, err
	}

	if err := validateUint256(
		"amount",
		amount,
	); err != nil {
		return nil, err
	}

	if amount.Sign() == 0 {
		return new(big.Int).Set(sqrtPX96), nil
	}

	numerator1 := new(big.Int).Lsh(
		new(big.Int).Set(liquidity),
		96,
	)

	product := new(big.Int).Mul(
		amount,
		sqrtPX96,
	)

	if add {
		// This is the primary Solidity branch:
		//
		// ceil(
		//     numerator1 * sqrtP
		//     ------------------
		//     numerator1 + product
		// )
		//
		// It is used only when both multiplication and addition fit uint256.
		if product.Cmp(maxUint256) <= 0 {
			denominator := new(big.Int).Add(
				numerator1,
				product,
			)

			if denominator.Cmp(maxUint256) <= 0 &&
				denominator.Cmp(numerator1) >= 0 {
				result, err := mulDivRoundingUp(
					numerator1,
					sqrtPX96,
					denominator,
				)
				if err != nil {
					return nil, fmt.Errorf(
						"calculate token0 next sqrt price: %w",
						err,
					)
				}

				return requirePositiveUint160Result(
					"next sqrt price",
					result,
				)
			}
		}

		// Solidity overflow fallback:
		//
		// ceil(
		//     numerator1
		//     ---------------------------
		//     numerator1 / sqrtP + amount
		// )
		quotient := new(big.Int).Quo(
			numerator1,
			sqrtPX96,
		)

		denominator := new(big.Int).Add(
			quotient,
			amount,
		)

		if denominator.Cmp(maxUint256) > 0 {
			return nil, fmt.Errorf(
				"token0 fallback denominator exceeds uint256: %s",
				denominator,
			)
		}

		result, err := divRoundingUp(
			numerator1,
			denominator,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"calculate token0 fallback sqrt price: %w",
				err,
			)
		}

		return requirePositiveUint160Result(
			"next sqrt price",
			result,
		)
	}

	// Removing token0 increases the square-root price. Solidity requires the
	// multiplication to fit uint256 and the denominator to remain positive.
	if product.Cmp(maxUint256) > 0 {
		return nil, fmt.Errorf(
			"token0 product exceeds uint256",
		)
	}

	if numerator1.Cmp(product) <= 0 {
		return nil, fmt.Errorf(
			"token0 removal exceeds virtual reserve",
		)
	}

	denominator := new(big.Int).Sub(
		numerator1,
		product,
	)

	result, err := mulDivRoundingUp(
		numerator1,
		sqrtPX96,
		denominator,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate token0 removal sqrt price: %w",
			err,
		)
	}

	return requirePositiveUint160Result(
		"next sqrt price",
		result,
	)
}

// getNextSqrtPriceFromAmount1RoundingDown calculates the next price after
// adding or removing token1.
//
// The mathematical expression is:
//
//	sqrtP ± amount * Q96 / liquidity
//
// When adding token1 the quotient is rounded down. When removing token1 the
// quotient is rounded up before subtraction.
func getNextSqrtPriceFromAmount1RoundingDown(
	sqrtPX96 *big.Int,
	liquidity *big.Int,
	amount *big.Int,
	add bool,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		"sqrt price",
		sqrtPX96,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return nil, err
	}

	if err := validateUint256(
		"amount",
		amount,
	); err != nil {
		return nil, err
	}

	if amount.Sign() == 0 {
		return new(big.Int).Set(sqrtPX96), nil
	}

	var (
		quotient *big.Int
		err      error
	)

	if add {
		// For amounts that fit uint160, amount << 96 is guaranteed to fit
		// uint256 and matches the optimized Solidity path.
		if amount.Cmp(maxUint160) <= 0 {
			quotient = new(big.Int).Lsh(
				new(big.Int).Set(amount),
				96,
			)

			quotient.Quo(
				quotient,
				liquidity,
			)
		} else {
			quotient, err = mulDivFloor(
				amount,
				sqrtPriceMathQ96,
				liquidity,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"calculate token1 input quotient: %w",
					err,
				)
			}
		}

		result := new(big.Int).Add(
			sqrtPX96,
			quotient,
		)

		return requirePositiveUint160Result(
			"next sqrt price",
			result,
		)
	}

	if amount.Cmp(maxUint160) <= 0 {
		shiftedAmount := new(big.Int).Lsh(
			new(big.Int).Set(amount),
			96,
		)

		quotient, err = divRoundingUp(
			shiftedAmount,
			liquidity,
		)
	} else {
		quotient, err = mulDivRoundingUp(
			amount,
			sqrtPriceMathQ96,
			liquidity,
		)
	}

	if err != nil {
		return nil, fmt.Errorf(
			"calculate token1 output quotient: %w",
			err,
		)
	}

	if sqrtPX96.Cmp(quotient) <= 0 {
		return nil, fmt.Errorf(
			"token1 removal would make sqrt price non-positive",
		)
	}

	result := new(big.Int).Sub(
		sqrtPX96,
		quotient,
	)

	return requirePositiveUint160Result(
		"next sqrt price",
		result,
	)
}

// getAmount0Delta calculates the token0 amount represented by a liquidity
// amount between two square-root prices.
//
// The formula is:
//
//	liquidity * Q96 * (sqrtB - sqrtA)
//	──────────────────────────────────
//	          sqrtB * sqrtA
func getAmount0Delta(
	sqrtRatioAX96 *big.Int,
	sqrtRatioBX96 *big.Int,
	liquidity *big.Int,
	roundUp bool,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		"sqrt ratio A",
		sqrtRatioAX96,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint160(
		"sqrt ratio B",
		sqrtRatioBX96,
	); err != nil {
		return nil, err
	}

	if err := validateUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return nil, err
	}

	lower := new(big.Int).Set(
		sqrtRatioAX96,
	)

	upper := new(big.Int).Set(
		sqrtRatioBX96,
	)

	if lower.Cmp(upper) > 0 {
		lower, upper = upper, lower
	}

	if liquidity.Sign() == 0 ||
		lower.Cmp(upper) == 0 {
		return big.NewInt(0), nil
	}

	numerator1 := new(big.Int).Lsh(
		new(big.Int).Set(liquidity),
		96,
	)

	numerator2 := new(big.Int).Sub(
		upper,
		lower,
	)

	if roundUp {
		partial, err := mulDivRoundingUp(
			numerator1,
			numerator2,
			upper,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"calculate rounded-up token0 partial delta: %w",
				err,
			)
		}

		result, err := divRoundingUp(
			partial,
			lower,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"calculate rounded-up token0 delta: %w",
				err,
			)
		}

		return result, nil
	}

	partial, err := mulDivFloor(
		numerator1,
		numerator2,
		upper,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate rounded-down token0 partial delta: %w",
			err,
		)
	}

	return new(big.Int).Quo(
		partial,
		lower,
	), nil
}

// getAmount1Delta calculates the token1 amount represented by a liquidity
// amount between two square-root prices:
//
//	liquidity * (sqrtB - sqrtA) / Q96
func getAmount1Delta(
	sqrtRatioAX96 *big.Int,
	sqrtRatioBX96 *big.Int,
	liquidity *big.Int,
	roundUp bool,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		"sqrt ratio A",
		sqrtRatioAX96,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint160(
		"sqrt ratio B",
		sqrtRatioBX96,
	); err != nil {
		return nil, err
	}

	if err := validateUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return nil, err
	}

	lower := new(big.Int).Set(
		sqrtRatioAX96,
	)

	upper := new(big.Int).Set(
		sqrtRatioBX96,
	)

	if lower.Cmp(upper) > 0 {
		lower, upper = upper, lower
	}

	if liquidity.Sign() == 0 ||
		lower.Cmp(upper) == 0 {
		return big.NewInt(0), nil
	}

	delta := new(big.Int).Sub(
		upper,
		lower,
	)

	if roundUp {
		result, err := mulDivRoundingUp(
			liquidity,
			delta,
			sqrtPriceMathQ96,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"calculate rounded-up token1 delta: %w",
				err,
			)
		}

		return result, nil
	}

	result, err := mulDivFloor(
		liquidity,
		delta,
		sqrtPriceMathQ96,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate rounded-down token1 delta: %w",
			err,
		)
	}

	return result, nil
}

func validateUint160(
	fieldName string,
	value *big.Int,
) error {
	if err := validateUint256(
		fieldName,
		value,
	); err != nil {
		return err
	}

	if value.Cmp(maxUint160) > 0 {
		return fmt.Errorf(
			"%s exceeds uint160: %s",
			fieldName,
			value,
		)
	}

	return nil
}

func validatePositiveUint160(
	fieldName string,
	value *big.Int,
) error {
	if err := validateUint160(
		fieldName,
		value,
	); err != nil {
		return err
	}

	if value.Sign() == 0 {
		return fmt.Errorf(
			"%s must be greater than zero",
			fieldName,
		)
	}

	return nil
}

func validateUint128(
	fieldName string,
	value *big.Int,
) error {
	if err := validateUint256(
		fieldName,
		value,
	); err != nil {
		return err
	}

	if value.Cmp(maxUint128) > 0 {
		return fmt.Errorf(
			"%s exceeds uint128: %s",
			fieldName,
			value,
		)
	}

	return nil
}

func validatePositiveUint128(
	fieldName string,
	value *big.Int,
) error {
	if err := validateUint128(
		fieldName,
		value,
	); err != nil {
		return err
	}

	if value.Sign() == 0 {
		return fmt.Errorf(
			"%s must be greater than zero",
			fieldName,
		)
	}

	return nil
}

func requirePositiveUint160Result(
	fieldName string,
	value *big.Int,
) (*big.Int, error) {
	if err := validatePositiveUint160(
		fieldName,
		value,
	); err != nil {
		return nil, err
	}

	return value, nil
}
