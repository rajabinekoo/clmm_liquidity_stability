package services

import (
	"context"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
)

const localAnalysisTestPool = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeLocalAnalysisRepository struct {
	bootstrap      repositories.LocalAnalysisBootstrap
	bootstrapCalls int
	swapPageCalls  int
}

func (f *fakeLocalAnalysisRepository) LoadLocalAnalysisBootstrap(
	_ context.Context,
	_ string,
	_ uint64,
) (repositories.LocalAnalysisBootstrap, error) {
	f.bootstrapCalls++
	return f.bootstrap, nil
}

func (f *fakeLocalAnalysisRepository) FetchSwapsPage(
	_ context.Context,
	_ string,
	_ uint64,
	_ uint64,
	_ string,
	_ int,
	_ int,
	_ int,
) ([]domain.SwapEvent, error) {
	f.swapPageCalls++
	return nil, nil
}

func TestLocalAnalysisProviderBuildsAndCachesLocalStates(t *testing.T) {
	t.Parallel()

	sqrtPrice := new(big.Int).Lsh(big.NewInt(1), 96)
	currentTick := 0

	repository := &fakeLocalAnalysisRepository{
		bootstrap: repositories.LocalAnalysisBootstrap{
			Metadata: domain.Pool{
				Address:      localAnalysisTestPool,
				FeeTier:      500,
				CreatedBlock: 1,
			},
			IndexedThrough: 101,
			AnchorInput: domain.ReconstructionInput{
				Snapshot: domain.ReconstructionSnapshot{
					PoolAddress:  localAnalysisTestPool,
					BlockNumber:  100,
					SqrtPriceX96: new(big.Int).Set(sqrtPrice),
					CurrentTick:  &currentTick,
					Liquidity:    big.NewInt(100),
				},
				Changes: []domain.LiquidityChange{{
					ID:             "anchor-mint",
					BlockNumber:    90,
					LogIndex:       1,
					TickLower:      -10,
					TickUpper:      10,
					LiquidityDelta: big.NewInt(100),
				}},
			},
			Snapshots: []domain.PoolSnapshot{{
				PoolAddress:     localAnalysisTestPool,
				BlockNumber:     101,
				Tick:            "0",
				SqrtPriceX96:    decimal.NewFromBigInt(sqrtPrice, 0),
				ActiveLiquidity: decimal.NewFromInt(150),
			}},
			LiquidityChanges: []domain.LiquidityChange{{
				ID:             "next-mint",
				BlockNumber:    101,
				LogIndex:       2,
				TickLower:      -10,
				TickUpper:      10,
				LiquidityDelta: big.NewInt(50),
			}},
		},
	}

	provider := NewLocalAnalysisProvider(repository, localAnalysisTestPool, 100)
	if err := provider.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	pool100, err := provider.ReconstructedPoolAt(
		context.Background(),
		localAnalysisTestPool,
		100,
	)
	if err != nil {
		t.Fatalf("ReconstructedPoolAt(100) error = %v", err)
	}
	if pool100.Liquidity.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("block 100 liquidity = %s, want 100", pool100.Liquidity)
	}

	pool101, err := provider.ReconstructedPoolAt(
		context.Background(),
		localAnalysisTestPool,
		101,
	)
	if err != nil {
		t.Fatalf("ReconstructedPoolAt(101) error = %v", err)
	}
	if pool101.Liquidity.Cmp(big.NewInt(150)) != 0 {
		t.Fatalf("block 101 liquidity = %s, want 150", pool101.Liquidity)
	}

	// Returned states are deep clones; callers cannot corrupt the cache.
	pool101.Liquidity.SetInt64(1)
	again, err := provider.ReconstructedPoolAt(
		context.Background(),
		localAnalysisTestPool,
		101,
	)
	if err != nil {
		t.Fatalf("ReconstructedPoolAt(101) second call error = %v", err)
	}
	if again.Liquidity.Cmp(big.NewInt(150)) != 0 {
		t.Fatalf("cached liquidity = %s, want 150", again.Liquidity)
	}

	if repository.bootstrapCalls != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", repository.bootstrapCalls)
	}
}

func TestMergeLocalAnalysisEventsAcceptsObservedZeroOutputSwap(t *testing.T) {
	t.Parallel()

	sqrtPrice := new(big.Int).Lsh(big.NewInt(1), 96)

	events, err := mergeLocalAnalysisEvents(
		100,
		101,
		nil,
		[]domain.SwapEvent{{
			ID:                "zero-output",
			TxHash:            "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			PoolAddress:       localAnalysisTestPool,
			BlockNumber:       101,
			LogIndex:          1,
			Timestamp:         1,
			Amount0Raw:        big.NewInt(1),
			Amount1Raw:        big.NewInt(0),
			SqrtPriceX96After: sqrtPrice,
			TickAfter:         0,
		}},
	)
	if err != nil {
		t.Fatalf("mergeLocalAnalysisEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Type != domain.PoolBlockEventSwap {
		t.Fatalf("events = %#v, want one swap", events)
	}
}

func TestLocalAnalysisProviderReturnsInMemoryEventWindow(t *testing.T) {
	t.Parallel()

	sqrtPrice := new(big.Int).Lsh(big.NewInt(1), 96)
	currentTick := 0
	repository := &fakeLocalAnalysisRepository{
		bootstrap: repositories.LocalAnalysisBootstrap{
			Metadata:       domain.Pool{Address: localAnalysisTestPool, FeeTier: 500, CreatedBlock: 1},
			IndexedThrough: 102,
			AnchorInput: domain.ReconstructionInput{
				Snapshot: domain.ReconstructionSnapshot{
					PoolAddress:  localAnalysisTestPool,
					BlockNumber:  100,
					SqrtPriceX96: new(big.Int).Set(sqrtPrice),
					CurrentTick:  &currentTick,
					Liquidity:    big.NewInt(100),
				},
				Changes: []domain.LiquidityChange{{
					ID: "anchor", BlockNumber: 90, LogIndex: 1,
					TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(100),
				}},
			},
			LiquidityChanges: []domain.LiquidityChange{
				{ID: "mint-101", BlockNumber: 101, LogIndex: 1, TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(10)},
				{ID: "burn-102", BlockNumber: 102, LogIndex: 1, TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(-5)},
			},
		},
	}
	provider := NewLocalAnalysisProvider(repository, localAnalysisTestPool, 100)
	events, err := provider.PoolEventsAfterCursorThroughBlock(
		context.Background(),
		localAnalysisTestPool,
		domain.EventCursor{BlockNumber: 100, LogIndex: 0},
		101,
	)
	if err != nil {
		t.Fatalf("PoolEventsAfterCursorThroughBlock() error = %v", err)
	}
	if len(events) != 1 || events[0].Cursor.BlockNumber != 101 {
		t.Fatalf("events = %#v, want only block 101", events)
	}
}
