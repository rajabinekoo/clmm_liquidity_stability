package services

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

const defaultPreBurnSwapPageSize = 1_000

type PreBurnStateProvider interface {
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

type PreBurnStateRepository interface {
	LoadReconstructionInputFromSnapshot(
		ctx context.Context,
		snapshot domain.PoolSnapshot,
	) (domain.ReconstructionInput, error)

	LoadLiquidityChangesBeforeCursor(
		ctx context.Context,
		poolAddress string,
		cursor domain.EventCursor,
	) ([]domain.LiquidityChange, error)
}

type PreBurnStateResult struct {
	Burn domain.BurnCandidate

	Pool *domain.ReconstructedPool

	SnapshotBlock uint64

	PriorLiquidityEvents int
	PriorSwapEvents      int
	ReplayedEvents       int

	SwapReplays []BurnSwapReplayAudit

	LastReplayedCursor *domain.EventCursor

	BurnRangeActive bool

	ActiveLiquidityBeforeBurn *big.Int
}

type PreBurnStateService struct {
	provider   PreBurnStateProvider
	repository PreBurnStateRepository
}

func NewPreBurnStateService(
	provider PreBurnStateProvider,
	repository PreBurnStateRepository,
) *PreBurnStateService {
	return &PreBurnStateService{
		provider: provider,

		repository: repository,
	}
}

func (s *PreBurnStateService) Build(
	ctx context.Context,
	burn domain.BurnCandidate,
	pageSize int,
) (PreBurnStateResult, error) {
	if s == nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: service is nil",
		)
	}

	if s.provider == nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: provider is nil",
		)
	}

	if s.repository == nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: repository is nil",
		)
	}

	if err := burn.Validate(); err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: invalid burn: %w",
			err,
		)
	}

	burn.PoolAddress =
		normalizeAddress(
			burn.PoolAddress,
		)

	if pageSize <= 0 {
		pageSize =
			defaultPreBurnSwapPageSize
	}

	if pageSize >
		defaultPreBurnSwapPageSize {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: page size %d exceeds %d",
			pageSize,
			defaultPreBurnSwapPageSize,
		)
	}

	head, err :=
		s.provider.IndexedHead(
			ctx,
		)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: read indexed head: %w",
			err,
		)
	}

	if head.BlockNumber <
		burn.Cursor.BlockNumber {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: local indexed head %d is before burn block %d",
			head.BlockNumber,
			burn.Cursor.BlockNumber,
		)
	}

	metadata, err :=
		s.provider.PoolMetadata(
			ctx,
			burn.PoolAddress,
		)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: load pool metadata: %w",
			err,
		)
	}

	if normalizeAddress(
		metadata.Address,
	) != burn.PoolAddress {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: metadata pool %s does not match burn pool %s",
			metadata.Address,
			burn.PoolAddress,
		)
	}

	if burn.Cursor.BlockNumber <=
		metadata.CreatedBlock {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: burn block %d does not have a valid preceding pool snapshot after creation block %d",
			burn.Cursor.BlockNumber,
			metadata.CreatedBlock,
		)
	}

	snapshotBlock :=
		burn.Cursor.BlockNumber - 1

	pool, err := loadHistoricalPoolAt(
		ctx,
		s.provider,
		s.repository,
		burn.PoolAddress,
		snapshotBlock,
	)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: load local pool at block %d: %w",
			snapshotBlock,
			err,
		)
	}

	liquidityChanges, err :=
		s.repository.
			LoadLiquidityChangesBeforeCursor(
				ctx,
				burn.PoolAddress,
				burn.Cursor,
			)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: load prior liquidity events: %w",
			err,
		)
	}

	swaps, err :=
		s.fetchSwapsBeforeBurn(
			ctx,
			metadata,
			burn.Cursor,
			pageSize,
		)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: load prior swaps: %w",
			err,
		)
	}

	events, err :=
		mergePreBurnEvents(
			burn.Cursor,
			liquidityChanges,
			swaps,
		)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: merge events: %w",
			err,
		)
	}

	simulator, err :=
		uniswapv3.NewSimulator(int64(metadata.FeeTier))
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: create simulator: %w",
			err,
		)
	}

	replayedPool,
		lastCursor,
		swapReplays,
		err :=
		replayPoolBlockEvents(
			pool,
			events,
			simulator,
		)
	if err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: replay events before burn %s: %w",
			burn.EventKey(),
			err,
		)
	}

	replayedPool.BlockNumber =
		burn.Cursor.BlockNumber

	if err :=
		validateBurnCanApplyToPool(
			replayedPool,
			burn,
		); err != nil {
		return PreBurnStateResult{}, fmt.Errorf(
			"build pre-burn state: reconstructed burn is not applicable: %w",
			err,
		)
	}

	burnRangeActive :=
		burn.TickLower <=
			replayedPool.CurrentTick &&
			replayedPool.CurrentTick <
				burn.TickUpper

	return PreBurnStateResult{
		Burn: burn,

		Pool: replayedPool,

		SnapshotBlock: snapshotBlock,

		PriorLiquidityEvents: len(liquidityChanges),

		PriorSwapEvents: len(swaps),

		ReplayedEvents: len(events),

		SwapReplays: cloneBurnSwapReplayAudits(
			swapReplays,
		),

		LastReplayedCursor: lastCursor,

		BurnRangeActive: burnRangeActive,

		ActiveLiquidityBeforeBurn: new(big.Int).Set(
			replayedPool.Liquidity,
		),
	}, nil
}

