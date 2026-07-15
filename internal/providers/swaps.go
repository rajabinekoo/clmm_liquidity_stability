package providers

import (
	"context"
	"fmt"
	"math/big"
	"oracle/internal/utils"
	"strconv"

	"github.com/shopspring/decimal"

	"oracle/internal/domain"
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
	type responseData struct {
		Swaps []rawSwap `json:"swaps"`
	}

	data, err := execute[responseData](
		ctx,
		c,
		utils.GenerateSwapsQuery(
			poolAddress,
			fromBlock,
			toBlock,
			afterID,
			limit,
		),
	)
	if err != nil {
		return nil, err
	}

	swaps := make([]domain.SwapEvent, 0, len(data.Swaps))

	for _, raw := range data.Swaps {
		swap, err := mapRawSwap(
			poolAddress,
			raw,
			token0Decimals,
			token1Decimals,
		)
		if err != nil {
			return nil, fmt.Errorf("map swap %s: %w", raw.ID, err)
		}

		swaps = append(swaps, swap)
	}

	return swaps, nil
}

func mapRawSwap(
	poolAddress string,
	raw rawSwap,
	token0Decimals int,
	token1Decimals int,
) (domain.SwapEvent, error) {
	blockNumber, err := parseUintString(raw.Transaction.BlockNumber.String())
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf("parse block number: %w", err)
	}

	logIndex, err := parseIntString(raw.LogIndex)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf("parse log index: %w", err)
	}

	timestamp, err := parseUintString(raw.Timestamp)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf("parse timestamp: %w", err)
	}

	tick, err := parseIntString(raw.Tick)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf("parse tick: %w", err)
	}

	amount0Raw, err := tokenDecimalToRaw(raw.Amount0, token0Decimals)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf("parse amount0: %w", err)
	}

	amount1Raw, err := tokenDecimalToRaw(raw.Amount1, token1Decimals)
	if err != nil {
		return domain.SwapEvent{}, fmt.Errorf("parse amount1: %w", err)
	}

	sqrtPriceX96, ok := new(big.Int).SetString(raw.SqrtPriceX96, 10)
	if !ok {
		return domain.SwapEvent{}, fmt.Errorf("parse sqrtPriceX96: %q", raw.SqrtPriceX96)
	}

	return domain.SwapEvent{
		ID:                raw.ID,
		TxHash:            raw.Transaction.ID,
		PoolAddress:       poolAddress,
		BlockNumber:       blockNumber,
		LogIndex:          logIndex,
		Timestamp:         timestamp,
		Amount0Raw:        amount0Raw,
		Amount1Raw:        amount1Raw,
		SqrtPriceX96After: sqrtPriceX96,
		TickAfter:         tick,
	}, nil
}

func tokenDecimalToRaw(
	value string,
	decimals int,
) (*big.Int, error) {
	amount, err := decimal.NewFromString(value)
	if err != nil {
		return nil, err
	}

	raw := amount.Shift(int32(decimals)).Truncate(0)
	rawString := raw.StringFixed(0)

	result, ok := new(big.Int).SetString(rawString, 10)
	if !ok {
		return nil, fmt.Errorf("invalid raw amount %q", rawString)
	}

	return result, nil
}

func parseUintString(value string) (uint64, error) {
	return strconv.ParseUint(value, 10, 64)
}

func parseIntString(value string) (int, error) {
	result, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}

	return int(result), nil
}
