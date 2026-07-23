package uniswapv3

import (
	"fmt"
	"math/big"
)

// mulDivFloor calculates:
//
//	floor(a * b / denominator)
//
// with full intermediate precision.
//
// Solidity's FullMath library uses a 512-bit intermediate product to avoid
// losing precision when a*b exceeds uint256. Go's big.Int preserves the full
// product naturally, while the explicit validation below keeps inputs and the
// final result inside the uint256 domain used by Uniswap v3.
func mulDivFloor(
	a *big.Int,
	b *big.Int,
	denominator *big.Int,
) (*big.Int, error) {
	if err := validateUint256(
		"a",
		a,
	); err != nil {
		return nil, err
	}

	if err := validateUint256(
		"b",
		b,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint256(
		"denominator",
		denominator,
	); err != nil {
		return nil, err
	}

	product := new(big.Int).Mul(
		a,
		b,
	)

	result := new(big.Int).Quo(
		product,
		denominator,
	)

	if result.Cmp(maxUint256) > 0 {
		return nil, fmt.Errorf(
			"mulDiv result exceeds uint256: %s",
			result,
		)
	}

	return result, nil
}

// mulDivRoundingUp calculates:
//
//	ceil(a * b / denominator)
//
// with full intermediate precision.
func mulDivRoundingUp(
	a *big.Int,
	b *big.Int,
	denominator *big.Int,
) (*big.Int, error) {
	if err := validateUint256(
		"a",
		a,
	); err != nil {
		return nil, err
	}

	if err := validateUint256(
		"b",
		b,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint256(
		"denominator",
		denominator,
	); err != nil {
		return nil, err
	}

	product := new(big.Int).Mul(
		a,
		b,
	)

	quotient, remainder := new(big.Int).QuoRem(
		product,
		denominator,
		new(big.Int),
	)

	if quotient.Cmp(maxUint256) > 0 {
		return nil, fmt.Errorf(
			"mulDiv result exceeds uint256: %s",
			quotient,
		)
	}

	if remainder.Sign() == 0 {
		return quotient, nil
	}

	if quotient.Cmp(maxUint256) == 0 {
		return nil, fmt.Errorf(
			"rounded mulDiv result exceeds uint256",
		)
	}

	quotient.Add(
		quotient,
		big.NewInt(1),
	)

	return quotient, nil
}

// divRoundingUp calculates:
//
//	ceil(numerator / denominator)
//
// inside the uint256 domain.
func divRoundingUp(
	numerator *big.Int,
	denominator *big.Int,
) (*big.Int, error) {
	if err := validateUint256(
		"numerator",
		numerator,
	); err != nil {
		return nil, err
	}

	if err := validatePositiveUint256(
		"denominator",
		denominator,
	); err != nil {
		return nil, err
	}

	quotient, remainder := new(big.Int).QuoRem(
		numerator,
		denominator,
		new(big.Int),
	)

	if remainder.Sign() != 0 {
		quotient.Add(
			quotient,
			big.NewInt(1),
		)
	}

	return quotient, nil
}

func validateUint256(
	fieldName string,
	value *big.Int,
) error {
	if value == nil {
		return fmt.Errorf(
			"%s is nil",
			fieldName,
		)
	}

	if value.Sign() < 0 {
		return fmt.Errorf(
			"%s must not be negative: %s",
			fieldName,
			value,
		)
	}

	if value.Cmp(maxUint256) > 0 {
		return fmt.Errorf(
			"%s exceeds uint256: %s",
			fieldName,
			value,
		)
	}

	return nil
}

func validatePositiveUint256(
	fieldName string,
	value *big.Int,
) error {
	if err := validateUint256(
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
