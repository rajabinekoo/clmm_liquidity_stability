package services

import (
	"fmt"
	"math/big"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// burnEventSampleRuntime contains full in-memory states required by later
// counterfactual analyses. It is intentionally unexported and is never written
// to CSV, so the empirical sample remains compact on disk.
type burnEventSampleRuntime struct {
	PreBurnPool  *domain.ReconstructedPool
	PostBurnPool *domain.ReconstructedPool
}

func attachBurnEventSampleRuntime(
	sample *BurnEventSample,
	preBurnPool *domain.ReconstructedPool,
	postBurnPool *domain.ReconstructedPool,
) error {
	if sample == nil {
		return fmt.Errorf("attach burn sample runtime: sample is nil")
	}
	if preBurnPool == nil || postBurnPool == nil {
		return fmt.Errorf("attach burn sample runtime: pool state is incomplete")
	}
	if normalizeAddress(preBurnPool.PoolAddress) != normalizeAddress(sample.Burn.PoolAddress) ||
		normalizeAddress(postBurnPool.PoolAddress) != normalizeAddress(sample.Burn.PoolAddress) {
		return fmt.Errorf("attach burn sample runtime: pool address mismatch")
	}
	if preBurnPool.CurrentTick != sample.CurrentTick ||
		postBurnPool.CurrentTick != sample.CurrentTick {
		return fmt.Errorf(
			"attach burn sample runtime: tick mismatch pre=%d post=%d sample=%d",
			preBurnPool.CurrentTick,
			postBurnPool.CurrentTick,
			sample.CurrentTick,
		)
	}
	if preBurnPool.SqrtPriceX96 == nil ||
		preBurnPool.SqrtPriceX96.Cmp(sample.SqrtPriceX96BeforeBurn) != 0 ||
		postBurnPool.SqrtPriceX96 == nil ||
		postBurnPool.SqrtPriceX96.Cmp(sample.SqrtPriceX96BeforeBurn) != 0 {
		return fmt.Errorf("attach burn sample runtime: sqrt price mismatch")
	}
	if preBurnPool.Liquidity == nil ||
		preBurnPool.Liquidity.Cmp(sample.ActiveLiquidityBeforeBurn) != 0 ||
		postBurnPool.Liquidity == nil ||
		postBurnPool.Liquidity.Cmp(sample.ActiveLiquidityAfterBurn) != 0 {
		return fmt.Errorf("attach burn sample runtime: active liquidity mismatch")
	}

	sample.runtime = &burnEventSampleRuntime{
		PreBurnPool:  cloneBurnRuntimePool(preBurnPool),
		PostBurnPool: cloneBurnRuntimePool(postBurnPool),
	}

	return nil
}

func burnEventSampleRuntimePools(
	sample BurnEventSample,
) (*domain.ReconstructedPool, *domain.ReconstructedPool, error) {
	if sample.runtime == nil ||
		sample.runtime.PreBurnPool == nil ||
		sample.runtime.PostBurnPool == nil {
		return nil, nil, fmt.Errorf(
			"burn sample %s does not contain runtime pool states",
			sample.Burn.EventKey(),
		)
	}

	return cloneBurnRuntimePool(sample.runtime.PreBurnPool),
		cloneBurnRuntimePool(sample.runtime.PostBurnPool),
		nil
}

func cloneBurnRuntimePool(
	pool *domain.ReconstructedPool,
) *domain.ReconstructedPool {
	if pool == nil {
		return nil
	}

	cloned := &domain.ReconstructedPool{
		PoolAddress: pool.PoolAddress,
		BlockNumber: pool.BlockNumber,
		CurrentTick: pool.CurrentTick,
		Ticks: make(
			map[int]*domain.TickState,
			len(pool.Ticks),
		),
		InitializedTicks: append(
			[]int(nil),
			pool.InitializedTicks...,
		),
	}

	if pool.SqrtPriceX96 != nil {
		cloned.SqrtPriceX96 = new(big.Int).Set(pool.SqrtPriceX96)
	}
	if pool.Liquidity != nil {
		cloned.Liquidity = new(big.Int).Set(pool.Liquidity)
	}

	for index, tick := range pool.Ticks {
		if tick == nil {
			cloned.Ticks[index] = nil
			continue
		}

		clonedTick := &domain.TickState{Index: tick.Index}
		if tick.LiquidityGross != nil {
			clonedTick.LiquidityGross = new(big.Int).Set(tick.LiquidityGross)
		}
		if tick.LiquidityNet != nil {
			clonedTick.LiquidityNet = new(big.Int).Set(tick.LiquidityNet)
		}
		cloned.Ticks[index] = clonedTick
	}

	return cloned
}
