package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// LoadRangeLiquidityBeforeCursor returns the aggregate liquidity associated
// with one exact [tickLower, tickUpper] range immediately before the target
// event cursor.
//
// The aggregation intentionally ignores owner, sender and origin:
//
//	range liquidity before cursor
//	= sum(Mint liquidity) - sum(Burn liquidity)
//
// for the exact tick pair and all events strictly before the target cursor.
func (r *PoolStateRepository) LoadRangeLiquidityBeforeCursor(
	ctx context.Context,
	poolAddress string,
	tickLower int,
	tickUpper int,
	cursor domain.EventCursor,
) (*big.Int, error) {
	if r == nil ||
		r.db == nil {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: repository database is nil",
		)
	}

	normalizedPoolAddress, cursor, err :=
		normalizeRangeLiquidityBeforeCursorQuery(
			poolAddress,
			tickLower,
			tickUpper,
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
			"load range liquidity before cursor: begin transaction: %w",
			err,
		)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	indexedThrough, err :=
		loadBurnCandidateCheckpoint(
			ctx,
			tx,
			normalizedPoolAddress,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: %w",
			err,
		)
	}

	if indexedThrough <
		cursor.BlockNumber {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: checkpoint %d is before target block %d",
			indexedThrough,
			cursor.BlockNumber,
		)
	}

	var liquidityString string

	err = tx.QueryRowContext(
		ctx,
		`
			SELECT
				COALESCE(
					SUM(liquidity_delta),
					0
				)::text
			FROM lp_actions
			WHERE pool_address = $1
			  AND tick_lower = $2
			  AND tick_upper = $3
			  AND (
					block_number < $4
					OR (
						block_number = $4
						AND log_index < $5
					)
			  )
		`,
		normalizedPoolAddress,
		tickLower,
		tickUpper,
		int64(cursor.BlockNumber),
		cursor.LogIndex,
	).Scan(
		&liquidityString,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: query: %w",
			err,
		)
	}

	liquidity, ok :=
		new(big.Int).SetString(
			liquidityString,
			10,
		)
	if !ok {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: invalid aggregate liquidity %q",
			liquidityString,
		)
	}

	if liquidity.Sign() < 0 {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: aggregate range liquidity is negative: %s",
			liquidity,
		)
	}

	// Aggregate liquidity on an exact range is also bounded by the uint128
	// liquidity stored at each of its boundary ticks.
	if liquidity.BitLen() > 128 {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: aggregate range liquidity exceeds uint128: %s",
			liquidity,
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(
			"load range liquidity before cursor: commit transaction: %w",
			err,
		)
	}

	committed = true

	return new(big.Int).Set(
		liquidity,
	), nil
}

func normalizeRangeLiquidityBeforeCursorQuery(
	poolAddress string,
	tickLower int,
	tickUpper int,
	cursor domain.EventCursor,
) (string, domain.EventCursor, error) {
	normalizedPoolAddress, normalizedCursor, err :=
		normalizePreBurnEventQuery(
			poolAddress,
			cursor,
		)
	if err != nil {
		return "", domain.EventCursor{}, fmt.Errorf(
			"load range liquidity before cursor: %w",
			err,
		)
	}

	if tickLower >= tickUpper {
		return "", domain.EventCursor{}, fmt.Errorf(
			"load range liquidity before cursor: invalid tick range [%d,%d]",
			tickLower,
			tickUpper,
		)
	}

	return normalizedPoolAddress, normalizedCursor, nil
}
