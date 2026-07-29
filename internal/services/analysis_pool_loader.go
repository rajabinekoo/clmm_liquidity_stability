package services

import (
	"context"
	"fmt"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type directReconstructedPoolProvider interface {
	ReconstructedPoolAt(
		ctx context.Context,
		poolAddress string,
		blockNumber uint64,
	) (*domain.ReconstructedPool, error)
}

type historicalPoolSnapshotProvider interface {
	PoolSnapshotAt(
		ctx context.Context,
		poolAddress string,
		blockNumber uint64,
	) (domain.PoolSnapshot, error)
}

type historicalPoolReconstructionRepository interface {
	LoadReconstructionInputFromSnapshot(
		ctx context.Context,
		snapshot domain.PoolSnapshot,
	) (domain.ReconstructionInput, error)
}

// loadHistoricalPoolAt uses the offline provider's direct reconstructed-state
// fast path when available. Test fakes and legacy providers retain the snapshot
// fallback, keeping service contracts backward compatible.
func loadHistoricalPoolAt(
	ctx context.Context,
	provider historicalPoolSnapshotProvider,
	repository historicalPoolReconstructionRepository,
	poolAddress string,
	blockNumber uint64,
) (*domain.ReconstructedPool, error) {
	if direct, ok := provider.(directReconstructedPoolProvider); ok {
		pool, err := direct.ReconstructedPoolAt(
			ctx,
			poolAddress,
			blockNumber,
		)
		if err != nil {
			return nil, err
		}
		if pool == nil {
			return nil, fmt.Errorf(
				"load historical pool at block %d: direct provider returned nil pool",
				blockNumber,
			)
		}
		return pool, nil
	}

	snapshot, err := provider.PoolSnapshotAt(
		ctx,
		poolAddress,
		blockNumber,
	)
	if err != nil {
		return nil, err
	}

	input, err := repository.LoadReconstructionInputFromSnapshot(
		ctx,
		snapshot,
	)
	if err != nil {
		return nil, err
	}

	return ReconstructPool(input)
}
