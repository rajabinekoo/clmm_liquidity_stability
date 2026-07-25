package utils

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

type Config struct {
	PoolName    string `env:"POOL_NAME,required"`
	PoolAddress string `env:"POOL_ADDRESS,required"`
	PoolFee     int64  `env:"POOL_FEE,required"`

	Token0Symbol   string `env:"TOKEN0_SYMBOL,required"`
	Token1Symbol   string `env:"TOKEN1_SYMBOL,required"`
	Token0Decimals int    `env:"TOKEN0_DECIMALS,required"`
	Token1Decimals int    `env:"TOKEN1_DECIMALS,required"`

	AmountGridToken0 string `env:"AMOUNT_GRID_TOKEN0,required"`

	TheGraphAPIKey string `env:"THE_GRAPH_API_KEY,required"`
	PostgresURL    string `env:"POSTGRES_URL,required"`

	OutputBaseDir string `env:"OUTPUT_BASE_DIR" envDefault:"outputs"`

	WindowSize        uint64        `env:"WINDOW_SIZE" envDefault:"10000"`
	PageSize          int           `env:"PAGE_SIZE" envDefault:"1000"`
	ConfirmationDepth uint64        `env:"CONFIRMATION_DEPTH" envDefault:"20"`
	PollInterval      time.Duration `env:"POLL_INTERVAL" envDefault:"5s"`
	TheGraphTimeout   time.Duration `env:"THE_GRAPH_TIMEOUT" envDefault:"2m"`

	LookbackBlocks      uint64 `env:"LOOKBACK_BLOCKS" envDefault:"300000"`
	StepBlocks          uint64 `env:"STEP_BLOCKS" envDefault:"10000"`
	MaxSnapshots        int    `env:"MAX_SNAPSHOTS" envDefault:"30"`
	PositionLimit       int    `env:"POSITION_LIMIT" envDefault:"20"`
	PositionCoverageBps int64  `env:"POSITION_COVERAGE_BPS" envDefault:"9500"`

	ValidationSamples         int    `env:"VALIDATION_SAMPLES" envDefault:"30"`
	ValidationBlockWindowSize uint64 `env:"VALIDATION_BLOCK_WINDOW_SIZE" envDefault:"8"`

	BurnPageSize      int `env:"BURN_PAGE_SIZE" envDefault:"1000"`
	BurnSwapPageSize  int `env:"BURN_SWAP_PAGE_SIZE" envDefault:"1000"`
	BurnMaxCandidates int `env:"BURN_MAX_CANDIDATES" envDefault:"50000"`
	BurnMaxSamples    int `env:"BURN_MAX_SAMPLES" envDefault:"30"`

	BurnSamplingBins int    `env:"BURN_SAMPLING_BINS" envDefault:"30"`
	BurnSamplingSeed uint64 `env:"BURN_SAMPLING_SEED" envDefault:"20260725"`

	BurnMinimumSpacingBlocks uint64 `env:"BURN_MINIMUM_SPACING_BLOCKS" envDefault:"7200"`

	BurnRequireMaxSamples bool `env:"BURN_REQUIRE_MAX_SAMPLES" envDefault:"true"`
}

type ExportedPoolConfig struct {
	PoolName    string `json:"pool_name"`
	PoolAddress string `json:"pool_address"`
	PoolFee     int64  `json:"pool_fee"`

	Token0Symbol   string `json:"token0_symbol"`
	Token1Symbol   string `json:"token1_symbol"`
	Token0Decimals int    `json:"token0_decimals"`
	Token1Decimals int    `json:"token1_decimals"`

	AmountGridToken0Human []string `json:"amount_grid_token0_human"`
	AmountGridToken0Raw   []string `json:"amount_grid_token0_raw"`

	WindowSize        uint64 `json:"window_size"`
	PageSize          int    `json:"page_size"`
	ConfirmationDepth uint64 `json:"confirmation_depth"`

	LookbackBlocks      uint64 `json:"lookback_blocks"`
	StepBlocks          uint64 `json:"step_blocks"`
	MaxSnapshots        int    `json:"max_snapshots"`
	PositionLimit       int    `json:"position_limit"`
	PositionCoverageBps int64  `json:"position_coverage_bps"`

	ValidationSamples         int    `json:"validation_samples"`
	ValidationBlockWindowSize uint64 `json:"validation_block_window_size"`

	BurnPageSize      int `env:"BURN_PAGE_SIZE" envDefault:"1000"`
	BurnSwapPageSize  int `env:"BURN_SWAP_PAGE_SIZE" envDefault:"1000"`
	BurnMaxCandidates int `env:"BURN_MAX_CANDIDATES" envDefault:"50000"`
	BurnMaxSamples    int `env:"BURN_MAX_SAMPLES" envDefault:"30"`

	BurnSamplingBins int    `env:"BURN_SAMPLING_BINS" envDefault:"30"`
	BurnSamplingSeed uint64 `env:"BURN_SAMPLING_SEED" envDefault:"20260725"`

	BurnMinimumSpacingBlocks uint64 `json:"burn_minimum_spacing_blocks"`

	BurnRequireMaxSamples bool `json:"burn_require_max_samples"`
}

