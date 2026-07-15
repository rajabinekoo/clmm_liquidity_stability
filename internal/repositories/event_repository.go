package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type EventRepository struct {
	db *sql.DB
}

func NewEventRepository(db *sql.DB) *EventRepository {
	return &EventRepository{
		db: db,
	}
}

func (r *EventRepository) BootstrapPool(
	ctx context.Context,
	pool domain.Pool,
) error {
	if pool.CreatedBlock == 0 {
		return fmt.Errorf("bootstrap pool: created block cannot be zero")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bootstrap pool: begin transaction: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pools (
			address,
			token0_address,
			token1_address,
			token0_decimals,
			token1_decimals,
			fee_tier,
			tick_spacing,
			created_block
		)
		VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8
		)
		ON CONFLICT (address)
		DO UPDATE SET
			token0_address = EXCLUDED.token0_address,
			token1_address = EXCLUDED.token1_address,
			token0_decimals = EXCLUDED.token0_decimals,
			token1_decimals = EXCLUDED.token1_decimals,
			fee_tier = EXCLUDED.fee_tier,
			tick_spacing = EXCLUDED.tick_spacing,
			created_block = EXCLUDED.created_block
	`,
		pool.Address,
		pool.Token0Address,
		pool.Token1Address,
		pool.Token0Decimals,
		pool.Token1Decimals,
		pool.FeeTier,
		pool.TickSpacing,
		pool.CreatedBlock,
	)
	if err != nil {
		return fmt.Errorf("bootstrap pool: upsert pool: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO indexer_checkpoints (
			pool_address,
			last_completed_block
		)
		VALUES ($1, $2)
		ON CONFLICT (pool_address)
		DO NOTHING
	`,
		pool.Address,
		pool.CreatedBlock-1,
	)
	if err != nil {
		return fmt.Errorf(
			"bootstrap pool: create checkpoint: %w",
			err,
		)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(
			"bootstrap pool: commit transaction: %w",
			err,
		)
	}

	committed = true

	return nil
}

func (r *EventRepository) Checkpoint(
	ctx context.Context,
	poolAddress string,
) (uint64, error) {
	var blockNumber int64

	err := r.db.QueryRowContext(ctx, `
		SELECT last_completed_block
		FROM indexer_checkpoints
		WHERE pool_address = $1
	`, poolAddress).Scan(&blockNumber)
	if err != nil {
		return 0, fmt.Errorf(
			"get checkpoint for pool %s: %w",
			poolAddress,
			err,
		)
	}

	if blockNumber < 0 {
		return 0, fmt.Errorf(
			"invalid negative checkpoint for pool %s: %d",
			poolAddress,
			blockNumber,
		)
	}

	return uint64(blockNumber), nil
}

