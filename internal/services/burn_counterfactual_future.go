package services

import (
	"context"
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type BurnCounterfactualTieBreak string

const (
	BurnCounterfactualExactInputTieBreak  BurnCounterfactualTieBreak = "exact_input_tiebreak"
	BurnCounterfactualExactOutputTieBreak BurnCounterfactualTieBreak = "exact_output_tiebreak"
)

type BurnCounterfactualDirectionalResult struct {
	ZeroForOne bool

	ActualFutureAUCBps decimal.Decimal
	NoBurnFutureAUCBps decimal.Decimal

	// Positive means the focal Burn made future execution quality worse.
	MechanicalEffectPIAUCBps   decimal.Decimal
	MechanicalDeteriorationBps decimal.Decimal

	ActualDepths []ThresholdDepth
	NoBurnDepths []ThresholdDepth
}

type BurnCounterfactualBranchResult struct {
	TieBreak  BurnCounterfactualTieBreak
	Available bool

	FailureCursor *domain.EventCursor
	FailureDetail string

	FutureCurrentTick     int
	FutureSqrtPriceX96    *big.Int
	FutureActiveLiquidity *big.Int

	ZeroForOne BurnCounterfactualDirectionalResult
	OneForZero BurnCounterfactualDirectionalResult

	TotalMechanicalEffectPIAUCBps            decimal.Decimal
	TotalMechanicalDeteriorationBps          decimal.Decimal
	MaxDirectionalMechanicalDeteriorationBps decimal.Decimal
}

type BurnCounterfactualSwapReplayAudit struct {
	Burn domain.BurnCandidate

	SwapID     string
	TxHash     string
	Cursor     domain.EventCursor
	ZeroForOne bool

	AmountInRaw  *big.Int
	AmountOutRaw *big.Int

	ActualReplayMode   observedSwapReplayMode
	ActualModeResolved bool
	ActualAmbiguous    bool
	ActualReplayDetail string

	ActualTickAfter            int
	ActualSqrtPriceX96After    *big.Int
	ActualActiveLiquidityAfter *big.Int

	ExactInputTieBreakMode         observedSwapReplayMode
	ExactInputAvailable            bool
	ExactInputFailureDetail        string
	ExactInputTickAfter            int
	ExactInputSqrtPriceX96After    *big.Int
	ExactInputActiveLiquidityAfter *big.Int

	ExactOutputTieBreakMode         observedSwapReplayMode
	ExactOutputAvailable            bool
	ExactOutputFailureDetail        string
	ExactOutputTickAfter            int
	ExactOutputSqrtPriceX96After    *big.Int
	ExactOutputActiveLiquidityAfter *big.Int
}

type BurnCounterfactualObservation struct {
	Burn domain.BurnCandidate

	HorizonLabel  string
	HorizonBlocks uint64
	FutureBlock   uint64

	FutureLiquidityEvents int
	FutureSwapEvents      int
	AmbiguousSwapModes    int

	ActualFutureCurrentTick     int
	ActualFutureSqrtPriceX96    *big.Int
	ActualFutureActiveLiquidity *big.Int

	ExactInputTieBreak  BurnCounterfactualBranchResult
	ExactOutputTieBreak BurnCounterfactualBranchResult

	BothBranchesAvailable bool

	MinTotalMechanicalEffectPIAUCBps   decimal.Decimal
	MaxTotalMechanicalEffectPIAUCBps   decimal.Decimal
	MinTotalMechanicalDeteriorationBps decimal.Decimal
	MaxTotalMechanicalDeteriorationBps decimal.Decimal
}

type BurnCounterfactualDepthObservation struct {
	Burn                      domain.BurnCandidate
	HorizonLabel              string
	HorizonBlocks             uint64
	FutureBlock               uint64
	TieBreak                  BurnCounterfactualTieBreak
	Available                 bool
	FailureDetail             string
	ZeroForOne                bool
	ThresholdBps              decimal.Decimal
	ActualFutureDepthAmount   decimal.Decimal
	NoBurnFutureDepthAmount   decimal.Decimal
	MechanicalDepthLossAmount decimal.Decimal
	ActualBreached            bool
	NoBurnBreached            bool
}

type BurnCounterfactualFutureReport struct {
	PoolAddress           string
	FromBlock             uint64
	ToBlock               uint64
	BurnSamples           int
	HorizonsPerSample     int
	ExpectedObservations  int
	Observations          []BurnCounterfactualObservation
	Depths                []BurnCounterfactualDepthObservation
	ReplayAudits          []BurnCounterfactualSwapReplayAudit
	BothBranchesAvailable int
	SingleBranchAvailable int
	NoBranchAvailable     int
	AmbiguousSwapModes    int
}

type BurnCounterfactualFutureService struct {
	outcomeService *BurnRealizedOutcomeService

	// A historical Swap has the same authoritative pre-state across every Burn
	// sample whose future window contains it. Cache protocol-mode classification
	// once per on-chain cursor so overlapping samples do not repeat two expensive
	// simulator runs for the same actual event.
	observedModeCache sync.Map
}

func NewBurnCounterfactualFutureService(
	outcomeService *BurnRealizedOutcomeService,
) *BurnCounterfactualFutureService {
	return &BurnCounterfactualFutureService{outcomeService: outcomeService}
}

type burnCounterfactualObservedModeCacheKey struct {
	PoolAddress  string
	BlockNumber  uint64
	LogIndex     int
	PreTick      int
	PreSqrt      string
	PreLiquidity string
}

type burnCounterfactualObservedModeCacheValue struct {
	Mode   observedSwapReplayMode
	Detail string
}

type burnCounterfactualObservedModeResolution struct {
	Mode   observedSwapReplayMode
	Detail string
}

type BurnCounterfactualFutureRequest struct {
	Collection          BurnSampleCollectionReport
	Realized            BurnRealizedDatasetReport
	Horizons            []BurnOutcomeHorizon
	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int
	ThresholdsBps       []decimal.Decimal
}

const maxBurnCounterfactualWorkers = 8

type burnCounterfactualSampleBuildResult struct {
	Index          int
	Observations   []BurnCounterfactualObservation
	Depths         []BurnCounterfactualDepthObservation
	ReplayAudits   []BurnCounterfactualSwapReplayAudit
	AmbiguousModes int
	Err            error
}

func (s *BurnCounterfactualFutureService) workerCount(
	samples []BurnEventSample,
	horizons []BurnOutcomeHorizon,
) int {
	if len(samples) <= 1 {
		return 1
	}

	// Parallel counterfactual execution is safe and fast only when the realized
	// dataset has already populated every burn-to-max-horizon event stream. If
	// any cache entry is missing, remain sequential to avoid concurrent pressure
	// on The Graph and preserve its strict block-chunking behavior.
	maximumHorizon := horizons[len(horizons)-1].Blocks
	for _, sample := range samples {
		throughBlock := sample.Burn.Cursor.BlockNumber + maximumHorizon
		key := newBurnRealizedFlowCacheKey(
			sample.Burn,
			throughBlock,
		)
		if _, exists := s.outcomeService.cachedBurnRealizedFlowEvents(key); !exists {
			return 1
		}
	}

	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > maxBurnCounterfactualWorkers {
		workers = maxBurnCounterfactualWorkers
	}
	if workers > len(samples) {
		workers = len(samples)
	}

	return workers
}

func (s *BurnCounterfactualFutureService) Build(
	ctx context.Context,
	req BurnCounterfactualFutureRequest,
) (BurnCounterfactualFutureReport, error) {
	if s == nil || s.outcomeService == nil {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: service is nil",
		)
	}
	if s.outcomeService.curveService == nil ||
		s.outcomeService.curveService.simulator == nil {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: curve service is nil",
		)
	}
	if err := validateBurnRealizedDatasetReport(req.Realized); err != nil {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: invalid realized report: %w",
			err,
		)
	}
	if err := validateBurnSampleCollectionReportForCounterfactual(req.Collection); err != nil {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: invalid collection: %w",
			err,
		)
	}

	horizons, err := normalizeBurnOutcomeHorizons(req.Collection.FromBlock, req.Horizons)
	if err != nil {
		return BurnCounterfactualFutureReport{}, err
	}
	zeroForOneAmounts, err := normalizeBurnAmountGrid(
		"counterfactual zero_for_one",
		req.ZeroForOneAmountsIn,
	)
	if err != nil {
		return BurnCounterfactualFutureReport{}, err
	}
	oneForZeroAmounts, err := normalizeBurnAmountGrid(
		"counterfactual one_for_zero",
		req.OneForZeroAmountsIn,
	)
	if err != nil {
		return BurnCounterfactualFutureReport{}, err
	}
	thresholds, err := normalizeBurnThresholds(req.ThresholdsBps)
	if err != nil {
		return BurnCounterfactualFutureReport{}, err
	}

	if req.Realized.BurnSamples != len(req.Collection.Samples) {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: realized burn samples=%d collection samples=%d",
			req.Realized.BurnSamples,
			len(req.Collection.Samples),
		)
	}

	actualByKey := make(
		map[string]BurnRegressionObservation,
		len(req.Realized.Observations),
	)
	for _, observation := range req.Realized.Observations {
		key := burnCounterfactualObservationKey(
			observation.Burn.EventKey(),
			observation.HorizonLabel,
		)
		if _, exists := actualByKey[key]; exists {
			return BurnCounterfactualFutureReport{}, fmt.Errorf(
				"build burn counterfactual future report: duplicate realized observation %s",
				key,
			)
		}
		actualByKey[key] = observation
	}

	report := BurnCounterfactualFutureReport{
		PoolAddress:          normalizeAddress(req.Realized.PoolAddress),
		FromBlock:            req.Realized.FromBlock,
		ToBlock:              req.Realized.ToBlock,
		BurnSamples:          len(req.Collection.Samples),
		HorizonsPerSample:    len(horizons),
		ExpectedObservations: len(req.Collection.Samples) * len(horizons),
		Observations: make(
			[]BurnCounterfactualObservation,
			0,
			len(req.Collection.Samples)*len(horizons),
		),
		Depths: make(
			[]BurnCounterfactualDepthObservation,
			0,
			len(req.Collection.Samples)*len(horizons)*len(thresholds)*4,
		),
		ReplayAudits: make(
			[]BurnCounterfactualSwapReplayAudit,
			0,
		),
	}

	workerCount := s.workerCount(
		req.Collection.Samples,
		horizons,
	)

	jobs := make(chan int)
	results := make(
		chan burnCounterfactualSampleBuildResult,
		workerCount,
	)

	var workers sync.WaitGroup
	workers.Add(workerCount)

	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		go func() {
			defer workers.Done()

			for sampleIndex := range jobs {
				sample := req.Collection.Samples[sampleIndex]

				if err := ctx.Err(); err != nil {
					results <- burnCounterfactualSampleBuildResult{
						Index: sampleIndex,
						Err:   err,
					}
					continue
				}

				if err := sample.Validate(); err != nil {
					results <- burnCounterfactualSampleBuildResult{
						Index: sampleIndex,
						Err: fmt.Errorf(
							"sample %d: %w",
							sampleIndex,
							err,
						),
					}
					continue
				}

				sampleZeroForOneAmounts, sampleOneForZeroAmounts, _, _, _, gridErr := burnEventSampleRuntimeAmountGrids(
					sample,
					zeroForOneAmounts,
					oneForZeroAmounts,
				)
				if gridErr != nil {
					results <- burnCounterfactualSampleBuildResult{
						Index: sampleIndex,
						Err:   fmt.Errorf("sample amount grid: %w", gridErr),
					}
					continue
				}

				observations, depths, replayAudits, ambiguousModes, err := s.analyzeSample(
					ctx,
					sample,
					horizons,
					sampleZeroForOneAmounts,
					sampleOneForZeroAmounts,
					thresholds,
					actualByKey,
				)

				results <- burnCounterfactualSampleBuildResult{
					Index:          sampleIndex,
					Observations:   observations,
					Depths:         depths,
					ReplayAudits:   replayAudits,
					AmbiguousModes: ambiguousModes,
					Err:            err,
				}
			}
		}()
	}

	go func() {
		for sampleIndex := range req.Collection.Samples {
			jobs <- sampleIndex
		}
		close(jobs)
	}()

	go func() {
		workers.Wait()
		close(results)
	}()

	sampleResults := make(
		[]burnCounterfactualSampleBuildResult,
		len(req.Collection.Samples),
	)

	for result := range results {
		sampleResults[result.Index] = result
	}

	// Aggregate in original sample order so CSV output remains deterministic
	// regardless of worker scheduling.
	for sampleIndex, result := range sampleResults {
		sample := req.Collection.Samples[sampleIndex]
		if result.Err != nil {
			return BurnCounterfactualFutureReport{}, fmt.Errorf(
				"build burn counterfactual future report: sample %s: %w",
				sample.Burn.EventKey(),
				result.Err,
			)
		}

		report.Observations = append(report.Observations, result.Observations...)
		report.Depths = append(report.Depths, result.Depths...)
		report.ReplayAudits = append(report.ReplayAudits, result.ReplayAudits...)
		report.AmbiguousSwapModes += result.AmbiguousModes

		for _, observation := range result.Observations {
			switch {
			case observation.BothBranchesAvailable:
				report.BothBranchesAvailable++
			case observation.ExactInputTieBreak.Available ||
				observation.ExactOutputTieBreak.Available:
				report.SingleBranchAvailable++
			default:
				report.NoBranchAvailable++
			}
		}
	}

	if len(report.Observations) != report.ExpectedObservations {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: observations=%d expected=%d",
			len(report.Observations),
			report.ExpectedObservations,
		)
	}
	if report.BothBranchesAvailable+
		report.SingleBranchAvailable+
		report.NoBranchAvailable != report.ExpectedObservations {
		return BurnCounterfactualFutureReport{}, fmt.Errorf(
			"build burn counterfactual future report: availability accounting mismatch",
		)
	}

	return report, nil
}

