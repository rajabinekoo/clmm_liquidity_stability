package uniswapv3

import (
	"fmt"
	"math/big"
)

func QuoteToken1ForToken0Raw(
	sqrtPriceX96 *big.Int,
	amount0 *big.Int,
) (*big.Int, error) {
	if sqrtPriceX96 == nil || sqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf("invalid sqrt price")
	}
	if amount0 == nil || amount0.Sign() <= 0 {
		return nil, fmt.Errorf("amount0 must be positive")
	}

	priceNumerator := new(big.Int).Mul(sqrtPriceX96, sqrtPriceX96)
	q192 := new(big.Int).Lsh(big.NewInt(1), 192)

	amount1 := new(big.Int).Mul(amount0, priceNumerator)
	amount1.Quo(amount1, q192)

	if amount1.Sign() <= 0 {
		return nil, fmt.Errorf("quoted amount1 is zero")
	}

	return amount1, nil
}
