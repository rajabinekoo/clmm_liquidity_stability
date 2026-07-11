package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"oracle/internal/domain"
	"oracle/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const endpoint = "https://gateway.thegraph.com/api/subgraphs/id/5zvR82QoaXYFyDEKLZ9t6v9adgnptxYpKpSbxtgVENFV"

type Client struct {
	httpClient *http.Client
	apiKey     string
}

func New(
	apiKey string,
	timeout time.Duration,
) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type request struct {
	Query string `json:"query"`
}

type graphResponse[T any] struct {
	Data   T              `json:"data"`
	Errors []GraphQLError `json:"errors"`
}

type GraphQLError struct {
	Message string `json:"message"`
}

func (c *Client) Query(
	ctx context.Context,
	query string,
) ([]byte, error) {
	body, err := json.Marshal(request{
		Query: query,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"thegraph: encode request: %w",
			err,
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"thegraph: create request: %w",
			err,
		)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf(
			"thegraph: execute request: %w",
			err,
		)
	}
	defer resp.Body.Close()

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf(
			"thegraph: read response: %w",
			err,
		)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"thegraph returned status %d: %s",
			resp.StatusCode,
			string(resBody),
		)
	}

	return resBody, nil
}

func execute[T any](
	ctx context.Context,
	client *Client,
	query string,
) (T, error) {
	var zero T

	body, err := client.Query(ctx, query)
	if err != nil {
		return zero, err
	}

	var response graphResponse[T]
	if err = json.Unmarshal(body, &response); err != nil {
		return zero, fmt.Errorf(
			"thegraph: decode response: %w",
			err,
		)
	}

	if len(response.Errors) > 0 {
		messages := make([]string, 0, len(response.Errors))

		for _, graphErr := range response.Errors {
			messages = append(messages, graphErr.Message)
		}

		return zero, fmt.Errorf(
			"thegraph graphql error: %s",
			strings.Join(messages, "; "),
		)
	}

	return response.Data, nil
}

type graphNumber string

func (n *graphNumber) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))

	if value == "null" {
		return fmt.Errorf("unexpected null graph number")
	}

	value = strings.Trim(value, `"`)

	if value == "" {
		return fmt.Errorf("empty graph number")
	}

	*n = graphNumber(value)

	return nil
}

func (n graphNumber) String() string {
	return string(n)
}

type rawTransaction struct {
	ID          string      `json:"id"`
	BlockNumber graphNumber `json:"blockNumber"`
}

type rawToken struct {
	ID       string      `json:"id"`
	Decimals graphNumber `json:"decimals"`
}

type rawPool struct {
	ID                   string          `json:"id"`
	FeeTier              graphNumber     `json:"feeTier"`
	FeesUSD              decimal.Decimal `json:"feesUSD"`
	CreatedAtBlockNumber graphNumber     `json:"createdAtBlockNumber"`

	Tick            decimal.Decimal `json:"tick"`
	SqrtPriceX96    decimal.Decimal `json:"sqrtPrice"`
	ActiveLiquidity decimal.Decimal `json:"liquidity"`

	Token0 rawToken `json:"token0"`
	Token1 rawToken `json:"token1"`
}

type rawPoolSnapshot struct {
	ID string `json:"id"`

	Liquidity graphNumber `json:"liquidity"`
	SqrtPrice graphNumber `json:"sqrtPrice"`
	Tick      graphNumber `json:"tick"`

	FeesUSD                string      `json:"feesUSD"`
	LiquidityProviderCount graphNumber `json:"liquidityProviderCount"`
}

type rawLPAction struct {
	ID string `json:"id"`

	Amount    string `json:"amount"`
	Amount0   string `json:"amount0"`
	Amount1   string `json:"amount1"`
	AmountUSD string `json:"amountUSD"`

	LogIndex  *graphNumber `json:"logIndex"`
	Timestamp graphNumber  `json:"timestamp"`

	Origin string `json:"origin"`
	Owner  string `json:"owner"`
	Sender string `json:"sender"`

	TickLower graphNumber `json:"tickLower"`
	TickUpper graphNumber `json:"tickUpper"`

	Transaction rawTransaction `json:"transaction"`
}