func (s *BurnCounterfactualFutureService) analyzeSample(
	ctx context.Context,
	sample BurnEventSample,
	horizons []BurnOutcomeHorizon,
	zeroForOneAmounts []*big.Int,
	oneForZeroAmounts []*big.Int,
	thresholds []decimal.Decimal,
	actualByKey map[string]BurnRegressionObservation,
) (
	[]BurnCounterfactualObservation,
	[]BurnCounterfactualDepthObservation,
	[]BurnCounterfactualSwapReplayAudit,
	int,
	error,
) {
	preBurnPool, postBurnPool, err := burnEventSampleRuntimePools(sample)
	if err != nil {
		return nil, nil, nil, 0, err
	}

	maximumFutureBlock := sample.Burn.Cursor.BlockNumber +
		horizons[len(horizons)-1].Blocks

	flowEvents, err := s.outcomeService.loadBurnRealizedFlowEvents(
		ctx,
		sample.Burn,
		maximumFutureBlock,
		true,
	)
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("load future event stream: %w", err)
	}

	// The realized dataset populated this cache entry. Counterfactual analysis is
	// its final consumer, so release it even when this sample fails. This bounds
	// memory while still eliminating the second The Graph scan.
	defer s.outcomeService.releaseBurnRealizedFlowEvents(
		sample.Burn,
		maximumFutureBlock,
	)

	events, err := mergeBurnCounterfactualEvents(flowEvents)
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("merge future event stream: %w", err)
	}

	actual := cloneBurnRuntimePool(postBurnPool)
	sharedNoBurnPool := cloneBurnRuntimePool(preBurnPool)
	exactInputBranch := burnCounterfactualBranchState{
		TieBreak:  BurnCounterfactualExactInputTieBreak,
		Available: true,
		Pool:      sharedNoBurnPool,
	}
	exactOutputBranch := burnCounterfactualBranchState{
		TieBreak:  BurnCounterfactualExactOutputTieBreak,
		Available: true,
		Pool:      sharedNoBurnPool,
	}

	eventIndex := 0
	liquidityEvents := 0
	swapEvents := 0
	ambiguousModes := 0
	branchesDiverged := false

	observations := make([]BurnCounterfactualObservation, 0, len(horizons))
	depths := make(
		[]BurnCounterfactualDepthObservation,
		0,
		len(horizons)*len(thresholds)*4,
	)
	replayAudits := make(
		[]BurnCounterfactualSwapReplayAudit,
		0,
	)

	for _, horizon := range horizons {
		futureBlock := sample.Burn.Cursor.BlockNumber + horizon.Blocks

		for eventIndex < len(events) &&
			events[eventIndex].Cursor.BlockNumber <= futureBlock {
			event := events[eventIndex]
			switch event.Type {
			case burnCounterfactualEventLiquidity:
				actual, err = uniswapv3.ApplyLiquidityChange(actual, *event.LiquidityChange)
				if err != nil {
					return nil, nil, nil, 0, fmt.Errorf(
						"apply actual liquidity event at %s: %w",
						event.Cursor,
						err,
					)
				}
				applyCounterfactualLiquidityChangePair(
					&exactInputBranch,
					&exactOutputBranch,
					event,
					branchesDiverged,
				)
				liquidityEvents++

			case burnCounterfactualEventSwap:
				var (
					actualMode       observedSwapReplayMode
					actualModeDetail string
				)
				actual, actualMode, actualModeDetail, err =
					s.replayBurnCounterfactualActualSwap(
						actual,
						*event.Swap,
						s.outcomeService.curveService.simulator,
					)
				if err != nil {
					return nil, nil, nil, 0, fmt.Errorf(
						"advance actual swap %s at %s: %w",
						event.Swap.ID,
						event.Cursor,
						err,
					)
				}
				if observedSwapReplayModeRequiresTieBreak(actualMode) {
					ambiguousModes++
					branchesDiverged = true
				}
				applyCounterfactualSwapPair(
					&exactInputBranch,
					&exactOutputBranch,
					*event.Swap,
					actualMode,
					s.outcomeService.curveService.simulator,
					branchesDiverged,
				)
				replayAudits = append(
					replayAudits,
					newBurnCounterfactualSwapReplayAudit(
						sample.Burn,
						*event.Swap,
						actualMode,
						actualModeDetail,
						actual,
						exactInputBranch,
						exactOutputBranch,
					),
				)
				swapEvents++

			default:
				return nil, nil, nil, 0, fmt.Errorf(
					"unsupported future event type %d",
					event.Type,
				)
			}

			actual.BlockNumber = event.Cursor.BlockNumber
			if exactInputBranch.Available {
				exactInputBranch.Pool.BlockNumber = event.Cursor.BlockNumber
			}
			if exactOutputBranch.Available {
				exactOutputBranch.Pool.BlockNumber = event.Cursor.BlockNumber
			}
			eventIndex++
		}

		observationKey := burnCounterfactualObservationKey(
			sample.Burn.EventKey(),
			horizon.Label,
		)
		actualObservation, exists := actualByKey[observationKey]
		if !exists {
			return nil, nil, nil, 0, fmt.Errorf(
				"missing realized observation for horizon %s",
				horizon.Label,
			)
		}
		if err := validateBurnCounterfactualActualState(
			actual,
			actualObservation,
			futureBlock,
		); err != nil {
			return nil, nil, nil, 0, fmt.Errorf(
				"horizon %s actual replay guard: %w",
				horizon.Label,
				err,
			)
		}

		inputResult, inputDepths, err := s.buildCounterfactualBranchResult(
			ctx,
			sample,
			horizon,
			futureBlock,
			actualObservation,
			exactInputBranch,
			zeroForOneAmounts,
			oneForZeroAmounts,
			thresholds,
		)
		if err != nil {
			return nil, nil, nil, 0, err
		}

		var outputResult BurnCounterfactualBranchResult
		var outputDepths []BurnCounterfactualDepthObservation

		if !branchesDiverged {
			// Both branches are still identical. Curve construction is the most
			// expensive local operation, so reuse the exact-input result and only
			// relabel the explicit sensitivity branch.
			outputResult = cloneBurnCounterfactualBranchResultForTieBreak(
				inputResult,
				BurnCounterfactualExactOutputTieBreak,
			)
			outputDepths = cloneBurnCounterfactualDepthRowsForTieBreak(
				inputDepths,
				BurnCounterfactualExactOutputTieBreak,
			)
		} else {
			outputResult, outputDepths, err = s.buildCounterfactualBranchResult(
				ctx,
				sample,
				horizon,
				futureBlock,
				actualObservation,
				exactOutputBranch,
				zeroForOneAmounts,
				oneForZeroAmounts,
				thresholds,
			)
			if err != nil {
				return nil, nil, nil, 0, err
			}
		}

		observation := BurnCounterfactualObservation{
			Burn:                        cloneBurnCandidate(sample.Burn),
			HorizonLabel:                horizon.Label,
			HorizonBlocks:               horizon.Blocks,
			FutureBlock:                 futureBlock,
			FutureLiquidityEvents:       liquidityEvents,
			FutureSwapEvents:            swapEvents,
			AmbiguousSwapModes:          ambiguousModes,
			ActualFutureCurrentTick:     actualObservation.FutureCurrentTick,
			ActualFutureSqrtPriceX96:    cloneBigInt(actualObservation.FutureSqrtPriceX96),
			ActualFutureActiveLiquidity: cloneBigInt(actualObservation.FutureActiveLiquidity),
			ExactInputTieBreak:          inputResult,
			ExactOutputTieBreak:         outputResult,
			BothBranchesAvailable:       inputResult.Available && outputResult.Available,
		}
		setBurnCounterfactualEffectBounds(&observation)

		observations = append(observations, observation)
		depths = append(depths, inputDepths...)
		depths = append(depths, outputDepths...)
	}

	return observations, depths, replayAudits, ambiguousModes, nil
}

