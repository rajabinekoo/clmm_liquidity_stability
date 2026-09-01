package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

// LocalAnalysisRepository is the complete data boundary of the analyzer.
// Implementations must serve every method from local durable storage; The Graph
// belongs only to the indexing process.
type LocalAnalysisRepository interface {
	LoadLocalAnalysisBootstrap(
		ctx context.Context,
		poolAddress string,
		requiredFromBlock uint64,
	) (repositories.LocalAnalysisBootstrap, error)

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

// LocalAnalysisProvider reconstructs all requested historical states from one
// PostgreSQL bootstrap loaded at startup. Stored snapshots are validation and
// cache checkpoints; LP actions and swaps between checkpoints are replayed from
// memory. No analyzer method performs HTTP requests.
type LocalAnalysisProvider struct {
	repository        LocalAnalysisRepository
	poolAddress       string
	requiredFromBlock uint64

	prepareOnce sync.Once
	prepareErr  error

	metadata       domain.Pool
	indexedThrough uint64

	events []domain.PoolBlockEvent

	checkpointBlocks []uint64
	checkpoints      map[uint64]*domain.ReconstructedPool

	cacheMu sync.RWMutex
	cache   map[uint64]*domain.ReconstructedPool
}

func NewLocalAnalysisProvider(
	repository LocalAnalysisRepository,
	poolAddress string,
	requiredFromBlock uint64,
) *LocalAnalysisProvider {
	return &LocalAnalysisProvider{
		repository:        repository,
		poolAddress:       normalizeAddress(poolAddress),
		requiredFromBlock: requiredFromBlock,
		checkpoints:       make(map[uint64]*domain.ReconstructedPool),
		cache:             make(map[uint64]*domain.ReconstructedPool),
	}
}

func (p *LocalAnalysisProvider) Prepare(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("prepare local analysis provider: provider is nil")
	}

	p.prepareOnce.Do(func() {
		p.prepareErr = p.prepare(ctx)
	})

	return p.prepareErr
}

func (p *LocalAnalysisProvider) prepare(ctx context.Context) error {
	if p.repository == nil {
		return fmt.Errorf("prepare local analysis provider: repository is nil")
	}
	if p.poolAddress == "" {
		return fmt.Errorf("prepare local analysis provider: pool address is required")
	}
	if p.requiredFromBlock == 0 {
		return fmt.Errorf("prepare local analysis provider: required-from block is zero")
	}

	bootstrap, err := p.repository.LoadLocalAnalysisBootstrap(
		ctx,
		p.poolAddress,
		p.requiredFromBlock,
	)
	if err != nil {
		return fmt.Errorf("prepare local analysis provider: %w", err)
	}

	if normalizeAddress(bootstrap.Metadata.Address) != p.poolAddress {
		return fmt.Errorf(
			"prepare local analysis provider: metadata pool %s does not match %s",
			bootstrap.Metadata.Address,
			p.poolAddress,
		)
	}
	if bootstrap.IndexedThrough < p.requiredFromBlock {
		return fmt.Errorf(
			"prepare local analysis provider: indexed-through %d is before required block %d",
			bootstrap.IndexedThrough,
			p.requiredFromBlock,
		)
	}

	anchorPool, err := ReconstructPool(bootstrap.AnchorInput)
	if err != nil {
		return fmt.Errorf("prepare local analysis provider: reconstruct anchor: %w", err)
	}

	events, err := mergeLocalAnalysisEvents(
		anchorPool.BlockNumber,
		bootstrap.IndexedThrough,
		bootstrap.LiquidityChanges,
		bootstrap.Swaps,
	)
	if err != nil {
		return fmt.Errorf("prepare local analysis provider: merge events: %w", err)
	}

	checkpointBlocks := make([]uint64, 0, len(bootstrap.Snapshots)+2)
	checkpoints := make(map[uint64]*domain.ReconstructedPool, len(bootstrap.Snapshots)+2)

	anchorPool.BlockNumber = bootstrap.AnchorInput.Snapshot.BlockNumber
	checkpoints[anchorPool.BlockNumber] = cloneBurnRuntimePool(anchorPool)
	checkpointBlocks = append(checkpointBlocks, anchorPool.BlockNumber)

	current := cloneBurnRuntimePool(anchorPool)
	eventIndex := 0

	for snapshotIndex, snapshot := range bootstrap.Snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}

		if snapshot.BlockNumber <= current.BlockNumber {
			return fmt.Errorf(
				"prepare local analysis provider: non-increasing snapshot at index %d block=%d previous=%d",
				snapshotIndex,
				snapshot.BlockNumber,
				current.BlockNumber,
			)
		}

		for eventIndex < len(events) && events[eventIndex].Cursor.BlockNumber <= snapshot.BlockNumber {
			next, replayErr := applyLocalAnalysisEvent(current, events[eventIndex])
			if replayErr != nil {
				return fmt.Errorf(
					"prepare local analysis provider: replay event %s at %s: %w",
					events[eventIndex].Type,
					events[eventIndex].Cursor,
					replayErr,
				)
			}
			current = next
			eventIndex++
		}

		current.BlockNumber = snapshot.BlockNumber
		if err := validateLocalAnalysisCheckpoint(current, snapshot); err != nil {
			return fmt.Errorf(
				"prepare local analysis provider: validate stored snapshot block %d: %w",
				snapshot.BlockNumber,
				err,
			)
		}

		checkpoints[snapshot.BlockNumber] = cloneBurnRuntimePool(current)
		checkpointBlocks = append(checkpointBlocks, snapshot.BlockNumber)
	}

	if len(checkpointBlocks) == 0 || checkpointBlocks[len(checkpointBlocks)-1] < bootstrap.IndexedThrough {
		for eventIndex < len(events) && events[eventIndex].Cursor.BlockNumber <= bootstrap.IndexedThrough {
			next, replayErr := applyLocalAnalysisEvent(current, events[eventIndex])
			if replayErr != nil {
				return fmt.Errorf(
					"prepare local analysis provider: replay tail event %s at %s: %w",
					events[eventIndex].Type,
					events[eventIndex].Cursor,
					replayErr,
				)
			}
			current = next
			eventIndex++
		}

		current.BlockNumber = bootstrap.IndexedThrough
		checkpoints[bootstrap.IndexedThrough] = cloneBurnRuntimePool(current)
		checkpointBlocks = append(checkpointBlocks, bootstrap.IndexedThrough)
	}

	if eventIndex != len(events) {
		return fmt.Errorf(
			"prepare local analysis provider: %d event(s) remain after indexed head %d",
			len(events)-eventIndex,
			bootstrap.IndexedThrough,
		)
	}

	sort.Slice(checkpointBlocks, func(i, j int) bool {
		return checkpointBlocks[i] < checkpointBlocks[j]
	})
	checkpointBlocks = compactUint64s(checkpointBlocks)

	p.metadata = bootstrap.Metadata
	p.indexedThrough = bootstrap.IndexedThrough
	p.events = events
	p.checkpointBlocks = checkpointBlocks
	p.checkpoints = checkpoints

	p.cacheMu.Lock()
	for block, state := range checkpoints {
		p.cache[block] = cloneBurnRuntimePool(state)
	}
	p.cacheMu.Unlock()

	return nil
}

