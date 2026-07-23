package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type SwapValidationService struct {
	provider   SwapValidationProvider
	repository SwapValidationRepository
	simulator  SwapValidationSimulator
}

func NewSwapValidationService(
	provider SwapValidationProvider,
	repository SwapValidationRepository,
	simulator SwapValidationSimulator,
) *SwapValidationService {
	return &SwapValidationService{
		provider:   provider,
		repository: repository,
		simulator:  simulator,
	}
}

// ValidateFirstSwapsPerBlock keeps the previous API compatible while applying
// the clean-sample eligibility rules internally.
func (s *SwapValidationService) ValidateFirstSwapsPerBlock(
	ctx context.Context,
	req SwapValidationRequest,
) ([]SwapValidationResult, error) {
	report, err := s.ValidateCleanFirstSwapsPerBlock(
		ctx,
		req,
	)
	if err != nil {
		return nil, err
	}

	return report.Results, nil
}

// ValidateCleanFirstSwapsPerBlock validates only the actual first Swap in each
// block.
//
// A candidate is eligible only when:
//
//   - The Graph has indexed the requested block;
//   - the local LP-action checkpoint covers the Swap block;
//   - no Mint or Burn precedes the Swap in that block.
//
// Expected sample exclusions are recorded in report.Skipped. Infrastructure,
// reconstruction and simulation errors remain fatal and are never hidden.
func (s *SwapValidationService) ValidateCleanFirstSwapsPerBlock(
	ctx context.Context,
	req SwapValidationRequest,
) (SwapValidationReport, error) {
	req, err := normalizeSwapValidationRequest(
		req,
	)
	if err != nil {
		return SwapValidationReport{}, err
	}

	if err := s.validateDependencies(); err != nil {
		return SwapValidationReport{}, err
	}

	head, err := s.provider.IndexedHead(ctx)
	if err != nil {
		return SwapValidationReport{}, fmt.Errorf(
			"swap validation: get The Graph indexed head: %w",
			err,
		)
	}

	if head.BlockNumber < req.ToBlock {
		return SwapValidationReport{}, fmt.Errorf(
			"swap validation: The Graph indexed head %d "+
				"is before requested to-block %d",
			head.BlockNumber,
			req.ToBlock,
		)
	}

	pool, err := s.provider.PoolMetadata(
		ctx,
		req.PoolAddress,
	)
	if err != nil {
		return SwapValidationReport{}, fmt.Errorf(
			"swap validation: get pool metadata: %w",
			err,
		)
	}

	if err := validateSwapValidationPool(
		pool,
		req.PoolAddress,
	); err != nil {
		return SwapValidationReport{}, err
	}

	if req.ToBlock < pool.CreatedBlock {
		return SwapValidationReport{}, fmt.Errorf(
			"swap validation: requested to-block %d "+
				"is before pool creation block %d",
			req.ToBlock,
			pool.CreatedBlock,
		)
	}

	if req.FromBlock < pool.CreatedBlock {
		req.FromBlock = pool.CreatedBlock
	}

	report := SwapValidationReport{
		PoolAddress: req.PoolAddress,

		FromBlock: req.FromBlock,

		ToBlock: req.ToBlock,

		GraphIndexedThrough: head.BlockNumber,

		Results: make(
			[]SwapValidationResult,
			0,
			req.MaxSamples,
		),

		Skipped: make([]SwapValidationSkip, 0),
	}

	seenBlocks := make(
		map[uint64]struct{},
	)

	windowEnd := req.ToBlock

	for windowEnd >= req.FromBlock &&
		len(report.Results) < req.MaxSamples &&
		report.CandidateBlocks < req.MaxCandidateBlocks &&
		report.ScannedWindows < req.MaxScannedWindows {
		windowStart := swapValidationWindowStart(
			req.FromBlock,
			windowEnd,
			req.BlockWindowSize,
		)

		swaps, err := s.fetchSwapsInWindow(
			ctx,
			pool,
			windowStart,
			windowEnd,
			req.PageSize,
		)
		if err != nil {
			return SwapValidationReport{}, fmt.Errorf(
				"swap validation: fetch swaps window [%d,%d]: %w",
				windowStart,
				windowEnd,
				err,
			)
		}

		report.ScannedWindows++

		candidates :=
			firstSwapsPerBlockNewestFirst(
				swaps,
			)

		for _, swap := range candidates {
			if len(report.Results) >= req.MaxSamples ||
				report.CandidateBlocks >= req.MaxCandidateBlocks {
				break
			}

			if _, exists :=
				seenBlocks[swap.BlockNumber]; exists {
				continue
			}

			seenBlocks[swap.BlockNumber] =
				struct{}{}

			report.CandidateBlocks++

			if err := swap.ValidateForSimulation(); err != nil {
				report.Skipped = append(
					report.Skipped,
					newSwapValidationSkip(
						swap,
						SwapValidationSkipInvalidSwap,
						err.Error(),
					),
				)

				continue
			}

			validationContext, err :=
				s.repository.LoadSwapValidationContext(
					ctx,
					req.PoolAddress,
					swap.Cursor(),
				)
			if err != nil {
				return SwapValidationReport{}, fmt.Errorf(
					"swap validation: load context "+
						"for swap %s at %s: %w",
					swap.ID,
					swap.Cursor(),
					err,
				)
			}

			if !validationContext.IsIndexedThroughSwap() {
				report.Skipped = append(
					report.Skipped,
					newSwapValidationSkip(
						swap,
						SwapValidationSkipIncompleteLPIndex,
						fmt.Sprintf(
							"LP-action checkpoint=%d "+
								"is before swap block=%d",
							validationContext.IndexedThrough,
							swap.BlockNumber,
						),
					),
				)

				continue
			}

			if validationContext.HasPriorLiquidityAction() {
				report.Skipped = append(
					report.Skipped,
					newSwapValidationSkip(
						swap,
						SwapValidationSkipPriorLiquidityAction,
						fmt.Sprintf(
							"%d Mint/Burn event(s) "+
								"precede the first Swap "+
								"in block %d",
							validationContext.
								PriorLiquidityActionCount,
							swap.BlockNumber,
						),
					),
				)

				continue
			}

			result, err := s.validateSwap(
				ctx,
				swap,
			)
			if err != nil {
				return SwapValidationReport{}, fmt.Errorf(
					"swap validation: validate clean swap %s "+
						"block=%d log=%d: %w",
					swap.ID,
					swap.BlockNumber,
					swap.LogIndex,
					err,
				)
			}

			report.Results = append(
				report.Results,
				result,
			)
		}

		if windowStart == req.FromBlock {
			break
		}

		windowEnd = windowStart - 1
	}

	sortSwapValidationReport(
		&report,
	)

	return report, nil
}

