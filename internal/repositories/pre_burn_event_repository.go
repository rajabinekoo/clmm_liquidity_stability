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

// LoadLiquidityChangesBeforeCursor loads Mint/Burn events from the target
// Burn block that happened strictly before the target Burn log index.
func (r *PoolStateRepository) LoadLiquidityChangesBeforeCursor(
	ctx context.Context,
	poolAddress string,
	cursor domain.EventCursor,
) ([]domain.LiquidityChange, error) {
	if r == nil ||
		r.db == nil {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: repository database is nil",
		)
	}

	normalizedPoolAddress, cursor, err :=
		normalizePreBurnEventQuery(
			poolAddress,
			cursor,
		)
	if err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(
		ctx,
		&sql.TxOptions{
			ReadOnly:  true,
			Isolation: sql.LevelRepeatableRead,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: begin transaction: %w",
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

	err = tx.QueryRowContext(
		ctx,
		`
			SELECT last_completed_block
			FROM indexer_checkpoints
			WHERE pool_address = $1
		`,
		normalizedPoolAddress,
	).Scan(
		&indexedThrough,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: read checkpoint: %w",
			err,
		)
	}

	if indexedThrough < 0 {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: negative checkpoint %d",
			indexedThrough,
		)
	}

	if uint64(indexedThrough) <
		cursor.BlockNumber {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: checkpoint %d is before target block %d",
			indexedThrough,
			cursor.BlockNumber,
		)
	}

	rows, err := tx.QueryContext(
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
		  AND block_number = $2
		  AND log_index < $3
		  AND liquidity_delta <> 0
		ORDER BY log_index ASC
	`,
		normalizedPoolAddress,
		int64(cursor.BlockNumber),
		cursor.LogIndex,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: query: %w",
			err,
		)
	}
	defer rows.Close()

	changes := make(
		[]domain.LiquidityChange,
		0,
	)

	for rows.Next() {
		var (
			change domain.LiquidityChange

			blockNumber int64
			deltaString string
		)

		if err := rows.Scan(
			&change.ID,
			&blockNumber,
			&change.LogIndex,
			&change.TickLower,
			&change.TickUpper,
			&deltaString,
		); err != nil {
			return nil, fmt.Errorf(
				"load pre-burn liquidity changes: scan: %w",
				err,
			)
		}

		if blockNumber <= 0 {
			return nil, fmt.Errorf(
				"load pre-burn liquidity changes: event %s has invalid block %d",
				change.ID,
				blockNumber,
			)
		}

		change.BlockNumber =
			uint64(blockNumber)

		delta, ok :=
			new(big.Int).SetString(
				deltaString,
				10,
			)
		if !ok {
			return nil, fmt.Errorf(
				"load pre-burn liquidity changes: event %s has invalid delta %q",
				change.ID,
				deltaString,
			)
		}

		// Zero-liquidity Burn events are valid fee-accounting pokes but do not
		// modify pool liquidity, initialized ticks, or active liquidity.
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
				"load pre-burn liquidity changes: event %s: %w",
				change.ID,
				err,
			)
		}

		if !event.Cursor.Before(
			cursor,
		) {
			return nil, fmt.Errorf(
				"load pre-burn liquidity changes: event %s cursor %s is not before target %s",
				change.ID,
				event.Cursor,
				cursor,
			)
		}

		changes = append(
			changes,
			change,
		)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: iterate: %w",
			err,
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(
			"load pre-burn liquidity changes: commit transaction: %w",
			err,
		)
	}

	committed = true

	return changes, nil
}

func normalizePreBurnEventQuery(
	poolAddress string,
	cursor domain.EventCursor,
) (string, domain.EventCursor, error) {
	normalizedPoolAddress :=
		strings.ToLower(
			strings.TrimSpace(
				poolAddress,
			),
		)

	if normalizedPoolAddress == "" {
		return "", domain.EventCursor{}, fmt.Errorf(
			"load pre-burn liquidity changes: pool address is required",
		)
	}

	if err := cursor.Validate(); err != nil {
		return "", domain.EventCursor{}, fmt.Errorf(
			"load pre-burn liquidity changes: %w",
			err,
		)
	}

	if cursor.BlockNumber >
		math.MaxInt64 {
		return "", domain.EventCursor{}, fmt.Errorf(
			"load pre-burn liquidity changes: block number exceeds PostgreSQL BIGINT",
		)
	}

	if cursor.LogIndex >
		math.MaxInt32 {
		return "", domain.EventCursor{}, fmt.Errorf(
			"load pre-burn liquidity changes: log index exceeds PostgreSQL INTEGER",
		)
	}

	return normalizedPoolAddress, cursor, nil
}
