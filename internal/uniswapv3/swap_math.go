package uniswapv3

import (
	"fmt"
	"math/big"
)

const FeeDenominator = int64(1_000_000)

var q96 = new(big.Int).Lsh(big.NewInt(1), 96)

func amountLessFee(amountIn *big.Int, feePips int64) (*big.Int, *big.Int, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, nil, fmt.Errorf("amount_in must be positive")
	}
	if feePips < 0 || feePips >= FeeDenominator {
		return nil, nil, fmt.Errorf("invalid fee pips: %d", feePips)
	}

	multiplier := big.NewInt(FeeDenominator - feePips)
	denominator := big.NewInt(FeeDenominator)

	usable := new(big.Int).Mul(amountIn, multiplier)
	usable.Quo(usable, denominator)

	fee := new(big.Int).Sub(new(big.Int).Set(amountIn), usable)
	return usable, fee, nil
}

func nextSqrtPriceFromAmount0In(
	sqrtPriceX96 *big.Int,
	liquidity *big.Int,
	amountIn *big.Int,
) *big.Int {
	// next = ceil((L * Q96 * sqrtP) / (L * Q96 + amountIn * sqrtP))
	numerator1 := new(big.Int).Lsh(new(big.Int).Set(liquidity), 96)

	product := new(big.Int).Mul(amountIn, sqrtPriceX96)
	denominator := new(big.Int).Add(numerator1, product)

	return mulDivCeil(numerator1, sqrtPriceX96, denominator)
}

func nextSqrtPriceFromAmount1In(
	sqrtPriceX96 *big.Int,
	liquidity *big.Int,
	amountIn *big.Int,
) *big.Int {
	// next = sqrtP + floor(amountIn * Q96 / L)
	quotient := new(big.Int).Lsh(new(big.Int).Set(amountIn), 96)
	quotient.Quo(quotient, liquidity)

	return new(big.Int).Add(sqrtPriceX96, quotient)
}

func amount0DeltaRoundDown(
	sqrtA *big.Int,
	sqrtB *big.Int,
	liquidity *big.Int,
) *big.Int {
	if sqrtA.Cmp(sqrtB) > 0 {
		sqrtA, sqrtB = sqrtB, sqrtA
	}

	delta := new(big.Int).Sub(sqrtB, sqrtA)

	numerator := new(big.Int).Lsh(new(big.Int).Set(liquidity), 96)
	numerator.Mul(numerator, delta)

	denominator := new(big.Int).Mul(sqrtB, sqrtA)

	return numerator.Quo(numerator, denominator)
}

func amount1DeltaRoundDown(
	sqrtA *big.Int,
	sqrtB *big.Int,
	liquidity *big.Int,
) *big.Int {
	if sqrtA.Cmp(sqrtB) > 0 {
		sqrtA, sqrtB = sqrtB, sqrtA
	}

	delta := new(big.Int).Sub(sqrtB, sqrtA)

	amount := new(big.Int).Mul(liquidity, delta)
	amount.Quo(amount, q96)

	return amount
}

func mulDivCeil(a *big.Int, b *big.Int, denominator *big.Int) *big.Int {
	product := new(big.Int).Mul(a, b)

	quotient, remainder := new(big.Int).QuoRem(
		product,
		denominator,
		new(big.Int),
	)

	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	return quotient
}

func amount0DeltaRoundUp(
	sqrtA *big.Int,
	sqrtB *big.Int,
	liquidity *big.Int,
) *big.Int {
	if sqrtA.Cmp(sqrtB) > 0 {
		sqrtA, sqrtB = sqrtB, sqrtA
	}

	delta := new(big.Int).Sub(sqrtB, sqrtA)

	numerator := new(big.Int).Lsh(new(big.Int).Set(liquidity), 96)
	numerator.Mul(numerator, delta)

	denominator := new(big.Int).Mul(sqrtB, sqrtA)

	return ceilDiv(numerator, denominator)
}

func amount1DeltaRoundUp(
	sqrtA *big.Int,
	sqrtB *big.Int,
	liquidity *big.Int,
) *big.Int {
	if sqrtA.Cmp(sqrtB) > 0 {
		sqrtA, sqrtB = sqrtB, sqrtA
	}

	delta := new(big.Int).Sub(sqrtB, sqrtA)

	amount := new(big.Int).Mul(liquidity, delta)
	return ceilDiv(amount, q96)
}

func ceilDiv(
	numerator *big.Int,
	denominator *big.Int,
) *big.Int {
	quotient, remainder := new(big.Int).QuoRem(
		numerator,
		denominator,
		new(big.Int),
	)

	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	return quotient
}