func (s *SwapValidationService) validateDependencies() error {
	if s == nil {
		return fmt.Errorf(
			"swap validation: service is nil",
		)
	}

	if s.provider == nil {
		return fmt.Errorf(
			"swap validation: provider is nil",
		)
	}

	if s.repository == nil {
		return fmt.Errorf(
			"swap validation: repository is nil",
		)
	}

	if s.simulator == nil {
		return fmt.Errorf(
			"swap validation: simulator is nil",
		)
	}

	return nil
}

func normalizeSwapValidationRequest(
	req SwapValidationRequest,
) (SwapValidationRequest, error) {
	req.PoolAddress =
		normalizeAddress(
			req.PoolAddress,
		)

	if req.PoolAddress == "" {
		return SwapValidationRequest{}, fmt.Errorf(
			"swap validation: pool address is required",
		)
	}

	if req.FromBlock == 0 ||
		req.ToBlock == 0 ||
		req.FromBlock > req.ToBlock {
		return SwapValidationRequest{}, fmt.Errorf(
			"swap validation: invalid block range [%d,%d]",
			req.FromBlock,
			req.ToBlock,
		)
	}

	if req.PageSize <= 0 {
		req.PageSize = 100
	}

	if req.MaxSamples <= 0 {
		req.MaxSamples = 10
	}

	if req.BlockWindowSize == 0 {
		req.BlockWindowSize = 8
	}

	if req.MaxCandidateBlocks <= 0 {
		req.MaxCandidateBlocks =
			req.MaxSamples * 20
	}

	if req.MaxScannedWindows <= 0 {
		req.MaxScannedWindows =
			req.MaxCandidateBlocks * 2
	}

	if req.MaxCandidateBlocks <
		req.MaxSamples {
		return SwapValidationRequest{}, fmt.Errorf(
			"swap validation: max candidate blocks %d "+
				"is lower than max samples %d",
			req.MaxCandidateBlocks,
			req.MaxSamples,
		)
	}

	return req, nil
}