func (p *LocalAnalysisProvider) IndexedHead(
	ctx context.Context,
) (domain.IndexedHead, error) {
	if err := p.Prepare(ctx); err != nil {
		return domain.IndexedHead{}, err
	}
	return domain.IndexedHead{BlockNumber: p.indexedThrough}, nil
}

func (p *LocalAnalysisProvider) PoolMetadata(
	ctx context.Context,
	poolAddress string,
) (domain.Pool, error) {
	if err := p.Prepare(ctx); err != nil {
		return domain.Pool{}, err
	}
	if normalizeAddress(poolAddress) != p.poolAddress {
		return domain.Pool{}, fmt.Errorf(
			"load local pool metadata: pool %s does not match prepared pool %s",
			poolAddress,
			p.poolAddress,
		)
	}
	return p.metadata, nil
}

// PoolEventsAfterCursorThroughBlock returns the immutable local event window
// strictly after startCursor and through the end of throughBlock. The returned
// slice owns its header, while event payload pointers remain read-only views of
// the provider bootstrap. Analyzer services must never mutate them.
func (p *LocalAnalysisProvider) PoolEventsAfterCursorThroughBlock(
	ctx context.Context,
	poolAddress string,
	startCursor domain.EventCursor,
	throughBlock uint64,
) ([]domain.PoolBlockEvent, error) {
	if err := p.Prepare(ctx); err != nil {
		return nil, err
	}
	if normalizeAddress(poolAddress) != p.poolAddress {
		return nil, fmt.Errorf(
			"load local event window: pool %s does not match prepared pool %s",
			poolAddress,
			p.poolAddress,
		)
	}
	if err := startCursor.Validate(); err != nil {
		return nil, fmt.Errorf("load local event window: invalid start cursor: %w", err)
	}
	if throughBlock < startCursor.BlockNumber {
		return nil, fmt.Errorf(
			"load local event window: through block %d is before start block %d",
			throughBlock,
			startCursor.BlockNumber,
		)
	}
	if throughBlock > p.indexedThrough {
		return nil, fmt.Errorf(
			"load local event window: through block %d exceeds indexed head %d",
			throughBlock,
			p.indexedThrough,
		)
	}

	start := sort.Search(len(p.events), func(index int) bool {
		return p.events[index].Cursor.After(startCursor)
	})
	end := sort.Search(len(p.events), func(index int) bool {
		return p.events[index].Cursor.BlockNumber > throughBlock
	})

	result := append([]domain.PoolBlockEvent(nil), p.events[start:end]...)
	return result, nil
}

