package uniswapv3

import (
	"fmt"
	"math/big"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// ApplyLiquidityChange applies one exact Mint or Burn liquidity delta to a
// reconstructed pool.
//
// Positive delta represents Mint.
// Negative delta represents Burn.
//
// The input pool is never mutated.
func ApplyLiquidityChange(
	pool *domain.ReconstructedPool,
	change domain.LiquidityChange,
) (*domain.ReconstructedPool, error) {
	if pool == nil {
		return nil, fmt.Errorf(
			"apply liquidity change: pool is nil",
		)
	}

	if pool.SqrtPriceX96 == nil ||
		pool.SqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf(
			"apply liquidity change: invalid pool sqrt price",
		)
	}

	if pool.Liquidity == nil ||
		pool.Liquidity.Sign() < 0 {
		return nil, fmt.Errorf(
			"apply liquidity change: invalid active liquidity",
		)
	}

	if change.TickLower >=
		change.TickUpper {
		return nil, fmt.Errorf(
			"apply liquidity change: invalid range [%d,%d]",
			change.TickLower,
			change.TickUpper,
		)
	}

	if change.LiquidityDelta == nil {
		return nil, fmt.Errorf(
			"apply liquidity change: liquidity delta is nil",
		)
	}

	if change.LiquidityDelta.Sign() == 0 {
		return nil, fmt.Errorf(
			"apply liquidity change: liquidity delta is zero",
		)
	}

	absoluteDelta :=
		new(big.Int).Abs(
			new(big.Int).Set(
				change.LiquidityDelta,
			),
		)

	if err := validateUint128(
		"liquidity delta",
		absoluteDelta,
	); err != nil {
		return nil, fmt.Errorf(
			"apply liquidity change: %w",
			err,
		)
	}

	cloned :=
		clonePool(
			pool,
		)

	if err :=
		applyBoundaryLiquidityDelta(
			cloned,
			change.TickLower,
			change.LiquidityDelta,
			true,
		); err != nil {
		return nil, fmt.Errorf(
			"apply liquidity change lower tick %d: %w",
			change.TickLower,
			err,
		)
	}

	if err :=
		applyBoundaryLiquidityDelta(
			cloned,
			change.TickUpper,
			change.LiquidityDelta,
			false,
		); err != nil {
		return nil, fmt.Errorf(
			"apply liquidity change upper tick %d: %w",
			change.TickUpper,
			err,
		)
	}

	if change.TickLower <=
		cloned.CurrentTick &&
		cloned.CurrentTick <
			change.TickUpper {
		nextActiveLiquidity :=
			new(big.Int).Add(
				cloned.Liquidity,
				change.LiquidityDelta,
			)

		if nextActiveLiquidity.Sign() < 0 {
			return nil, fmt.Errorf(
				"apply liquidity change: active liquidity became negative: before=%s delta=%s after=%s",
				cloned.Liquidity,
				change.LiquidityDelta,
				nextActiveLiquidity,
			)
		}

		if err := validateUint128(
			"active liquidity after change",
			nextActiveLiquidity,
		); err != nil {
			return nil, fmt.Errorf(
				"apply liquidity change: %w",
				err,
			)
		}

		cloned.Liquidity =
			nextActiveLiquidity
	}

	cloned.InitializedTicks =
		rebuildInitializedTicks(
			cloned.Ticks,
		)

	return cloned, nil
}

func applyBoundaryLiquidityDelta(
	pool *domain.ReconstructedPool,
	tickIndex int,
	delta *big.Int,
	isLower bool,
) error {
	current :=
		&domain.TickState{
			Index: tickIndex,

			LiquidityGross: big.NewInt(0),

			LiquidityNet: big.NewInt(0),
		}

	if existing, exists :=
		pool.Ticks[tickIndex]; exists {
		if existing == nil ||
			existing.LiquidityGross == nil ||
			existing.LiquidityNet == nil {
			return fmt.Errorf(
				"tick state is incomplete",
			)
		}

		current =
			&domain.TickState{
				Index: tickIndex,

				LiquidityGross: new(big.Int).Set(
					existing.LiquidityGross,
				),

				LiquidityNet: new(big.Int).Set(
					existing.LiquidityNet,
				),
			}
	}

	nextGross :=
		new(big.Int).Add(
			current.LiquidityGross,
			delta,
		)

	if nextGross.Sign() < 0 {
		return fmt.Errorf(
			"gross liquidity became negative: before=%s delta=%s after=%s",
			current.LiquidityGross,
			delta,
			nextGross,
		)
	}

	if err := validateUint128(
		"tick gross liquidity",
		nextGross,
	); err != nil {
		return err
	}

	nextNet :=
		new(big.Int).Set(
			current.LiquidityNet,
		)

	if isLower {
		nextNet.Add(
			nextNet,
			delta,
		)
	} else {
		nextNet.Sub(
			nextNet,
			delta,
		)
	}

	if err := validateSignedInt128(
		nextNet,
	); err != nil {
		return err
	}

	if nextGross.Sign() == 0 {
		if nextNet.Sign() != 0 {
			return fmt.Errorf(
				"zero gross liquidity with non-zero net liquidity %s",
				nextNet,
			)
		}

		delete(
			pool.Ticks,
			tickIndex,
		)

		return nil
	}

	pool.Ticks[tickIndex] =
		&domain.TickState{
			Index: tickIndex,

			LiquidityGross: nextGross,

			LiquidityNet: nextNet,
		}

	return nil
}

func validateSignedInt128(
	value *big.Int,
) error {
	if value == nil {
		return fmt.Errorf(
			"int128 value is nil",
		)
	}

	minimum :=
		new(big.Int).Neg(
			new(big.Int).Lsh(
				big.NewInt(1),
				127,
			),
		)

	maximum :=
		new(big.Int).Sub(
			new(big.Int).Lsh(
				big.NewInt(1),
				127,
			),
			big.NewInt(1),
		)

	if value.Cmp(minimum) < 0 ||
		value.Cmp(maximum) > 0 {
		return fmt.Errorf(
			"tick net liquidity exceeds int128: %s",
			value,
		)
	}

	return nil
}