func validateSwapValidationPool(
	pool domain.Pool,
	expectedAddress string,
) error {
	actualAddress :=
		normalizeAddress(
			pool.Address,
		)

	if actualAddress == "" {
		return fmt.Errorf(
			"swap validation: pool metadata address is empty",
		)
	}

	if actualAddress != expectedAddress {
		return fmt.Errorf(
			"swap validation: metadata pool %s "+
				"does not match request pool %s",
			actualAddress,
			expectedAddress,
		)
	}

	if pool.CreatedBlock == 0 {
		return fmt.Errorf(
			"swap validation: pool creation block is zero",
		)
	}

	if pool.Token0Decimals < 0 ||
		pool.Token0Decimals > 255 {
		return fmt.Errorf(
			"swap validation: invalid token0 decimals %d",
			pool.Token0Decimals,
		)
	}

	if pool.Token1Decimals < 0 ||
		pool.Token1Decimals > 255 {
		return fmt.Errorf(
			"swap validation: invalid token1 decimals %d",
			pool.Token1Decimals,
		)
	}

	return nil
}

func swapValidationWindowStart(
	fromBlock uint64,
	windowEnd uint64,
	windowSize uint64,
) uint64 {
	if windowSize == 0 ||
		windowEnd-fromBlock+1 <= windowSize {
		return fromBlock
	}

	return windowEnd - windowSize + 1
}

// firstSwapsPerBlockNewestFirst selects the first actual Swap of every block
// before checking whether it is simulation-eligible.
//
// Selecting a later Swap after rejecting the first one would be invalid,
// because state from block-1 would no longer represent the state before that
// later Swap.
func firstSwapsPerBlockNewestFirst(
	swaps []domain.SwapEvent,
) []domain.SwapEvent {
	ordered := append(
		[]domain.SwapEvent(nil),
		swaps...,
	)

	sort.SliceStable(
		ordered,
		func(i int, j int) bool {
			if ordered[i].BlockNumber !=
				ordered[j].BlockNumber {
				return ordered[i].BlockNumber <
					ordered[j].BlockNumber
			}

			if ordered[i].LogIndex !=
				ordered[j].LogIndex {
				return ordered[i].LogIndex <
					ordered[j].LogIndex
			}

			return ordered[i].ID <
				ordered[j].ID
		},
	)

	first := make(
		[]domain.SwapEvent,
		0,
		len(ordered),
	)

	var (
		previousBlock uint64
		hasPrevious   bool
	)

	for _, swap := range ordered {
		if hasPrevious &&
			swap.BlockNumber == previousBlock {
			continue
		}

		first = append(
			first,
			swap,
		)

		previousBlock =
			swap.BlockNumber

		hasPrevious = true
	}

	for left, right := 0, len(first)-1; left < right; left, right = left+1, right-1 {
		first[left], first[right] =
			first[right], first[left]
	}

	return first
}

