package services

import (
	"context"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type SwapValidationProvider interface {
	IndexedHead(
		ctx context.Context,
	) (domain.IndexedHead, error)

	PoolMetadata(
		ctx context.Context,
		poolAddress string,
	) (domain.Pool, error)

	PoolSnapshotAt(
		ctx context.Context,
		poolAddress string,
		blockNumber uint64,
	) (domain.PoolSnapshot, error)

	FetchSwapsPage(
		ctx context.Context,
		poolAddress string,
		fromBlock uint64,
		toBlock uint64,
		afterID string,
		limit int,
		token0Decimals int,
		token1Decimals int,
	) ([]domain.SwapEvent, error)
}

type SwapValidationRepository interface {
	LoadSwapValidationContext(
		ctx context.Context,
		poolAddress string,
		cursor domain.EventCursor,
	) (repositories.SwapValidationContext, error)

	LoadReconstructionInputFromSnapshot(
		ctx context.Context,
		snapshot domain.PoolSnapshot,
	) (domain.ReconstructionInput, error)
}

type SwapValidationSimulator interface {
	SimulateExactInput(
		pool *domain.ReconstructedPool,
		req uniswapv3.ExactInputRequest,
	) (*uniswapv3.ExactInputResult, error)
}

type SwapValidationRequest struct {
	PoolAddress string
	FromBlock   uint64
	ToBlock     uint64
	PageSize    int
	MaxSamples  int

	// BlockWindowSize keeps each The Graph query limited to a small block
	// interval. Busy pools should normally use a small value.
	BlockWindowSize uint64

	// MaxCandidateBlocks bounds how many Swap-containing blocks may be
	// inspected while looking for clean validation samples.
	MaxCandidateBlocks int

	// MaxScannedWindows prevents unbounded Graph queries on low-activity
	// block ranges.
	MaxScannedWindows int
}

type SwapValidationSkipReason string

const (
	SwapValidationSkipInvalidSwap SwapValidationSkipReason = "invalid_swap"

	SwapValidationSkipIncompleteLPIndex SwapValidationSkipReason = "incomplete_lp_index"

	SwapValidationSkipPriorLiquidityAction SwapValidationSkipReason = "prior_liquidity_action"
)

type SwapValidationSkip struct {
	SwapID      string
	TxHash      string
	BlockNumber uint64
	LogIndex    int
	Reason      SwapValidationSkipReason
	Detail      string
}

type SwapValidationReport struct {
	PoolAddress string
	FromBlock   uint64
	ToBlock     uint64

	GraphIndexedThrough uint64
	ScannedWindows      int
	CandidateBlocks     int

	Results []SwapValidationResult
	Skipped []SwapValidationSkip
}

type SwapValidationResult struct {
	SwapID      string
	TxHash      string
	BlockNumber uint64
	LogIndex    int
	ZeroForOne  bool

	SnapshotBlock uint64

	AmountInRaw           *big.Int
	SimAmountInLessFeeRaw *big.Int
	SimFeeAmountRaw       *big.Int

	ActualAmountOutRaw  *big.Int
	SimAmountOutRaw     *big.Int
	AmountOutAbsDiffRaw *big.Int
	AmountOutDiffBps    decimal.Decimal
	AmountOutExact      bool

	ActualSqrtPriceX96After *big.Int
	SimSqrtPriceX96After    *big.Int
	SqrtPriceAbsDiffRaw     *big.Int
	SqrtPriceDiffBps        decimal.Decimal
	SqrtPriceExact          bool

	ActualTickAfter int
	SimTickAfter    int
	TickDelta       int
	TickExact       bool

	SimCrossedTicks int
	SimSwapSteps    int

	ExactMatch bool
}
