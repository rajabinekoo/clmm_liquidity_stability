package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const swapInsertBatchSize = 500

type SwapIndexCoverage struct {
	FirstIndexedBlock uint64
	IndexedThrough    uint64
}

func (c SwapIndexCoverage) Covers(
	fromBlock uint64,
	throughBlock uint64,
) bool {
	if fromBlock == 0 || throughBlock < fromBlock {
		return false
	}

	return c.FirstIndexedBlock <= fromBlock &&
		c.IndexedThrough >= throughBlock
}

func (r *EventRepository) EnsureSwapCheckpoint(
	ctx context.Context,
	poolAddress string,
	firstIndexedBlock uint64,
) (SwapIndexCoverage, bool, error) {
	if r == nil || r.db == nil {
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: repository database is nil",
		)
	}

	normalizedPoolAddress := strings.ToLower(
		strings.TrimSpace(poolAddress),
	)

	if normalizedPoolAddress == "" {
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: pool address is required",
		)
	}

	if firstIndexedBlock == 0 || firstIndexedBlock > math.MaxInt64 {
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: invalid first indexed block %d",
			firstIndexedBlock,
		)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: begin transaction: %w",
			err,
		)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var (
		storedFirst int64
		storedLast  int64
	)

	err = tx.QueryRowContext(ctx, `
		SELECT
			first_indexed_block,
			last_completed_block
		FROM swap_indexer_checkpoints
		WHERE pool_address = $1
		FOR UPDATE
	`, normalizedPoolAddress).Scan(
		&storedFirst,
		&storedLast,
	)

	reset := false

	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `
			INSERT INTO swap_indexer_checkpoints (
				pool_address,
				first_indexed_block,
				last_completed_block
			)
			VALUES ($1, $2, $3)
		`,
			normalizedPoolAddress,
			int64(firstIndexedBlock),
			int64(firstIndexedBlock)-1,
		)
		if err != nil {
			return SwapIndexCoverage{}, false, fmt.Errorf(
				"ensure swap checkpoint: insert: %w",
				err,
			)
		}

		storedFirst = int64(firstIndexedBlock)
		storedLast = int64(firstIndexedBlock) - 1

	case err != nil:
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: lock existing checkpoint: %w",
			err,
		)

	case storedFirst <= 0 || storedLast < storedFirst-1:
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: invalid stored coverage first=%d last=%d",
			storedFirst,
			storedLast,
		)

	case int64(firstIndexedBlock) < storedFirst:
		// The requested research range moved further into history. Swap rows are
		// derived index data, so reset this pool atomically and rebuild from the
		// earlier boundary instead of leaving a silent coverage gap.
		if _, err = tx.ExecContext(ctx, `
			DELETE FROM swaps
			WHERE pool_address = $1
		`, normalizedPoolAddress); err != nil {
			return SwapIndexCoverage{}, false, fmt.Errorf(
				"ensure swap checkpoint: clear insufficient swap coverage: %w",
				err,
			)
		}

		if _, err = tx.ExecContext(ctx, `
			UPDATE swap_indexer_checkpoints
			SET
				first_indexed_block = $2,
				last_completed_block = $3,
				updated_at = NOW()
			WHERE pool_address = $1
		`,
			normalizedPoolAddress,
			int64(firstIndexedBlock),
			int64(firstIndexedBlock)-1,
		); err != nil {
			return SwapIndexCoverage{}, false, fmt.Errorf(
				"ensure swap checkpoint: reset insufficient coverage: %w",
				err,
			)
		}

		storedFirst = int64(firstIndexedBlock)
		storedLast = int64(firstIndexedBlock) - 1
		reset = true
	}

	if err := tx.Commit(); err != nil {
		return SwapIndexCoverage{}, false, fmt.Errorf(
			"ensure swap checkpoint: commit: %w",
			err,
		)
	}

	committed = true

	return SwapIndexCoverage{
		FirstIndexedBlock: uint64(storedFirst),
		IndexedThrough:    uint64(storedLast),
	}, reset, nil
}

