package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const (
	defaultBurnRealizedFlowSwapPageSize = 1_000

	// The Graph may timeout when a large block interval is queried at once,
	// even when result pagination is enabled.
	//
	// Therefore each Burn-to-Horizon window is split into independent,
	// non-overlapping block chunks.
	defaultBurnRealizedFlowBlockChunkSize uint64 = 500
)

type BurnRealizedFlowProvider interface {
	PoolMetadata(
		ctx context.Context,
		poolAddress string,
	) (domain.Pool, error)

	FetchSwapsPage(
		ctx context.Context,
		poolAddress string,
		fromBlock uint64,
		toBlock uint64,
		afterID string,
		limit int,
		token0Decimals int,
		token1Decimals int,
	) ([]domain.SwapEvent, error)
}

type BurnRealizedFlowRepository interface {
	LoadLiquidityChangesAfterCursorThroughBlock(
		ctx context.Context,
		poolAddress string,
		startCursor domain.EventCursor,
		throughBlock uint64,
	) ([]domain.LiquidityChange, error)
}

type burnRealizedFlowEventSet struct {
	Available bool

	BurnCursor   domain.EventCursor
	ThroughBlock uint64

	LiquidityChanges []domain.LiquidityChange
	Swaps            []domain.SwapEvent
}

func (s *BurnRealizedOutcomeService) loadBurnRealizedFlowEvents(
	ctx context.Context,
	burn domain.BurnCandidate,
	throughBlock uint64,
	require bool,
) (burnRealizedFlowEventSet, error) {
	if throughBlock < burn.Cursor.BlockNumber {
		return burnRealizedFlowEventSet{}, fmt.Errorf(
			"load burn realized flow events: through block %d is before burn block %d",
			throughBlock,
			burn.Cursor.BlockNumber,
		)
	}

	flowProvider, providerAvailable :=
		s.provider.(BurnRealizedFlowProvider)

	flowRepository, repositoryAvailable :=
		s.repository.(BurnRealizedFlowRepository)

	if !providerAvailable ||
		!repositoryAvailable {
		if require {
			return burnRealizedFlowEventSet{}, fmt.Errorf(
				"load burn realized flow events: flow controls are required but provider/repository capabilities are unavailable: provider=%t repository=%t",
				providerAvailable,
				repositoryAvailable,
			)
		}

		return burnRealizedFlowEventSet{
			Available: false,
		}, nil
	}

	metadata, err :=
		flowProvider.PoolMetadata(
			ctx,
			burn.PoolAddress,
		)
	if err != nil {
		return burnRealizedFlowEventSet{}, fmt.Errorf(
			"load burn realized flow events: load pool metadata: %w",
			err,
		)
	}

	if normalizeAddress(
		metadata.Address,
	) != normalizeAddress(
		burn.PoolAddress,
	) {
		return burnRealizedFlowEventSet{}, fmt.Errorf(
			"load burn realized flow events: metadata pool %s does not match burn pool %s",
			metadata.Address,
			burn.PoolAddress,
		)
	}

	liquidityChanges, err :=
		flowRepository.
			LoadLiquidityChangesAfterCursorThroughBlock(
				ctx,
				burn.PoolAddress,
				burn.Cursor,
				throughBlock,
			)
	if err != nil {
		return burnRealizedFlowEventSet{}, fmt.Errorf(
			"load burn realized flow events: load liquidity changes: %w",
			err,
		)
	}

	swaps, err :=
		fetchBurnRealizedFlowSwaps(
			ctx,
			flowProvider,
			metadata,
			burn.Cursor,
			throughBlock,
			defaultBurnRealizedFlowSwapPageSize,
		)
	if err != nil {
		return burnRealizedFlowEventSet{}, fmt.Errorf(
			"load burn realized flow events: load swaps: %w",
			err,
		)
	}

	return burnRealizedFlowEventSet{
		Available: true,

		BurnCursor: burn.Cursor,

		ThroughBlock: throughBlock,

		LiquidityChanges: cloneBurnFlowLiquidityChanges(
			liquidityChanges,
		),

		Swaps: cloneBurnFlowSwaps(
			swaps,
		),
	}, nil
}