func (p *LocalAnalysisProvider) PoolSnapshotAt(
	ctx context.Context,
	poolAddress string,
	blockNumber uint64,
) (domain.PoolSnapshot, error) {
	pool, err := p.ReconstructedPoolAt(ctx, poolAddress, blockNumber)
	if err != nil {
		return domain.PoolSnapshot{}, err
	}

	return domain.PoolSnapshot{
		PoolAddress:     pool.PoolAddress,
		BlockNumber:     pool.BlockNumber,
		Tick:            strconv.Itoa(pool.CurrentTick),
		SqrtPriceX96:    decimal.NewFromBigInt(pool.SqrtPriceX96, 0),
		ActiveLiquidity: decimal.NewFromBigInt(pool.Liquidity, 0),
	}, nil
}

func (p *LocalAnalysisProvider) ReconstructedPoolAt(
	ctx context.Context,
	poolAddress string,
	blockNumber uint64,
) (*domain.ReconstructedPool, error) {
	if err := p.Prepare(ctx); err != nil {
		return nil, err
	}

	if normalizeAddress(poolAddress) != p.poolAddress {
		return nil, fmt.Errorf(
			"reconstruct local pool: pool %s does not match prepared pool %s",
			poolAddress,
			p.poolAddress,
		)
	}
	if blockNumber < p.checkpointBlocks[0] || blockNumber > p.indexedThrough {
		return nil, fmt.Errorf(
			"reconstruct local pool: block %d is outside local range [%d,%d]",
			blockNumber,
			p.checkpointBlocks[0],
			p.indexedThrough,
		)
	}

	p.cacheMu.RLock()
	cached := p.cache[blockNumber]
	p.cacheMu.RUnlock()
	if cached != nil {
		return cloneBurnRuntimePool(cached), nil
	}

	checkpointIndex := sort.Search(len(p.checkpointBlocks), func(index int) bool {
		return p.checkpointBlocks[index] > blockNumber
	}) - 1
	if checkpointIndex < 0 {
		return nil, fmt.Errorf(
			"reconstruct local pool: no checkpoint at or before block %d",
			blockNumber,
		)
	}

	checkpointBlock := p.checkpointBlocks[checkpointIndex]
	current := cloneBurnRuntimePool(p.checkpoints[checkpointBlock])

	start := sort.Search(len(p.events), func(index int) bool {
		return p.events[index].Cursor.BlockNumber > checkpointBlock
	})
	end := sort.Search(len(p.events), func(index int) bool {
		return p.events[index].Cursor.BlockNumber > blockNumber
	})

	for index := start; index < end; index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		next, err := applyLocalAnalysisEvent(current, p.events[index])
		if err != nil {
			return nil, fmt.Errorf(
				"reconstruct local pool block %d: replay %s at %s: %w",
				blockNumber,
				p.events[index].Type,
				p.events[index].Cursor,
				err,
			)
		}
		current = next
	}

	current.BlockNumber = blockNumber

	p.cacheMu.Lock()
	if existing := p.cache[blockNumber]; existing == nil {
		p.cache[blockNumber] = cloneBurnRuntimePool(current)
	} else {
		current = cloneBurnRuntimePool(existing)
	}
	p.cacheMu.Unlock()

	return cloneBurnRuntimePool(current), nil
}

func (p *LocalAnalysisProvider) FetchSwapsPage(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
	token0Decimals int,
	token1Decimals int,
) ([]domain.SwapEvent, error) {
	if err := p.Prepare(ctx); err != nil {
		return nil, err
	}
	if normalizeAddress(poolAddress) != p.poolAddress {
		return nil, fmt.Errorf(
			"fetch local swaps: pool %s does not match prepared pool %s",
			poolAddress,
			p.poolAddress,
		)
	}
	if toBlock > p.indexedThrough {
		return nil, fmt.Errorf(
			"fetch local swaps: requested through block %d exceeds local indexed head %d",
			toBlock,
			p.indexedThrough,
		)
	}

	return p.repository.FetchSwapsPage(
		ctx,
		poolAddress,
		fromBlock,
		toBlock,
		afterID,
		limit,
		token0Decimals,
		token1Decimals,
	)
}