func (c *Client) IndexedHead(
	ctx context.Context,
) (domain.IndexedHead, error) {
	type responseData struct {
		Meta struct {
			Block struct {
				Number graphNumber `json:"number"`
				Hash   string      `json:"hash"`
			} `json:"block"`

			HasIndexingErrors bool `json:"hasIndexingErrors"`
		} `json:"_meta"`
	}

	data, err := execute[responseData](
		ctx,
		c,
		utils.GenerateMetaQuery(),
	)
	if err != nil {
		return domain.IndexedHead{}, err
	}

	if data.Meta.HasIndexingErrors {
		return domain.IndexedHead{},
			fmt.Errorf("thegraph reports indexing errors")
	}

	blockNumber, err := parseUint64(
		data.Meta.Block.Number,
		"indexed block number",
	)
	if err != nil {
		return domain.IndexedHead{}, err
	}

	return domain.IndexedHead{
		BlockNumber: blockNumber,
		BlockHash:   data.Meta.Block.Hash,
	}, nil
}

func (c *Client) PoolMetadata(
	ctx context.Context,
	poolAddress string,
) (domain.Pool, error) {
	fmt.Println(1)
	type responseData struct {
		Pool *rawPool `json:"pool"`
	}
	fmt.Println(2)

	data, err := execute[responseData](
		ctx,
		c,
		utils.GeneratePoolMetadataQuery(poolAddress),
	)
	fmt.Println(3)
	if err != nil {
		return domain.Pool{}, err
	}
	fmt.Println(4)

	if data.Pool == nil {
		return domain.Pool{}, fmt.Errorf(
			"pool not found: %s",
			poolAddress,
		)
	}

	feeTier, err := parseInt(
		data.Pool.FeeTier,
		"pool fee tier",
	)
	fmt.Println(5)
	if err != nil {
		return domain.Pool{}, err
	}
	fmt.Println(6)

	tickSpacing, err := domain.TickSpacingForFeeTier(feeTier)
	if err != nil {
		return domain.Pool{}, err
	}

	token0Decimals, err := parseInt(
		data.Pool.Token0.Decimals,
		"token0 decimals",
	)
	if err != nil {
		return domain.Pool{}, err
	}

	token1Decimals, err := parseInt(
		data.Pool.Token1.Decimals,
		"token1 decimals",
	)
	if err != nil {
		return domain.Pool{}, err
	}

	createdBlock, err := parseUint64(
		data.Pool.CreatedAtBlockNumber,
		"pool created block",
	)
	if err != nil {
		return domain.Pool{}, err
	}

	return domain.Pool{
		Address: strings.ToLower(data.Pool.ID),

		Token0Address: strings.ToLower(data.Pool.Token0.ID),
		Token1Address: strings.ToLower(data.Pool.Token1.ID),

		Token0Decimals: token0Decimals,
		Token1Decimals: token1Decimals,

		FeeTier:     feeTier,
		TickSpacing: tickSpacing,

		CreatedBlock: createdBlock,
	}, nil
}

func (c *Client) PoolSnapshotAt(
	ctx context.Context,
	poolAddress string,
	blockNumber uint64,
) (domain.PoolSnapshot, error) {
	type responseData struct {
		Pool *rawPoolSnapshot `json:"pool"`
	}

	data, err := execute[responseData](
		ctx,
		c,
		utils.GeneratePoolSnapshotQuery(
			poolAddress,
			blockNumber,
		),
	)
	if err != nil {
		return domain.PoolSnapshot{}, err
	}

	if data.Pool == nil {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"pool %s does not exist at block %d",
			poolAddress,
			blockNumber,
		)
	}

	sqrtPriceX96, err := parseDecimal(
		data.Pool.SqrtPrice.String(),
		"pool sqrt price",
	)
	if err != nil {
		return domain.PoolSnapshot{}, err
	}

	activeLiquidity, err := parseDecimal(
		data.Pool.Liquidity.String(),
		"pool liquidity",
	)
	if err != nil {
		return domain.PoolSnapshot{}, err
	}

	return domain.PoolSnapshot{
		PoolAddress:            strings.ToLower(poolAddress),
		BlockNumber:            blockNumber,
		Tick:                   string(data.Pool.Tick),
		SqrtPriceX96:           sqrtPriceX96,
		ActiveLiquidity:        activeLiquidity,
		FeesUSD:                data.Pool.FeesUSD,
		LiquidityProviderCount: string(data.Pool.LiquidityProviderCount),
	}, nil
}

