package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// SwapValidationContext describes whether the state at block-1 can safely be
// used as the starting state for a candidate first Swap.
//
// A candidate is clean only when:
//
//   - the local LP-action index is complete through the Swap block;
//   - no Mint or Burn appears before the Swap in that block.
type SwapValidationContext struct {
	Cursor domain.EventCursor

	IndexedThrough uint64

	PriorLiquidityActionCount uint64
}

func (c SwapValidationContext) IsIndexedThroughSwap() bool {
	return c.IndexedThrough >=
		c.Cursor.BlockNumber
}

func (c SwapValidationContext) HasPriorLiquidityAction() bool {
	return c.PriorLiquidityActionCount > 0
}

func (c SwapValidationContext) IsCleanStart() bool {
	return c.IsIndexedThroughSwap() &&
		!c.HasPriorLiquidityAction()
}

func (c SwapValidationContext) Validate() error {
	if err := c.Cursor.Validate(); err != nil {
		return fmt.Errorf(
			"validate swap context cursor: %w",
			err,
		)
	}

	return nil
}

// LoadSwapValidationContext obtains all local-database facts needed to decide
// whether a candidate first Swap has a clean block-start state.
//
// The method does not treat an incomplete checkpoint as a database error. It
// returns IndexedThrough and lets the validation service record a precise skip
// reason.
func (r *PoolStateRepository) LoadSwapValidationContext(
	ctx context.Context,
	poolAddress string,
	cursor domain.EventCursor,
) (SwapValidationContext, error) {
	if r == nil || r.db == nil {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: repository database is nil",
		)
	}

	normalizedPoolAddress :=
		strings.ToLower(
			strings.TrimSpace(poolAddress),
		)

	if normalizedPoolAddress == "" {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: pool address is required",
		)
	}

	if err := cursor.Validate(); err != nil {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: %w",
			err,
		)
	}

	if cursor.BlockNumber >
		math.MaxInt64 {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: block number %d exceeds PostgreSQL BIGINT",
			cursor.BlockNumber,
		)
	}

	if cursor.LogIndex >
		math.MaxInt32 {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: log index %d exceeds PostgreSQL INTEGER",
			cursor.LogIndex,
		)
	}

	tx, err := r.db.BeginTx(
		ctx,
		&sql.TxOptions{
			ReadOnly:  true,
			Isolation: sql.LevelRepeatableRead,
		},
	)
	if err != nil {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: begin transaction: %w",
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
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: read checkpoint for pool %s: %w",
			normalizedPoolAddress,
			err,
		)
	}

	if indexedThrough < 0 {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: negative checkpoint %d for pool %s",
			indexedThrough,
			normalizedPoolAddress,
		)
	}

	var priorActionCount int64

	err = tx.QueryRowContext(
		ctx,
		`
			SELECT COUNT(*)
			FROM lp_actions
			WHERE pool_address = $1
			  AND block_number = $2
			  AND log_index < $3
		`,
		normalizedPoolAddress,
		int64(cursor.BlockNumber),
		cursor.LogIndex,
	).Scan(
		&priorActionCount,
	)
	if err != nil {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: count prior liquidity actions at %s: %w",
			cursor,
			err,
		)
	}

	if priorActionCount < 0 {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: negative prior-action count %d",
			priorActionCount,
		)
	}

	if err := tx.Commit(); err != nil {
		return SwapValidationContext{}, fmt.Errorf(
			"load swap validation context: commit transaction: %w",
			err,
		)
	}

	committed = true

	result := SwapValidationContext{
		Cursor: cursor,

		IndexedThrough: uint64(indexedThrough),

		PriorLiquidityActionCount: uint64(priorActionCount),
	}

	if err := result.Validate(); err != nil {
		return SwapValidationContext{}, err
	}

	return result, nil
}
