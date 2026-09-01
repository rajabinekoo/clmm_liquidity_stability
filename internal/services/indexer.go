package services

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/utils"
)

type IndexerProvider interface {
	PoolMetadata(
		ctx context.Context,
		poolAddress string,
	) (domain.Pool, error)

	IndexedHead(
		ctx context.Context,
	) (domain.IndexedHead, error)

	PoolSnapshotAt(
		ctx context.Context,
		poolAddress string,
		blockNumber uint64,
	) (domain.PoolSnapshot, error)

	FetchMintsPage(
		ctx context.Context,
		poolAddress string,
		fromBlock uint64,
		toBlock uint64,
		afterID string,
		limit int,
	) ([]domain.LPAction, error)

	FetchBurnsPage(
		ctx context.Context,
		poolAddress string,
		fromBlock uint64,
		toBlock uint64,
		afterID string,
		limit int,
	) ([]domain.LPAction, error)

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

type Indexer struct {
	config     utils.Config
	provider   IndexerProvider
	repository *repositories.EventRepository
}

func NewIndexer(
	config utils.Config,
	provider IndexerProvider,
	repository *repositories.EventRepository,
) *Indexer {
	return &Indexer{
		config:     config,
		provider:   provider,
		repository: repository,
	}
}

func (s *Indexer) Run(ctx context.Context) error {
	if s == nil || s.provider == nil || s.repository == nil {
		return fmt.Errorf(
			"initialize indexer: service dependencies are nil",
		)
	}

	pool, err := s.provider.PoolMetadata(
		ctx,
		s.config.PoolAddress,
	)
	if err != nil {
		return fmt.Errorf(
			"initialize indexer: get pool metadata: %w",
			err,
		)
	}

	if err := s.repository.BootstrapPool(ctx, pool); err != nil {
		return fmt.Errorf(
			"initialize indexer: bootstrap pool: %w",
			err,
		)
	}

	slog.Info(
		"pool indexer started",
		"pool", pool.Address,
		"created_block", pool.CreatedBlock,
		"fee_tier", pool.FeeTier,
		"tick_spacing", pool.TickSpacing,
		"index_lp_actions", s.config.IndexLPActions,
		"index_swaps", s.config.IndexSwaps,
		"one_shot", s.config.IndexerOnce,
	)

	for {
		caughtUp, err := s.syncNextWindow(ctx, pool)

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			slog.Error(
				"indexer window failed",
				"pool", s.config.PoolAddress,
				"error", err,
			)

			if waitForNextPoll(ctx, s.config.PollInterval) != nil {
				return nil
			}

			continue
		}

		if caughtUp && s.config.IndexerOnce {
			slog.Info(
				"pool indexer caught up",
				"pool", pool.Address,
			)

			return nil
		}

		if !caughtUp {
			continue
		}

		if waitForNextPoll(ctx, s.config.PollInterval) != nil {
			return nil
		}
	}
}

func (s *Indexer) syncNextWindow(
	ctx context.Context,
	pool domain.Pool,
) (bool, error) {
	head, err := s.provider.IndexedHead(ctx)
	if err != nil {
		return false, fmt.Errorf(
			"get indexed head: %w",
			err,
		)
	}

	if head.BlockNumber <= s.config.ConfirmationDepth {
		return true, nil
	}

	safeHead := head.BlockNumber - s.config.ConfirmationDepth

	lpCaughtUp := true
	if s.config.IndexLPActions {
		lpCaughtUp, err = s.syncNextLPWindow(ctx, safeHead)
		if err != nil {
			return false, err
		}
	}

	swapsCaughtUp := true
	if s.config.IndexSwaps {
		swapsCaughtUp, err = s.syncNextSwapWindow(
			ctx,
			pool,
			safeHead,
		)
		if err != nil {
			return false, err
		}
	}

	return lpCaughtUp && swapsCaughtUp, nil
}

func (s *Indexer) syncNextLPWindow(
	ctx context.Context,
	safeHead uint64,
) (bool, error) {
	checkpoint, err := s.repository.Checkpoint(
		ctx,
		s.config.PoolAddress,
	)
	if err != nil {
		return false, err
	}

	if checkpoint >= safeHead {
		return true, nil
	}

	fromBlock := checkpoint + 1
	toBlock := calculateWindowEnd(
		fromBlock,
		safeHead,
		s.config.WindowSize,
	)

	actions, err := s.fetchAllLPActions(ctx, fromBlock, toBlock)
	if err != nil {
		return false, err
	}

	snapshot, err := s.provider.PoolSnapshotAt(
		ctx,
		s.config.PoolAddress,
		toBlock,
	)
	if err != nil {
		return false, fmt.Errorf(
			"get pool snapshot at block %d: %w",
			toBlock,
			err,
		)
	}

	if err := s.repository.StoreWindow(
		ctx,
		s.config.PoolAddress,
		fromBlock,
		toBlock,
		actions,
		snapshot,
	); err != nil {
		return false, err
	}

	slog.Info(
		"lp index window completed",
		"pool", s.config.PoolAddress,
		"from_block", fromBlock,
		"to_block", toBlock,
		"safe_head", safeHead,
		"lp_actions", len(actions),
		"tick", snapshot.Tick,
		"active_liquidity", snapshot.ActiveLiquidity.String(),
	)

	return toBlock >= safeHead, nil
}