func (s *PreBurnStateService) fetchSwapsBeforeBurn(
	ctx context.Context,
	pool domain.Pool,
	burnCursor domain.EventCursor,
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
				burnCursor.BlockNumber,
				burnCursor.BlockNumber,
				afterID,
				pageSize,
				pool.Token0Decimals,
				pool.Token1Decimals,
			)
		if err != nil {
			return nil, err
		}

		for _, swap := range page {
			if swap.BlockNumber !=
				burnCursor.BlockNumber {
				return nil, fmt.Errorf(
					"swap %s belongs to block %d, expected %d",
					swap.ID,
					swap.BlockNumber,
					burnCursor.BlockNumber,
				)
			}

			if swap.Cursor().Before(
				burnCursor,
			) {
				if err :=
					swap.ValidateForObservation(); err != nil {
					return nil, fmt.Errorf(
						"prior swap %s: %w",
						swap.ID,
						err,
					)
				}

				result = append(
					result,
					swap,
				)
			}
		}

		if len(page) <
			pageSize {
			break
		}

		nextAfterID :=
			page[len(page)-1].ID

		if nextAfterID == "" ||
			nextAfterID <= afterID {
			return nil, fmt.Errorf(
				"swap cursor did not advance: previous=%q next=%q",
				afterID,
				nextAfterID,
			)
		}

		afterID =
			nextAfterID
	}

	return result, nil
}

func mergePreBurnEvents(
	burnCursor domain.EventCursor,
	liquidityChanges []domain.LiquidityChange,
	swaps []domain.SwapEvent,
) ([]domain.PoolBlockEvent, error) {
	if err := burnCursor.Validate(); err != nil {
		return nil, err
	}

	events := make(
		[]domain.PoolBlockEvent,
		0,
		len(liquidityChanges)+len(swaps),
	)

	for _, change := range liquidityChanges {
		event, err :=
			domain.NewLiquidityPoolBlockEvent(
				change,
			)
		if err != nil {
			return nil, err
		}

		if event.Cursor.BlockNumber !=
			burnCursor.BlockNumber ||
			!event.Cursor.Before(
				burnCursor,
			) {
			return nil, fmt.Errorf(
				"liquidity event %s at %s is not before burn %s",
				change.ID,
				event.Cursor,
				burnCursor,
			)
		}

		events = append(
			events,
			event,
		)
	}

	for _, swap := range swaps {
		event, err :=
			domain.NewSwapPoolBlockEvent(
				swap,
			)
		if err != nil {
			return nil, err
		}

		if event.Cursor.BlockNumber !=
			burnCursor.BlockNumber ||
			!event.Cursor.Before(
				burnCursor,
			) {
			return nil, fmt.Errorf(
				"swap event %s at %s is not before burn %s",
				swap.ID,
				event.Cursor,
				burnCursor,
			)
		}

		events = append(
			events,
			event,
		)
	}

	sort.SliceStable(
		events,
		func(i int, j int) bool {
			return events[i].
				Cursor.
				Before(
					events[j].Cursor,
				)
		},
	)

	for index := range events {
		if err :=
			events[index].Validate(); err != nil {
			return nil, fmt.Errorf(
				"event %d: %w",
				index,
				err,
			)
		}

		if index > 0 &&
			events[index-1].
				Cursor.
				Equal(
					events[index].Cursor,
				) {
			return nil, fmt.Errorf(
				"duplicate pool event cursor %s for %s and %s",
				events[index].Cursor,
				events[index-1].Type,
				events[index].Type,
			)
		}
	}

	return events, nil
}

