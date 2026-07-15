package uniswapv3

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func RemoveLiquidity(
	pool *domain.ReconstructedPool,
	position domain.LiquidityPosition,
) (*domain.ReconstructedPool, error) {
	if pool == nil {
		return nil, fmt.Errorf("remove liquidity: pool is nil")
	}
	if position.Liquidity == nil || position.Liquidity.Sign() <= 0 {
		return nil, fmt.Errorf("remove liquidity: invalid liquidity")
	}
	if position.TickLower >= position.TickUpper {
		return nil, fmt.Errorf(
			"remove liquidity: invalid range [%d,%d]",
			position.TickLower,
			position.TickUpper,
		)
	}

	cloned := clonePool(pool)

	if err := removeTickLiquidity(
		cloned,
		position.TickLower,
		position.Liquidity,
		true,
	); err != nil {
		return nil, err
	}

	if err := removeTickLiquidity(
		cloned,
		position.TickUpper,
		position.Liquidity,
		false,
	); err != nil {
		return nil, err
	}

	if position.IsActive(cloned.CurrentTick) {
		cloned.Liquidity.Sub(cloned.Liquidity, position.Liquidity)
		if cloned.Liquidity.Sign() < 0 {
			return nil, fmt.Errorf(
				"remove liquidity: active liquidity became negative: %s",
				cloned.Liquidity.String(),
			)
		}
	}

	cloned.InitializedTicks = rebuildInitializedTicks(cloned.Ticks)

	return cloned, nil
}

func removeTickLiquidity(
	pool *domain.ReconstructedPool,
	tick int,
	liquidity *big.Int,
	isLower bool,
) error {
	state, exists := pool.Ticks[tick]
	if !exists {
		return fmt.Errorf("remove liquidity: tick %d not found", tick)
	}

	nextGross := new(big.Int).Sub(state.LiquidityGross, liquidity)
	if nextGross.Sign() < 0 {
		return fmt.Errorf(
			"remove liquidity: tick %d gross became negative: before=%s remove=%s",
			tick,
			state.LiquidityGross.String(),
			liquidity.String(),
		)
	}

	nextNet := new(big.Int).Set(state.LiquidityNet)
	if isLower {
		nextNet.Sub(nextNet, liquidity)
	} else {
		nextNet.Add(nextNet, liquidity)
	}

	if nextGross.Sign() == 0 {
		if nextNet.Sign() != 0 {
			return fmt.Errorf(
				"remove liquidity: tick %d has zero gross but non-zero net %s",
				tick,
				nextNet.String(),
			)
		}

		delete(pool.Ticks, tick)
		return nil
	}

	pool.Ticks[tick] = &domain.TickState{
		Index:          tick,
		LiquidityGross: nextGross,
		LiquidityNet:   nextNet,
	}

	return nil
}

func clonePool(pool *domain.ReconstructedPool) *domain.ReconstructedPool {
	ticks := make(map[int]*domain.TickState, len(pool.Ticks))

	for index, tick := range pool.Ticks {
		ticks[index] = &domain.TickState{
			Index:          tick.Index,
			LiquidityGross: new(big.Int).Set(tick.LiquidityGross),
			LiquidityNet:   new(big.Int).Set(tick.LiquidityNet),
		}
	}

	initializedTicks := make([]int, len(pool.InitializedTicks))
	copy(initializedTicks, pool.InitializedTicks)

	return &domain.ReconstructedPool{
		PoolAddress:      pool.PoolAddress,
		BlockNumber:      pool.BlockNumber,
		SqrtPriceX96:     new(big.Int).Set(pool.SqrtPriceX96),
		CurrentTick:      pool.CurrentTick,
		Liquidity:        new(big.Int).Set(pool.Liquidity),
		Ticks:            ticks,
		InitializedTicks: initializedTicks,
	}
}

func rebuildInitializedTicks(
	ticks map[int]*domain.TickState,
) []int {
	initialized := make([]int, 0, len(ticks))

	for tick := range ticks {
		initialized = append(initialized, tick)
	}

	sort.Ints(initialized)
	return initialized
}