func (s *SwapValidationService) validateSwap(
	ctx context.Context,
	swap domain.SwapEvent,
) (SwapValidationResult, error) {
	if err := swap.ValidateForSimulation(); err != nil {
		return SwapValidationResult{}, err
	}

	snapshotBlock :=
		swap.BlockNumber - 1

	snapshot, err :=
		s.provider.PoolSnapshotAt(
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

	if err := validateValidationSnapshot(
		snapshot,
		swap.PoolAddress,
		snapshotBlock,
	); err != nil {
		return SwapValidationResult{}, err
	}

	input, err :=
		s.repository.
			LoadReconstructionInputFromSnapshot(
				ctx,
				snapshot,
			)
	if err != nil {
		return SwapValidationResult{}, fmt.Errorf(
			"load reconstruction input "+
				"from snapshot block %d: %w",
			snapshotBlock,
			err,
		)
	}

	pool, err :=
		ReconstructPool(input)
	if err != nil {
		return SwapValidationResult{}, fmt.Errorf(
			"reconstruct pool at block %d: %w",
			snapshotBlock,
			err,
		)
	}

	if pool.BlockNumber != snapshotBlock {
		return SwapValidationResult{}, fmt.Errorf(
			"reconstructed pool block=%d "+
				"does not match snapshot block=%d",
			pool.BlockNumber,
			snapshotBlock,
		)
	}

	if normalizeAddress(pool.PoolAddress) !=
		normalizeAddress(swap.PoolAddress) {
		return SwapValidationResult{}, fmt.Errorf(
			"reconstructed pool address %s "+
				"does not match swap pool %s",
			pool.PoolAddress,
			swap.PoolAddress,
		)
	}

	observedAmountIn :=
		swap.AmountInRaw()

	sim, err :=
		s.simulator.SimulateExactInput(
			pool,
			uniswapv3.ExactInputRequest{
				AmountIn: observedAmountIn,

				ZeroForOne: swap.IsZeroForOne(),
			},
		)
	if err != nil {
		return SwapValidationResult{}, err
	}

	if err := validateSwapSimulationResult(
		sim,
		observedAmountIn,
	); err != nil {
		return SwapValidationResult{}, err
	}

	actualOut :=
		swap.AmountOutRaw()

	amountOutDiff :=
		absBigIntDiff(
			sim.AmountOut,
			actualOut,
		)

	sqrtPriceDiff :=
		absBigIntDiff(
			sim.SqrtPriceAfterX96,
			swap.SqrtPriceX96After,
		)

	tickDelta :=
		absIntDiff(
			sim.TickAfter,
			swap.TickAfter,
		)

	amountOutExact :=
		amountOutDiff.Sign() == 0

	sqrtPriceExact :=
		sqrtPriceDiff.Sign() == 0

	tickExact :=
		tickDelta == 0

	return SwapValidationResult{
		SwapID: swap.ID,

		TxHash: swap.TxHash,

		BlockNumber: swap.BlockNumber,

		LogIndex: swap.LogIndex,

		ZeroForOne: swap.IsZeroForOne(),

		SnapshotBlock: snapshotBlock,

		AmountInRaw: cloneBigInt(
			observedAmountIn,
		),

		SimAmountInLessFeeRaw: cloneBigInt(
			sim.AmountInLessFee,
		),

		SimFeeAmountRaw: cloneBigInt(
			sim.FeeAmount,
		),

		ActualAmountOutRaw: cloneBigInt(
			actualOut,
		),

		SimAmountOutRaw: cloneBigInt(
			sim.AmountOut,
		),

		AmountOutAbsDiffRaw: amountOutDiff,

		AmountOutDiffBps: relativeDiffBps(
			amountOutDiff,
			actualOut,
		),

		AmountOutExact: amountOutExact,

		ActualSqrtPriceX96After: cloneBigInt(
			swap.SqrtPriceX96After,
		),

		SimSqrtPriceX96After: cloneBigInt(
			sim.SqrtPriceAfterX96,
		),

		SqrtPriceAbsDiffRaw: sqrtPriceDiff,

		SqrtPriceDiffBps: relativeDiffBps(
			sqrtPriceDiff,
			swap.SqrtPriceX96After,
		),

		SqrtPriceExact: sqrtPriceExact,

		ActualTickAfter: swap.TickAfter,

		SimTickAfter: sim.TickAfter,

		TickDelta: tickDelta,

		TickExact: tickExact,

		SimCrossedTicks: sim.CrossedTicks,

		SimSwapSteps: sim.SwapSteps,

		ExactMatch: amountOutExact &&
			sqrtPriceExact &&
			tickExact,
	}, nil
}

func validateValidationSnapshot(
	snapshot domain.PoolSnapshot,
	expectedPool string,
	expectedBlock uint64,
) error {
	if normalizeAddress(snapshot.PoolAddress) !=
		normalizeAddress(expectedPool) {
		return fmt.Errorf(
			"snapshot pool %s "+
				"does not match expected pool %s",
			snapshot.PoolAddress,
			expectedPool,
		)
	}

	if snapshot.BlockNumber != expectedBlock {
		return fmt.Errorf(
			"snapshot block=%d "+
				"does not match requested block=%d",
			snapshot.BlockNumber,
			expectedBlock,
		)
	}

	return nil
}

func validateSwapSimulationResult(
	sim *uniswapv3.ExactInputResult,
	expectedAmountIn *big.Int,
) error {
	if sim == nil {
		return fmt.Errorf(
			"simulator returned nil result",
		)
	}

	if expectedAmountIn == nil ||
		expectedAmountIn.Sign() <= 0 {
		return fmt.Errorf(
			"expected amount in must be positive",
		)
	}

	if sim.AmountIn == nil ||
		sim.AmountIn.Cmp(expectedAmountIn) != 0 {
		return fmt.Errorf(
			"simulated gross input " +
				"does not match observed input",
		)
	}

	if sim.AmountInLessFee == nil ||
		sim.AmountInLessFee.Sign() <= 0 {
		return fmt.Errorf(
			"simulated usable input must be positive",
		)
	}

	if sim.FeeAmount == nil ||
		sim.FeeAmount.Sign() < 0 {
		return fmt.Errorf(
			"simulated fee amount is invalid",
		)
	}

	if sim.AmountOut == nil ||
		sim.AmountOut.Sign() <= 0 {
		return fmt.Errorf(
			"simulated output must be positive",
		)
	}

	if sim.SqrtPriceAfterX96 == nil ||
		sim.SqrtPriceAfterX96.Sign() <= 0 {
		return fmt.Errorf(
			"simulated sqrt price after must be positive",
		)
	}

	if sim.TickAfter !=
		sim.TickAfterApprox {
		return fmt.Errorf(
			"simulated exact tick %d "+
				"differs from compatibility tick %d",
			sim.TickAfter,
			sim.TickAfterApprox,
		)
	}

	if sim.CrossedTicks < 0 ||
		sim.SwapSteps <= 0 ||
		sim.CrossedTicks > sim.SwapSteps {
		return fmt.Errorf(
			"invalid simulated step accounting: "+
				"crossed_ticks=%d swap_steps=%d",
			sim.CrossedTicks,
			sim.SwapSteps,
		)
	}

	accountedInput := new(big.Int).Add(
		cloneBigInt(
			sim.AmountInLessFee,
		),
		sim.FeeAmount,
	)

	if accountedInput.Cmp(
		expectedAmountIn,
	) != 0 {
		return fmt.Errorf(
			"simulated input accounting mismatch: "+
				"observed=%s usable=%s fee=%s accounted=%s",
			expectedAmountIn,
			sim.AmountInLessFee,
			sim.FeeAmount,
			accountedInput,
		)
	}

	return nil
}

func (s *SwapValidationService) fetchSwapsInWindow(
	ctx context.Context,
	pool domain.Pool,
	fromBlock uint64,
	toBlock uint64,
	pageSize int,
) ([]domain.SwapEvent, error) {
	result := make(
		[]domain.SwapEvent,
		0,
	)

	afterID := ""

	for {
		page, err :=
			s.provider.FetchSwapsPage(
				ctx,
				pool.Address,
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

		result = append(
			result,
			page...,
		)

		if len(page) < pageSize {
			break
		}

		nextAfterID :=
			page[len(page)-1].ID

		if nextAfterID == "" ||
			nextAfterID <= afterID {
			return nil, fmt.Errorf(
				"swap pagination cursor did not advance: "+
					"previous=%q next=%q",
				afterID,
				nextAfterID,
			)
		}

		afterID = nextAfterID
	}

	return result, nil
}

func newSwapValidationSkip(
	swap domain.SwapEvent,
	reason SwapValidationSkipReason,
	detail string,
) SwapValidationSkip {
	return SwapValidationSkip{
		SwapID: swap.ID,

		TxHash: swap.TxHash,

		BlockNumber: swap.BlockNumber,

		LogIndex: swap.LogIndex,

		Reason: reason,

		Detail: detail,
	}
}

func sortSwapValidationReport(
	report *SwapValidationReport,
) {
	sort.SliceStable(
		report.Results,
		func(i int, j int) bool {
			if report.Results[i].BlockNumber !=
				report.Results[j].BlockNumber {
				return report.Results[i].BlockNumber <
					report.Results[j].BlockNumber
			}

			return report.Results[i].LogIndex <
				report.Results[j].LogIndex
		},
	)

	sort.SliceStable(
		report.Skipped,
		func(i int, j int) bool {
			if report.Skipped[i].BlockNumber !=
				report.Skipped[j].BlockNumber {
				return report.Skipped[i].BlockNumber <
					report.Skipped[j].BlockNumber
			}

			return report.Skipped[i].LogIndex <
				report.Skipped[j].LogIndex
		},
	)
}

func normalizeAddress(
	value string,
) string {
	return strings.ToLower(
		strings.TrimSpace(value),
	)
}

func cloneBigInt(
	value *big.Int,
) *big.Int {
	if value == nil {
		return nil
	}

	return new(big.Int).Set(
		value,
	)
}

func absBigIntDiff(
	a *big.Int,
	b *big.Int,
) *big.Int {
	if a == nil || b == nil {
		return big.NewInt(0)
	}

	return new(big.Int).Abs(
		new(big.Int).Sub(
			a,
			b,
		),
	)
}

func absIntDiff(
	a int,
	b int,
) int {
	if a >= b {
		return a - b
	}

	return b - a
}

func relativeDiffBps(
	diff *big.Int,
	base *big.Int,
) decimal.Decimal {
	if diff == nil ||
		base == nil ||
		base.Sign() == 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(
		diff,
		0,
	).Div(
		decimal.NewFromBigInt(
			base,
			0,
		),
	).Mul(
		decimal.NewFromInt(
			10_000,
		),
	)
}