func replayPoolBlockEvents(
	initialPool *domain.ReconstructedPool,
	events []domain.PoolBlockEvent,
	simulator *uniswapv3.Simulator,
) (
	*domain.ReconstructedPool,
	*domain.EventCursor,
	[]BurnSwapReplayAudit,
	error,
) {
	if initialPool == nil {
		return nil, nil, nil, fmt.Errorf(
			"initial pool is nil",
		)
	}

	if simulator == nil {
		return nil, nil, nil, fmt.Errorf(
			"simulator is nil",
		)
	}

	current :=
		initialPool

	var lastCursor *domain.EventCursor

	swapReplays := make(
		[]BurnSwapReplayAudit,
		0,
	)

	for index, event := range events {
		if err := event.Validate(); err != nil {
			return nil, nil, nil, fmt.Errorf(
				"event %d validation: %w",
				index,
				err,
			)
		}

		if lastCursor != nil &&
			!lastCursor.Before(
				event.Cursor,
			) {
			return nil, nil, nil, fmt.Errorf(
				"event ordering is not strictly increasing: previous=%s current=%s",
				lastCursor,
				event.Cursor,
			)
		}

		switch event.Type {
		case domain.PoolBlockEventMint,
			domain.PoolBlockEventBurn:
			next, err :=
				uniswapv3.ApplyLiquidityChange(
					current,
					*event.LiquidityChange,
				)
			if err != nil {
				return nil, nil, nil, fmt.Errorf(
					"apply %s event at %s: %w",
					event.Type,
					event.Cursor,
					err,
				)
			}

			current =
				next

		case domain.PoolBlockEventSwap:
			next,
				replayAudit,
				err :=
				replayObservedSwap(
					current,
					*event.Swap,
					simulator,
				)
			if err != nil {
				return nil, nil, nil, fmt.Errorf(
					"replay swap %s at %s: %w",
					event.Swap.ID,
					event.Cursor,
					err,
				)
			}

			current =
				next

			swapReplays =
				append(
					swapReplays,
					replayAudit,
				)

		default:
			return nil, nil, nil, fmt.Errorf(
				"unsupported event type %s",
				event.Type,
			)
		}

		cursorCopy :=
			event.Cursor

		lastCursor =
			&cursorCopy
	}

	return current,
		lastCursor,
		swapReplays,
		nil
}

type observedSwapReplayMode string

const (
	observedSwapReplayExactInput observedSwapReplayMode = "exact_input"

	observedSwapReplayExactOutput observedSwapReplayMode = "exact_output"

	observedSwapReplayBoth observedSwapReplayMode = "exact_input_and_exact_output"

	// The Swap event does not expose amountSpecified. When neither reconstructed
	// protocol mode reproduces the observed event, counterfactual analysis keeps
	// both exact-input and exact-output interpretations as an explicit sensitivity
	// envelope instead of failing the entire empirical run. Historical actual-state
	// reconstruction still follows the authoritative post-state emitted on-chain.
	observedSwapReplayUnresolved observedSwapReplayMode = "unresolved_sensitivity"

	// Swap events expose integer token deltas, not the original amountSpecified.
	// Around integer-rounding boundaries, the same observed deltas can correspond
	// to a tiny interval of valid sqrtPriceX96 values. We accept only an
	// economically negligible relative sqrt difference: 1e-16 = 1e-12 bps.
	observedSwapSqrtRelativeToleranceDenominator int64 = 10_000_000_000_000_000

	// Multi-step swaps can accumulate one-wei counter-amount rounding differences
	// between the reconstructed simulator and the integer token deltas emitted by
	// the pool. The amountSpecified side must remain exact. Only the counter side
	// receives this strict tolerance: max(1 raw unit, one raw unit per swap
	// step, 1e-12 relative).
	observedSwapCounterAmountAbsoluteToleranceRaw         int64 = 1
	observedSwapCounterAmountRelativeToleranceDenominator int64 = 1_000_000_000_000
)

type observedSwapSimulation struct {
	Mode observedSwapReplayMode

	AmountIn        *big.Int
	AmountInLessFee *big.Int
	FeeAmount       *big.Int
	AmountOut       *big.Int

	SqrtPriceAfterX96 *big.Int
	TickAfter         int
	LiquidityAfter    *big.Int

	CrossedTicks int
	SwapSteps    int
}

