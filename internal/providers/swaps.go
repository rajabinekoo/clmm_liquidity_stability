package providers

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/utils"
)

type rawSwap struct {
	ID           string         `json:"id"`
	Amount0      string         `json:"amount0"`
	Amount1      string         `json:"amount1"`
	SqrtPriceX96 string         `json:"sqrtPriceX96"`
	Tick         string         `json:"tick"`
	LogIndex     string         `json:"logIndex"`
	Timestamp    string         `json:"timestamp"`
	Transaction  rawTransaction `json:"transaction"`
}

func (c *Client) FetchSwapsPage(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
	token0Decimals int,
	token1Decimals int,
) ([]domain.SwapEvent, error) {
	normalizedPoolAddress :=
		strings.ToLower(
			strings.TrimSpace(
				poolAddress,
			),
		)

	if normalizedPoolAddress == "" {
		return nil, fmt.Errorf(
			"fetch swaps: pool address is required",
		)
	}

	if fromBlock == 0 {
		return nil, fmt.Errorf(
			"fetch swaps: from block must be greater than zero",
		)
	}

	if fromBlock > toBlock {
		return nil, fmt.Errorf(
			"fetch swaps: from block %d exceeds to block %d",
			fromBlock,
			toBlock,
		)
	}

	if limit <= 0 || limit > 1_000 {
		return nil, fmt.Errorf(
			"fetch swaps: limit must be between 1 and 1000",
		)
	}

	if err := validateTokenDecimals(
		"token0 decimals",
		token0Decimals,
	); err != nil {
		return nil, err
	}

	if err := validateTokenDecimals(
		"token1 decimals",
		token1Decimals,
	); err != nil {
		return nil, err
	}

	type responseData struct {
		Swaps []rawSwap `json:"swaps"`
	}

	data, err := execute[responseData](
		ctx,
		c,
		utils.GenerateSwapsQuery(
			normalizedPoolAddress,
			fromBlock,
			toBlock,
			strings.TrimSpace(afterID),
			limit,
		),
	)
	if err != nil {
		return nil, err
	}

	swaps := make(
		[]domain.SwapEvent,
		0,
		len(data.Swaps),
	)

	for index, raw := range data.Swaps {
		swap, err := mapRawSwap(
			normalizedPoolAddress,
			raw,
			token0Decimals,
			token1Decimals,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"map swap page item %d id=%q: %w",
				index,
				raw.ID,
				err,
			)
		}

		swaps = append(
			swaps,
			swap,
		)
	}

	return swaps, nil
}

func mapRawSwap(
	poolAddress string,
	raw rawSwap,
	token0Decimals int,
	token1Decimals int,
) (domain.SwapEvent, error) {
	if err := validateTokenDecimals(
		"token0 decimals",
		token0Decimals,
	); err != nil {
		return domain.SwapEvent{}, err
	}

	if err := validateTokenDecimals(
		"token1 decimals",
		token1Decimals,
	); err != nil {
		return domain.SwapEvent{}, err
	}

	blockNumber, err := parseUintString(
		raw.Transaction.BlockNumber.String(),
	)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse block number: %w",
			err,
		)
	}

	logIndex, err := parseNonNegativeIntString(
		raw.LogIndex,
	)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse log index: %w",
			err,
		)
	}

	timestamp, err := parseUintString(
		raw.Timestamp,
	)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse timestamp: %w",
			err,
		)
	}

	tick, err := parseSignedIntString(
		raw.Tick,
	)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse tick: %w",
			err,
		)
	}

	amount0Raw, err := tokenDecimalToRaw(
		raw.Amount0,
		token0Decimals,
	)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse amount0: %w",
			err,
		)
	}

	amount1Raw, err := tokenDecimalToRaw(
		raw.Amount1,
		token1Decimals,
	)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse amount1: %w",
			err,
		)
	}

	sqrtPriceText :=
		strings.TrimSpace(
			raw.SqrtPriceX96,
		)

	sqrtPriceX96, ok :=
		new(big.Int).SetString(
			sqrtPriceText,
			10,
		)
	if !ok {
		return domain.SwapEvent{}, fmt.Errorf(
			"parse sqrtPriceX96 as integer: %q",
			raw.SqrtPriceX96,
		)
	}

	event := domain.SwapEvent{
		ID: strings.TrimSpace(
			raw.ID,
		),

		TxHash: strings.ToLower(
			strings.TrimSpace(
				raw.Transaction.ID,
			),
		),

		PoolAddress: strings.ToLower(
			strings.TrimSpace(
				poolAddress,
			),
		),

		BlockNumber: blockNumber,

		LogIndex: logIndex,

		Timestamp: timestamp,

		Amount0Raw: amount0Raw,

		Amount1Raw: amount1Raw,

		SqrtPriceX96After: sqrtPriceX96,

		TickAfter: tick,
	}

	if err := event.Validate(); err != nil {
		return domain.SwapEvent{}, fmt.Errorf(
			"validate mapped swap: %w",
			err,
		)
	}

	return event, nil
}

