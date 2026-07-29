package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// LocalAnalysisBootstrap is a repeatable-read, self-contained input for the
// offline analyzer. The Graph is used only by cmd/indexer; analysis consumes
// this local snapshot/event timeline exclusively.
type LocalAnalysisBootstrap struct {
	Metadata       domain.Pool
	IndexedThrough uint64

	AnchorInput domain.ReconstructionInput
	Snapshots   []domain.PoolSnapshot

	LiquidityChanges []domain.LiquidityChange
	Swaps            []domain.SwapEvent
}

func (r *PoolStateRepository) LoadPoolMetadata(
	ctx context.Context,
	poolAddress string,
) (domain.Pool, error) {
	if r == nil || r.db == nil {
		return domain.Pool{}, fmt.Errorf(
			"load local pool metadata: repository database is nil",
		)
	}

	return loadLocalPoolMetadata(
		ctx,
		r.db,
		normalizeLocalAnalysisAddress(poolAddress),
	)
}

func (r *PoolStateRepository) LocalIndexedHead(
	ctx context.Context,
	poolAddress string,
) (domain.IndexedHead, error) {
	if r == nil || r.db == nil {
		return domain.IndexedHead{}, fmt.Errorf(
			"load local indexed head: repository database is nil",
		)
	}

	address := normalizeLocalAnalysisAddress(poolAddress)
	if address == "" {
		return domain.IndexedHead{}, fmt.Errorf(
			"load local indexed head: pool address is required",
		)
	}

	var indexedThrough int64

	err := r.db.QueryRowContext(ctx, `
		SELECT LEAST(
			lp.last_completed_block,
			sw.last_completed_block
		)
		FROM indexer_checkpoints AS lp
		JOIN swap_indexer_checkpoints AS sw
		  ON sw.pool_address = lp.pool_address
		WHERE lp.pool_address = $1
	`, address).Scan(&indexedThrough)
	if err != nil {
		return domain.IndexedHead{}, fmt.Errorf(
			"load local indexed head for pool %s: %w",
			address,
			err,
		)
	}

	if indexedThrough <= 0 {
		return domain.IndexedHead{}, fmt.Errorf(
			"load local indexed head: invalid checkpoint %d",
			indexedThrough,
		)
	}

	return domain.IndexedHead{
		BlockNumber: uint64(indexedThrough),
	}, nil
}

// LoadLocalAnalysisBootstrap loads all state-changing data required by the
// analyzer in one repeatable-read transaction. requiredFromBlock is the
// earliest analysis block; the selected anchor is the latest stored snapshot
// at or before it.
func (r *PoolStateRepository) LoadLocalAnalysisBootstrap(
	ctx context.Context,
	poolAddress string,
	requiredFromBlock uint64,
) (LocalAnalysisBootstrap, error) {
	if r == nil || r.db == nil {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: repository database is nil",
		)
	}

	address := normalizeLocalAnalysisAddress(poolAddress)
	if address == "" {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: pool address is required",
		)
	}

	if requiredFromBlock == 0 || requiredFromBlock > math.MaxInt64 {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: invalid required-from block %d",
			requiredFromBlock,
		)
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		ReadOnly:  true,
		Isolation: sql.LevelRepeatableRead,
	})
	if err != nil {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: begin transaction: %w",
			err,
		)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	metadata, err := loadLocalPoolMetadata(ctx, tx, address)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	indexedThrough, swapFirstBlock, err := loadLocalAnalysisCoverage(
		ctx,
		tx,
		address,
	)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	if requiredFromBlock > indexedThrough {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: required-from block %d exceeds local indexed head %d",
			requiredFromBlock,
			indexedThrough,
		)
	}

	anchor, err := loadStoredSnapshotAtOrBefore(
		ctx,
		tx,
		address,
		requiredFromBlock,
	)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	if swapFirstBlock > anchor.BlockNumber+1 {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: swap coverage starts at %d after anchor block %d; re-run swap backfill with wider coverage",
			swapFirstBlock,
			anchor.BlockNumber,
		)
	}

	anchorInput, err := loadReconstructionInputInTx(
		ctx,
		tx,
		anchor,
	)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	snapshots, err := loadStoredSnapshotsAfterThrough(
		ctx,
		tx,
		address,
		anchor.BlockNumber,
		indexedThrough,
	)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	changes, err := loadLiquidityChangesBetweenBlocks(
		ctx,
		tx,
		address,
		anchor.BlockNumber,
		indexedThrough,
	)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	swaps, err := loadSwapsBetweenBlocks(
		ctx,
		tx,
		address,
		anchor.BlockNumber,
		indexedThrough,
	)
	if err != nil {
		return LocalAnalysisBootstrap{}, err
	}

	if err := tx.Commit(); err != nil {
		return LocalAnalysisBootstrap{}, fmt.Errorf(
			"load local analysis bootstrap: commit: %w",
			err,
		)
	}
	committed = true

	return LocalAnalysisBootstrap{
		Metadata:         metadata,
		IndexedThrough:   indexedThrough,
		AnchorInput:      anchorInput,
		Snapshots:        snapshots,
		LiquidityChanges: changes,
		Swaps:            swaps,
	}, nil
}

type localAnalysisQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadLocalPoolMetadata(
	ctx context.Context,
	q localAnalysisQueryer,
	poolAddress string,
) (domain.Pool, error) {
	if poolAddress == "" {
		return domain.Pool{}, fmt.Errorf(
			"load local pool metadata: pool address is required",
		)
	}

	var (
		pool         domain.Pool
		createdBlock int64
	)

	err := q.QueryRowContext(ctx, `
		SELECT
			address,
			token0_address,
			token1_address,
			token0_decimals,
			token1_decimals,
			fee_tier,
			tick_spacing,
			created_block
		FROM pools
		WHERE address = $1
	`, poolAddress).Scan(
		&pool.Address,
		&pool.Token0Address,
		&pool.Token1Address,
		&pool.Token0Decimals,
		&pool.Token1Decimals,
		&pool.FeeTier,
		&pool.TickSpacing,
		&createdBlock,
	)
	if err != nil {
		return domain.Pool{}, fmt.Errorf(
			"load local pool metadata for %s: %w",
			poolAddress,
			err,
		)
	}

	if createdBlock <= 0 {
		return domain.Pool{}, fmt.Errorf(
			"load local pool metadata: invalid creation block %d",
			createdBlock,
		)
	}

	pool.Address = normalizeLocalAnalysisAddress(pool.Address)
	pool.Token0Address = normalizeLocalAnalysisAddress(pool.Token0Address)
	pool.Token1Address = normalizeLocalAnalysisAddress(pool.Token1Address)
	pool.CreatedBlock = uint64(createdBlock)

	return pool, nil
}

func loadLocalAnalysisCoverage(
	ctx context.Context,
	q localAnalysisQueryer,
	poolAddress string,
) (uint64, uint64, error) {
	var (
		lpThrough   int64
		swapFirst   int64
		swapThrough int64
	)

	err := q.QueryRowContext(ctx, `
		SELECT
			lp.last_completed_block,
			sw.first_indexed_block,
			sw.last_completed_block
		FROM indexer_checkpoints AS lp
		JOIN swap_indexer_checkpoints AS sw
		  ON sw.pool_address = lp.pool_address
		WHERE lp.pool_address = $1
	`, poolAddress).Scan(
		&lpThrough,
		&swapFirst,
		&swapThrough,
	)
	if err != nil {
		return 0, 0, fmt.Errorf(
			"load local analysis coverage for pool %s: %w",
			poolAddress,
			err,
		)
	}

	if lpThrough <= 0 || swapFirst <= 0 || swapThrough < swapFirst-1 {
		return 0, 0, fmt.Errorf(
			"load local analysis coverage: invalid checkpoints lp=%d swap_first=%d swap_last=%d",
			lpThrough,
			swapFirst,
			swapThrough,
		)
	}

	through := lpThrough
	if swapThrough < through {
		through = swapThrough
	}

	if through <= 0 {
		return 0, 0, fmt.Errorf(
			"load local analysis coverage: non-positive common checkpoint %d",
			through,
		)
	}

	return uint64(through), uint64(swapFirst), nil
}