type burnCounterfactualBranchState struct {
	TieBreak      BurnCounterfactualTieBreak
	Available     bool
	Pool          *domain.ReconstructedPool
	FailureCursor *domain.EventCursor
	FailureDetail string
}

type burnCounterfactualEventType uint8

const (
	burnCounterfactualEventLiquidity burnCounterfactualEventType = iota + 1
	burnCounterfactualEventSwap
)

type burnCounterfactualEvent struct {
	Type            burnCounterfactualEventType
	Cursor          domain.EventCursor
	LiquidityChange *domain.LiquidityChange
	Swap            *domain.SwapEvent
}

func mergeBurnCounterfactualEvents(
	values burnRealizedFlowEventSet,
) ([]burnCounterfactualEvent, error) {
	if !values.Available {
		return nil, fmt.Errorf("future flow event stream is unavailable")
	}

	events := make(
		[]burnCounterfactualEvent,
		0,
		len(values.LiquidityChanges)+len(values.Swaps),
	)
	// The cached event set is immutable for the lifetime of this analysis. Point
	// directly into its backing slices instead of deep-cloning every big.Int a
	// second time. The local flowEvents value keeps those slices alive until the
	// sample has completed.
	for index := range values.LiquidityChanges {
		change := &values.LiquidityChanges[index]
		cursor := domain.EventCursor{
			BlockNumber: change.BlockNumber,
			LogIndex:    change.LogIndex,
		}
		if !cursor.After(values.BurnCursor) || cursor.BlockNumber > values.ThroughBlock {
			return nil, fmt.Errorf("liquidity event %s is outside flow window", cursor)
		}
		events = append(events, burnCounterfactualEvent{
			Type:            burnCounterfactualEventLiquidity,
			Cursor:          cursor,
			LiquidityChange: change,
		})
	}
	for index := range values.Swaps {
		swap := &values.Swaps[index]
		if err := swap.ValidateForObservation(); err != nil {
			return nil, fmt.Errorf("swap %s: %w", swap.ID, err)
		}
		cursor := swap.Cursor()
		if !cursor.After(values.BurnCursor) || cursor.BlockNumber > values.ThroughBlock {
			return nil, fmt.Errorf("swap event %s is outside flow window", cursor)
		}
		events = append(events, burnCounterfactualEvent{
			Type:   burnCounterfactualEventSwap,
			Cursor: cursor,
			Swap:   swap,
		})
	}

	sort.SliceStable(events, func(i int, j int) bool {
		return events[i].Cursor.Before(events[j].Cursor)
	})
	for index := 1; index < len(events); index++ {
		if events[index-1].Cursor.Equal(events[index].Cursor) {
			return nil, fmt.Errorf(
				"duplicate future pool event cursor %s",
				events[index].Cursor,
			)
		}
	}
	return events, nil
}