func fetchBurnRealizedFlowSwaps(
	ctx context.Context,
	provider BurnRealizedFlowProvider,
	pool domain.Pool,
	burnCursor domain.EventCursor,
	throughBlock uint64,
	pageSize int,
) ([]domain.SwapEvent, error) {
	if provider == nil {
		return nil, fmt.Errorf(
			"fetch burn realized flow swaps: provider is nil",
		)
	}

	if pageSize <= 0 ||
		pageSize > 1_000 {
		return nil, fmt.Errorf(
			"fetch burn realized flow swaps: page size %d must be inside [1,1000]",
			pageSize,
		)
	}

	if err := burnCursor.Validate(); err != nil {
		return nil, fmt.Errorf(
			"fetch burn realized flow swaps: invalid burn cursor: %w",
			err,
		)
	}

	if throughBlock <
		burnCursor.BlockNumber {
		return nil, fmt.Errorf(
			"fetch burn realized flow swaps: through block %d is before burn block %d",
			throughBlock,
			burnCursor.BlockNumber,
		)
	}

	if defaultBurnRealizedFlowBlockChunkSize == 0 {
		return nil, fmt.Errorf(
			"fetch burn realized flow swaps: block chunk size must be positive",
		)
	}

	result :=
		make(
			[]domain.SwapEvent,
			0,
		)

	chunkFromBlock :=
		burnCursor.BlockNumber

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		chunkToBlock :=
			burnRealizedFlowChunkEnd(
				chunkFromBlock,
				throughBlock,
				defaultBurnRealizedFlowBlockChunkSize,
			)

		chunkSwaps, err :=
			fetchBurnRealizedFlowSwapChunk(
				ctx,
				provider,
				pool,
				burnCursor,
				chunkFromBlock,
				chunkToBlock,
				pageSize,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"fetch burn realized flow swaps: chunk [%d,%d]: %w",
				chunkFromBlock,
				chunkToBlock,
				err,
			)
		}

		result =
			append(
				result,
				chunkSwaps...,
			)

		if chunkToBlock >=
			throughBlock {
			break
		}

		chunkFromBlock =
			chunkToBlock + 1
	}

	sort.Slice(
		result,
		func(
			left int,
			right int,
		) bool {
			leftCursor :=
				result[left].Cursor()

			rightCursor :=
				result[right].Cursor()

			if leftCursor.Equal(
				rightCursor,
			) {
				return result[left].ID <
					result[right].ID
			}

			return leftCursor.Before(
				rightCursor,
			)
		},
	)

	normalized, err :=
		normalizeBurnRealizedFlowSwaps(
			result,
			burnCursor,
			throughBlock,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch burn realized flow swaps: %w",
			err,
		)
	}

	return normalized, nil
}