func (r *EventRepository) SwapCheckpoint(
	ctx context.Context,
	poolAddress string,
) (SwapIndexCoverage, error) {
	if r == nil || r.db == nil {
		return SwapIndexCoverage{}, fmt.Errorf(
			"get swap checkpoint: repository database is nil",
		)
	}

	var (
		firstBlock int64
		lastBlock  int64
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT
			first_indexed_block,
			last_completed_block
		FROM swap_indexer_checkpoints
		WHERE pool_address = $1
	`, strings.ToLower(strings.TrimSpace(poolAddress))).Scan(
		&firstBlock,
		&lastBlock,
	)
	if err != nil {
		return SwapIndexCoverage{}, fmt.Errorf(
			"get swap checkpoint for pool %s: %w",
			poolAddress,
			err,
		)
	}

	if firstBlock <= 0 || lastBlock < firstBlock-1 {
		return SwapIndexCoverage{}, fmt.Errorf(
			"get swap checkpoint: invalid stored coverage first=%d last=%d",
			firstBlock,
			lastBlock,
		)
	}

	return SwapIndexCoverage{
		FirstIndexedBlock: uint64(firstBlock),
		IndexedThrough:    uint64(lastBlock),
	}, nil
}

func (r *EventRepository) StoreSwapWindow(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	swaps []domain.SwapEvent,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf(
			"store swap window: repository database is nil",
		)
	}

	normalizedPoolAddress := strings.ToLower(
		strings.TrimSpace(poolAddress),
	)

	if normalizedPoolAddress == "" {
		return fmt.Errorf(
			"store swap window: pool address is required",
		)
	}

	if fromBlock == 0 || fromBlock > toBlock || toBlock > math.MaxInt64 {
		return fmt.Errorf(
			"store swap window: invalid block range [%d,%d]",
			fromBlock,
			toBlock,
		)
	}

	if err := validateStoredSwapWindow(
		normalizedPoolAddress,
		fromBlock,
		toBlock,
		swaps,
	); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"store swap window: begin transaction: %w",
			err,
		)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := lockAndValidateSwapCheckpoint(
		ctx,
		tx,
		normalizedPoolAddress,
		fromBlock,
	); err != nil {
		return err
	}

	for start := 0; start < len(swaps); start += swapInsertBatchSize {
		end := start + swapInsertBatchSize
		if end > len(swaps) {
			end = len(swaps)
		}

		if err := insertSwapBatch(
			ctx,
			tx,
			swaps[start:end],
		); err != nil {
			return err
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE swap_indexer_checkpoints
		SET
			last_completed_block = $2,
			updated_at = NOW()
		WHERE pool_address = $1
	`,
		normalizedPoolAddress,
		int64(toBlock),
	)
	if err != nil {
		return fmt.Errorf(
			"store swap window: update checkpoint: %w",
			err,
		)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"store swap window: read checkpoint rows affected: %w",
			err,
		)
	}

	if affected != 1 {
		return fmt.Errorf(
			"store swap window: checkpoint not found for pool %s",
			normalizedPoolAddress,
		)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(
			"store swap window: commit: %w",
			err,
		)
	}

	committed = true
	return nil
}

func lockAndValidateSwapCheckpoint(
	ctx context.Context,
	tx *sql.Tx,
	poolAddress string,
	fromBlock uint64,
) error {
	var (
		firstBlock int64
		lastBlock  int64
	)

	err := tx.QueryRowContext(ctx, `
		SELECT
			first_indexed_block,
			last_completed_block
		FROM swap_indexer_checkpoints
		WHERE pool_address = $1
		FOR UPDATE
	`, poolAddress).Scan(
		&firstBlock,
		&lastBlock,
	)
	if err != nil {
		return fmt.Errorf(
			"store swap window: lock checkpoint: %w",
			err,
		)
	}

	if firstBlock <= 0 || lastBlock < firstBlock-1 {
		return fmt.Errorf(
			"store swap window: invalid checkpoint first=%d last=%d",
			firstBlock,
			lastBlock,
		)
	}

	expectedFromBlock := uint64(lastBlock) + 1
	if fromBlock != expectedFromBlock {
		return fmt.Errorf(
			"store swap window: non-contiguous range: expected from block %d, received %d",
			expectedFromBlock,
			fromBlock,
		)
	}

	return nil
}