func (s *BurnCounterfactualFutureService) replayBurnCounterfactualActualSwap(
	current *domain.ReconstructedPool,
	swap domain.SwapEvent,
	simulator *uniswapv3.Simulator,
) (
	*domain.ReconstructedPool,
	observedSwapReplayMode,
	string,
	error,
) {
	if current == nil {
		return nil, "", "", fmt.Errorf("actual replay pool is nil")
	}
	if simulator == nil {
		return nil, "", "", fmt.Errorf("actual replay simulator is nil")
	}

	// The actual branch is not a counterfactual. Its post-swap sqrtPriceX96 and
	// tick are directly observed on-chain, so advancing it must never depend on a
	// second token-amount simulation reproducing the event exactly. This is the
	// same authoritative transition used by the offline PostgreSQL timeline.
	next, err := applyObservedSwapState(current, swap)
	if err != nil {
		return nil, "", "", err
	}

	resolution, err := s.resolveBurnCounterfactualObservedMode(
		current,
		swap,
		simulator,
	)
	if err != nil {
		return nil, "", "", err
	}

	return next, resolution.Mode, resolution.Detail, nil
}

func (s *BurnCounterfactualFutureService) resolveBurnCounterfactualObservedMode(
	current *domain.ReconstructedPool,
	swap domain.SwapEvent,
	simulator *uniswapv3.Simulator,
) (burnCounterfactualObservedModeResolution, error) {
	if s == nil {
		return burnCounterfactualObservedModeResolution{}, fmt.Errorf(
			"counterfactual service is nil",
		)
	}
	if current == nil || current.SqrtPriceX96 == nil || current.Liquidity == nil {
		return burnCounterfactualObservedModeResolution{}, fmt.Errorf(
			"counterfactual actual pre-state is incomplete",
		)
	}
	if simulator == nil {
		return burnCounterfactualObservedModeResolution{}, fmt.Errorf(
			"counterfactual simulator is nil",
		)
	}
	if err := swap.ValidateForObservation(); err != nil {
		return burnCounterfactualObservedModeResolution{}, fmt.Errorf(
			"invalid observed swap: %w",
			err,
		)
	}

	key := burnCounterfactualObservedModeCacheKey{
		PoolAddress:  normalizeAddress(swap.PoolAddress),
		BlockNumber:  swap.BlockNumber,
		LogIndex:     swap.LogIndex,
		PreTick:      current.CurrentTick,
		PreSqrt:      current.SqrtPriceX96.String(),
		PreLiquidity: current.Liquidity.String(),
	}
	if cached, exists := s.observedModeCache.Load(key); exists {
		value := cached.(burnCounterfactualObservedModeCacheValue)
		return burnCounterfactualObservedModeResolution{
			Mode:   value.Mode,
			Detail: value.Detail,
		}, nil
	}

	exactInput, exactInputErr := simulateObservedSwapAsExactInput(
		current,
		swap,
		simulator,
	)

	var (
		exactOutput    observedSwapSimulation
		exactOutputErr error
	)
	if swap.AmountOutRaw().Sign() == 0 {
		exactOutput = observedSwapSimulation{Mode: observedSwapReplayExactOutput}
		exactOutputErr = fmt.Errorf(
			"observed output is zero; exact-output mode is unavailable",
		)
	} else {
		exactOutput, exactOutputErr = simulateObservedSwapAsExactOutput(
			current,
			swap,
			simulator,
		)
	}

	resolution := classifyBurnCounterfactualObservedMode(
		exactInput,
		exactInputErr,
		exactOutput,
		exactOutputErr,
		swap,
	)

	stored, _ := s.observedModeCache.LoadOrStore(
		key,
		burnCounterfactualObservedModeCacheValue{
			Mode:   resolution.Mode,
			Detail: resolution.Detail,
		},
	)
	value := stored.(burnCounterfactualObservedModeCacheValue)

	return burnCounterfactualObservedModeResolution{
		Mode:   value.Mode,
		Detail: value.Detail,
	}, nil
}

