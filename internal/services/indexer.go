package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"oracle/internal/domain"
	"oracle/internal/providers"
	"oracle/internal/repositories"
	"oracle/internal/utils"
)

type Indexer struct {
	config     utils.Config
	provider   *providers.Client
	repository *repositories.EventRepository
}

func NewIndexer(
	config utils.Config,
	provider *providers.Client,
	repository *repositories.EventRepository,
) *Indexer {
	return &Indexer{
		config:     config,
		provider:   provider,
		repository: repository,
	}
}

func (s *Indexer) Run(ctx context.Context) error {
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
	)

	for {
		caughtUp, err := s.syncNextWindow(ctx)

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			slog.Error(
				"indexer window failed",
				"pool", s.config.PoolAddress,
				"error", err,
			)

			if waitForNextPoll(
				ctx,
				s.config.PollInterval,
			) != nil {
				return nil
			}

			continue
		}

		if !caughtUp {
			continue
		}

		if waitForNextPoll(
			ctx,
			s.config.PollInterval,
		) != nil {
			return nil
		}
	}
}

func (s *Indexer) syncNextWindow(
	ctx context.Context,
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

	actions, err := s.fetchAllLPActions(
		ctx,
		fromBlock,
		toBlock,
	)
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
		"indexer window completed",
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

func (s *Indexer) fetchAllLPActions(
	ctx context.Context,
	fromBlock uint64,
	toBlock uint64,
) ([]domain.LPAction, error) {
	mints, err := s.fetchAllMints(
		ctx,
		fromBlock,
		toBlock,
	)
	if err != nil {
		return nil, err
	}

	burns, err := s.fetchAllBurns(
		ctx,
		fromBlock,
		toBlock,
	)
	if err != nil {
		return nil, err
	}

	actions := make(
		[]domain.LPAction,
		0,
		len(mints)+len(burns),
	)

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