// normalizeBurnRealizedFlowSwaps validates the ordered stream and collapses
// duplicate representations of the same on-chain log.
//
// Ethereum log indexes are block-global, so two Swap records with the same
// block number and log index cannot represent two distinct on-chain events.
// Some Graph indexers can nevertheless expose the same log more than once
// under different entity IDs. Equivalent duplicates are safe to collapse;
// conflicting payloads remain a hard error because silently choosing one would
// corrupt the empirical flow controls.
func normalizeBurnRealizedFlowSwaps(
	values []domain.SwapEvent,
	burnCursor domain.EventCursor,
	throughBlock uint64,
) ([]domain.SwapEvent, error) {
	normalized := make(
		[]domain.SwapEvent,
		0,
		len(values),
	)

	seenByID := make(
		map[string]domain.SwapEvent,
		len(values),
	)

	for index, swap := range values {
		if err := swap.ValidateForObservation(); err != nil {
			return nil, fmt.Errorf(
				"swap index=%d id=%q: %w",
				index,
				swap.ID,
				err,
			)
		}

		cursor := swap.Cursor()

		if !cursor.After(burnCursor) {
			return nil, fmt.Errorf(
				"swap %s cursor %s is not after burn cursor %s",
				swap.ID,
				cursor,
				burnCursor,
			)
		}

		if cursor.BlockNumber > throughBlock {
			return nil, fmt.Errorf(
				"swap %s cursor %s exceeds through block %d",
				swap.ID,
				cursor,
				throughBlock,
			)
		}

		normalizedID := strings.TrimSpace(swap.ID)

		if previous, exists := seenByID[normalizedID]; exists {
			if !burnRealizedFlowSwapsEquivalent(previous, swap) {
				return nil, fmt.Errorf(
					"duplicate swap ID %q has conflicting payloads: first={%s} current={%s}",
					normalizedID,
					burnRealizedFlowSwapFingerprint(previous),
					burnRealizedFlowSwapFingerprint(swap),
				)
			}

			continue
		}

		seenByID[normalizedID] = swap

		if len(normalized) > 0 {
			previous := normalized[len(normalized)-1]
			previousCursor := previous.Cursor()

			switch cursor.Compare(previousCursor) {
			case -1:
				return nil, fmt.Errorf(
					"decreasing cursor at index %d: previous=%s current=%s",
					index,
					previousCursor,
					cursor,
				)

			case 0:
				if !burnRealizedFlowSwapsEquivalent(previous, swap) {
					return nil, fmt.Errorf(
						"conflicting swaps share cursor %s: first={%s} current={%s}",
						cursor,
						burnRealizedFlowSwapFingerprint(previous),
						burnRealizedFlowSwapFingerprint(swap),
					)
				}

				// The Graph occasionally exposes the same Ethereum log under
				// multiple entity IDs. Keep the first deterministic record
				// (the input was sorted by cursor and then ID) and do not
				// double-count its volume or tick movement.
				continue
			}
		}

		normalized = append(
			normalized,
			swap,
		)
	}

	return normalized, nil
}

func burnRealizedFlowSwapsEquivalent(
	left domain.SwapEvent,
	right domain.SwapEvent,
) bool {
	return left.Cursor().Equal(right.Cursor()) &&
		strings.EqualFold(
			strings.TrimSpace(left.TxHash),
			strings.TrimSpace(right.TxHash),
		) &&
		normalizeAddress(left.PoolAddress) ==
			normalizeAddress(right.PoolAddress) &&
		left.Timestamp == right.Timestamp &&
		burnRealizedFlowBigIntsEqual(left.Amount0Raw, right.Amount0Raw) &&
		burnRealizedFlowBigIntsEqual(left.Amount1Raw, right.Amount1Raw) &&
		burnRealizedFlowBigIntsEqual(
			left.SqrtPriceX96After,
			right.SqrtPriceX96After,
		) &&
		left.TickAfter == right.TickAfter
}

func burnRealizedFlowBigIntsEqual(
	left *big.Int,
	right *big.Int,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return left.Cmp(right) == 0
}

func burnRealizedFlowSwapFingerprint(
	swap domain.SwapEvent,
) string {
	return fmt.Sprintf(
		"id=%q tx=%q cursor=%s amount0=%s amount1=%s sqrt=%s tick=%d timestamp=%d",
		swap.ID,
		swap.TxHash,
		swap.Cursor(),
		burnRealizedFlowOptionalBigIntString(swap.Amount0Raw),
		burnRealizedFlowOptionalBigIntString(swap.Amount1Raw),
		burnRealizedFlowOptionalBigIntString(swap.SqrtPriceX96After),
		swap.TickAfter,
		swap.Timestamp,
	)
}

func burnRealizedFlowOptionalBigIntString(
	value *big.Int,
) string {
	if value == nil {
		return "<nil>"
	}

	return value.String()
}