func classifyBurnCounterfactualObservedMode(
	exactInput observedSwapSimulation,
	exactInputErr error,
	exactOutput observedSwapSimulation,
	exactOutputErr error,
	swap domain.SwapEvent,
) burnCounterfactualObservedModeResolution {
	exactInputMatches := exactInputErr == nil &&
		observedSwapSimulationMatches(exactInput, swap)
	exactOutputMatches := exactOutputErr == nil &&
		observedSwapSimulationMatches(exactOutput, swap)

	switch {
	case exactInputMatches && exactOutputMatches:
		return burnCounterfactualObservedModeResolution{
			Mode: observedSwapReplayBoth,
			Detail: "both protocol modes reproduce the observed event; " +
				"counterfactual branches preserve both interpretations",
		}

	case exactInputMatches:
		return burnCounterfactualObservedModeResolution{
			Mode:   observedSwapReplayExactInput,
			Detail: "exact-input protocol mode reproduces the observed event",
		}

	case exactOutputMatches:
		return burnCounterfactualObservedModeResolution{
			Mode:   observedSwapReplayExactOutput,
			Detail: "exact-output protocol mode reproduces the observed event",
		}

	default:
		// amountSpecified is not emitted by the Swap event. A mismatch can arise
		// from historical state/data granularity or integer boundary effects. The
		// actual branch remains authoritative from the emitted post-state, while
		// the no-burn analysis explicitly carries both protocol interpretations.
		return burnCounterfactualObservedModeResolution{
			Mode: observedSwapReplayUnresolved,
			Detail: fmt.Sprintf(
				"neither protocol mode reproduces the observed event; preserving an exact-input/exact-output sensitivity envelope: exact_input={%s} exact_output={%s}",
				describeObservedSwapSimulation(exactInput, exactInputErr, swap),
				describeObservedSwapSimulation(exactOutput, exactOutputErr, swap),
			),
		}
	}
}

func observedSwapReplayModeRequiresTieBreak(
	mode observedSwapReplayMode,
) bool {
	return mode == observedSwapReplayBoth ||
		mode == observedSwapReplayUnresolved
}

func applyCounterfactualLiquidityChangePair(
	exactInput *burnCounterfactualBranchState,
	exactOutput *burnCounterfactualBranchState,
	event burnCounterfactualEvent,
	branchesDiverged bool,
) {
	if !branchesDiverged &&
		exactInput != nil &&
		exactOutput != nil &&
		exactInput.Available &&
		exactOutput.Available {
		applyCounterfactualLiquidityChange(exactInput, event)
		copyBurnCounterfactualBranchRuntime(exactOutput, exactInput)
		return
	}

	applyCounterfactualLiquidityChange(exactInput, event)
	applyCounterfactualLiquidityChange(exactOutput, event)
}

func applyCounterfactualLiquidityChange(
	branch *burnCounterfactualBranchState,
	event burnCounterfactualEvent,
) {
	if branch == nil || !branch.Available {
		return
	}
	next, err := uniswapv3.ApplyLiquidityChange(branch.Pool, *event.LiquidityChange)
	if err != nil {
		failBurnCounterfactualBranch(branch, event.Cursor, fmt.Sprintf(
			"apply liquidity event: %v",
			err,
		))
		return
	}
	branch.Pool = next
}

func applyCounterfactualSwapPair(
	exactInput *burnCounterfactualBranchState,
	exactOutput *burnCounterfactualBranchState,
	swap domain.SwapEvent,
	actualMode observedSwapReplayMode,
	simulator *uniswapv3.Simulator,
	branchesDiverged bool,
) {
	// Before the first genuinely ambiguous historical swap, both no-burn
	// branches have identical state and apply the same protocol mode. Simulate
	// once and clone the resulting runtime instead of repeating every swap.
	if !branchesDiverged &&
		!observedSwapReplayModeRequiresTieBreak(actualMode) &&
		exactInput != nil &&
		exactOutput != nil &&
		exactInput.Available &&
		exactOutput.Available {
		applyCounterfactualSwap(
			exactInput,
			swap,
			actualMode,
			simulator,
		)
		copyBurnCounterfactualBranchRuntime(exactOutput, exactInput)
		return
	}

	applyCounterfactualSwap(
		exactInput,
		swap,
		actualMode,
		simulator,
	)
	applyCounterfactualSwap(
		exactOutput,
		swap,
		actualMode,
		simulator,
	)
}

func copyBurnCounterfactualBranchRuntime(
	destination *burnCounterfactualBranchState,
	source *burnCounterfactualBranchState,
) {
	if destination == nil || source == nil {
		return
	}

	destination.Available = source.Available

	// Reconstructed pools are treated as immutable inputs: simulations and
	// liquidity changes return new pools. Until an ambiguous mode forces the
	// branches apart, sharing the same runtime pointer avoids cloning the entire
	// initialized-tick map after every event.
	destination.Pool = source.Pool
	destination.FailureDetail = source.FailureDetail

	if source.FailureCursor == nil {
		destination.FailureCursor = nil
	} else {
		cursor := *source.FailureCursor
		destination.FailureCursor = &cursor
	}
}

func applyCounterfactualSwap(
	branch *burnCounterfactualBranchState,
	swap domain.SwapEvent,
	actualMode observedSwapReplayMode,
	simulator *uniswapv3.Simulator,
) {
	if branch == nil || !branch.Available {
		return
	}

	zeroForOne, amountIn, amountOut, err := burnRealizedFlowSwapAmounts(swap)
	if err != nil {
		failBurnCounterfactualBranch(branch, swap.Cursor(), err.Error())
		return
	}

	mode := actualMode
	if observedSwapReplayModeRequiresTieBreak(actualMode) {
		switch branch.TieBreak {
		case BurnCounterfactualExactInputTieBreak:
			mode = observedSwapReplayExactInput
		case BurnCounterfactualExactOutputTieBreak:
			mode = observedSwapReplayExactOutput
		default:
			failBurnCounterfactualBranch(branch, swap.Cursor(), "unknown tie-break")
			return
		}
	}

	switch mode {
	case observedSwapReplayExactInput:
		result, err := simulator.SimulateExactInput(
			branch.Pool,
			uniswapv3.ExactInputRequest{
				AmountIn:        amountIn,
				ZeroForOne:      zeroForOne,
				AllowZeroOutput: amountOut.Sign() == 0,
			},
		)
		if err != nil {
			failBurnCounterfactualBranch(branch, swap.Cursor(), fmt.Sprintf(
				"simulate exact-input swap: %v",
				err,
			))
			return
		}
		branch.Pool = poolAfterCounterfactualExactInput(branch.Pool, result, swap.BlockNumber)

	case observedSwapReplayExactOutput:
		if amountOut.Sign() <= 0 {
			failBurnCounterfactualBranch(
				branch,
				swap.Cursor(),
				"exact-output mode has non-positive observed output",
			)
			return
		}
		result, err := simulator.SimulateExactOutput(
			branch.Pool,
			uniswapv3.ExactOutputRequest{
				AmountOut:  amountOut,
				ZeroForOne: zeroForOne,
			},
		)
		if err != nil {
			failBurnCounterfactualBranch(branch, swap.Cursor(), fmt.Sprintf(
				"simulate exact-output swap: %v",
				err,
			))
			return
		}
		branch.Pool = poolAfterCounterfactualExactOutput(branch.Pool, result, swap.BlockNumber)

	default:
		failBurnCounterfactualBranch(branch, swap.Cursor(), fmt.Sprintf(
			"unsupported actual replay mode %q",
			actualMode,
		))
	}
}