func replayObservedSwap(
	current *domain.ReconstructedPool,
	swap domain.SwapEvent,
	simulator *uniswapv3.Simulator,
) (
	*domain.ReconstructedPool,
	BurnSwapReplayAudit,
	error,
) {
	if current == nil {
		return nil,
			BurnSwapReplayAudit{},
			fmt.Errorf(
				"current pool is nil",
			)
	}

	if simulator == nil {
		return nil,
			BurnSwapReplayAudit{},
			fmt.Errorf(
				"simulator is nil",
			)
	}

	if err :=
		swap.ValidateForObservation(); err != nil {
		return nil,
			BurnSwapReplayAudit{},
			fmt.Errorf(
				"invalid observed swap: %w",
				err,
			)
	}

	exactInput,
		exactInputErr :=
		simulateObservedSwapAsExactInput(
			current,
			swap,
			simulator,
		)

	var (
		exactOutput    observedSwapSimulation
		exactOutputErr error
	)

	// A zero-output event cannot originate from exact-output semantics because
	// exact output requires a strictly positive requested output. It can still
	// be a valid exact-input event when all usable input and/or output rounds to
	// zero, including a fee-only swap.
	if swap.AmountOutRaw().Sign() == 0 {
		exactOutput = observedSwapSimulation{
			Mode: observedSwapReplayExactOutput,
		}
		exactOutputErr = fmt.Errorf(
			"observed output is zero; exact-output mode is unavailable",
		)
	} else {
		exactOutput,
			exactOutputErr =
			simulateObservedSwapAsExactOutput(
				current,
				swap,
				simulator,
			)
	}

	exactInputMatches :=
		exactInputErr == nil &&
			observedSwapSimulationMatches(
				exactInput,
				swap,
			)

	exactOutputMatches :=
		exactOutputErr == nil &&
			observedSwapSimulationMatches(
				exactOutput,
				swap,
			)

	if !exactInputMatches &&
		!exactOutputMatches {
		return nil,
			BurnSwapReplayAudit{},
			fmt.Errorf(
				"observed swap matches neither protocol mode: exact_input={%s} exact_output={%s}",
				describeObservedSwapSimulation(
					exactInput,
					exactInputErr,
					swap,
				),
				describeObservedSwapSimulation(
					exactOutput,
					exactOutputErr,
					swap,
				),
			)
	}

	selected :=
		exactInput

	if exactInputMatches &&
		exactOutputMatches {
		var err error
		selected, err =
			selectObservedSwapSimulation(
				exactInput,
				exactOutput,
				swap,
			)
		if err != nil {
			return nil,
				BurnSwapReplayAudit{},
				fmt.Errorf(
					"select protocol mode: %w",
					err,
				)
		}
	} else if exactOutputMatches {
		selected =
			exactOutput
	}

	next :=
		*current

	next.SqrtPriceX96 =
		new(big.Int).Set(
			swap.SqrtPriceX96After,
		)

	next.CurrentTick =
		swap.TickAfter

	next.Liquidity =
		new(big.Int).Set(
			selected.LiquidityAfter,
		)

	sqrtDifference :=
		new(big.Int).Sub(
			selected.SqrtPriceAfterX96,
			swap.SqrtPriceX96After,
		)

	sqrtDifference.Abs(
		sqrtDifference,
	)

	audit :=
		BurnSwapReplayAudit{
			SwapID: swap.ID,
			TxHash: swap.TxHash,

			Cursor: swap.Cursor(),

			ZeroForOne: swap.IsZeroForOne(),

			Mode: SwapReplayMode(
				selected.Mode,
			),

			AmountInRaw: cloneBigInt(
				swap.AmountInRaw(),
			),

			AmountOutRaw: cloneBigInt(
				swap.AmountOutRaw(),
			),

			SqrtPriceX96After: cloneBigInt(
				swap.SqrtPriceX96After,
			),

			SimulatedSqrtPriceX96After: cloneBigInt(
				selected.SqrtPriceAfterX96,
			),

			SqrtPriceAbsDiffRaw: cloneBigInt(
				sqrtDifference,
			),

			SqrtPriceExact: sqrtDifference.Sign() == 0,

			SqrtPriceWithinTolerance: observedSwapSimulationSqrtWithinTolerance(
				selected,
				swap,
			),

			TickAfter: swap.TickAfter,

			SwapSteps: selected.SwapSteps,

			CrossedTicks: selected.CrossedTicks,
		}

	if err := audit.Validate(); err != nil {
		return nil,
			BurnSwapReplayAudit{},
			fmt.Errorf(
				"build replay audit: %w",
				err,
			)
	}

	return &next,
		audit,
		nil
}

