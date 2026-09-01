package services

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// applyObservedSwapState advances an already reconstructed pool with the
// authoritative post-state emitted by an on-chain Swap event.
//
// Historical state reconstruction must not require token-amount parity from a
// second simulator run: indexed token amounts can sit on protocol rounding
// boundaries, while sqrtPriceX96 and tick are the actual post-swap pool state.
// Active liquidity is updated deterministically from the initialized ticks
// crossed between the previous and observed ticks.
func applyObservedSwapState(
	current *domain.ReconstructedPool,
	swap domain.SwapEvent,
) (*domain.ReconstructedPool, error) {
	if current == nil {
		return nil, fmt.Errorf("apply observed swap state: current pool is nil")
	}
	if err := swap.ValidateForObservation(); err != nil {
		return nil, fmt.Errorf("apply observed swap state: invalid swap: %w", err)
	}
	if current.SqrtPriceX96 == nil || current.SqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf("apply observed swap state: current sqrt price is invalid")
	}
	if current.Liquidity == nil || current.Liquidity.Sign() < 0 {
		return nil, fmt.Errorf("apply observed swap state: current liquidity is invalid")
	}

	nextLiquidity := new(big.Int).Set(current.Liquidity)

	switch {
	case swap.TickAfter < current.CurrentTick:
		// Active liquidity contains liquidityNet for every initialized tick <=
		// currentTick. Moving to a lower observed tick removes each boundary
		// in (tickAfter,currentTick]. The observed post-state is authoritative;
		// token-delta signs are intentionally not used to infer this transition.
		start := sort.SearchInts(current.InitializedTicks, swap.TickAfter+1)
		end := sort.SearchInts(current.InitializedTicks, current.CurrentTick+1)

		for index := start; index < end; index++ {
			tickIndex := current.InitializedTicks[index]
			tickState := current.Ticks[tickIndex]
			if tickState == nil || tickState.LiquidityNet == nil {
				return nil, fmt.Errorf(
					"apply observed swap state: initialized tick %d has invalid liquidity state",
					tickIndex,
				)
			}
			nextLiquidity.Sub(nextLiquidity, tickState.LiquidityNet)
		}

	case swap.TickAfter > current.CurrentTick:
		// Moving to a higher observed tick adds each boundary in
		// (currentTick,tickAfter]. This remains correct even when the local
		// timeline had to resynchronize after a missing or non-replayable
		// intermediate Swap, because active liquidity is path-independent for
		// a fixed tick map.
		start := sort.SearchInts(current.InitializedTicks, current.CurrentTick+1)
		end := sort.SearchInts(current.InitializedTicks, swap.TickAfter+1)

		for index := start; index < end; index++ {
			tickIndex := current.InitializedTicks[index]
			tickState := current.Ticks[tickIndex]
			if tickState == nil || tickState.LiquidityNet == nil {
				return nil, fmt.Errorf(
					"apply observed swap state: initialized tick %d has invalid liquidity state",
					tickIndex,
				)
			}
			nextLiquidity.Add(nextLiquidity, tickState.LiquidityNet)
		}
	}

	if nextLiquidity.Sign() < 0 || nextLiquidity.BitLen() > 128 {
		return nil, fmt.Errorf(
			"apply observed swap state: active liquidity after observed crossing is outside uint128: %s",
			nextLiquidity,
		)
	}

	next := cloneBurnRuntimePool(current)
	next.BlockNumber = swap.BlockNumber
	next.SqrtPriceX96 = new(big.Int).Set(swap.SqrtPriceX96After)
	next.CurrentTick = swap.TickAfter
	next.Liquidity = nextLiquidity

	return next, nil
}