func newBurnCounterfactualSwapReplayAudit(
	burn domain.BurnCandidate,
	swap domain.SwapEvent,
	actualMode observedSwapReplayMode,
	actualModeDetail string,
	actual *domain.ReconstructedPool,
	exactInput burnCounterfactualBranchState,
	exactOutput burnCounterfactualBranchState,
) BurnCounterfactualSwapReplayAudit {
	zeroForOne, amountIn, amountOut, _ := burnRealizedFlowSwapAmounts(swap)

	audit := BurnCounterfactualSwapReplayAudit{
		Burn:               cloneBurnCandidate(burn),
		SwapID:             strings.TrimSpace(swap.ID),
		TxHash:             strings.ToLower(strings.TrimSpace(swap.TxHash)),
		Cursor:             swap.Cursor(),
		ZeroForOne:         zeroForOne,
		AmountInRaw:        cloneBigInt(amountIn),
		AmountOutRaw:       cloneBigInt(amountOut),
		ActualReplayMode:   actualMode,
		ActualModeResolved: actualMode != observedSwapReplayUnresolved,
		ActualAmbiguous:    observedSwapReplayModeRequiresTieBreak(actualMode),
		ActualReplayDetail: strings.TrimSpace(actualModeDetail),
		ExactInputTieBreakMode: burnCounterfactualAppliedMode(
			actualMode,
			BurnCounterfactualExactInputTieBreak,
		),
		ExactInputAvailable:     exactInput.Available,
		ExactInputFailureDetail: exactInput.FailureDetail,
		ExactOutputTieBreakMode: burnCounterfactualAppliedMode(
			actualMode,
			BurnCounterfactualExactOutputTieBreak,
		),
		ExactOutputAvailable:     exactOutput.Available,
		ExactOutputFailureDetail: exactOutput.FailureDetail,
	}

	if actual != nil {
		audit.ActualTickAfter = actual.CurrentTick
		audit.ActualSqrtPriceX96After = cloneBigInt(actual.SqrtPriceX96)
		audit.ActualActiveLiquidityAfter = cloneBigInt(actual.Liquidity)
	}
	if exactInput.Available && exactInput.Pool != nil {
		audit.ExactInputTickAfter = exactInput.Pool.CurrentTick
		audit.ExactInputSqrtPriceX96After = cloneBigInt(exactInput.Pool.SqrtPriceX96)
		audit.ExactInputActiveLiquidityAfter = cloneBigInt(exactInput.Pool.Liquidity)
	}
	if exactOutput.Available && exactOutput.Pool != nil {
		audit.ExactOutputTickAfter = exactOutput.Pool.CurrentTick
		audit.ExactOutputSqrtPriceX96After = cloneBigInt(exactOutput.Pool.SqrtPriceX96)
		audit.ExactOutputActiveLiquidityAfter = cloneBigInt(exactOutput.Pool.Liquidity)
	}

	return audit
}

func burnCounterfactualAppliedMode(
	actualMode observedSwapReplayMode,
	tieBreak BurnCounterfactualTieBreak,
) observedSwapReplayMode {
	if !observedSwapReplayModeRequiresTieBreak(actualMode) {
		return actualMode
	}
	if tieBreak == BurnCounterfactualExactOutputTieBreak {
		return observedSwapReplayExactOutput
	}
	return observedSwapReplayExactInput
}

func failBurnCounterfactualBranch(
	branch *burnCounterfactualBranchState,
	cursor domain.EventCursor,
	detail string,
) {
	branch.Available = false
	cursorCopy := cursor
	branch.FailureCursor = &cursorCopy
	branch.FailureDetail = strings.TrimSpace(detail)
	branch.Pool = nil
}

func poolAfterCounterfactualExactInput(
	pool *domain.ReconstructedPool,
	result *uniswapv3.ExactInputResult,
	blockNumber uint64,
) *domain.ReconstructedPool {
	next := cloneBurnRuntimePool(pool)
	next.BlockNumber = blockNumber
	next.SqrtPriceX96 = cloneBigInt(result.SqrtPriceAfterX96)
	next.CurrentTick = result.TickAfter
	next.Liquidity = cloneBigInt(result.LiquidityAfter)
	return next
}

func poolAfterCounterfactualExactOutput(
	pool *domain.ReconstructedPool,
	result *uniswapv3.ExactOutputResult,
	blockNumber uint64,
) *domain.ReconstructedPool {
	next := cloneBurnRuntimePool(pool)
	next.BlockNumber = blockNumber
	next.SqrtPriceX96 = cloneBigInt(result.SqrtPriceAfterX96)
	next.CurrentTick = result.TickAfter
	next.Liquidity = cloneBigInt(result.LiquidityAfter)
	return next
}

func burnCounterfactualSqrtWithinTolerance(
	simulated *big.Int,
	observed *big.Int,
) bool {
	if simulated == nil || observed == nil || observed.Sign() <= 0 {
		return false
	}

	difference := new(big.Int).Sub(simulated, observed)
	difference.Abs(difference)
	if difference.Sign() == 0 {
		return true
	}

	scaledDifference := new(big.Int).Mul(
		difference,
		big.NewInt(observedSwapSqrtRelativeToleranceDenominator),
	)

	return scaledDifference.Cmp(observed) <= 0
}

func validateBurnCounterfactualActualState(
	actual *domain.ReconstructedPool,
	observation BurnRegressionObservation,
	futureBlock uint64,
) error {
	if actual == nil {
		return fmt.Errorf("actual replay pool is nil")
	}
	if observation.FutureBlock != futureBlock {
		return fmt.Errorf(
			"realized observation future block=%d expected=%d",
			observation.FutureBlock,
			futureBlock,
		)
	}
	if actual.CurrentTick != observation.FutureCurrentTick {
		return fmt.Errorf(
			"tick=%d expected=%d",
			actual.CurrentTick,
			observation.FutureCurrentTick,
		)
	}
	if actual.SqrtPriceX96 == nil ||
		actual.SqrtPriceX96.Cmp(observation.FutureSqrtPriceX96) != 0 {
		return fmt.Errorf(
			"sqrt price=%v expected=%v",
			actual.SqrtPriceX96,
			observation.FutureSqrtPriceX96,
		)
	}
	if actual.Liquidity == nil ||
		actual.Liquidity.Cmp(observation.FutureActiveLiquidity) != 0 {
		return fmt.Errorf(
			"active liquidity=%v expected=%v",
			actual.Liquidity,
			observation.FutureActiveLiquidity,
		)
	}
	return nil
}