func selectObservedSwapSimulation(
	exactInput observedSwapSimulation,
	exactOutput observedSwapSimulation,
	swap domain.SwapEvent,
) (observedSwapSimulation, error) {
	if sameObservedSwapFinalState(
		exactInput,
		exactOutput,
	) {
		selected :=
			exactInput

		selected.Mode =
			observedSwapReplayBoth

		return selected, nil
	}

	exactInputDifference, err :=
		observedSwapSimulationSqrtAbsDifference(
			exactInput,
			swap,
		)
	if err != nil {
		return observedSwapSimulation{},
			fmt.Errorf(
				"exact-input sqrt difference: %w",
				err,
			)
	}

	exactOutputDifference, err :=
		observedSwapSimulationSqrtAbsDifference(
			exactOutput,
			swap,
		)
	if err != nil {
		return observedSwapSimulation{},
			fmt.Errorf(
				"exact-output sqrt difference: %w",
				err,
			)
	}

	switch exactInputDifference.Cmp(
		exactOutputDifference,
	) {
	case -1:
		return exactInput, nil

	case 1:
		return exactOutput, nil

	default:
		return observedSwapSimulation{},
			fmt.Errorf(
				"exact-input and exact-output replay are equally close to the observed sqrt price but disagree on final state: exact_input={%s} exact_output={%s}",
				describeObservedSwapSimulation(
					exactInput,
					nil,
					swap,
				),
				describeObservedSwapSimulation(
					exactOutput,
					nil,
					swap,
				),
			)
	}
}

func observedSwapSimulationSqrtAbsDifference(
	simulation observedSwapSimulation,
	swap domain.SwapEvent,
) (*big.Int, error) {
	if simulation.SqrtPriceAfterX96 == nil {
		return nil, fmt.Errorf(
			"simulated sqrt price is nil",
		)
	}

	if swap.SqrtPriceX96After == nil ||
		swap.SqrtPriceX96After.Sign() <= 0 {
		return nil, fmt.Errorf(
			"observed sqrt price is invalid: %v",
			swap.SqrtPriceX96After,
		)
	}

	difference :=
		new(big.Int).Sub(
			simulation.SqrtPriceAfterX96,
			swap.SqrtPriceX96After,
		)

	difference.Abs(
		difference,
	)

	return difference, nil
}

func simulateObservedSwapAsExactInput(
	pool *domain.ReconstructedPool,
	swap domain.SwapEvent,
	simulator *uniswapv3.Simulator,
) (observedSwapSimulation, error) {
	allowZeroOutput := swap.AmountOutRaw().Sign() == 0

	result, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn: swap.AmountInRaw(),

			ZeroForOne: swap.IsZeroForOne(),

			AllowZeroOutput: allowZeroOutput,
		},
	)
	if err != nil {
		return observedSwapSimulation{
			Mode: observedSwapReplayExactInput,
		}, err
	}

	if err := validateObservedExactInputSimulationResult(
		result,
		swap.AmountInRaw(),
		allowZeroOutput,
	); err != nil {
		return observedSwapSimulation{
			Mode: observedSwapReplayExactInput,
		}, err
	}

	return observedSwapSimulation{
		Mode: observedSwapReplayExactInput,

		AmountIn: cloneBigInt(
			result.AmountIn,
		),

		AmountInLessFee: cloneBigInt(
			result.AmountInLessFee,
		),

		FeeAmount: cloneBigInt(
			result.FeeAmount,
		),

		AmountOut: cloneBigInt(
			result.AmountOut,
		),

		SqrtPriceAfterX96: cloneBigInt(
			result.SqrtPriceAfterX96,
		),

		TickAfter: result.TickAfter,

		LiquidityAfter: cloneBigInt(
			result.LiquidityAfter,
		),

		CrossedTicks: result.CrossedTicks,

		SwapSteps: result.SwapSteps,
	}, nil
}