func (s *Indexer) syncNextSwapWindow(
	ctx context.Context,
	pool domain.Pool,
	safeHead uint64,
) (bool, error) {
	coverageReferenceHead := safeHead

	// A swap-only backfill may run while the local LP/snapshot index is pinned
	// to an older research head. Preserve enough history for that exact local
	// analyzer head instead of deriving the start only from today's Graph head.
	lpCheckpoint, checkpointErr := s.repository.Checkpoint(
		ctx,
		pool.Address,
	)
	if checkpointErr != nil {
		return false, fmt.Errorf(
			"load LP checkpoint for swap coverage: %w",
			checkpointErr,
		)
	}

	if lpCheckpoint >= pool.CreatedBlock &&
		lpCheckpoint < coverageReferenceHead {
		coverageReferenceHead = lpCheckpoint
	}

	requiredStart, err := s.config.RequiredSwapIndexStartBlock(
		coverageReferenceHead,
		pool.CreatedBlock,
	)
	if err != nil {
		return false, fmt.Errorf(
			"resolve swap index start: %w",
			err,
		)
	}

	coverage, reset, err := s.repository.EnsureSwapCheckpoint(
		ctx,
		pool.Address,
		requiredStart,
	)
	if err != nil {
		return false, err
	}

	if reset {
		slog.Warn(
			"swap index coverage reset for wider research range",
			"pool", pool.Address,
			"first_indexed_block", coverage.FirstIndexedBlock,
		)
	}

	if coverage.IndexedThrough >= safeHead {
		return true, nil
	}

	fromBlock := coverage.IndexedThrough + 1
	toBlock := calculateWindowEnd(
		fromBlock,
		safeHead,
		s.config.SwapWindowSize,
	)

	swaps, err := s.fetchAllSwapsAdaptive(
		ctx,
		pool,
		fromBlock,
		toBlock,
	)
	if err != nil {
		return false, fmt.Errorf(
			"index swaps for blocks %d-%d: %w",
			fromBlock,
			toBlock,
			err,
		)
	}

	if err := s.repository.StoreSwapWindow(
		ctx,
		pool.Address,
		fromBlock,
		toBlock,
		swaps,
	); err != nil {
		return false, err
	}

	progress := swapIndexProgressPercent(
		coverage.FirstIndexedBlock,
		toBlock,
		safeHead,
	)

	slog.Info(
		"swap index window completed",
		"pool", pool.Address,
		"from_block", fromBlock,
		"to_block", toBlock,
		"safe_head", safeHead,
		"swaps", len(swaps),
		"progress_percent", progress.String(),
	)

	return toBlock >= safeHead, nil
}

func (s *Indexer) fetchAllSwapsAdaptive(
	ctx context.Context,
	pool domain.Pool,
	fromBlock uint64,
	toBlock uint64,
) ([]domain.SwapEvent, error) {
	swaps, err := s.fetchAllSwapsWindow(
		ctx,
		pool,
		fromBlock,
		toBlock,
	)
	if err == nil {
		return normalizeIndexedSwapWindow(
			pool.Address,
			fromBlock,
			toBlock,
			swaps,
		)
	}

	windowBlocks := toBlock - fromBlock + 1
	if windowBlocks <= s.config.SwapMinimumWindowSize {
		return nil, err
	}

	midBlock := fromBlock + (toBlock-fromBlock)/2

	slog.Warn(
		"swap Graph window failed; splitting range",
		"pool", pool.Address,
		"from_block", fromBlock,
		"to_block", toBlock,
		"left_to_block", midBlock,
		"right_from_block", midBlock+1,
		"error", err,
	)

	left, leftErr := s.fetchAllSwapsAdaptive(
		ctx,
		pool,
		fromBlock,
		midBlock,
	)
	if leftErr != nil {
		return nil, leftErr
	}

	right, rightErr := s.fetchAllSwapsAdaptive(
		ctx,
		pool,
		midBlock+1,
		toBlock,
	)
	if rightErr != nil {
		return nil, rightErr
	}

	combined := make(
		[]domain.SwapEvent,
		0,
		len(left)+len(right),
	)
	combined = append(combined, left...)
	combined = append(combined, right...)

	return normalizeIndexedSwapWindow(
		pool.Address,
		fromBlock,
		toBlock,
		combined,
	)
}

