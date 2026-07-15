package services

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/shopspring/decimal"

	"oracle/internal/domain"
	"oracle/internal/providers"
	"oracle/internal/repositories"
	"oracle/internal/uniswapv3"
)

type SwapValidationService struct {
	provider   *providers.Client
	repository *repositories.PoolStateRepository
	simulator  *uniswapv3.Simulator
}

func NewSwapValidationService(
	provider *providers.Client,
	repository *repositories.PoolStateRepository,
	simulator *uniswapv3.Simulator,
) *SwapValidationService {
	return &SwapValidationService{
		provider:   provider,
		repository: repository,
		simulator:  simulator,
	}
}

type SwapValidationRequest struct {
	PoolAddress string
	FromBlock   uint64
	ToBlock     uint64
	PageSize    int
	MaxSamples  int

	// TheGraph swap queries are expensive on busy pools.
	// Keep this small, e.g. 5, 8, 10, or 20 blocks.
	BlockWindowSize uint64
}

type SwapValidationResult struct {
	SwapID      string
	TxHash      string
	BlockNumber uint64
	LogIndex    int
	ZeroForOne  bool

	AmountInRaw        *big.Int
	ActualAmountOutRaw *big.Int
	SimAmountOutRaw    *big.Int

	AmountOutAbsDiffRaw *big.Int
	AmountOutDiffBps    decimal.Decimal

	ActualTickAfter int
	SimTickAfter    int
	TickDelta       int
}

func (s *SwapValidationService) ValidateFirstSwapsPerBlock(
	ctx context.Context,
	req SwapValidationRequest,
) ([]SwapValidationResult, error) {
	if req.PoolAddress == "" {
		return nil, fmt.Errorf("swap validation: pool address is required")
	}
	if req.FromBlock == 0 || req.ToBlock == 0 || req.FromBlock > req.ToBlock {
		return nil, fmt.Errorf("swap validation: invalid block range")
	}
	if req.PageSize <= 0 {
		req.PageSize = 100
	}
	if req.MaxSamples <= 0 {
		req.MaxSamples = 10
	}

	pool, err := s.provider.PoolMetadata(ctx, req.PoolAddress)
	if err != nil {
		return nil, fmt.Errorf("swap validation: get pool metadata: %w", err)
	}

	swaps, err := s.fetchCandidateSwaps(
		ctx,
		pool,
		req,
	)
	if err != nil {
		return nil, err
	}

	firstSwaps := firstSwapPerBlock(swaps)
	if len(firstSwaps) > req.MaxSamples {
		firstSwaps = firstSwaps[:req.MaxSamples]
	}

	results := make([]SwapValidationResult, 0, len(firstSwaps))

	for _, swap := range firstSwaps {
		result, err := s.validateSwap(ctx, swap)
		if err != nil {
			return nil, fmt.Errorf(
				"validate swap %s block=%d log=%d: %w",
				swap.ID,
				swap.BlockNumber,
				swap.LogIndex,
				err,
			)
		}

		results = append(results, result)
	}

	return results, nil
}

func (s *SwapValidationService) fetchCandidateSwaps(
	ctx context.Context,
	pool domain.Pool,
	req SwapValidationRequest,
) ([]domain.SwapEvent, error) {
	if req.BlockWindowSize == 0 {
		req.BlockWindowSize = 8
	}

	result := make([]domain.SwapEvent, 0, req.MaxSamples*2)

	// Walk backwards from the target block range.
	// This is better for validation because we usually validate near the reconstructed snapshot.
	windowEnd := req.ToBlock

	for windowEnd >= req.FromBlock {
		windowStart := uint64(0)

		if windowEnd < req.BlockWindowSize {
			windowStart = req.FromBlock
		} else {
			windowStart = windowEnd - req.BlockWindowSize + 1
			if windowStart < req.FromBlock {
				windowStart = req.FromBlock
			}
		}

		pageSwaps, err := s.fetchSwapsInSmallWindow(
			ctx,
			pool,
			req.PoolAddress,
			windowStart,
			windowEnd,
			req.PageSize,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"swap validation: fetch swaps window [%d,%d]: %w",
				windowStart,
				windowEnd,
				err,
			)
		}

		result = append(result, pageSwaps...)

		firstSwaps := firstSwapPerBlock(result)
		if len(firstSwaps) >= req.MaxSamples {
			break
		}

		if windowStart == req.FromBlock {
			break
		}

		windowEnd = windowStart - 1
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].BlockNumber == result[j].BlockNumber {
			return result[i].LogIndex < result[j].LogIndex
		}

		return result[i].BlockNumber < result[j].BlockNumber
	})

	return result, nil
}