func validateStoredSwapWindow(
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	swaps []domain.SwapEvent,
) error {
	var previous *domain.EventCursor

	for index, swap := range swaps {
		if err := swap.ValidateForObservation(); err != nil {
			return fmt.Errorf(
				"store swap window: swap index %d id=%q: %w",
				index,
				swap.ID,
				err,
			)
		}

		if strings.TrimSpace(swap.TxHash) == "" {
			return fmt.Errorf(
				"store swap window: swap %s transaction hash is required",
				swap.ID,
			)
		}

		if swap.Timestamp == 0 || swap.Timestamp > math.MaxInt64 {
			return fmt.Errorf(
				"store swap window: swap %s has invalid timestamp %d",
				swap.ID,
				swap.Timestamp,
			)
		}

		if strings.ToLower(strings.TrimSpace(swap.PoolAddress)) != poolAddress {
			return fmt.Errorf(
				"store swap window: swap %s belongs to pool %s, expected %s",
				swap.ID,
				swap.PoolAddress,
				poolAddress,
			)
		}

		if swap.BlockNumber < fromBlock || swap.BlockNumber > toBlock {
			return fmt.Errorf(
				"store swap window: swap %s block %d is outside [%d,%d]",
				swap.ID,
				swap.BlockNumber,
				fromBlock,
				toBlock,
			)
		}

		cursor := swap.Cursor()
		if previous != nil && !previous.Before(cursor) {
			return fmt.Errorf(
				"store swap window: non-increasing cursor at index %d: previous=%s current=%s",
				index,
				previous,
				cursor,
			)
		}

		cursorCopy := cursor
		previous = &cursorCopy
	}

	return nil
}

func insertSwapBatch(
	ctx context.Context,
	tx *sql.Tx,
	swaps []domain.SwapEvent,
) error {
	if len(swaps) == 0 {
		return nil
	}

	const columnsPerRow = 10

	var query strings.Builder
	query.WriteString(`
		INSERT INTO swaps (
			pool_address,
			block_number,
			log_index,
			id,
			tx_hash,
			timestamp_unix,
			amount0_raw,
			amount1_raw,
			sqrt_price_x96,
			tick_after
		)
		VALUES
	`)

	args := make([]any, 0, len(swaps)*columnsPerRow)

	for index, swap := range swaps {
		if index > 0 {
			query.WriteString(",")
		}

		base := index*columnsPerRow + 1
		fmt.Fprintf(
			&query,
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d::numeric,$%d::numeric,$%d::numeric,$%d)",
			base,
			base+1,
			base+2,
			base+3,
			base+4,
			base+5,
			base+6,
			base+7,
			base+8,
			base+9,
		)

		args = append(
			args,
			strings.ToLower(strings.TrimSpace(swap.PoolAddress)),
			int64(swap.BlockNumber),
			swap.LogIndex,
			strings.TrimSpace(swap.ID),
			strings.ToLower(strings.TrimSpace(swap.TxHash)),
			int64(swap.Timestamp),
			swap.Amount0Raw.String(),
			swap.Amount1Raw.String(),
			swap.SqrtPriceX96After.String(),
			swap.TickAfter,
		)
	}

	if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
		return fmt.Errorf(
			"insert swap batch first=%s last=%s count=%d: %w",
			swaps[0].Cursor(),
			swaps[len(swaps)-1].Cursor(),
			len(swaps),
			err,
		)
	}

	return nil
}
