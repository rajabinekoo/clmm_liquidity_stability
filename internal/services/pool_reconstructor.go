package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
)

type PoolReconstructor struct {
	repository *repositories.PoolStateRepository
}

func NewPoolReconstructor(
	repository *repositories.PoolStateRepository,
) *PoolReconstructor {
	return &PoolReconstructor{
		repository: repository,
	}
}

func (s *PoolReconstructor) ReconstructLatest(
	ctx context.Context,
	poolAddress string,
) (*domain.ReconstructedPool, error) {
	input, err := s.repository.LoadLatestReconstructionInput(
		ctx,
		poolAddress,
	)
	if err != nil {
		return nil, err
	}

	return ReconstructPool(input)
}

func ReconstructPool(
	input domain.ReconstructionInput,
) (*domain.ReconstructedPool, error) {
	snapshot := input.Snapshot

	if snapshot.CurrentTick == nil {
		return nil, fmt.Errorf(
			"reconstruct pool at block %d: pool is not initialized",
			snapshot.BlockNumber,
		)
	}

	if snapshot.SqrtPriceX96 == nil ||
		snapshot.SqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf(
			"reconstruct pool at block %d: invalid sqrt price",
			snapshot.BlockNumber,
		)
	}

	if snapshot.Liquidity == nil ||
		snapshot.Liquidity.Sign() < 0 {
		return nil, fmt.Errorf(
			"reconstruct pool at block %d: invalid liquidity",
			snapshot.BlockNumber,
		)
	}

	ticks := make(map[int]*domain.TickState)

	for _, change := range input.Changes {
		if change.BlockNumber > snapshot.BlockNumber {
			return nil, fmt.Errorf(
				"liquidity change %s belongs to block %d after snapshot block %d",
				change.ID,
				change.BlockNumber,
				snapshot.BlockNumber,
			)
		}

		if err := applyLiquidityChange(
			ticks,
			change,
		); err != nil {
			return nil, fmt.Errorf(
				"apply liquidity change %s at block %d log %d: %w",
				change.ID,
				change.BlockNumber,
				change.LogIndex,
				err,
			)
		}
	}

	initializedTicks := make(
		[]int,
		0,
		len(ticks),
	)

	for index := range ticks {
		initializedTicks = append(
			initializedTicks,
			index,
		)
	}

	sort.Ints(initializedTicks)

	reconstructedLiquidity := big.NewInt(0)

	for _, index := range initializedTicks {
		if index > *snapshot.CurrentTick {
			break
		}

		reconstructedLiquidity.Add(
			reconstructedLiquidity,
			ticks[index].LiquidityNet,
		)
	}

	if reconstructedLiquidity.Cmp(
		snapshot.Liquidity,
	) != 0 {
		return nil, fmt.Errorf(
			"active liquidity mismatch at block %d: reconstructed=%s snapshot=%s",
			snapshot.BlockNumber,
			reconstructedLiquidity.String(),
			snapshot.Liquidity.String(),
		)
	}

	return &domain.ReconstructedPool{
		PoolAddress: snapshot.PoolAddress,
		BlockNumber: snapshot.BlockNumber,

		SqrtPriceX96: new(big.Int).Set(
			snapshot.SqrtPriceX96,
		),
		CurrentTick: *snapshot.CurrentTick,
		Liquidity: new(big.Int).Set(
			snapshot.Liquidity,
		),

		Ticks:            ticks,
		InitializedTicks: initializedTicks,
	}, nil
}

func applyLiquidityChange(
	ticks map[int]*domain.TickState,
	change domain.LiquidityChange,
) error {
	if change.TickLower >= change.TickUpper {
		return fmt.Errorf(
			"invalid tick range [%d, %d]",
			change.TickLower,
			change.TickUpper,
		)
	}

	if change.LiquidityDelta == nil {
		return fmt.Errorf("nil liquidity delta")
	}

	if change.LiquidityDelta.Sign() == 0 {
		return nil
	}

	lower := tickOrZero(
		ticks,
		change.TickLower,
	)

	upper := tickOrZero(
		ticks,
		change.TickUpper,
	)

	lowerGross := new(big.Int).Add(
		lower.LiquidityGross,
		change.LiquidityDelta,
	)

	upperGross := new(big.Int).Add(
		upper.LiquidityGross,
		change.LiquidityDelta,
	)

	if lowerGross.Sign() < 0 || upperGross.Sign() < 0 {
		return fmt.Errorf(
			"liquidity gross became negative: "+
				"range=[%d,%d] delta=%s "+
				"lower_before=%s lower_after=%s "+
				"upper_before=%s upper_after=%s",
			change.TickLower,
			change.TickUpper,
			change.LiquidityDelta.String(),
			lower.LiquidityGross.String(),
			lowerGross.String(),
			upper.LiquidityGross.String(),
			upperGross.String(),
		)
	}

	if lowerGross.Sign() < 0 ||
		upperGross.Sign() < 0 {
		return fmt.Errorf(
			"liquidity gross became negative for range [%d, %d]",
			change.TickLower,
			change.TickUpper,
		)
	}

	lowerNet := new(big.Int).Add(
		lower.LiquidityNet,
		change.LiquidityDelta,
	)

	upperNet := new(big.Int).Sub(
		upper.LiquidityNet,
		change.LiquidityDelta,
	)

	if err := storeTick(
		ticks,
		change.TickLower,
		lowerGross,
		lowerNet,
	); err != nil {
		return err
	}

	if err := storeTick(
		ticks,
		change.TickUpper,
		upperGross,
		upperNet,
	); err != nil {
		return err
	}

	return nil
}

func tickOrZero(
	ticks map[int]*domain.TickState,
	index int,
) *domain.TickState {
	if tick, exists := ticks[index]; exists {
		return tick
	}

	return &domain.TickState{
		Index: index,

		LiquidityGross: big.NewInt(0),
		LiquidityNet:   big.NewInt(0),
	}
}

func storeTick(
	ticks map[int]*domain.TickState,
	index int,
	gross *big.Int,
	net *big.Int,
) error {
	if gross.Sign() == 0 {
		if net.Sign() != 0 {
			return fmt.Errorf(
				"tick %d has zero gross liquidity but non-zero net liquidity %s",
				index,
				net.String(),
			)
		}

		delete(ticks, index)

		return nil
	}

	ticks[index] = &domain.TickState{
		Index: index,

		LiquidityGross: gross,
		LiquidityNet:   net,
	}

	return nil
}
