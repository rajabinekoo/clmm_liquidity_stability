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

// FetchSwapsPage implements the existing provider contract from PostgreSQL.
// token decimals are intentionally unused because swaps are stored as exact raw
// integer amounts during indexing.
func (r *PoolStateRepository) FetchSwapsPage(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
	_ int,
	_ int,
) ([]domain.SwapEvent, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf(
			"fetch local swaps page: repository database is nil",
		)
	}

	normalizedPoolAddress := strings.ToLower(
		strings.TrimSpace(poolAddress),
	)

	if normalizedPoolAddress == "" {
		return nil, fmt.Errorf(
			"fetch local swaps page: pool address is required",
		)
	}

	if fromBlock == 0 || fromBlock > toBlock || toBlock > math.MaxInt64 {
		return nil, fmt.Errorf(
			"fetch local swaps page: invalid block range [%d,%d]",
			fromBlock,
			toBlock,
		)
	}

	if limit <= 0 || limit > 1_000 {
		return nil, fmt.Errorf(
			"fetch local swaps page: limit %d must be inside [1,1000]",
			limit,
		)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			tx_hash,
			pool_address,
			block_number,
			log_index,
			timestamp_unix,
			amount0_raw::text,
			amount1_raw::text,
			sqrt_price_x96::text,
			tick_after
		FROM swaps
		WHERE pool_address = $1
		  AND block_number >= $2
		  AND block_number <= $3
		  AND id > $4
		ORDER BY id ASC
		LIMIT $5
	`,
		normalizedPoolAddress,
		int64(fromBlock),
		int64(toBlock),
		strings.TrimSpace(afterID),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch local swaps page: query: %w",
			err,
		)
	}
	defer rows.Close()

	return scanLocalSwapRows(
		rows,
		"fetch local swaps page",
	)
}

// LoadSwapsAfterCursorThroughBlock is the fast path used by realized-flow and
// no-burn counterfactual analysis. It performs one ordered PostgreSQL range
// scan and therefore does not need Graph pagination or block chunking.
func (r *PoolStateRepository) LoadSwapsAfterCursorThroughBlock(
	ctx context.Context,
	poolAddress string,
	startCursor domain.EventCursor,
	throughBlock uint64,
) ([]domain.SwapEvent, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf(
			"load local swaps after cursor: repository database is nil",
		)
	}

	normalizedPoolAddress := strings.ToLower(
		strings.TrimSpace(poolAddress),
	)

	if normalizedPoolAddress == "" {
		return nil, fmt.Errorf(
			"load local swaps after cursor: pool address is required",
		)
	}

	if err := startCursor.Validate(); err != nil {
		return nil, fmt.Errorf(
			"load local swaps after cursor: invalid start cursor: %w",
			err,
		)
	}

	if throughBlock < startCursor.BlockNumber || throughBlock > math.MaxInt64 {
		return nil, fmt.Errorf(
			"load local swaps after cursor: invalid through block %d for start %s",
			throughBlock,
			startCursor,
		)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			tx_hash,
			pool_address,
			block_number,
			log_index,
			timestamp_unix,
			amount0_raw::text,
			amount1_raw::text,
			sqrt_price_x96::text,
			tick_after
		FROM swaps
		WHERE pool_address = $1
		  AND (
			block_number > $2
			OR (
				block_number = $2
				AND log_index > $3
			)
		  )
		  AND block_number <= $4
		ORDER BY block_number ASC, log_index ASC
	`,
		normalizedPoolAddress,
		int64(startCursor.BlockNumber),
		startCursor.LogIndex,
		int64(throughBlock),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load local swaps after cursor: query: %w",
			err,
		)
	}
	defer rows.Close()

	swaps, err := scanLocalSwapRows(
		rows,
		"load local swaps after cursor",
	)
	if err != nil {
		return nil, err
	}

	var previous *domain.EventCursor
	for index, swap := range swaps {
		cursor := swap.Cursor()

		if !cursor.After(startCursor) {
			return nil, fmt.Errorf(
				"load local swaps after cursor: swap index %d cursor %s is not after %s",
				index,
				cursor,
				startCursor,
			)
		}

		if cursor.BlockNumber > throughBlock {
			return nil, fmt.Errorf(
				"load local swaps after cursor: swap index %d cursor %s exceeds through block %d",
				index,
				cursor,
				throughBlock,
			)
		}

		if previous != nil && !previous.Before(cursor) {
			return nil, fmt.Errorf(
				"load local swaps after cursor: non-increasing order at index %d: previous=%s current=%s",
				index,
				previous,
				cursor,
			)
		}

		cursorCopy := cursor
		previous = &cursorCopy
	}

	return swaps, nil
}