// tokenDecimalToRaw performs an exact conversion from a The Graph BigDecimal
// token amount to the integer raw token amount.
//
// It deliberately rejects excess fractional precision. Truncating such a value
// would silently modify the observed on-chain Swap amount and invalidate the
// simulator parity test.
func tokenDecimalToRaw(
	value string,
	decimals int,
) (*big.Int, error) {
	if err := validateTokenDecimals(
		"token decimals",
		decimals,
	); err != nil {
		return nil, err
	}

	normalized :=
		strings.TrimSpace(value)

	if normalized == "" {
		return nil, fmt.Errorf(
			"token decimal amount is empty",
		)
	}

	amount, err :=
		decimal.NewFromString(
			normalized,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"parse token decimal %q: %w",
			value,
			err,
		)
	}

	scaled :=
		amount.Shift(
			int32(decimals),
		)

	integerScaled :=
		scaled.Truncate(0)

	if !scaled.Equal(integerScaled) {
		return nil, fmt.Errorf(
			"token decimal %q has more than %d fractional digits",
			value,
			decimals,
		)
	}

	rawText :=
		integerScaled.StringFixed(0)

	result, ok :=
		new(big.Int).SetString(
			rawText,
			10,
		)
	if !ok {
		return nil, fmt.Errorf(
			"convert exact raw token amount %q to integer",
			rawText,
		)
	}

	return result, nil
}

func validateTokenDecimals(
	fieldName string,
	decimals int,
) error {
	if decimals < 0 || decimals > 255 {
		return fmt.Errorf(
			"%s must be between 0 and 255: %d",
			fieldName,
			decimals,
		)
	}

	return nil
}

func parseUintString(
	value string,
) (uint64, error) {
	normalized :=
		strings.TrimSpace(value)

	if normalized == "" {
		return 0, fmt.Errorf(
			"unsigned integer is empty",
		)
	}

	result, err := strconv.ParseUint(
		normalized,
		10,
		64,
	)
	if err != nil {
		return 0, err
	}

	return result, nil
}

func parseNonNegativeIntString(
	value string,
) (int, error) {
	parsed, err := parseUintString(value)
	if err != nil {
		return 0, err
	}

	maximumInt :=
		uint64(^uint(0) >> 1)

	if parsed > maximumInt {
		return 0, fmt.Errorf(
			"value %d exceeds platform int",
			parsed,
		)
	}

	return int(parsed), nil
}

func parseSignedIntString(
	value string,
) (int, error) {
	normalized :=
		strings.TrimSpace(value)

	if normalized == "" {
		return 0, fmt.Errorf(
			"signed integer is empty",
		)
	}

	parsed, err := strconv.ParseInt(
		normalized,
		10,
		64,
	)
	if err != nil {
		return 0, err
	}

	maximumInt :=
		int64(^uint(0) >> 1)

	minimumInt :=
		-maximumInt - 1

	if parsed < minimumInt ||
		parsed > maximumInt {
		return 0, fmt.Errorf(
			"value %d exceeds platform int",
			parsed,
		)
	}

	return int(parsed), nil
}
