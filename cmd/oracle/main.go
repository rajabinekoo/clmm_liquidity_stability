package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"oracle/internal/database"
	"oracle/internal/repositories"
	"oracle/internal/services"
	"oracle/internal/utils"
	"oracle/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error(
			"oracle exited with error",
			"error", err,
		)

		os.Exit(1)
	}
}

func run() error {
	config, err := utils.LoadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	db, err := database.OpenPostgresDB(
		config.PostgresURL,
	)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := database.MigrationFS(
		db,
		migrations.FS,
		".",
	); err != nil {
		return fmt.Errorf(
			"run migrations: %w",
			err,
		)
	}

	poolStateRepository :=
		repositories.NewPoolStateRepository(db)

	poolReconstructor :=
		services.NewPoolReconstructor(
			poolStateRepository,
		)

	pool, err := poolReconstructor.ReconstructLatest(
		ctx,
		config.PoolAddress,
	)
	if err != nil {
		return fmt.Errorf(
			"reconstruct pool: %w",
			err,
		)
	}

	slog.Info(
		"pool reconstructed",
		"pool_address", pool.PoolAddress,
		"block_number", pool.BlockNumber,
		"current_tick", pool.CurrentTick,
		"sqrt_price_x96", pool.SqrtPriceX96.String(),
		"liquidity", pool.Liquidity.String(),
		"initialized_ticks", len(pool.InitializedTicks),
	)

	return nil
}