func validateObservedExactInputSimulationResult(
	result *uniswapv3.ExactInputResult,
	expectedAmountIn *big.Int,
	allowZeroOutput bool,
) error {
	if !allowZeroOutput {
		return validateSwapSimulationResult(
			result,
			expectedAmountIn,
		)
	}

	if result == nil {
		return fmt.Errorf(
			"simulator returned nil historical replay result",
		)
	}

	if expectedAmountIn == nil ||
		expectedAmountIn.Sign() <= 0 {
		return fmt.Errorf(
			"expected historical amount in must be positive",
		)
	}

	if result.AmountIn == nil ||
		result.AmountIn.Cmp(expectedAmountIn) != 0 {
		return fmt.Errorf(
			"historical replay gross input does not match observed input",
		)
	}

	if result.AmountInLessFee == nil ||
		result.AmountInLessFee.Sign() < 0 {
		return fmt.Errorf(
			"historical replay usable input is invalid: %v",
			result.AmountInLessFee,
		)
	}

	if result.FeeAmount == nil ||
		result.FeeAmount.Sign() < 0 {
		return fmt.Errorf(
			"historical replay fee amount is invalid: %v",
			result.FeeAmount,
		)
	}

	if result.AmountOut == nil ||
		result.AmountOut.Sign() < 0 {
		return fmt.Errorf(
			"historical replay output is invalid: %v",
			result.AmountOut,
		)
	}

	if result.SqrtPriceAfterX96 == nil ||
		result.SqrtPriceAfterX96.Sign() <= 0 {
		return fmt.Errorf(
			"historical replay sqrt price after is invalid: %v",
			result.SqrtPriceAfterX96,
		)
	}

	if result.LiquidityAfter == nil ||
		result.LiquidityAfter.Sign() < 0 ||
		result.LiquidityAfter.BitLen() > 128 {
		return fmt.Errorf(
			"historical replay active liquidity after is invalid: %v",
			result.LiquidityAfter,
		)
	}

	if result.TickAfter != result.TickAfterApprox {
		return fmt.Errorf(
			"historical replay exact tick %d differs from compatibility tick %d",
			result.TickAfter,
			result.TickAfterApprox,
		)
	}

	if result.CrossedTicks < 0 ||
		result.SwapSteps <= 0 ||
		result.CrossedTicks > result.SwapSteps {
		return fmt.Errorf(
			"invalid historical replay step accounting: crossed_ticks=%d swap_steps=%d",
			result.CrossedTicks,
			result.SwapSteps,
		)
	}

	accountedInput := new(big.Int).Add(
		cloneBigInt(result.AmountInLessFee),
		result.FeeAmount,
	)

	if accountedInput.Cmp(expectedAmountIn) != 0 {
		return fmt.Errorf(
			"historical replay input accounting mismatch: observed=%s usable=%s fee=%s accounted=%s",
			expectedAmountIn,
			result.AmountInLessFee,
			result.FeeAmount,
			accountedInput,
		)
	}

	if result.AmountInLessFee.Sign() == 0 &&
		result.AmountOut.Sign() != 0 {
		return fmt.Errorf(
			"fee-only historical replay produced non-zero output %s",
			result.AmountOut,
		)
	}

	return nil
}

func simulateObservedSwapAsExactOutput(
	pool *domain.ReconstructedPool,
	swap domain.SwapEvent,
	simulator *uniswapv3.Simulator,
) (observedSwapSimulation, error) {
	result, err := simulator.SimulateExactOutput(
		pool,
		uniswapv3.ExactOutputRequest{
			AmountOut: swap.AmountOutRaw(),

			ZeroForOne: swap.IsZeroForOne(),
		},
	)
	if err != nil {
		return observedSwapSimulation{
			Mode: observedSwapReplayExactOutput,
		}, err
	}

	if err := validateExactOutputSimulationResult(
		result,
		swap.AmountOutRaw(),
	); err != nil {
		return observedSwapSimulation{
			Mode: observedSwapReplayExactOutput,
		}, err
	}

	return observedSwapSimulation{
		Mode: observedSwapReplayExactOutput,

		AmountIn: cloneBigInt(
			result.AmountIn,
		),

		AmountInLessFee: cloneBigInt(
			result.AmountInLessFee,
		),

		FeeAmount: cloneBigInt(
			result.FeeAmount,
		),

		AmountOut: cloneBigInt(
			result.AmountOut,
		),

		SqrtPriceAfterX96: cloneBigInt(
			result.SqrtPriceAfterX96,
		),

		TickAfter: result.TickAfter,

		LiquidityAfter: cloneBigInt(
			result.LiquidityAfter,
		),

		CrossedTicks: result.CrossedTicks,

		SwapSteps: result.SwapSteps,
	}, nil
}