func LoadConfig() (Config, error) {
	envFile := strings.TrimSpace(os.Getenv("ENV_FILE"))
	if envFile == "" {
		envFile = ".env"
	}

	if err := godotenv.Load(envFile); err != nil {
		return Config{}, fmt.Errorf("config: load env file %q: %w", envFile, err)
	}

	var cfg Config

	if err := load(&cfg); err != nil {
		return Config{}, err
	}

	cfg.PoolAddress = strings.ToLower(strings.TrimSpace(cfg.PoolAddress))
	cfg.PoolName = strings.TrimSpace(cfg.PoolName)
	cfg.Token0Symbol = strings.ToUpper(strings.TrimSpace(cfg.Token0Symbol))
	cfg.Token1Symbol = strings.ToUpper(strings.TrimSpace(cfg.Token1Symbol))

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func load[T any](dst *T) error {
	if err := env.Parse(dst); err != nil {
		return fmt.Errorf("config: parse env: %w", err)
	}

	return nil
}

func (c Config) Validate() error {
	if c.PoolAddress == "" {
		return fmt.Errorf("config: POOL_ADDRESS is required")
	}

	if !isValidUniswapV3Fee(c.PoolFee) {
		return fmt.Errorf(
			"config: POOL_FEE must be one of 100, 500, 3000, 10000",
		)
	}

	if c.Token0Decimals < 0 || c.Token0Decimals > 30 {
		return fmt.Errorf(
			"config: TOKEN0_DECIMALS must be between 0 and 30",
		)
	}

	if c.Token1Decimals < 0 || c.Token1Decimals > 30 {
		return fmt.Errorf(
			"config: TOKEN1_DECIMALS must be between 0 and 30",
		)
	}

	if _, err := c.AmountGridToken0Raw(); err != nil {
		return err
	}

	if c.WindowSize == 0 {
		return fmt.Errorf("config: WINDOW_SIZE must be greater than zero")
	}

	if c.PageSize <= 0 || c.PageSize > 1000 {
		return fmt.Errorf(
			"config: PAGE_SIZE must be between 1 and 1000",
		)
	}

	if c.PollInterval <= 0 {
		return fmt.Errorf(
			"config: POLL_INTERVAL must be greater than zero",
		)
	}

	if c.TheGraphTimeout <= 0 {
		return fmt.Errorf(
			"config: THE_GRAPH_TIMEOUT must be greater than zero",
		)
	}

	if c.LookbackBlocks == 0 {
		return fmt.Errorf(
			"config: LOOKBACK_BLOCKS must be greater than zero",
		)
	}

	if c.StepBlocks == 0 {
		return fmt.Errorf(
			"config: STEP_BLOCKS must be greater than zero",
		)
	}

	if c.MaxSnapshots <= 0 {
		return fmt.Errorf(
			"config: MAX_SNAPSHOTS must be greater than zero",
		)
	}

	if c.PositionLimit <= 0 {
		return fmt.Errorf(
			"config: POSITION_LIMIT must be greater than zero",
		)
	}

	if c.PositionCoverageBps < 1 ||
		c.PositionCoverageBps > 10_000 {
		return fmt.Errorf(
			"config: POSITION_COVERAGE_BPS must be between 1 and 10000",
		)
	}

	if c.ValidationSamples <= 0 {
		return fmt.Errorf(
			"config: VALIDATION_SAMPLES must be greater than zero",
		)
	}

	if c.ValidationBlockWindowSize == 0 {
		return fmt.Errorf(
			"config: VALIDATION_BLOCK_WINDOW_SIZE must be greater than zero",
		)
	}

	if c.BurnPageSize <= 0 ||
		c.BurnPageSize > 1000 {
		return fmt.Errorf(
			"config: BURN_PAGE_SIZE must be between 1 and 1000",
		)
	}

	if c.BurnSwapPageSize <= 0 ||
		c.BurnSwapPageSize > 1000 {
		return fmt.Errorf(
			"config: BURN_SWAP_PAGE_SIZE must be between 1 and 1000",
		)
	}

	if c.BurnMaxCandidates <= 0 {
		return fmt.Errorf(
			"config: BURN_MAX_CANDIDATES must be greater than zero",
		)
	}

	if c.BurnMaxSamples <= 0 {
		return fmt.Errorf(
			"config: BURN_MAX_SAMPLES must be greater than zero",
		)
	}

	if c.BurnMaxSamples >
		c.BurnMaxCandidates {
		return fmt.Errorf(
			"config: BURN_MAX_SAMPLES must not exceed BURN_MAX_CANDIDATES",
		)
	}

	if c.BurnSamplingBins < 0 {
		return fmt.Errorf(
			"config: BURN_SAMPLING_BINS must not be negative",
		)
	}

	if c.BurnSamplingBins > c.BurnMaxSamples {
		return fmt.Errorf(
			"config: BURN_SAMPLING_BINS must not exceed BURN_MAX_SAMPLES",
		)
	}

	return nil
}

func (c Config) OutputDir() string {
	return filepath.Join(
		c.OutputBaseDir,
		slugify(c.PoolName),
	)
}

func (c Config) AmountGridToken0Raw() ([]*big.Int, error) {
	return parseHumanAmountGridToRaw(
		c.AmountGridToken0,
		c.Token0Decimals,
	)
}

func WritePoolConfigJSON(
	path string,
	cfg Config,
) error {
	rawAmounts, err := cfg.AmountGridToken0Raw()
	if err != nil {
		return err
	}

	exported := ExportedPoolConfig{
		PoolName:    cfg.PoolName,
		PoolAddress: cfg.PoolAddress,
		PoolFee:     cfg.PoolFee,

		Token0Symbol:   cfg.Token0Symbol,
		Token1Symbol:   cfg.Token1Symbol,
		Token0Decimals: cfg.Token0Decimals,
		Token1Decimals: cfg.Token1Decimals,

		AmountGridToken0Human: splitAmountGrid(cfg.AmountGridToken0),
		AmountGridToken0Raw:   bigIntSliceToStringSlice(rawAmounts),

		WindowSize:        cfg.WindowSize,
		PageSize:          cfg.PageSize,
		ConfirmationDepth: cfg.ConfirmationDepth,

		LookbackBlocks: cfg.LookbackBlocks,
		StepBlocks:     cfg.StepBlocks,
		MaxSnapshots:   cfg.MaxSnapshots,
		PositionLimit:  cfg.PositionLimit,

		ValidationSamples:         cfg.ValidationSamples,
		ValidationBlockWindowSize: cfg.ValidationBlockWindowSize,

		BurnPageSize: cfg.BurnPageSize,

		BurnSwapPageSize: cfg.BurnSwapPageSize,

		BurnMaxCandidates: cfg.BurnMaxCandidates,

		BurnMaxSamples: cfg.BurnMaxSamples,

		BurnSamplingBins: cfg.BurnSamplingBins,

		BurnSamplingSeed: cfg.BurnSamplingSeed,

		PositionCoverageBps: cfg.PositionCoverageBps,

		BurnMinimumSpacingBlocks: cfg.BurnMinimumSpacingBlocks,

		BurnRequireMaxSamples: cfg.BurnRequireMaxSamples,
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("write pool config: create directory: %w", err)
		}
	}

	payload, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return fmt.Errorf("write pool config: marshal json: %w", err)
	}

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write pool config: write file: %w", err)
	}

	return nil
}

