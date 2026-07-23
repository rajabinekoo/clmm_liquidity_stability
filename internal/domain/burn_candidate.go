package domain

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// BurnCandidate represents one actual Uniswap v3 Pool Burn event.
//
// The event is identified by its pool and Ethereum event cursor. The subgraph
// owner field is intentionally excluded because it may represent the
// NonfungiblePositionManager contract rather than the economic owner or a
// uniquely identifiable NFT position.
type BurnCandidate struct {
	ID string

	PoolAddress string
	TxHash      string

	Cursor EventCursor

	Timestamp time.Time

	TickLower int
	TickUpper int

	// LiquidityRemoved is the positive absolute liquidity amount removed by
	// this exact Burn event.
	LiquidityRemoved *big.Int
}

func (b BurnCandidate) Validate() error {
	if strings.TrimSpace(b.ID) == "" {
		return fmt.Errorf(
			"burn candidate id is required",
		)
	}

	if strings.TrimSpace(b.PoolAddress) == "" {
		return fmt.Errorf(
			"burn candidate pool address is required",
		)
	}

	if strings.TrimSpace(b.TxHash) == "" {
		return fmt.Errorf(
			"burn candidate transaction hash is required",
		)
	}

	if err := b.Cursor.Validate(); err != nil {
		return fmt.Errorf(
			"burn candidate cursor: %w",
			err,
		)
	}

	if b.Timestamp.IsZero() {
		return fmt.Errorf(
			"burn candidate timestamp is required",
		)
	}

	if b.TickLower < minUniswapV3Tick ||
		b.TickLower > maxUniswapV3Tick {
		return fmt.Errorf(
			"burn candidate lower tick %d is outside [%d,%d]",
			b.TickLower,
			minUniswapV3Tick,
			maxUniswapV3Tick,
		)
	}

	if b.TickUpper < minUniswapV3Tick ||
		b.TickUpper > maxUniswapV3Tick {
		return fmt.Errorf(
			"burn candidate upper tick %d is outside [%d,%d]",
			b.TickUpper,
			minUniswapV3Tick,
			maxUniswapV3Tick,
		)
	}

	if b.TickLower >= b.TickUpper {
		return fmt.Errorf(
			"burn candidate lower tick %d must be below upper tick %d",
			b.TickLower,
			b.TickUpper,
		)
	}

	if b.LiquidityRemoved == nil {
		return fmt.Errorf(
			"burn candidate removed liquidity is nil",
		)
	}

	if b.LiquidityRemoved.Sign() <= 0 {
		return fmt.Errorf(
			"burn candidate removed liquidity must be positive: %s",
			b.LiquidityRemoved,
		)
	}

	// Uniswap v3 liquidity amounts use uint128.
	if b.LiquidityRemoved.BitLen() > 128 {
		return fmt.Errorf(
			"burn candidate removed liquidity exceeds uint128: %s",
			b.LiquidityRemoved,
		)
	}

	return nil
}

// EventKey uniquely identifies this event inside one pool.
func (b BurnCandidate) EventKey() string {
	return fmt.Sprintf(
		"%s:%d:%d",
		strings.ToLower(
			strings.TrimSpace(
				b.PoolAddress,
			),
		),
		b.Cursor.BlockNumber,
		b.Cursor.LogIndex,
	)
}

// RangeKey identifies the Uniswap v3 core tick range.
//
// It does not claim that the range belongs to one independently identifiable
// NFT position.
func (b BurnCandidate) RangeKey() string {
	return fmt.Sprintf(
		"%d:%d",
		b.TickLower,
		b.TickUpper,
	)
}

func (b BurnCandidate) LiquidityRemovedCopy() *big.Int {
	if b.LiquidityRemoved == nil {
		return nil
	}

	return new(big.Int).Set(
		b.LiquidityRemoved,
	)
}