func mergeLocalAnalysisEvents(
	afterBlock uint64,
	throughBlock uint64,
	liquidityChanges []domain.LiquidityChange,
	swaps []domain.SwapEvent,
) ([]domain.PoolBlockEvent, error) {
	events := make([]domain.PoolBlockEvent, 0, len(liquidityChanges)+len(swaps))

	for _, change := range liquidityChanges {
		event, err := domain.NewLiquidityPoolBlockEvent(change)
		if err != nil {
			return nil, err
		}
		if event.Cursor.BlockNumber <= afterBlock || event.Cursor.BlockNumber > throughBlock {
			return nil, fmt.Errorf(
				"liquidity event %s at %s is outside (%d,%d]",
				change.ID,
				event.Cursor,
				afterBlock,
				throughBlock,
			)
		}
		events = append(events, event)
	}

	for _, swap := range swaps {
		event, err := domain.NewSwapPoolBlockEvent(swap)
		if err != nil {
			return nil, err
		}
		if event.Cursor.BlockNumber <= afterBlock || event.Cursor.BlockNumber > throughBlock {
			return nil, fmt.Errorf(
				"swap event %s at %s is outside (%d,%d]",
				swap.ID,
				event.Cursor,
				afterBlock,
				throughBlock,
			)
		}
		events = append(events, event)
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Cursor.Equal(events[j].Cursor) {
			return events[i].Type < events[j].Type
		}
		return events[i].Cursor.Before(events[j].Cursor)
	})

	for index := range events {
		if err := events[index].Validate(); err != nil {
			return nil, fmt.Errorf("event %d: %w", index, err)
		}
		if index > 0 && !events[index-1].Cursor.Before(events[index].Cursor) {
			return nil, fmt.Errorf(
				"local pool event ordering is not strictly increasing: previous=%s current=%s",
				events[index-1].Cursor,
				events[index].Cursor,
			)
		}
	}

	return events, nil
}

func applyLocalAnalysisEvent(
	current *domain.ReconstructedPool,
	event domain.PoolBlockEvent,
) (*domain.ReconstructedPool, error) {
	if current == nil {
		return nil, fmt.Errorf("current pool is nil")
	}
	if err := event.Validate(); err != nil {
		return nil, err
	}

	var (
		next *domain.ReconstructedPool
		err  error
	)

	switch event.Type {
	case domain.PoolBlockEventMint, domain.PoolBlockEventBurn:
		next, err = uniswapv3.ApplyLiquidityChange(current, *event.LiquidityChange)
	case domain.PoolBlockEventSwap:
		next, err = applyObservedSwapState(current, *event.Swap)
	default:
		err = fmt.Errorf("unsupported event type %s", event.Type)
	}
	if err != nil {
		return nil, err
	}

	next.BlockNumber = event.Cursor.BlockNumber
	return next, nil
}

func validateLocalAnalysisCheckpoint(
	pool *domain.ReconstructedPool,
	snapshot domain.PoolSnapshot,
) error {
	if pool == nil {
		return fmt.Errorf("pool is nil")
	}
	if normalizeAddress(pool.PoolAddress) != normalizeAddress(snapshot.PoolAddress) {
		return fmt.Errorf(
			"pool address mismatch: reconstructed=%s stored=%s",
			pool.PoolAddress,
			snapshot.PoolAddress,
		)
	}

	tick, err := strconv.Atoi(strings.TrimSpace(snapshot.Tick))
	if err != nil {
		return fmt.Errorf("parse stored tick %q: %w", snapshot.Tick, err)
	}
	sqrtPrice, err := localAnalysisDecimalBigInt(snapshot.SqrtPriceX96)
	if err != nil {
		return fmt.Errorf("parse stored sqrt price: %w", err)
	}
	liquidity, err := localAnalysisDecimalBigInt(snapshot.ActiveLiquidity)
	if err != nil {
		return fmt.Errorf("parse stored active liquidity: %w", err)
	}

	if pool.CurrentTick != tick {
		return fmt.Errorf("tick mismatch: reconstructed=%d stored=%d", pool.CurrentTick, tick)
	}
	if pool.SqrtPriceX96.Cmp(sqrtPrice) != 0 {
		return fmt.Errorf(
			"sqrt price mismatch: reconstructed=%s stored=%s",
			pool.SqrtPriceX96,
			sqrtPrice,
		)
	}
	if pool.Liquidity.Cmp(liquidity) != 0 {
		return fmt.Errorf(
			"active liquidity mismatch: reconstructed=%s stored=%s",
			pool.Liquidity,
			liquidity,
		)
	}

	return nil
}

func localAnalysisDecimalBigInt(value decimal.Decimal) (*big.Int, error) {
	if value.IsNegative() {
		return nil, fmt.Errorf("value is negative: %s", value)
	}
	text := value.Truncate(0).StringFixed(0)
	result, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", text)
	}
	return result, nil
}

func compactUint64s(values []uint64) []uint64 {
	if len(values) < 2 {
		return values
	}

	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