func (s *Indexer) fetchAllSwapsWindow(
	ctx context.Context,
	pool domain.Pool,
	fromBlock uint64,
	toBlock uint64,
) ([]domain.SwapEvent, error) {
	result := make([]domain.SwapEvent, 0)
	afterID := ""

	for {
		page, err := s.fetchSwapPageWithRetry(
			ctx,
			pool,
			fromBlock,
			toBlock,
			afterID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"fetch swap page blocks=%d-%d after=%q: %w",
				fromBlock,
				toBlock,
				afterID,
				err,
			)
		}

		if len(page) == 0 {
			break
		}

		result = append(result, page...)

		nextCursor := strings.TrimSpace(page[len(page)-1].ID)
		if nextCursor == "" || nextCursor <= afterID {
			return nil, fmt.Errorf(
				"swap cursor did not advance: previous=%q next=%q",
				afterID,
				nextCursor,
			)
		}

		afterID = nextCursor

		if len(page) < s.config.SwapPageSize {
			break
		}
	}

	return result, nil
}

func (s *Indexer) fetchSwapPageWithRetry(
	ctx context.Context,
	pool domain.Pool,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
) ([]domain.SwapEvent, error) {
	var lastErr error

	for attempt := 1; attempt <= s.config.SwapFetchMaxAttempts; attempt++ {
		attemptContext, cancel := context.WithTimeout(
			ctx,
			s.config.SwapRequestTimeout,
		)

		page, err := s.provider.FetchSwapsPage(
			attemptContext,
			pool.Address,
			fromBlock,
			toBlock,
			afterID,
			s.config.SwapPageSize,
			pool.Token0Decimals,
			pool.Token1Decimals,
		)
		cancel()
		if err == nil {
			return page, nil
		}

		lastErr = err

		if attempt == s.config.SwapFetchMaxAttempts {
			break
		}

		delay := swapRetryDelay(
			s.config.SwapRetryBaseDelay,
			attempt,
		)

		slog.Warn(
			"swap Graph page failed; retrying",
			"pool", pool.Address,
			"from_block", fromBlock,
			"to_block", toBlock,
			"after_id", afterID,
			"attempt", attempt,
			"max_attempts", s.config.SwapFetchMaxAttempts,
			"retry_delay", delay,
			"error", err,
		)

		if waitForNextPoll(ctx, delay) != nil {
			return nil, ctx.Err()
		}
	}

	return nil, lastErr
}

func normalizeIndexedSwapWindow(
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	values []domain.SwapEvent,
) ([]domain.SwapEvent, error) {
	normalizedPoolAddress := normalizeAddress(poolAddress)

	sort.Slice(values, func(left int, right int) bool {
		leftCursor := values[left].Cursor()
		rightCursor := values[right].Cursor()

		if leftCursor.Equal(rightCursor) {
			return values[left].ID < values[right].ID
		}

		return leftCursor.Before(rightCursor)
	})

	result := make([]domain.SwapEvent, 0, len(values))
	seenIDs := make(map[string]domain.SwapEvent, len(values))

	for index, swap := range values {
		if err := swap.ValidateForObservation(); err != nil {
			return nil, fmt.Errorf(
				"normalize indexed swaps: swap index=%d id=%q: %w",
				index,
				swap.ID,
				err,
			)
		}

		if normalizeAddress(swap.PoolAddress) != normalizedPoolAddress {
			return nil, fmt.Errorf(
				"normalize indexed swaps: swap %s belongs to pool %s, expected %s",
				swap.ID,
				swap.PoolAddress,
				poolAddress,
			)
		}

		if swap.BlockNumber < fromBlock || swap.BlockNumber > toBlock {
			return nil, fmt.Errorf(
				"normalize indexed swaps: swap %s block %d is outside [%d,%d]",
				swap.ID,
				swap.BlockNumber,
				fromBlock,
				toBlock,
			)
		}

		id := strings.TrimSpace(swap.ID)
		if previous, exists := seenIDs[id]; exists {
			if !burnRealizedFlowSwapsEquivalent(previous, swap) {
				return nil, fmt.Errorf(
					"normalize indexed swaps: duplicate ID %q has conflicting payloads: first={%s} current={%s}",
					id,
					burnRealizedFlowSwapFingerprint(previous),
					burnRealizedFlowSwapFingerprint(swap),
				)
			}

			continue
		}
		seenIDs[id] = swap

		if len(result) > 0 {
			previous := result[len(result)-1]
			cursor := swap.Cursor()
			previousCursor := previous.Cursor()

			switch cursor.Compare(previousCursor) {
			case -1:
				return nil, fmt.Errorf(
					"normalize indexed swaps: decreasing cursor at index %d: previous=%s current=%s",
					index,
					previousCursor,
					cursor,
				)

			case 0:
				if !burnRealizedFlowSwapsEquivalent(previous, swap) {
					return nil, fmt.Errorf(
						"normalize indexed swaps: conflicting swaps share cursor %s: first={%s} current={%s}",
						cursor,
						burnRealizedFlowSwapFingerprint(previous),
						burnRealizedFlowSwapFingerprint(swap),
					)
				}

				continue
			}
		}

		result = append(result, swap)
	}

	return result, nil
}

