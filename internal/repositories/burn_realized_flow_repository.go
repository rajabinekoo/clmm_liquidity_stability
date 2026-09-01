package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// LoadLiquidityChangesAfterCursorThroughBlock loads every non-zero Mint/Burn
// strictly after startCursor and through the end of throughBlock.
//
// The starting Burn itself is excluded by the strict cursor comparison.
func (r *PoolStateRepository) LoadLiquidityChangesAfterCursorThroughBlock(
	ctx context.Context,
	poolAddress string,
	startCursor domain.EventCursor,
	throughBlock uint64,
) ([]domain.LiquidityChange, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: repository database is nil",
		)
	}

	normalizedPoolAddress :=
		strings.ToLower(
			strings.TrimSpace(
				poolAddress,
			),
		)

	if normalizedPoolAddress == "" {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: pool address is required",
		)
	}

	if err := startCursor.Validate(); err != nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: invalid start cursor: %w",
			err,
		)
	}

	if throughBlock < startCursor.BlockNumber {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: through block %d is before start block %d",
			throughBlock,
			startCursor.BlockNumber,
		)
	}

	if startCursor.BlockNumber > math.MaxInt64 ||
		throughBlock > math.MaxInt64 {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: block number exceeds PostgreSQL BIGINT",
		)
	}

	if startCursor.LogIndex > math.MaxInt32 {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: log index exceeds PostgreSQL INTEGER",
		)
	}

	tx, err :=
		r.db.BeginTx(
			ctx,
			&sql.TxOptions{
				ReadOnly:  true,
				Isolation: sql.LevelRepeatableRead,
			},
		)
	if err != nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: begin transaction: %w",
			err,
		)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var indexedThrough int64

	if err :=
		tx.QueryRowContext(
			ctx,
			`
				SELECT last_completed_block
				FROM indexer_checkpoints
				WHERE pool_address = $1
			`,
			normalizedPoolAddress,
		).Scan(
			&indexedThrough,
		); err != nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: read checkpoint: %w",
			err,
		)
	}

	if indexedThrough < 0 {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: negative checkpoint %d",
			indexedThrough,
		)
	}

	if uint64(indexedThrough) < throughBlock {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: local checkpoint %d is before through block %d",
			indexedThrough,
			throughBlock,
		)
	}

	rows, err :=
		tx.QueryContext(
			ctx,
			`
				SELECT
					id,
					block_number,
					log_index,
					tick_lower,
					tick_upper,
					liquidity_delta::text
				FROM lp_actions
				WHERE pool_address = $1
				  AND (
						block_number > $2
						OR (
							block_number = $2
							AND log_index > $3
						)
				  )
				  AND block_number <= $4
				  AND liquidity_delta <> 0
				ORDER BY
					block_number ASC,
					log_index ASC
			`,
			normalizedPoolAddress,
			int64(startCursor.BlockNumber),
			startCursor.LogIndex,
			int64(throughBlock),
		)
	if err != nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: query: %w",
			err,
		)
	}
	defer rows.Close()

	changes := make(
		[]domain.LiquidityChange,
		0,
	)

	var previousCursor *domain.EventCursor

	for rows.Next() {
		var (
			change domain.LiquidityChange

			blockNumber int64
			deltaText   string
		)

		if err := rows.Scan(
			&change.ID,
			&blockNumber,
			&change.LogIndex,
			&change.TickLower,
			&change.TickUpper,
			&deltaText,
		); err != nil {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: scan: %w",
				err,
			)
		}

		if blockNumber <= 0 {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: event %s has invalid block %d",
				change.ID,
				blockNumber,
			)
		}

		change.BlockNumber =
			uint64(blockNumber)

		delta, ok :=
			new(big.Int).SetString(
				deltaText,
				10,
			)
		if !ok {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: event %s has invalid liquidity delta %q",
				change.ID,
				deltaText,
			)
		}

		if delta.Sign() == 0 {
			continue
		}

		change.LiquidityDelta =
			delta

		event, err :=
			domain.NewLiquidityPoolBlockEvent(
				change,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: event %s: %w",
				change.ID,
				err,
			)
		}

		if !event.Cursor.After(
			startCursor,
		) {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: event cursor %s is not after start cursor %s",
				event.Cursor,
				startCursor,
			)
		}

		if event.Cursor.BlockNumber >
			throughBlock {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: event cursor %s exceeds through block %d",
				event.Cursor,
				throughBlock,
			)
		}

		if previousCursor != nil &&
			!previousCursor.Before(
				event.Cursor,
			) {
			return nil, fmt.Errorf(
				"load burn realized liquidity flow: non-increasing event order: previous=%s current=%s",
				previousCursor,
				event.Cursor,
			)
		}

		cursorCopy :=
			event.Cursor

		previousCursor =
			&cursorCopy

		changes =
			append(
				changes,
				change,
			)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: iterate: %w",
			err,
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(
			"load burn realized liquidity flow: commit: %w",
			err,
		)
	}

	committed = true

	return changes, nil
}