func (r *PoolStateRepository) SwapIndexCoverage(
	ctx context.Context,
	poolAddress string,
) (SwapIndexCoverage, error) {
	if r == nil || r.db == nil {
		return SwapIndexCoverage{}, fmt.Errorf(
			"load swap index coverage: repository database is nil",
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
			"load swap index coverage for pool %s: %w",
			poolAddress,
			err,
		)
	}

	if firstBlock <= 0 || lastBlock < firstBlock-1 {
		return SwapIndexCoverage{}, fmt.Errorf(
			"load swap index coverage: invalid stored coverage first=%d last=%d",
			firstBlock,
			lastBlock,
		)
	}

	return SwapIndexCoverage{
		FirstIndexedBlock: uint64(firstBlock),
		IndexedThrough:    uint64(lastBlock),
	}, nil
}

type localSwapRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanLocalSwapRows(
	rows localSwapRows,
	operation string,
) ([]domain.SwapEvent, error) {
	result := make([]domain.SwapEvent, 0)

	for rows.Next() {
		var (
			swap domain.SwapEvent

			poolAddress string
			blockNumber int64
			timestamp   int64

			amount0Text   string
			amount1Text   string
			sqrtPriceText string
		)

		if err := rows.Scan(
			&swap.ID,
			&swap.TxHash,
			&poolAddress,
			&blockNumber,
			&swap.LogIndex,
			&timestamp,
			&amount0Text,
			&amount1Text,
			&sqrtPriceText,
			&swap.TickAfter,
		); err != nil {
			return nil, fmt.Errorf(
				"%s: scan: %w",
				operation,
				err,
			)
		}

		if blockNumber <= 0 || timestamp < 0 {
			return nil, fmt.Errorf(
				"%s: swap %q has invalid block/timestamp block=%d timestamp=%d",
				operation,
				swap.ID,
				blockNumber,
				timestamp,
			)
		}

		swap.PoolAddress = strings.ToLower(strings.TrimSpace(poolAddress))
		swap.TxHash = strings.ToLower(strings.TrimSpace(swap.TxHash))
		swap.BlockNumber = uint64(blockNumber)
		swap.Timestamp = uint64(timestamp)

		var ok bool

		swap.Amount0Raw, ok = new(big.Int).SetString(amount0Text, 10)
		if !ok {
			return nil, fmt.Errorf(
				"%s: swap %q has invalid amount0 %q",
				operation,
				swap.ID,
				amount0Text,
			)
		}

		swap.Amount1Raw, ok = new(big.Int).SetString(amount1Text, 10)
		if !ok {
			return nil, fmt.Errorf(
				"%s: swap %q has invalid amount1 %q",
				operation,
				swap.ID,
				amount1Text,
			)
		}

		swap.SqrtPriceX96After, ok = new(big.Int).SetString(sqrtPriceText, 10)
		if !ok {
			return nil, fmt.Errorf(
				"%s: swap %q has invalid sqrt price %q",
				operation,
				swap.ID,
				sqrtPriceText,
			)
		}

		if err := swap.ValidateForObservation(); err != nil {
			return nil, fmt.Errorf(
				"%s: validate swap %q: %w",
				operation,
				swap.ID,
				err,
			)
		}

		result = append(result, swap)
	}

	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf(
			"%s: iterate: %w",
			operation,
			err,
		)
	}

	return result, nil
}