func swapRetryDelay(
	base time.Duration,
	failedAttempt int,
) time.Duration {
	if failedAttempt <= 1 {
		return base
	}

	delay := base
	for index := 1; index < failedAttempt; index++ {
		if delay >= 30*time.Second/2 {
			return 30 * time.Second
		}
		delay *= 2
	}

	if delay > 30*time.Second {
		return 30 * time.Second
	}

	return delay
}

func swapIndexProgressPercent(
	firstBlock uint64,
	indexedThrough uint64,
	safeHead uint64,
) decimal.Decimal {
	if firstBlock == 0 || safeHead < firstBlock || indexedThrough < firstBlock {
		return decimal.Zero
	}

	total := safeHead - firstBlock + 1
	completed := indexedThrough - firstBlock + 1
	if completed > total {
		completed = total
	}

	return decimal.NewFromInt(int64(completed)).
		Div(decimal.NewFromInt(int64(total))).
		Mul(decimal.NewFromInt(100))
}

func (s *Indexer) fetchAllLPActions(
	ctx context.Context,
	fromBlock uint64,
	toBlock uint64,
) ([]domain.LPAction, error) {
	mints, err := s.fetchAllMints(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}

	burns, err := s.fetchAllBurns(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}

	actions := make([]domain.LPAction, 0, len(mints)+len(burns))
	actions = append(actions, mints...)
	actions = append(actions, burns...)

	return actions, nil
}

func (s *Indexer) fetchAllMints(
	ctx context.Context,
	fromBlock uint64,
	toBlock uint64,
) ([]domain.LPAction, error) {
	var result []domain.LPAction
	afterID := ""

	for {
		page, err := s.provider.FetchMintsPage(
			ctx,
			s.config.PoolAddress,
			fromBlock,
			toBlock,
			afterID,
			s.config.PageSize,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"fetch mints for blocks %d-%d after %q: %w",
				fromBlock,
				toBlock,
				afterID,
				err,
			)
		}

		if len(page) == 0 {
			break
		}

		result = append(result, page...)
		nextCursor := page[len(page)-1].SourceID

		if nextCursor == "" || nextCursor == afterID {
			return nil, fmt.Errorf(
				"mint cursor did not advance: %q",
				afterID,
			)
		}

		afterID = nextCursor

		if len(page) < s.config.PageSize {
			break
		}
	}

	return result, nil
}

func (s *Indexer) fetchAllBurns(
	ctx context.Context,
	fromBlock uint64,
	toBlock uint64,
) ([]domain.LPAction, error) {
	var result []domain.LPAction
	afterID := ""

	for {
		page, err := s.provider.FetchBurnsPage(
			ctx,
			s.config.PoolAddress,
			fromBlock,
			toBlock,
			afterID,
			s.config.PageSize,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"fetch burns for blocks %d-%d after %q: %w",
				fromBlock,
				toBlock,
				afterID,
				err,
			)
		}

		if len(page) == 0 {
			break
		}

		result = append(result, page...)
		nextCursor := page[len(page)-1].SourceID

		if nextCursor == "" || nextCursor == afterID {
			return nil, fmt.Errorf(
				"burn cursor did not advance: %q",
				afterID,
			)
		}

		afterID = nextCursor

		if len(page) < s.config.PageSize {
			break
		}
	}

	return result, nil
}

func calculateWindowEnd(
	fromBlock uint64,
	safeHead uint64,
	windowSize uint64,
) uint64 {
	remainingBlocks := safeHead - fromBlock + 1

	if remainingBlocks <= windowSize {
		return safeHead
	}

	return fromBlock + windowSize - 1
}

func waitForNextPoll(
	ctx context.Context,
	duration time.Duration,
) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-timer.C:
		return nil
	}
}