func (s *BurnCounterfactualFutureService) buildCounterfactualBranchResult(
	ctx context.Context,
	sample BurnEventSample,
	horizon BurnOutcomeHorizon,
	futureBlock uint64,
	actual BurnRegressionObservation,
	state burnCounterfactualBranchState,
	zeroForOneAmounts []*big.Int,
	oneForZeroAmounts []*big.Int,
	thresholds []decimal.Decimal,
) (
	BurnCounterfactualBranchResult,
	[]BurnCounterfactualDepthObservation,
	error,
) {
	result := BurnCounterfactualBranchResult{
		TieBreak:      state.TieBreak,
		Available:     state.Available,
		FailureCursor: cloneEventCursorPointer(state.FailureCursor),
		FailureDetail: state.FailureDetail,
	}

	if !state.Available {
		return result,
			unavailableBurnCounterfactualDepthRows(
				sample,
				horizon,
				futureBlock,
				state.TieBreak,
				state.FailureDetail,
				thresholds,
			),
			nil
	}
	if state.Pool == nil || state.Pool.Liquidity == nil ||
		state.Pool.Liquidity.Sign() <= 0 {
		result.Available = false
		result.FailureDetail = "counterfactual future active liquidity is zero"
		return result,
			unavailableBurnCounterfactualDepthRows(
				sample,
				horizon,
				futureBlock,
				state.TieBreak,
				result.FailureDetail,
				thresholds,
			),
			nil
	}

	zeroSummary, err := s.buildCounterfactualCurveSummary(
		ctx,
		state.Pool,
		zeroForOneAmounts,
		thresholds,
		true,
	)
	if err != nil {
		result.Available = false
		result.FailureDetail = fmt.Sprintf(
			"evaluate zero_for_one counterfactual curve at horizon %s: %v",
			horizon.Label,
			err,
		)
		return result,
			unavailableBurnCounterfactualDepthRows(
				sample,
				horizon,
				futureBlock,
				state.TieBreak,
				result.FailureDetail,
				thresholds,
			),
			nil
	}
	oneSummary, err := s.buildCounterfactualCurveSummary(
		ctx,
		state.Pool,
		oneForZeroAmounts,
		thresholds,
		false,
	)
	if err != nil {
		result.Available = false
		result.FailureDetail = fmt.Sprintf(
			"evaluate one_for_zero counterfactual curve at horizon %s: %v",
			horizon.Label,
			err,
		)
		return result,
			unavailableBurnCounterfactualDepthRows(
				sample,
				horizon,
				futureBlock,
				state.TieBreak,
				result.FailureDetail,
				thresholds,
			),
			nil
	}

	actualZeroDepths, err := burnActualFutureDepths(actual.RealizedZeroForOne)
	if err != nil {
		return BurnCounterfactualBranchResult{}, nil, err
	}
	actualOneDepths, err := burnActualFutureDepths(actual.RealizedOneForZero)
	if err != nil {
		return BurnCounterfactualBranchResult{}, nil, err
	}
	if err := validateBurnCounterfactualDepthAlignment(
		"zero_for_one",
		actualZeroDepths,
		zeroSummary.ThresholdDepths,
	); err != nil {
		return BurnCounterfactualBranchResult{}, nil, err
	}
	if err := validateBurnCounterfactualDepthAlignment(
		"one_for_zero",
		actualOneDepths,
		oneSummary.ThresholdDepths,
	); err != nil {
		return BurnCounterfactualBranchResult{}, nil, err
	}

	result.FutureCurrentTick = state.Pool.CurrentTick
	result.FutureSqrtPriceX96 = cloneBigInt(state.Pool.SqrtPriceX96)
	result.FutureActiveLiquidity = cloneBigInt(state.Pool.Liquidity)
	result.ZeroForOne = newBurnCounterfactualDirectionalResult(
		true,
		actual.RealizedZeroForOne.FutureAUCBps,
		actualZeroDepths,
		zeroSummary,
	)
	result.OneForZero = newBurnCounterfactualDirectionalResult(
		false,
		actual.RealizedOneForZero.FutureAUCBps,
		actualOneDepths,
		oneSummary,
	)
	result.TotalMechanicalEffectPIAUCBps = result.ZeroForOne.
		MechanicalEffectPIAUCBps.Add(result.OneForZero.MechanicalEffectPIAUCBps)
	result.TotalMechanicalDeteriorationBps = result.ZeroForOne.
		MechanicalDeteriorationBps.Add(result.OneForZero.MechanicalDeteriorationBps)
	result.MaxDirectionalMechanicalDeteriorationBps = burnMaxDecimal(
		result.ZeroForOne.MechanicalDeteriorationBps,
		result.OneForZero.MechanicalDeteriorationBps,
	)

	depths := append(
		burnCounterfactualDepthRows(
			sample,
			horizon,
			futureBlock,
			state.TieBreak,
			result.ZeroForOne,
		),
		burnCounterfactualDepthRows(
			sample,
			horizon,
			futureBlock,
			state.TieBreak,
			result.OneForZero,
		)...,
	)

	return result, depths, nil
}

func cloneBurnCounterfactualBranchResultForTieBreak(
	value BurnCounterfactualBranchResult,
	tieBreak BurnCounterfactualTieBreak,
) BurnCounterfactualBranchResult {
	result := value
	result.TieBreak = tieBreak
	result.FailureCursor = cloneEventCursorPointer(value.FailureCursor)
	result.FutureSqrtPriceX96 = cloneBigInt(value.FutureSqrtPriceX96)
	result.FutureActiveLiquidity = cloneBigInt(value.FutureActiveLiquidity)
	result.ZeroForOne = cloneBurnCounterfactualDirectionalResult(
		value.ZeroForOne,
	)
	result.OneForZero = cloneBurnCounterfactualDirectionalResult(
		value.OneForZero,
	)
	return result
}

func cloneBurnCounterfactualDirectionalResult(
	value BurnCounterfactualDirectionalResult,
) BurnCounterfactualDirectionalResult {
	result := value
	result.ActualDepths = cloneThresholdDepths(value.ActualDepths)
	result.NoBurnDepths = cloneThresholdDepths(value.NoBurnDepths)
	return result
}

func cloneBurnCounterfactualDepthRowsForTieBreak(
	values []BurnCounterfactualDepthObservation,
	tieBreak BurnCounterfactualTieBreak,
) []BurnCounterfactualDepthObservation {
	result := make(
		[]BurnCounterfactualDepthObservation,
		len(values),
	)

	for index, value := range values {
		result[index] = value
		result[index].Burn = cloneBurnCandidate(value.Burn)
		result[index].TieBreak = tieBreak
	}

	return result
}

func (s *BurnCounterfactualFutureService) buildCounterfactualCurveSummary(
	ctx context.Context,
	pool *domain.ReconstructedPool,
	amounts []*big.Int,
	thresholds []decimal.Decimal,
	zeroForOne bool,
) (*PriceImpactCurveSummary, error) {
	curve, err := s.outcomeService.curveService.Build(
		ctx,
		PriceImpactCurveRequest{
			Pool:       pool,
			AmountsIn:  cloneBurnAmountGrid(amounts),
			ZeroForOne: zeroForOne,
		},
	)
	if err != nil {
		return nil, err
	}
	return s.outcomeService.curveService.Summarize(curve, thresholds)
}

