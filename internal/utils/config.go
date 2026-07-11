package utils

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	PoolAddress    string `env:"POOL_ADDRESS,required"`
	TheGraphAPIKey string `env:"THE_GRAPH_API_KEY,required"`
	PostgresURL    string `env:"POSTGRES_URL,required"`

	WindowSize        uint64        `env:"WINDOW_SIZE" envDefault:"10000"`
	PageSize          int           `env:"PAGE_SIZE" envDefault:"1000"`
	ConfirmationDepth uint64        `env:"CONFIRMATION_DEPTH" envDefault:"20"`
	PollInterval      time.Duration `env:"POLL_INTERVAL" envDefault:"5s"`
	TheGraphTimeout   time.Duration `env:"THE_GRAPH_TIMEOUT" envDefault:"2m"`
}

func load[T any](dst *T) error {
	if err := env.Parse(dst); err != nil {
		return fmt.Errorf("config: parse env: %w", err)
	}

	return nil
}

func LoadConfig() (Config, error) {
	err := godotenv.Load()
	if err != nil {
		return Config{}, errors.New("Error loading .env file")
	}

	var cfg Config

	if err := load(&cfg); err != nil {
		return Config{}, err
	}

	cfg.PoolAddress = strings.ToLower(cfg.PoolAddress)

	if cfg.WindowSize == 0 {
		return Config{}, fmt.Errorf("config: WINDOW_SIZE must be greater than zero")
	}

	if cfg.PageSize <= 0 || cfg.PageSize > 1000 {
		return Config{}, fmt.Errorf(
			"config: PAGE_SIZE must be between 1 and 1000",
		)
	}

	if cfg.PollInterval <= 0 {
		return Config{}, fmt.Errorf(
			"config: POLL_INTERVAL must be greater than zero",
		)
	}

	if cfg.TheGraphTimeout <= 0 {
		return Config{}, fmt.Errorf(
			"config: THE_GRAPH_TIMEOUT must be greater than zero",
		)
	}

	return cfg, nil
}
