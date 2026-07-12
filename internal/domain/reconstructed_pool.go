package domain

import "math/big"

type LiquidityChange struct {
	ID             string
	BlockNumber    uint64
	LogIndex       int
	TickLower      int
	TickUpper      int
	LiquidityDelta *big.Int
}

type ReconstructionSnapshot struct {
	PoolAddress string
	BlockNumber uint64

	SqrtPriceX96 *big.Int
	CurrentTick  *int
	Liquidity    *big.Int
}

type ReconstructionInput struct {
	Snapshot ReconstructionSnapshot
	Changes  []LiquidityChange
}

type TickState struct {
	Index int

	LiquidityGross *big.Int
	LiquidityNet   *big.Int
}

type ReconstructedPool struct {
	PoolAddress string
	BlockNumber uint64

	SqrtPriceX96 *big.Int
	CurrentTick  int
	Liquidity    *big.Int

	Ticks            map[int]*TickState
	InitializedTicks []int
}