func newBurnCounterfactualDirectionalResult(
	zeroForOne bool,
	actualAUC decimal.Decimal,
	actualDepths []ThresholdDepth,
	noBurn *PriceImpactCurveSummary,
) BurnCounterfactualDirectionalResult {
	effect := actualAUC.Sub(noBurn.PriceImpactAUCBps)
	return BurnCounterfactualDirectionalResult{
		ZeroForOne:                 zeroForOne,
		ActualFutureAUCBps:         actualAUC,
		NoBurnFutureAUCBps:         noBurn.PriceImpactAUCBps,
		MechanicalEffectPIAUCBps:   effect,
		MechanicalDeteriorationBps: positiveOnly(effect),
		ActualDepths:               cloneThresholdDepths(actualDepths),
		NoBurnDepths:               cloneThresholdDepths(noBurn.ThresholdDepths),
	}
}

func burnActualFutureDepths(
	outcome BurnRealizedDirectionalOutcome,
) ([]ThresholdDepth, error) {
	result := make([]ThresholdDepth, len(outcome.DepthDeltas))
	for index, depth := range outcome.DepthDeltas {
		result[index] = ThresholdDepth{
			ThresholdBps: depth.ThresholdBps,
			DepthAmount:  depth.CounterfactualDepthAmount,
			Breached:     depth.CounterfactualBreached,
		}
	}
	return result, nil
}

func validateBurnCounterfactualDepthAlignment(
	direction string,
	actual []ThresholdDepth,
	noBurn []ThresholdDepth,
) error {
	if len(actual) != len(noBurn) {
		return fmt.Errorf(
			"counterfactual depth alignment %s: actual=%d no_burn=%d",
			direction,
			len(actual),
			len(noBurn),
		)
	}
	for index := range actual {
		if !actual[index].ThresholdBps.Equal(noBurn[index].ThresholdBps) {
			return fmt.Errorf(
				"counterfactual depth alignment %s index=%d: actual_threshold=%s no_burn_threshold=%s",
				direction,
				index,
				actual[index].ThresholdBps,
				noBurn[index].ThresholdBps,
			)
		}
	}
	return nil
}

func cloneThresholdDepths(values []ThresholdDepth) []ThresholdDepth {
	return append([]ThresholdDepth(nil), values...)
}

func burnCounterfactualDepthRows(
	sample BurnEventSample,
	horizon BurnOutcomeHorizon,
	futureBlock uint64,
	tieBreak BurnCounterfactualTieBreak,
	direction BurnCounterfactualDirectionalResult,
) []BurnCounterfactualDepthObservation {
	rows := make(
		[]BurnCounterfactualDepthObservation,
		0,
		len(direction.ActualDepths),
	)
	for index := range direction.ActualDepths {
		actual := direction.ActualDepths[index]
		noBurn := direction.NoBurnDepths[index]
		rows = append(rows, BurnCounterfactualDepthObservation{
			Burn:                    cloneBurnCandidate(sample.Burn),
			HorizonLabel:            horizon.Label,
			HorizonBlocks:           horizon.Blocks,
			FutureBlock:             futureBlock,
			TieBreak:                tieBreak,
			Available:               true,
			ZeroForOne:              direction.ZeroForOne,
			ThresholdBps:            actual.ThresholdBps,
			ActualFutureDepthAmount: actual.DepthAmount,
			NoBurnFutureDepthAmount: noBurn.DepthAmount,
			MechanicalDepthLossAmount: positiveOnly(
				noBurn.DepthAmount.Sub(actual.DepthAmount),
			),
			ActualBreached: actual.Breached,
			NoBurnBreached: noBurn.Breached,
		})
	}
	return rows
}

func unavailableBurnCounterfactualDepthRows(
	sample BurnEventSample,
	horizon BurnOutcomeHorizon,
	futureBlock uint64,
	tieBreak BurnCounterfactualTieBreak,
	failure string,
	thresholds []decimal.Decimal,
) []BurnCounterfactualDepthObservation {
	rows := make(
		[]BurnCounterfactualDepthObservation,
		0,
		len(thresholds)*2,
	)
	for _, zeroForOne := range []bool{true, false} {
		for _, threshold := range thresholds {
			rows = append(rows, BurnCounterfactualDepthObservation{
				Burn:          cloneBurnCandidate(sample.Burn),
				HorizonLabel:  horizon.Label,
				HorizonBlocks: horizon.Blocks,
				FutureBlock:   futureBlock,
				TieBreak:      tieBreak,
				Available:     false,
				FailureDetail: failure,
				ZeroForOne:    zeroForOne,
				ThresholdBps:  threshold,
			})
		}
	}
	return rows
}

func setBurnCounterfactualEffectBounds(
	observation *BurnCounterfactualObservation,
) {
	if observation == nil {
		return
	}
	available := make([]BurnCounterfactualBranchResult, 0, 2)
	if observation.ExactInputTieBreak.Available {
		available = append(available, observation.ExactInputTieBreak)
	}
	if observation.ExactOutputTieBreak.Available {
		available = append(available, observation.ExactOutputTieBreak)
	}
	if len(available) == 0 {
		return
	}

	observation.MinTotalMechanicalEffectPIAUCBps = available[0].TotalMechanicalEffectPIAUCBps
	observation.MaxTotalMechanicalEffectPIAUCBps = available[0].TotalMechanicalEffectPIAUCBps
	observation.MinTotalMechanicalDeteriorationBps = available[0].TotalMechanicalDeteriorationBps
	observation.MaxTotalMechanicalDeteriorationBps = available[0].TotalMechanicalDeteriorationBps
	for _, branch := range available[1:] {
		if branch.TotalMechanicalEffectPIAUCBps.LessThan(
			observation.MinTotalMechanicalEffectPIAUCBps,
		) {
			observation.MinTotalMechanicalEffectPIAUCBps = branch.TotalMechanicalEffectPIAUCBps
		}
		if branch.TotalMechanicalEffectPIAUCBps.GreaterThan(
			observation.MaxTotalMechanicalEffectPIAUCBps,
		) {
			observation.MaxTotalMechanicalEffectPIAUCBps = branch.TotalMechanicalEffectPIAUCBps
		}
		if branch.TotalMechanicalDeteriorationBps.LessThan(
			observation.MinTotalMechanicalDeteriorationBps,
		) {
			observation.MinTotalMechanicalDeteriorationBps = branch.TotalMechanicalDeteriorationBps
		}
		if branch.TotalMechanicalDeteriorationBps.GreaterThan(
			observation.MaxTotalMechanicalDeteriorationBps,
		) {
			observation.MaxTotalMechanicalDeteriorationBps = branch.TotalMechanicalDeteriorationBps
		}
	}
}

func validateBurnSampleCollectionReportForCounterfactual(
	report BurnSampleCollectionReport,
) error {
	if len(report.Samples) == 0 {
		return fmt.Errorf("samples are empty")
	}
	for index, sample := range report.Samples {
		if err := sample.Validate(); err != nil {
			return fmt.Errorf("sample %d: %w", index, err)
		}
		if sample.runtime == nil {
			return fmt.Errorf(
				"sample %d event=%s has no runtime pool state",
				index,
				sample.Burn.EventKey(),
			)
		}
	}
	return nil
}

func burnCounterfactualObservationKey(
	eventKey string,
	horizonLabel string,
) string {
	return strings.TrimSpace(eventKey) + "|" + strings.TrimSpace(horizonLabel)
}