func isValidUniswapV3Fee(fee int64) bool {
	switch fee {
	case 100, 500, 3000, 10000:
		return true
	default:
		return false
	}
}

func parseHumanAmountGridToRaw(
	value string,
	decimals int,
) ([]*big.Int, error) {
	parts := splitAmountGrid(value)
	if len(parts) == 0 {
		return nil, fmt.Errorf("config: AMOUNT_GRID_TOKEN0 is empty")
	}

	amounts := make([]*big.Int, 0, len(parts))

	for _, part := range parts {
		amount, err := decimal.NewFromString(part)
		if err != nil {
			return nil, fmt.Errorf(
				"config: invalid AMOUNT_GRID_TOKEN0 value %q: %w",
				part,
				err,
			)
		}

		if amount.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf(
				"config: AMOUNT_GRID_TOKEN0 value %q must be greater than zero",
				part,
			)
		}

		raw := amount.Shift(int32(decimals))
		truncated := raw.Truncate(0)

		if !raw.Equal(truncated) {
			return nil, fmt.Errorf(
				"config: AMOUNT_GRID_TOKEN0 value %q has more decimals than TOKEN0_DECIMALS=%d",
				part,
				decimals,
			)
		}

		rawInt, ok := new(big.Int).SetString(truncated.StringFixed(0), 10)
		if !ok {
			return nil, fmt.Errorf(
				"config: cannot convert AMOUNT_GRID_TOKEN0 value %q to raw integer",
				part,
			)
		}

		amounts = append(amounts, rawInt)
	}

	return amounts, nil
}

func splitAmountGrid(value string) []string {
	parts := strings.Split(value, ",")

	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		result = append(result, trimmed)
	}

	return result
}

func bigIntSliceToStringSlice(values []*big.Int) []string {
	result := make([]string, 0, len(values))

	for _, value := range values {
		if value == nil {
			result = append(result, "0")
			continue
		}

		result = append(result, value.String())
	}

	return result
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	var builder strings.Builder
	previousWasSeparator := false

	for _, r := range value {
		isLetter := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'

		if isLetter || isDigit {
			builder.WriteRune(r)
			previousWasSeparator = false
			continue
		}

		if !previousWasSeparator {
			builder.WriteRune('_')
			previousWasSeparator = true
		}
	}

	slug := strings.Trim(builder.String(), "_")
	if slug == "" {
		return "pool"
	}

	return slug
}
