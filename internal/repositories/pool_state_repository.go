package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"

	"oracle/internal/domain"
)

type PoolStateRepository struct {
	db *sql.DB
}

func NewPoolStateRepository(db *sql.DB) *PoolStateRepository {
	return &PoolStateRepository{db: db}
}

func (r *PoolStateRepository) LoadLatestReconstructionInput(
	ctx context.Context,
	poolAddress string,
) (domain.ReconstructionInput, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		ReadOnly:  true,
		Isolation: sql.LevelRepeatableRead,
	})
	if err != nil {
		return domain.ReconstructionInput{}, fmt.Errorf(
			"load reconstruction input: begin transaction: %w",
			err,
		)
	}
	defer tx.Rollback()

	snapshot, err := loadLatestSnapshot(
		ctx,
		tx,
		poolAddress,
	)
	if err != nil {
		return domain.ReconstructionInput{}, err
	}

	changes, err := loadLiquidityChanges(
		ctx,
		tx,
		poolAddress,
		snapshot.BlockNumber,
	)
	if err != nil {
		return domain.ReconstructionInput{}, err
	}

	if err := tx.Commit(); err != nil {
		return domain.ReconstructionInput{}, fmt.Errorf(
			"load reconstruction input: commit transaction: %w",
			err,
		)
	}

	return domain.ReconstructionInput{
		Snapshot: snapshot,
		Changes:  changes,
	}, nil
}

func loadLatestSnapshot(
	ctx context.Context,
	tx *sql.Tx,
	poolAddress string,
) (domain.ReconstructionSnapshot, error) {
	var (
		blockNumber int64
		sqrtPrice   string
		tick        sql.NullInt64
		liquidity   string
	)

	err := tx.QueryRowContext(ctx, `
		SELECT
			block_number,
			sqrt_price_x96,
			tick,
			active_liquidity
		FROM pool_snapshots
		WHERE pool_address = $1
		ORDER BY block_number DESC
		LIMIT 1
	`, poolAddress).Scan(
		&blockNumber,
		&sqrtPrice,
		&tick,
		&liquidity,
	)
	if err != nil {
		return domain.ReconstructionSnapshot{}, fmt.Errorf(
			"load latest pool snapshot: %w",
			err,
		)
	}

	if blockNumber < 0 {
		return domain.ReconstructionSnapshot{}, fmt.Errorf(
			"load latest pool snapshot: negative block number %d",
			blockNumber,
		)
	}

	sqrtPriceX96, err := parseBigInt(
		sqrtPrice,
		"sqrt_price_x96",
	)
	if err != nil {
		return domain.ReconstructionSnapshot{}, err
	}

	poolLiquidity, err := parseBigInt(
		liquidity,
		"liquidity",
	)
	if err != nil {
		return domain.ReconstructionSnapshot{}, err
	}

	var currentTick *int

	if tick.Valid {
		value := int(tick.Int64)
		currentTick = &value
	}

	return domain.ReconstructionSnapshot{
		PoolAddress: poolAddress,
		BlockNumber: uint64(blockNumber),

		SqrtPriceX96: sqrtPriceX96,
		CurrentTick:  currentTick,
		Liquidity:    poolLiquidity,
	}, nil
}

func loadLiquidityChanges(
	ctx context.Context,
	tx *sql.Tx,
	poolAddress string,
	toBlock uint64,
) ([]domain.LiquidityChange, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			id,
			block_number,
			log_index,
			tick_lower,
			tick_upper,
			liquidity_delta
		FROM lp_actions
		WHERE pool_address = $1
		  AND block_number <= $2
		ORDER BY
			block_number ASC,
			log_index ASC
	`, poolAddress, toBlock)
	if err != nil {
		return nil, fmt.Errorf(
			"load liquidity changes: query: %w",
			err,
		)
	}
	defer rows.Close()

	changes := make([]domain.LiquidityChange, 0)

	for rows.Next() {
		var (
			change      domain.LiquidityChange
			blockNumber int64
			delta       string
		)

		if err := rows.Scan(
			&change.ID,
			&blockNumber,
			&change.LogIndex,
			&change.TickLower,
			&change.TickUpper,
			&delta,
		); err != nil {
			return nil, fmt.Errorf(
				"load liquidity changes: scan: %w",
				err,
			)
		}

		if blockNumber < 0 {
			return nil, fmt.Errorf(
				"liquidity change %s has negative block number %d",
				change.ID,
				blockNumber,
			)
		}

		change.BlockNumber = uint64(blockNumber)

		change.LiquidityDelta, err = parseBigInt(
			delta,
			"liquidity_delta",
		)
		if err != nil {
			return nil, fmt.Errorf(
				"load liquidity change %s: %w",
				change.ID,
				err,
			)
		}

		changes = append(changes, change)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"load liquidity changes: iterate: %w",
			err,
		)
	}

	return changes, nil
}

func parseBigInt(
	value string,
	field string,
) (*big.Int, error) {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf(
			"parse %s as integer: %q",
			field,
			value,
		)
	}

	return result, nil
}