func fetchBurnRealizedFlowSwapChunk(
	ctx context.Context,
	provider BurnRealizedFlowProvider,
	pool domain.Pool,
	burnCursor domain.EventCursor,
	fromBlock uint64,
	toBlock uint64,
	pageSize int,
) ([]domain.SwapEvent, error) {
	if fromBlock == 0 ||
		toBlock == 0 ||
		fromBlock > toBlock {
		return nil, fmt.Errorf(
			"invalid block chunk [%d,%d]",
			fromBlock,
			toBlock,
		)
	}

	result :=
		make(
			[]domain.SwapEvent,
			0,
		)

	// Cursor pagination must restart for every independent block chunk.
	afterID := ""

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		page, err :=
			provider.FetchSwapsPage(
				ctx,
				pool.Address,
				fromBlock,
				toBlock,
				afterID,
				pageSize,
				pool.Token0Decimals,
				pool.Token1Decimals,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"fetch page after_id=%q: %w",
				afterID,
				err,
			)
		}

		for pageIndex, swap := range page {
			if err :=
				swap.ValidateForObservation(); err != nil {
				return nil, fmt.Errorf(
					"swap page_index=%d id=%s: %w",
					pageIndex,
					swap.ID,
					err,
				)
			}

			if normalizeAddress(
				swap.PoolAddress,
			) != normalizeAddress(
				pool.Address,
			) {
				return nil, fmt.Errorf(
					"swap %s belongs to pool %s, expected %s",
					swap.ID,
					swap.PoolAddress,
					pool.Address,
				)
			}

			if swap.BlockNumber <
				fromBlock ||
				swap.BlockNumber >
					toBlock {
				return nil, fmt.Errorf(
					"swap %s block %d is outside chunk [%d,%d]",
					swap.ID,
					swap.BlockNumber,
					fromBlock,
					toBlock,
				)
			}

			// The first chunk begins at the Burn block, so swaps earlier
			// than the Burn log index in that same block must be excluded.
			if !swap.Cursor().After(
				burnCursor,
			) {
				continue
			}

			result =
				append(
					result,
					swap,
				)
		}

		if len(page) <
			pageSize {
			break
		}

		nextAfterID :=
			strings.TrimSpace(
				page[len(page)-1].ID,
			)

		if nextAfterID == "" {
			return nil, fmt.Errorf(
				"pagination page tail has empty swap ID",
			)
		}

		if nextAfterID <=
			afterID {
			return nil, fmt.Errorf(
				"pagination cursor did not advance: previous=%q next=%q",
				afterID,
				nextAfterID,
			)
		}

		afterID =
			nextAfterID
	}

	return result, nil
}

func burnRealizedFlowChunkEnd(
	fromBlock uint64,
	throughBlock uint64,
	chunkSize uint64,
) uint64 {
	if chunkSize == 0 ||
		fromBlock >= throughBlock {
		return throughBlock
	}

	// Avoid uint64 overflow when calculating:
	//
	// fromBlock + chunkSize - 1
	if chunkSize-1 >
		throughBlock-fromBlock {
		return throughBlock
	}

	return fromBlock +
		chunkSize -
		1
}

func cloneBurnFlowLiquidityChanges(
	values []domain.LiquidityChange,
) []domain.LiquidityChange {
	if values == nil {
		return nil
	}

	result := make(
		[]domain.LiquidityChange,
		len(values),
	)

	for index, value := range values {
		result[index] =
			value

		if value.LiquidityDelta != nil {
			result[index].LiquidityDelta =
				new(big.Int).Set(
					value.LiquidityDelta,
				)
		}
	}

	return result
}

func cloneBurnFlowSwaps(
	values []domain.SwapEvent,
) []domain.SwapEvent {
	if values == nil {
		return nil
	}

	result := make(
		[]domain.SwapEvent,
		len(values),
	)

	for index, value := range values {
		result[index] =
			value

		result[index].Amount0Raw =
			new(big.Int).Set(
				value.Amount0Raw,
			)

		result[index].Amount1Raw =
			new(big.Int).Set(
				value.Amount1Raw,
			)

		result[index].SqrtPriceX96After =
			new(big.Int).Set(
				value.SqrtPriceX96After,
			)
	}

	return result
}