func (r *EventRepository) StoreWindow(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	actions []domain.LPAction,
	snapshot domain.PoolSnapshot,
) error {
	if fromBlock > toBlock {
		return fmt.Errorf(
			"store window: invalid block range %d-%d",
			fromBlock,
			toBlock,
		)
	}

	if snapshot.PoolAddress != poolAddress {
		return fmt.Errorf(
			"store window: snapshot pool %s does not match %s",
			snapshot.PoolAddress,
			poolAddress,
		)
	}

	if snapshot.BlockNumber != toBlock {
		return fmt.Errorf(
			"store window: snapshot block %d does not match window end %d",
			snapshot.BlockNumber,
			toBlock,
		)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"store window: begin transaction: %w",
			err,
		)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := lockAndValidateCheckpoint(
		ctx,
		tx,
		poolAddress,
		fromBlock,
	); err != nil {
		return err
	}

	if err := insertLPActions(ctx, tx, actions); err != nil {
		return err
	}

	if err := upsertPoolSnapshot(
		ctx,
		tx,
		snapshot,
	); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE indexer_checkpoints
		SET
			last_completed_block = $2,
			updated_at = NOW()
		WHERE pool_address = $1
	`,
		poolAddress,
		toBlock,
	)
	if err != nil {
		return fmt.Errorf(
			"store window: update checkpoint: %w",
			err,
		)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"store window: get affected rows: %w",
			err,
		)
	}

	if affected != 1 {
		return fmt.Errorf(
			"store window: checkpoint not found for pool %s",
			poolAddress,
		)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(
			"store window: commit transaction: %w",
			err,
		)
	}

	committed = true

	return nil
}

func lockAndValidateCheckpoint(
	ctx context.Context,
	tx *sql.Tx,
	poolAddress string,
	fromBlock uint64,
) error {
	var lastCompletedBlock int64

	err := tx.QueryRowContext(ctx, `
		SELECT last_completed_block
		FROM indexer_checkpoints
		WHERE pool_address = $1
		FOR UPDATE
	`, poolAddress).Scan(&lastCompletedBlock)
	if err != nil {
		return fmt.Errorf(
			"store window: lock checkpoint: %w",
			err,
		)
	}

	if lastCompletedBlock < 0 {
		return fmt.Errorf(
			"store window: invalid negative checkpoint: %d",
			lastCompletedBlock,
		)
	}

	expectedFromBlock := uint64(lastCompletedBlock) + 1

	if fromBlock != expectedFromBlock {
		return fmt.Errorf(
			"store window: non-contiguous range: expected from block %d, received %d",
			expectedFromBlock,
			fromBlock,
		)
	}

	return nil
}

func insertLPActions(
	ctx context.Context,
	tx *sql.Tx,
	actions []domain.LPAction,
) error {
	if len(actions) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO lp_actions (
			id,
			pool_address,
			action,
			tx_hash,
			block_number,
			log_index,
			timestamp,
			owner,
			sender,
			origin,
			tick_lower,
			tick_upper,
			liquidity_delta,
			amount0,
			amount1,
			amount_usd
		)
		VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12,
			$13::numeric,
			$14::numeric,
			$15::numeric,
			$16
		)
		ON CONFLICT (id)
		DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf(
			"insert lp actions: prepare statement: %w",
			err,
		)
	}
	defer stmt.Close()

	for _, action := range actions {
		amountUSD, _ := action.AmountUSD.Float64()

		_, err := stmt.ExecContext(
			ctx,
			action.ID,
			action.PoolAddress,
			int16(action.Action),
			action.TxHash,
			action.BlockNumber,
			action.LogIndex,
			action.Timestamp,
			action.Owner,
			action.Sender,
			action.Origin,
			action.TickLower,
			action.TickUpper,
			action.LiquidityDelta.String(),
			action.Amount0.String(),
			action.Amount1.String(),
			amountUSD,
		)
		if err != nil {
			return fmt.Errorf(
				"insert lp action %s: %w",
				action.ID,
				err,
			)
		}
	}

	return nil
}

func upsertPoolSnapshot(
	ctx context.Context,
	tx *sql.Tx,
	snapshot domain.PoolSnapshot,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO pool_snapshots (
			pool_address,
			block_number,
			sqrt_price_x96,
			tick,
			active_liquidity
		)
		VALUES (
			$1,
			$2,
			$3::numeric,
			$4,
			$5::numeric
		)
		ON CONFLICT (pool_address, block_number)
		DO UPDATE SET
			sqrt_price_x96 = EXCLUDED.sqrt_price_x96,
			tick = EXCLUDED.tick,
			active_liquidity = EXCLUDED.active_liquidity,
			created_at = NOW()
	`,
		snapshot.PoolAddress,
		snapshot.BlockNumber,
		snapshot.SqrtPriceX96,
		snapshot.Tick,
		snapshot.ActiveLiquidity,
	)
	if err != nil {
		return fmt.Errorf(
			"upsert pool snapshot at block %d: %w",
			snapshot.BlockNumber,
			err,
		)
	}

	return nil
}
