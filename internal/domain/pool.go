package domain

import (
	"fmt"

	"github.com/shopspring/decimal"
)

type IndexedHead struct {
	BlockNumber uint64
	BlockHash   string
}

type Pool struct {
	Address string

	Token0Address  string
	Token1Address  string
	Token0Decimals int
	Token1Decimals int

	FeeTier     int
	TickSpacing int

	Tick            decimal.Decimal
	SqrtPriceX96    decimal.Decimal
	ActiveLiquidity decimal.Decimal

	CreatedBlock uint64
}

type PoolSnapshot struct {
	PoolAddress string
	BlockNumber uint64
	Tick        string

	SqrtPriceX96    decimal.Decimal
	ActiveLiquidity decimal.Decimal

	FeesUSD                string
	LiquidityProviderCount string
}

func TickSpacingForFeeTier(feeTier int) (int, error) {
	switch feeTier {
	case 100:
		return 1, nil
	case 500:
		return 10, nil
	case 3000:
		return 60, nil
	case 10000:
		return 200, nil
	default:
		return 0, fmt.Errorf(
			"unsupported Uniswap V3 fee tier: %d",
			feeTier,
		)
	}
}