func validateExactOutputSimulationResult(
	result *uniswapv3.ExactOutputResult,
	expectedAmountOut *big.Int,
) error {
	if result == nil {
		return fmt.Errorf(
			"exact-output result is nil",
		)
	}

	positiveValues := []struct {
		name  string
		value *big.Int
	}{
		{
			name:  "amount in",
			value: result.AmountIn,
		},
		{
			name:  "amount in less fee",
			value: result.AmountInLessFee,
		},
		{
			name:  "amount out",
			value: result.AmountOut,
		},
		{
			name:  "sqrt price before",
			value: result.SqrtPriceBeforeX96,
		},
		{
			name:  "sqrt price after",
			value: result.SqrtPriceAfterX96,
		},
	}

	for _, item := range positiveValues {
		if item.value == nil ||
			item.value.Sign() <= 0 {
			return fmt.Errorf(
				"exact-output %s is invalid: %v",
				item.name,
				item.value,
			)
		}
	}

	if result.FeeAmount == nil ||
		result.FeeAmount.Sign() < 0 {
		return fmt.Errorf(
			"exact-output fee is invalid: %v",
			result.FeeAmount,
		)
	}

	if result.LiquidityAfter == nil ||
		result.LiquidityAfter.Sign() < 0 {
		return fmt.Errorf(
			"exact-output liquidity after is invalid: %v",
			result.LiquidityAfter,
		)
	}

	if expectedAmountOut == nil ||
		expectedAmountOut.Sign() <= 0 {
		return fmt.Errorf(
			"expected exact-output amount is invalid: %v",
			expectedAmountOut,
		)
	}

	if result.AmountOut.Cmp(
		expectedAmountOut,
	) != 0 {
		return fmt.Errorf(
			"exact-output delivered amount=%s, expected=%s",
			result.AmountOut,
			expectedAmountOut,
		)
	}

	accountedInput := new(big.Int).Add(
		new(big.Int).Set(
			result.AmountInLessFee,
		),
		result.FeeAmount,
	)

	if accountedInput.Cmp(
		result.AmountIn,
	) != 0 {
		return fmt.Errorf(
			"exact-output input accounting mismatch: gross=%s usable=%s fee=%s accounted=%s",
			result.AmountIn,
			result.AmountInLessFee,
			result.FeeAmount,
			accountedInput,
		)
	}

	if result.SwapSteps <= 0 {
		return fmt.Errorf(
			"exact-output swap steps must be positive: %d",
			result.SwapSteps,
		)
	}

	if result.CrossedTicks < 0 ||
		result.CrossedTicks > result.SwapSteps {
		return fmt.Errorf(
			"exact-output crossed ticks=%d is invalid for swap steps=%d",
			result.CrossedTicks,
			result.SwapSteps,
		)
	}

	return nil
}

func observedSwapSimulationMatches(
	simulation observedSwapSimulation,
	swap domain.SwapEvent,
) bool {
	return observedSwapSimulationAmountsAndTickMatch(
		simulation,
		swap,
	) &&
		observedSwapSimulationSqrtWithinTolerance(
			simulation,
			swap,
		)
}

func observedSwapSimulationAmountsAndTickMatch(
	simulation observedSwapSimulation,
	swap domain.SwapEvent,
) bool {
	observedAmountIn := swap.AmountInRaw()
	observedAmountOut := swap.AmountOutRaw()

	if simulation.AmountIn == nil ||
		simulation.AmountOut == nil ||
		simulation.SqrtPriceAfterX96 == nil ||
		simulation.LiquidityAfter == nil ||
		observedAmountIn == nil ||
		observedAmountOut == nil ||
		simulation.TickAfter != swap.TickAfter {
		return false
	}

	switch simulation.Mode {
	case observedSwapReplayExactInput:
		// Exact-input replay must consume the exact observed gross input. A
		// zero-output event must remain exactly zero; for positive outputs, the
		// counter amount may differ only by bounded integer rounding accumulated
		// across swap steps.
		if simulation.AmountIn.Cmp(observedAmountIn) != 0 {
			return false
		}

		if observedAmountOut.Sign() == 0 {
			return simulation.AmountOut.Sign() == 0
		}

		return observedSwapCounterAmountWithinTolerance(
			simulation.AmountOut,
			observedAmountOut,
			simulation.SwapSteps,
		)

	case observedSwapReplayExactOutput:
		// Exact-output replay must deliver the exact observed output. The input
		// is the counter amount and receives the same strict rounding bound.
		return simulation.AmountOut.Cmp(observedAmountOut) == 0 &&
			observedSwapCounterAmountWithinTolerance(
				simulation.AmountIn,
				observedAmountIn,
				simulation.SwapSteps,
			)

	default:
		return false
	}
}

func observedSwapCounterAmountWithinTolerance(
	simulated *big.Int,
	observed *big.Int,
	swapSteps int,
) bool {
	if simulated == nil ||
		observed == nil ||
		simulated.Sign() < 0 ||
		observed.Sign() < 0 {
		return false
	}

	difference := new(big.Int).Sub(
		simulated,
		observed,
	)
	difference.Abs(difference)

	absoluteTolerance := observedSwapCounterAmountAbsoluteToleranceRaw
	if int64(swapSteps) > absoluteTolerance {
		absoluteTolerance = int64(swapSteps)
	}

	if difference.Cmp(
		big.NewInt(absoluteTolerance),
	) <= 0 {
		return true
	}

	if observed.Sign() == 0 {
		return false
	}

	scaledDifference := new(big.Int).Mul(
		difference,
		big.NewInt(
			observedSwapCounterAmountRelativeToleranceDenominator,
		),
	)

	return scaledDifference.Cmp(observed) <= 0
}