func loadStoredSnapshotAtOrBefore(
	ctx context.Context,
	q localAnalysisQueryer,
	poolAddress string,
	blockNumber uint64,
) (domain.PoolSnapshot, error) {
	row := q.QueryRowContext(ctx, `
		SELECT
			pool_address,
			block_number,
			sqrt_price_x96::text,
			tick,
			active_liquidity::text
		FROM pool_snapshots
		WHERE pool_address = $1
		  AND block_number <= $2
		ORDER BY block_number DESC
		LIMIT 1
	`, poolAddress, int64(blockNumber))

	snapshot, err := scanStoredPoolSnapshot(row)
	if err != nil {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"load stored snapshot at or before block %d: %w",
			blockNumber,
			err,
		)
	}

	return snapshot, nil
}

func loadStoredSnapshotsAfterThrough(
	ctx context.Context,
	q localAnalysisQueryer,
	poolAddress string,
	afterBlock uint64,
	throughBlock uint64,
) ([]domain.PoolSnapshot, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT
			pool_address,
			block_number,
			sqrt_price_x96::text,
			tick,
			active_liquidity::text
		FROM pool_snapshots
		WHERE pool_address = $1
		  AND block_number > $2
		  AND block_number <= $3
		ORDER BY block_number ASC
	`, poolAddress, int64(afterBlock), int64(throughBlock))
	if err != nil {
		return nil, fmt.Errorf(
			"load stored analysis snapshots: query: %w",
			err,
		)
	}
	defer rows.Close()

	result := make([]domain.PoolSnapshot, 0)
	for rows.Next() {
		snapshot, scanErr := scanStoredPoolSnapshot(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"load stored analysis snapshots: %w",
				scanErr,
			)
		}
		result = append(result, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"load stored analysis snapshots: iterate: %w",
			err,
		)
	}

	return result, nil
}

type localSnapshotScanner interface {
	Scan(...any) error
}

func scanStoredPoolSnapshot(
	scanner localSnapshotScanner,
) (domain.PoolSnapshot, error) {
	var (
		snapshot      domain.PoolSnapshot
		poolAddress   string
		blockNumber   int64
		sqrtText      string
		tick          sql.NullInt64
		liquidityText string
	)

	if err := scanner.Scan(
		&poolAddress,
		&blockNumber,
		&sqrtText,
		&tick,
		&liquidityText,
	); err != nil {
		return domain.PoolSnapshot{}, err
	}

	if blockNumber <= 0 || !tick.Valid {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"invalid stored snapshot block=%d tick_valid=%t",
			blockNumber,
			tick.Valid,
		)
	}

	sqrtPrice, err := decimal.NewFromString(sqrtText)
	if err != nil {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"parse stored sqrt price %q: %w",
			sqrtText,
			err,
		)
	}

	liquidity, err := decimal.NewFromString(liquidityText)
	if err != nil {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"parse stored liquidity %q: %w",
			liquidityText,
			err,
		)
	}

	snapshot.PoolAddress = normalizeLocalAnalysisAddress(poolAddress)
	snapshot.BlockNumber = uint64(blockNumber)
	snapshot.Tick = strconv.FormatInt(tick.Int64, 10)
	snapshot.SqrtPriceX96 = sqrtPrice
	snapshot.ActiveLiquidity = liquidity

	return snapshot, nil
}

func loadReconstructionInputInTx(
	ctx context.Context,
	tx *sql.Tx,
	snapshot domain.PoolSnapshot,
) (domain.ReconstructionInput, error) {
	currentTick, err := strconv.Atoi(snapshot.Tick)
	if err != nil {
		return domain.ReconstructionInput{}, fmt.Errorf(
			"load local anchor reconstruction: parse tick %q: %w",
			snapshot.Tick,
			err,
		)
	}

	sqrtPrice, err := decimalToBigInt(snapshot.SqrtPriceX96, "sqrt_price_x96")
	if err != nil {
		return domain.ReconstructionInput{}, err
	}

	liquidity, err := decimalToBigInt(snapshot.ActiveLiquidity, "active_liquidity")
	if err != nil {
		return domain.ReconstructionInput{}, err
	}

	changes, err := loadLiquidityChanges(
		ctx,
		tx,
		snapshot.PoolAddress,
		snapshot.BlockNumber,
	)
	if err != nil {
		return domain.ReconstructionInput{}, err
	}

	return domain.ReconstructionInput{
		Snapshot: domain.ReconstructionSnapshot{
			PoolAddress:  snapshot.PoolAddress,
			BlockNumber:  snapshot.BlockNumber,
			SqrtPriceX96: sqrtPrice,
			CurrentTick:  &currentTick,
			Liquidity:    liquidity,
		},
		Changes: changes,
	}, nil
}

func loadLiquidityChangesBetweenBlocks(
	ctx context.Context,
	q localAnalysisQueryer,
	poolAddress string,
	afterBlock uint64,
	throughBlock uint64,
) ([]domain.LiquidityChange, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT
			id,
			block_number,
			log_index,
			tick_lower,
			tick_upper,
			liquidity_delta::text
		FROM lp_actions
		WHERE pool_address = $1
		  AND block_number > $2
		  AND block_number <= $3
		  AND liquidity_delta <> 0
		ORDER BY block_number ASC, log_index ASC
	`, poolAddress, int64(afterBlock), int64(throughBlock))
	if err != nil {
		return nil, fmt.Errorf(
			"load local analysis liquidity events: query: %w",
			err,
		)
	}
	defer rows.Close()

	result := make([]domain.LiquidityChange, 0)
	for rows.Next() {
		var (
			change domain.LiquidityChange
			block  int64
			delta  string
		)

		if err := rows.Scan(
			&change.ID,
			&block,
			&change.LogIndex,
			&change.TickLower,
			&change.TickUpper,
			&delta,
		); err != nil {
			return nil, fmt.Errorf(
				"load local analysis liquidity events: scan: %w",
				err,
			)
		}

		if block <= 0 {
			return nil, fmt.Errorf(
				"load local analysis liquidity events: invalid block %d",
				block,
			)
		}

		change.BlockNumber = uint64(block)
		value, ok := newBigIntFromText(delta)
		if !ok {
			return nil, fmt.Errorf(
				"load local analysis liquidity events: invalid delta %q",
				delta,
			)
		}
		change.LiquidityDelta = value
		result = append(result, change)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"load local analysis liquidity events: iterate: %w",
			err,
		)
	}

	return result, nil
}

func loadSwapsBetweenBlocks(
	ctx context.Context,
	q localAnalysisQueryer,
	poolAddress string,
	afterBlock uint64,
	throughBlock uint64,
) ([]domain.SwapEvent, error) {
	rows, err := q.QueryContext(ctx, `
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
		  AND block_number > $2
		  AND block_number <= $3
		ORDER BY block_number ASC, log_index ASC
	`, poolAddress, int64(afterBlock), int64(throughBlock))
	if err != nil {
		return nil, fmt.Errorf(
			"load local analysis swaps: query: %w",
			err,
		)
	}
	defer rows.Close()

	return scanLocalSwapRows(rows, "load local analysis swaps")
}

func normalizeLocalAnalysisAddress(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func newBigIntFromText(value string) (*big.Int, bool) {
	return new(big.Int).SetString(value, 10)
}
