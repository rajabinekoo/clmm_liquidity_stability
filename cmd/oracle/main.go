package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"oracle/internal/database"
	"oracle/internal/providers"
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

	db, err := database.OpenPostgresDB(config.PostgresURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := database.MigrationFS(
		db,
		migrations.FS,
		".",
	); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	graphClient := providers.New(
		config.TheGraphAPIKey,
		config.TheGraphTimeout,
	)
	eventRepository := repositories.NewEventRepository(db)

	indexer := services.NewIndexer(
		config,
		graphClient,
		eventRepository,
	)

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	return indexer.Run(ctx)
}