func observedSwapSimulationSqrtWithinTolerance(
	simulation observedSwapSimulation,
	swap domain.SwapEvent,
) bool {
	if simulation.SqrtPriceAfterX96 == nil ||
		swap.SqrtPriceX96After == nil ||
		swap.SqrtPriceX96After.Sign() <= 0 {
		return false
	}

	difference :=
		new(big.Int).Sub(
			simulation.SqrtPriceAfterX96,
			swap.SqrtPriceX96After,
		)

	difference.Abs(
		difference,
	)

	if difference.Sign() == 0 {
		return true
	}

	scaledDifference :=
		new(big.Int).Mul(
			difference,
			big.NewInt(
				observedSwapSqrtRelativeToleranceDenominator,
			),
		)

	return scaledDifference.Cmp(
		swap.SqrtPriceX96After,
	) <= 0
}

func sameObservedSwapFinalState(
	left observedSwapSimulation,
	right observedSwapSimulation,
) bool {
	return left.SqrtPriceAfterX96 != nil &&
		right.SqrtPriceAfterX96 != nil &&
		left.LiquidityAfter != nil &&
		right.LiquidityAfter != nil &&
		left.SqrtPriceAfterX96.Cmp(
			right.SqrtPriceAfterX96,
		) == 0 &&
		left.TickAfter == right.TickAfter &&
		left.LiquidityAfter.Cmp(
			right.LiquidityAfter,
		) == 0
}

func describeObservedSwapSimulation(
	simulation observedSwapSimulation,
	simulationErr error,
	swap domain.SwapEvent,
) string {
	if simulationErr != nil {
		return fmt.Sprintf(
			"mode=%s error=%q",
			simulation.Mode,
			simulationErr,
		)
	}

	return fmt.Sprintf(
		"mode=%s input=%s/%s output=%s/%s sqrt=%s/%s tick=%d/%d liquidity_after=%s crossed_ticks=%d swap_steps=%d",
		simulation.Mode,
		formatOptionalBigInt(
			simulation.AmountIn,
		),
		swap.AmountInRaw(),
		formatOptionalBigInt(
			simulation.AmountOut,
		),
		swap.AmountOutRaw(),
		formatOptionalBigInt(
			simulation.SqrtPriceAfterX96,
		),
		swap.SqrtPriceX96After,
		simulation.TickAfter,
		swap.TickAfter,
		formatOptionalBigInt(
			simulation.LiquidityAfter,
		),
		simulation.CrossedTicks,
		simulation.SwapSteps,
	)
}

func formatOptionalBigInt(
	value *big.Int,
) string {
	if value == nil {
		return "<nil>"
	}

	return value.String()
}

func validateBurnCanApplyToPool(
	pool *domain.ReconstructedPool,
	burn domain.BurnCandidate,
) error {
	if pool == nil {
		return fmt.Errorf(
			"pool is nil",
		)
	}

	lower, lowerExists :=
		pool.Ticks[burn.TickLower]

	if !lowerExists ||
		lower == nil ||
		lower.LiquidityGross == nil {
		return fmt.Errorf(
			"burn lower tick %d is not initialized",
			burn.TickLower,
		)
	}

	upper, upperExists :=
		pool.Ticks[burn.TickUpper]

	if !upperExists ||
		upper == nil ||
		upper.LiquidityGross == nil {
		return fmt.Errorf(
			"burn upper tick %d is not initialized",
			burn.TickUpper,
		)
	}

	if lower.LiquidityGross.Cmp(
		burn.LiquidityRemoved,
	) < 0 {
		return fmt.Errorf(
			"lower tick gross liquidity %s is below burn amount %s",
			lower.LiquidityGross,
			burn.LiquidityRemoved,
		)
	}

	if upper.LiquidityGross.Cmp(
		burn.LiquidityRemoved,
	) < 0 {
		return fmt.Errorf(
			"upper tick gross liquidity %s is below burn amount %s",
			upper.LiquidityGross,
			burn.LiquidityRemoved,
		)
	}

	isActive :=
		burn.TickLower <=
			pool.CurrentTick &&
			pool.CurrentTick <
				burn.TickUpper

	if isActive &&
		pool.Liquidity.Cmp(
			burn.LiquidityRemoved,
		) < 0 {
		return fmt.Errorf(
			"active liquidity %s is below active burn amount %s",
			pool.Liquidity,
			burn.LiquidityRemoved,
		)
	}

	return nil
}