func firstSwapPerBlock(
	swaps []domain.SwapEvent,
) []domain.SwapEvent {
	result := make([]domain.SwapEvent, 0, len(swaps))
	seen := make(map[uint64]bool)

	for _, swap := range swaps {
		if seen[swap.BlockNumber] {
			continue
		}

		if !swap.IsZeroForOne() && !swap.IsOneForZero() {
			continue
		}

		result = append(result, swap)
		seen[swap.BlockNumber] = true
	}

	return result
}

func (s *SwapValidationService) validateSwap(
	ctx context.Context,
	swap domain.SwapEvent,
) (SwapValidationResult, error) {
	if swap.BlockNumber == 0 {
		return SwapValidationResult{}, fmt.Errorf("swap block is zero")
	}

	snapshotBlock := swap.BlockNumber - 1

	snapshot, err := s.provider.PoolSnapshotAt(
		ctx,
		swap.PoolAddress,
		snapshotBlock,
	)
	if err != nil {
		return SwapValidationResult{}, fmt.Errorf(
			"get pool snapshot at block %d: %w",
			snapshotBlock,
			err,
		)
	}

	input, err := s.repository.LoadReconstructionInputFromSnapshot(
		ctx,
		snapshot,
	)
	if err != nil {
		return SwapValidationResult{}, err
	}

	pool, err := ReconstructPool(input)
	if err != nil {
		return SwapValidationResult{}, err
	}

	sim, err := s.simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:   swap.AmountInRaw(),
			ZeroForOne: swap.IsZeroForOne(),
		},
	)
	if err != nil {
		return SwapValidationResult{}, err
	}

	actualOut := swap.AmountOutRaw()
	diff := absDiff(sim.AmountOut, actualOut)

	return SwapValidationResult{
		SwapID:      swap.ID,
		TxHash:      swap.TxHash,
		BlockNumber: swap.BlockNumber,
		LogIndex:    swap.LogIndex,
		ZeroForOne:  swap.IsZeroForOne(),

		AmountInRaw:        swap.AmountInRaw(),
		ActualAmountOutRaw: actualOut,
		SimAmountOutRaw:    new(big.Int).Set(sim.AmountOut),

		AmountOutAbsDiffRaw: diff,
		AmountOutDiffBps:    relativeDiffBps(diff, actualOut),

		ActualTickAfter: swap.TickAfter,
		SimTickAfter:    sim.TickAfterApprox,
		TickDelta:       int(math.Abs(float64(sim.TickAfterApprox - swap.TickAfter))),
	}, nil
}

func (s *SwapValidationService) fetchSwapsInSmallWindow(
	ctx context.Context,
	pool domain.Pool,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	pageSize int,
) ([]domain.SwapEvent, error) {
	if pageSize <= 0 {
		pageSize = 20
	}

	var result []domain.SwapEvent
	afterID := ""

	for {
		page, err := s.provider.FetchSwapsPage(
			ctx,
			poolAddress,
			fromBlock,
			toBlock,
			afterID,
			pageSize,
			pool.Token0Decimals,
			pool.Token1Decimals,
		)
		if err != nil {
			return nil, err
		}

		if len(page) == 0 {
			break
		}

		result = append(result, page...)

		if len(page) < pageSize {
			break
		}

		afterID = page[len(page)-1].ID

		// Validation does not need huge swap pages.
		// Avoid stressing TheGraph on busy pools.
		if len(result) >= pageSize*3 {
			break
		}
	}

	return result, nil
}

func absDiff(a *big.Int, b *big.Int) *big.Int {
	diff := new(big.Int).Sub(a, b)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}

	return diff
}

func relativeDiffBps(
	diff *big.Int,
	base *big.Int,
) decimal.Decimal {
	if diff == nil || base == nil || base.Sign() == 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(diff, 0).
		Div(decimal.NewFromBigInt(base, 0)).
		Mul(decimal.NewFromInt(10_000))
}
