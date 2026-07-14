package domain

import "math/big"

type LiquidityPosition struct {
	PoolAddress string
	Owner       string
	TickLower   int
	TickUpper   int
	Liquidity   *big.Int
}

func (p LiquidityPosition) IsActive(currentTick int) bool {
	return p.TickLower <= currentTick && currentTick < p.TickUpper
}
