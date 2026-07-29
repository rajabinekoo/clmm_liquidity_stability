package services

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/utils"
)

type fakeIndexerSwapRange struct {
	fromBlock uint64
	toBlock   uint64
	afterID   string
}

type fakeIndexerProvider struct {
	maximumSuccessfulRange uint64
	swapCalls              []fakeIndexerSwapRange
}

func (f *fakeIndexerProvider) PoolMetadata(
	_ context.Context,
	poolAddress string,
) (domain.Pool, error) {
	return domain.Pool{Address: poolAddress}, nil
}

func (f *fakeIndexerProvider) IndexedHead(
	_ context.Context,
) (domain.IndexedHead, error) {
	return domain.IndexedHead{BlockNumber: 1_000}, nil
}

func (f *fakeIndexerProvider) PoolSnapshotAt(
	_ context.Context,
	poolAddress string,
	blockNumber uint64,
) (domain.PoolSnapshot, error) {
	return domain.PoolSnapshot{
		PoolAddress: poolAddress,
		BlockNumber: blockNumber,
	}, nil
}

func (f *fakeIndexerProvider) FetchMintsPage(
	_ context.Context,
	_ string,
	_ uint64,
	_ uint64,
	_ string,
	_ int,
) ([]domain.LPAction, error) {
	return nil, nil
}

func (f *fakeIndexerProvider) FetchBurnsPage(
	_ context.Context,
	_ string,
	_ uint64,
	_ uint64,
	_ string,
	_ int,
) ([]domain.LPAction, error) {
	return nil, nil
}

func (f *fakeIndexerProvider) FetchSwapsPage(
	_ context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	_ int,
	_ int,
	_ int,
) ([]domain.SwapEvent, error) {
	f.swapCalls = append(f.swapCalls, fakeIndexerSwapRange{
		fromBlock: fromBlock,
		toBlock:   toBlock,
		afterID:   afterID,
	})

	if toBlock-fromBlock+1 > f.maximumSuccessfulRange {
		return nil, fmt.Errorf("synthetic timeout")
	}

	if afterID != "" {
		return nil, nil
	}

	return []domain.SwapEvent{
		indexedSwapTestEvent(
			poolAddress,
			fromBlock,
			1,
			fmt.Sprintf("swap-%012d", fromBlock),
		),
	}, nil
}

func TestFetchAllSwapsAdaptiveSplitsFailingGraphWindow(t *testing.T) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	provider := &fakeIndexerProvider{
		maximumSuccessfulRange: 500,
	}

	service := &Indexer{
		config: utils.Config{
			SwapMinimumWindowSize: 100,
			SwapPageSize:          1_000,
			SwapFetchMaxAttempts:  1,
			SwapRequestTimeout:    time.Second,
			SwapRetryBaseDelay:    time.Millisecond,
		},
		provider: provider,
	}

	swaps, err := service.fetchAllSwapsAdaptive(
		context.Background(),
		domain.Pool{
			Address:        poolAddress,
			Token0Decimals: 6,
			Token1Decimals: 18,
		},
		100,
		1_099,
	)
	if err != nil {
		t.Fatalf("fetchAllSwapsAdaptive() error = %v", err)
	}

	if len(provider.swapCalls) != 3 {
		t.Fatalf("swap call count = %d, want 3", len(provider.swapCalls))
	}

	if len(swaps) != 2 {
		t.Fatalf("swap count = %d, want 2", len(swaps))
	}

	if swaps[0].BlockNumber != 100 || swaps[1].BlockNumber != 600 {
		t.Fatalf(
			"swap blocks = [%d,%d], want [100,600]",
			swaps[0].BlockNumber,
			swaps[1].BlockNumber,
		)
	}
}

func TestNormalizeIndexedSwapWindowCollapsesEquivalentDuplicateCursor(t *testing.T) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	first := indexedSwapTestEvent(poolAddress, 120, 7, "duplicate-b")
	duplicate := first
	duplicate.ID = "duplicate-a"

	got, err := normalizeIndexedSwapWindow(
		poolAddress,
		100,
		200,
		[]domain.SwapEvent{first, duplicate},
	)
	if err != nil {
		t.Fatalf("normalizeIndexedSwapWindow() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("normalized count = %d, want 1", len(got))
	}

	if got[0].ID != "duplicate-a" {
		t.Fatalf("canonical ID = %q, want duplicate-a", got[0].ID)
	}
}

func TestNormalizeIndexedSwapWindowRejectsConflictingCursor(t *testing.T) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	first := indexedSwapTestEvent(poolAddress, 120, 7, "duplicate-a")
	conflicting := first
	conflicting.ID = "duplicate-b"
	conflicting.Amount1Raw = big.NewInt(-201)

	_, err := normalizeIndexedSwapWindow(
		poolAddress,
		100,
		200,
		[]domain.SwapEvent{first, conflicting},
	)
	if err == nil {
		t.Fatal("normalizeIndexedSwapWindow() error = nil, want conflict")
	}

	if !strings.Contains(err.Error(), "conflicting swaps share cursor 120:7") {
		t.Fatalf("error = %q, want cursor conflict detail", err)
	}
}

func indexedSwapTestEvent(
	poolAddress string,
	blockNumber uint64,
	logIndex int,
	id string,
) domain.SwapEvent {
	return domain.SwapEvent{
		ID:                id,
		TxHash:            "0x" + id,
		PoolAddress:       poolAddress,
		BlockNumber:       blockNumber,
		LogIndex:          logIndex,
		Timestamp:         1,
		Amount0Raw:        big.NewInt(100),
		Amount1Raw:        big.NewInt(-200),
		SqrtPriceX96After: big.NewInt(1_000),
		TickAfter:         100,
	}
}