func (c *Client) FetchMintsPage(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
) ([]domain.LPAction, error) {
	type responseData struct {
		Mints []rawLPAction `json:"mints"`
	}

	data, err := execute[responseData](
		ctx,
		c,
		utils.GenerateMintsQuery(
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

	actions := make([]domain.LPAction, 0, len(data.Mints))

	for _, item := range data.Mints {
		action, err := convertLPAction(
			poolAddress,
			item,
			domain.LPActionMint,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"convert mint %s: %w",
				item.ID,
				err,
			)
		}

		actions = append(actions, action)
	}

	return actions, nil
}

func (c *Client) FetchBurnsPage(
	ctx context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
) ([]domain.LPAction, error) {
	type responseData struct {
		Burns []rawLPAction `json:"burns"`
	}

	data, err := execute[responseData](
		ctx,
		c,
		utils.GenerateBurnsQuery(
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

	actions := make([]domain.LPAction, 0, len(data.Burns))

	for _, item := range data.Burns {
		action, err := convertLPAction(
			poolAddress,
			item,
			domain.LPActionBurn,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"convert burn %s: %w",
				item.ID,
				err,
			)
		}

		actions = append(actions, action)
	}

	return actions, nil
}

func convertLPAction(
	poolAddress string,
	item rawLPAction,
	actionType domain.LPActionType,
) (domain.LPAction, error) {
	if item.LogIndex == nil {
		return domain.LPAction{}, fmt.Errorf(
			"missing log index",
		)
	}

	prefix := "mint"
	if actionType == domain.LPActionBurn {
		prefix = "burn"
	}

	blockNumber, err := parseUint64(
		item.Transaction.BlockNumber,
		"block number",
	)
	if err != nil {
		return domain.LPAction{}, err
	}

	logIndex, err := parseInt(
		*item.LogIndex,
		"log index",
	)
	if err != nil {
		return domain.LPAction{}, err
	}

	timestamp, err := parseTimestamp(item.Timestamp)
	if err != nil {
		return domain.LPAction{}, err
	}

	tickLower, err := parseInt(
		item.TickLower,
		"tick lower",
	)
	if err != nil {
		return domain.LPAction{}, err
	}

	tickUpper, err := parseInt(
		item.TickUpper,
		"tick upper",
	)
	if err != nil {
		return domain.LPAction{}, err
	}

	liquidityDelta, err := parseDecimal(
		item.Amount,
		"liquidity amount",
	)
	if err != nil {
		return domain.LPAction{}, err
	}

	if actionType == domain.LPActionBurn {
		liquidityDelta = liquidityDelta.Neg()
	}

	amount0, err := parseDecimal(item.Amount0, "amount0")
	if err != nil {
		return domain.LPAction{}, err
	}

	amount1, err := parseDecimal(item.Amount1, "amount1")
	if err != nil {
		return domain.LPAction{}, err
	}

	amountUSD, err := parseDecimal(
		item.AmountUSD,
		"amount USD",
	)
	if err != nil {
		return domain.LPAction{}, err
	}

	return domain.LPAction{
		ID:       prefix + ":" + item.ID,
		SourceID: item.ID,

		PoolAddress: strings.ToLower(poolAddress),
		Action:      actionType,

		TxHash:      strings.ToLower(item.Transaction.ID),
		BlockNumber: blockNumber,
		LogIndex:    logIndex,
		Timestamp:   timestamp,

		Owner:  optionalAddress(item.Owner),
		Sender: optionalAddress(item.Sender),
		Origin: optionalAddress(item.Origin),

		TickLower: tickLower,
		TickUpper: tickUpper,

		LiquidityDelta: liquidityDelta,

		Amount0:   amount0,
		Amount1:   amount1,
		AmountUSD: amountUSD,
	}, nil
}

func parseUint64(
	value graphNumber,
	field string,
) (uint64, error) {
	result, err := strconv.ParseUint(
		value.String(),
		10,
		64,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"parse %s %q: %w",
			field,
			value,
			err,
		)
	}

	return result, nil
}

func parseInt(
	value graphNumber,
	field string,
) (int, error) {
	result, err := strconv.ParseInt(
		value.String(),
		10,
		32,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"parse %s %q: %w",
			field,
			value,
			err,
		)
	}

	return int(result), nil
}

func parseDecimal(
	value string,
	field string,
) (decimal.Decimal, error) {
	result, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, fmt.Errorf(
			"parse %s %q: %w",
			field,
			value,
			err,
		)
	}

	return result, nil
}

func parseTimestamp(
	value graphNumber,
) (time.Time, error) {
	seconds, err := strconv.ParseInt(
		value.String(),
		10,
		64,
	)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"parse timestamp %q: %w",
			value,
			err,
		)
	}

	return time.Unix(seconds, 0).UTC(), nil
}

func optionalAddress(value string) *string {
	if value == "" {
		return nil
	}

	normalized := strings.ToLower(value)

	return &normalized
}
