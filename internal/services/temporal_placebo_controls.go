package services

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"runtime"
	"sort"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const maxTemporalPlaceboWorkers = 8

type TemporalPlaceboProvider interface {
	IndexedHead(ctx context.Context) (domain.IndexedHead, error)
	ReconstructedPoolAt(ctx context.Context, poolAddress string, blockNumber uint64) (*domain.ReconstructedPool, error)
	PoolEventsAfterCursorThroughBlock(ctx context.Context, poolAddress string, startCursor domain.EventCursor, throughBlock uint64) ([]domain.PoolBlockEvent, error)
}

type TemporalPlaceboRequest struct {
	Collection                     BurnSampleCollectionReport
	Realized                       BurnRealizedDatasetReport
	Counterfactual                 BurnCounterfactualFutureReport
	Horizons                       []BurnOutcomeHorizon
	ZeroForOneAmountsIn            []*big.Int
	OneForZeroAmountsIn            []*big.Int
	ThresholdsBps                  []decimal.Decimal
	AmountGridResolver             AnalysisAmountGridResolver
	MinimumSpacingBlocks           uint64
	LiquidityActionExclusionBlocks uint64
}

type TemporalPlaceboMatch struct {
	MatchID                     string
	Burn                        domain.BurnCandidate
	BurnRangeActive             bool
	BurnRangeLocation           BurnRangeLocation
	PlaceboAnchorBlock          uint64
	PlaceboReferenceBlock       uint64
	MatchDistance               float64
	BlockDistance               uint64
	BurnCurrentTick             int
	PlaceboCurrentTick          int
	BurnActiveLiquidity         *big.Int
	PlaceboActiveLiquidity      *big.Int
	BurnBaselineTotalAUCBps     decimal.Decimal
	BurnImmediateTotalLSISBps   decimal.Decimal
	PlaceboBaselineTotalAUCBps  decimal.Decimal
	BurnDirectionalImbalance    decimal.Decimal
	PlaceboDirectionalImbalance decimal.Decimal
}

type TemporalPlaceboFlowControls struct {
	SwapCount               int
	ZeroForOneSwapCount     int
	OneForZeroSwapCount     int
	LiquidityEventCount     int
	MintEventCount          int
	BurnEventCount          int
	GrossToken0VolumeRaw    *big.Int
	GrossToken1VolumeRaw    *big.Int
	GrossMintLiquidity      *big.Int
	GrossBurnLiquidity      *big.Int
	NetLiquidityFlow        *big.Int
	TickPathTotalVariation  uint64
	TickPathRange           int
	TickPathMaxAbsoluteStep int
}

type TemporalPlaceboObservation struct {
	Match         TemporalPlaceboMatch
	HorizonLabel  string
	HorizonBlocks uint64
	FutureBlock   uint64
	Available     bool
	FailureDetail string

	ActualTotalRealizedDeteriorationBps  decimal.Decimal
	PlaceboTotalRealizedDeltaPIAUCBps    decimal.Decimal
	PlaceboTotalRealizedDeteriorationBps decimal.Decimal
	ExcessDeteriorationBps               decimal.Decimal
	ActualMinMechanicalEffectPIAUCBps    decimal.Decimal
	ActualMaxMechanicalEffectPIAUCBps    decimal.Decimal
	ActualCounterfactualAvailable        bool

	PlaceboZeroForOneBaseAUCBps   decimal.Decimal
	PlaceboZeroForOneFutureAUCBps decimal.Decimal
	PlaceboOneForZeroBaseAUCBps   decimal.Decimal
	PlaceboOneForZeroFutureAUCBps decimal.Decimal

	ReferenceTick            int
	FutureTick               int
	TickChange               int
	AbsoluteTickChange       int
	ReferenceSqrtPriceX96    *big.Int
	FutureSqrtPriceX96       *big.Int
	PriceReturnBps           decimal.Decimal
	AbsolutePriceReturnBps   decimal.Decimal
	ReferenceActiveLiquidity *big.Int
	FutureActiveLiquidity    *big.Int
	ActiveLiquidityChangeBps decimal.Decimal
	Flow                     TemporalPlaceboFlowControls
}

type TemporalPlaceboHorizonSummary struct {
	Stratum                       string
	HorizonLabel                  string
	HorizonBlocks                 uint64
	Pairs                         int
	AvailablePairs                int
	UnavailablePairs              int
	MeanActualDeteriorationBps    decimal.Decimal
	MeanPlaceboDeteriorationBps   decimal.Decimal
	MeanExcessDeteriorationBps    decimal.Decimal
	MedianExcessDeteriorationBps  decimal.Decimal
	PositiveExcessPairs           int
	PositiveExcessShare           decimal.Decimal
	PearsonImmediateLSISVsExcess  float64
	SpearmanImmediateLSISVsExcess float64
}

type TemporalPlaceboBalance struct {
	Metric               string
	ActualCount          int
	CandidateCount       int
	MatchedCount         int
	ActualMean           float64
	CandidateMean        float64
	SMDBefore            float64
	MatchedPlaceboMean   float64
	SMDAfter             float64
	AbsoluteSMDReduction float64
}

type TemporalPlaceboCandidateSkip struct {
	AnchorBlock    uint64
	ReferenceBlock uint64
	Detail         string
}

type TemporalPlaceboReport struct {
	PoolAddress                    string
	FromBlock                      uint64
	ToBlock                        uint64
	IndexedThrough                 uint64
	CandidateBlocks                int
	CandidateStates                int
	CandidateSkips                 []TemporalPlaceboCandidateSkip
	LiquidityActionExclusionBlocks uint64
	MinimumSpacingBlocks           uint64
	Matches                        []TemporalPlaceboMatch
	Balance                        []TemporalPlaceboBalance
	Observations                   []TemporalPlaceboObservation
	Summaries                      []TemporalPlaceboHorizonSummary
}

type temporalPlaceboState struct {
	AnchorBlock          uint64
	ReferenceBlock       uint64
	Pool                 *domain.ReconstructedPool
	AmountGridStateID    string
	AmountGridMode       string
	ZeroForOneAmountsIn  []*big.Int
	OneForZeroAmountsIn  []*big.Int
	ZeroForOne           PriceImpactCurveSummary
	OneForZero           PriceImpactCurveSummary
	TotalAUCBps          decimal.Decimal
	DirectionalImbalance decimal.Decimal
}

type temporalPlaceboFeature struct {
	BlockNumber          float64
	Tick                 float64
	LogLiquidity         float64
	LogAUC               float64
	DirectionalImbalance float64
}

type TemporalPlaceboService struct {
	provider     TemporalPlaceboProvider
	curveService *PriceImpactCurveService
}

func NewTemporalPlaceboService(provider TemporalPlaceboProvider, curveService *PriceImpactCurveService) *TemporalPlaceboService {
	return &TemporalPlaceboService{provider: provider, curveService: curveService}
}

func (s *TemporalPlaceboService) Build(ctx context.Context, req TemporalPlaceboRequest) (TemporalPlaceboReport, error) {
	if s == nil || s.provider == nil || s.curveService == nil || s.curveService.simulator == nil {
		return TemporalPlaceboReport{}, fmt.Errorf("build temporal placebo controls: service is incomplete")
	}
	if err := validateBurnRealizedDatasetReport(req.Realized); err != nil {
		return TemporalPlaceboReport{}, fmt.Errorf("build temporal placebo controls: invalid realized dataset: %w", err)
	}
	if req.Realized.FromBlock <= 1 {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: from block %d has no valid preceding reference block",
			req.Realized.FromBlock,
		)
	}
	if len(req.Collection.Samples) == 0 {
		return TemporalPlaceboReport{}, fmt.Errorf("build temporal placebo controls: burn samples are empty")
	}
	if normalizeAddress(req.Collection.PoolAddress) != normalizeAddress(req.Realized.PoolAddress) {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: collection pool %s does not match realized pool %s",
			req.Collection.PoolAddress,
			req.Realized.PoolAddress,
		)
	}
	if req.Collection.FromBlock != req.Realized.FromBlock || req.Collection.ToBlock != req.Realized.ToBlock {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: collection range [%d,%d] does not match realized range [%d,%d]",
			req.Collection.FromBlock,
			req.Collection.ToBlock,
			req.Realized.FromBlock,
			req.Realized.ToBlock,
		)
	}

	horizons, err := normalizeBurnOutcomeHorizons(req.Collection.FromBlock, req.Horizons)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}
	zeroForOneAmounts, err := normalizeBurnAmountGrid("temporal placebo zero_for_one", req.ZeroForOneAmountsIn)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}
	oneForZeroAmounts, err := normalizeBurnAmountGrid("temporal placebo one_for_zero", req.OneForZeroAmountsIn)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}
	thresholds, err := normalizeBurnThresholds(req.ThresholdsBps)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}
	maximumHorizon := horizons[len(horizons)-1].Blocks
	if req.MinimumSpacingBlocks <= maximumHorizon {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: minimum spacing %d must exceed maximum horizon %d",
			req.MinimumSpacingBlocks,
			maximumHorizon,
		)
	}
	if maximumHorizon > ^uint64(0)-req.Realized.ToBlock {
		return TemporalPlaceboReport{}, fmt.Errorf("build temporal placebo controls: future block overflow")
	}
	maximumFutureBlock := req.Realized.ToBlock + maximumHorizon

	actualByKey, err := indexTemporalPlaceboActualObservations(req.Collection.Samples, horizons, req.Realized.Observations)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}
	counterfactualByKey, err := indexTemporalPlaceboCounterfactualObservations(req.Counterfactual.Observations)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}

	head, err := s.provider.IndexedHead(ctx)
	if err != nil {
		return TemporalPlaceboReport{}, fmt.Errorf("build temporal placebo controls: indexed head: %w", err)
	}
	if head.BlockNumber < maximumFutureBlock {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: indexed head %d is before maximum placebo future block %d",
			head.BlockNumber,
			maximumFutureBlock,
		)
	}

	startCursor := domain.EventCursor{BlockNumber: req.Realized.FromBlock - 1, LogIndex: math.MaxInt32}
	studyEvents, err := s.provider.PoolEventsAfterCursorThroughBlock(
		ctx,
		req.Realized.PoolAddress,
		startCursor,
		req.Realized.ToBlock,
	)
	if err != nil {
		return TemporalPlaceboReport{}, fmt.Errorf("build temporal placebo controls: load study events: %w", err)
	}
	liquidityActionBlocks := temporalPlaceboLiquidityActionBlocks(studyEvents, req.Collection.Samples)
	candidateBlocks := buildTemporalPlaceboCandidateBlocks(
		req.Realized.FromBlock,
		req.Realized.ToBlock,
		req.MinimumSpacingBlocks,
		req.LiquidityActionExclusionBlocks,
		liquidityActionBlocks,
	)
	if len(candidateBlocks) < len(req.Collection.Samples) {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: only %d eligible spaced candidate blocks for %d burns",
			len(candidateBlocks),
			len(req.Collection.Samples),
		)
	}

	states, candidateSkips := s.buildCandidateStates(
		ctx,
		req.Realized.PoolAddress,
		candidateBlocks,
		zeroForOneAmounts,
		oneForZeroAmounts,
		thresholds,
		req.AmountGridResolver,
	)
	if len(states) < len(req.Collection.Samples) {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: only %d valid candidate states for %d burns",
			len(states),
			len(req.Collection.Samples),
		)
	}
	matches, err := matchTemporalPlacebos(req.Collection.Samples, states)
	if err != nil {
		return TemporalPlaceboReport{}, err
	}
	balance := buildTemporalPlaceboBalance(req.Collection.Samples, states, matches)

	observations := s.buildObservations(
		ctx,
		req.Realized.PoolAddress,
		matches,
		states,
		horizons,
		zeroForOneAmounts,
		oneForZeroAmounts,
		thresholds,
		actualByKey,
		counterfactualByKey,
	)
	expectedObservations := len(matches) * len(horizons)
	if len(observations) != expectedObservations {
		return TemporalPlaceboReport{}, fmt.Errorf(
			"build temporal placebo controls: observation count %d does not match expected %d",
			len(observations),
			expectedObservations,
		)
	}
	if err := ctx.Err(); err != nil {
		return TemporalPlaceboReport{}, err
	}

	return TemporalPlaceboReport{
		PoolAddress:                    req.Realized.PoolAddress,
		FromBlock:                      req.Realized.FromBlock,
		ToBlock:                        req.Realized.ToBlock,
		IndexedThrough:                 head.BlockNumber,
		CandidateBlocks:                len(candidateBlocks),
		CandidateStates:                len(states),
		CandidateSkips:                 candidateSkips,
		LiquidityActionExclusionBlocks: req.LiquidityActionExclusionBlocks,
		MinimumSpacingBlocks:           req.MinimumSpacingBlocks,
		Matches:                        matches,
		Balance:                        balance,
		Observations:                   observations,
		Summaries:                      buildTemporalPlaceboSummaries(observations),
	}, nil
}

func indexTemporalPlaceboActualObservations(
	samples []BurnEventSample,
	horizons []BurnOutcomeHorizon,
	observations []BurnRegressionObservation,
) (map[string]BurnRegressionObservation, error) {
	result := make(map[string]BurnRegressionObservation, len(observations))
	for _, observation := range observations {
		key := burnCounterfactualObservationKey(observation.Burn.EventKey(), observation.HorizonLabel)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("build temporal placebo controls: duplicate realized observation %s", key)
		}
		result[key] = observation
	}
	for _, sample := range samples {
		for _, horizon := range horizons {
			key := burnCounterfactualObservationKey(sample.Burn.EventKey(), horizon.Label)
			if _, exists := result[key]; !exists {
				return nil, fmt.Errorf("build temporal placebo controls: missing realized observation %s", key)
			}
		}
	}
	return result, nil
}

func indexTemporalPlaceboCounterfactualObservations(
	observations []BurnCounterfactualObservation,
) (map[string]BurnCounterfactualObservation, error) {
	result := make(map[string]BurnCounterfactualObservation, len(observations))
	for _, observation := range observations {
		key := burnCounterfactualObservationKey(observation.Burn.EventKey(), observation.HorizonLabel)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("build temporal placebo controls: duplicate counterfactual observation %s", key)
		}
		result[key] = observation
	}
	return result, nil
}

func temporalPlaceboLiquidityActionBlocks(events []domain.PoolBlockEvent, samples []BurnEventSample) []uint64 {
	blocks := make([]uint64, 0, len(events)+len(samples))
	for _, event := range events {
		if (event.Type != domain.PoolBlockEventMint && event.Type != domain.PoolBlockEventBurn) || event.LiquidityChange == nil {
			continue
		}
		blocks = append(blocks, event.Cursor.BlockNumber)
	}
	for _, sample := range samples {
		blocks = append(blocks, sample.Burn.Cursor.BlockNumber)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i] < blocks[j] })
	return compactUint64s(blocks)
}

func buildTemporalPlaceboCandidateBlocks(
	fromBlock uint64,
	toBlock uint64,
	minimumSpacing uint64,
	exclusion uint64,
	liquidityActionBlocks []uint64,
) []uint64 {
	if fromBlock == 0 || toBlock <= fromBlock || minimumSpacing == 0 {
		return nil
	}
	offsets := []uint64{
		minimumSpacing / 8,
		3 * minimumSpacing / 8,
		5 * minimumSpacing / 8,
		7 * minimumSpacing / 8,
	}
	var best []uint64
	for _, offset := range offsets {
		if offset == 0 {
			offset = 1
		}
		if offset > toBlock-fromBlock {
			continue
		}
		items := make([]uint64, 0)
		for block := fromBlock + offset; block <= toBlock; {
			if block <= 1 || temporalPlaceboNearLiquidityAction(block, liquidityActionBlocks, exclusion) {
				if block == toBlock {
					break
				}
				block++
				continue
			}
			items = append(items, block)
			if minimumSpacing > toBlock-block {
				break
			}
			block += minimumSpacing
		}
		if len(items) > len(best) {
			best = items
		}
	}
	return best
}

func temporalPlaceboNearLiquidityAction(block uint64, actionBlocks []uint64, exclusion uint64) bool {
	index := sort.Search(len(actionBlocks), func(i int) bool { return actionBlocks[i] >= block })
	if index < len(actionBlocks) && absUint64Diff(block, actionBlocks[index]) <= exclusion {
		return true
	}
	if index > 0 && absUint64Diff(block, actionBlocks[index-1]) <= exclusion {
		return true
	}
	return false
}

func absUint64Diff(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

func (s *TemporalPlaceboService) buildCandidateStates(ctx context.Context, poolAddress string, blocks []uint64, zfAmounts, ofAmounts []*big.Int, thresholds []decimal.Decimal, resolver AnalysisAmountGridResolver) ([]temporalPlaceboState, []TemporalPlaceboCandidateSkip) {
	type result struct {
		index int
		state temporalPlaceboState
		err   error
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > maxTemporalPlaceboWorkers {
		workers = maxTemporalPlaceboWorkers
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	results := make(chan result, len(blocks))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				block := blocks[index]
				pool, err := s.provider.ReconstructedPoolAt(ctx, poolAddress, block-1)
				if err != nil {
					results <- result{index: index, err: err}
					continue
				}
				stateZF := cloneBurnAmountGrid(zfAmounts)
				stateOF := cloneBurnAmountGrid(ofAmounts)
				stateID := ""
				stateMode := AnalysisAmountGridModeRaw
				if resolver != nil {
					grid, gridErr := resolver.Resolve(ctx, pool)
					if gridErr != nil {
						results <- result{index: index, err: fmt.Errorf("resolve candidate amount grid: %w", gridErr)}
						continue
					}
					if gridErr = grid.RequireComplete(); gridErr != nil {
						results <- result{index: index, err: gridErr}
						continue
					}
					stateZF, gridErr = grid.ZeroForOneAmountsIn()
					if gridErr != nil {
						results <- result{index: index, err: gridErr}
						continue
					}
					stateOF, gridErr = grid.OneForZeroAmountsIn()
					if gridErr != nil {
						results <- result{index: index, err: gridErr}
						continue
					}
					stateID = grid.StateID
					stateMode = grid.Mode
				}
				zf, of, err := summarizeTemporalPlaceboPool(ctx, s.curveService, pool, stateZF, stateOF, thresholds)
				if err != nil {
					results <- result{index: index, err: err}
					continue
				}
				total := zf.PriceImpactAUCBps.Add(of.PriceImpactAUCBps)
				imbalance := decimal.Zero
				if !total.IsZero() {
					imbalance = zf.PriceImpactAUCBps.Sub(of.PriceImpactAUCBps).Abs().Div(total)
				}
				results <- result{index: index, state: temporalPlaceboState{AnchorBlock: block, ReferenceBlock: block - 1, Pool: compactTemporalPlaceboPoolState(pool), AmountGridStateID: stateID, AmountGridMode: stateMode, ZeroForOneAmountsIn: cloneBurnAmountGrid(stateZF), OneForZeroAmountsIn: cloneBurnAmountGrid(stateOF), ZeroForOne: *zf, OneForZero: *of, TotalAUCBps: total, DirectionalImbalance: imbalance}}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range blocks {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	ordered := make([]*temporalPlaceboState, len(blocks))
	errorsByIndex := make([]error, len(blocks))
	for item := range results {
		if item.err == nil {
			state := item.state
			ordered[item.index] = &state
		} else {
			errorsByIndex[item.index] = item.err
		}
	}
	states := make([]temporalPlaceboState, 0, len(blocks))
	skips := make([]TemporalPlaceboCandidateSkip, 0)
	for index, item := range ordered {
		if item != nil {
			states = append(states, *item)
			continue
		}
		skips = append(skips, TemporalPlaceboCandidateSkip{
			AnchorBlock:    blocks[index],
			ReferenceBlock: blocks[index] - 1,
			Detail:         temporalPlaceboCandidateSkipDetail(errorsByIndex[index]),
		})
	}
	return states, skips
}

func compactTemporalPlaceboPoolState(pool *domain.ReconstructedPool) *domain.ReconstructedPool {
	if pool == nil {
		return nil
	}
	result := &domain.ReconstructedPool{
		PoolAddress: pool.PoolAddress,
		BlockNumber: pool.BlockNumber,
		CurrentTick: pool.CurrentTick,
	}
	if pool.SqrtPriceX96 != nil {
		result.SqrtPriceX96 = new(big.Int).Set(pool.SqrtPriceX96)
	}
	if pool.Liquidity != nil {
		result.Liquidity = new(big.Int).Set(pool.Liquidity)
	}
	return result
}

func temporalPlaceboCandidateSkipDetail(err error) string {
	if err == nil {
		return "candidate state or baseline curve unavailable"
	}
	return err.Error()
}

func summarizeTemporalPlaceboPool(ctx context.Context, curveService *PriceImpactCurveService, pool *domain.ReconstructedPool, zfAmounts, ofAmounts []*big.Int, thresholds []decimal.Decimal) (*PriceImpactCurveSummary, *PriceImpactCurveSummary, error) {
	zfCurve, err := curveService.Build(ctx, PriceImpactCurveRequest{Pool: pool, AmountsIn: zfAmounts, ZeroForOne: true})
	if err != nil {
		return nil, nil, err
	}
	zf, err := curveService.Summarize(zfCurve, thresholds)
	if err != nil {
		return nil, nil, err
	}
	ofCurve, err := curveService.Build(ctx, PriceImpactCurveRequest{Pool: pool, AmountsIn: ofAmounts, ZeroForOne: false})
	if err != nil {
		return nil, nil, err
	}
	of, err := curveService.Summarize(ofCurve, thresholds)
	if err != nil {
		return nil, nil, err
	}
	return zf, of, nil
}

func matchTemporalPlacebos(samples []BurnEventSample, states []temporalPlaceboState) ([]TemporalPlaceboMatch, error) {
	if len(states) < len(samples) {
		return nil, fmt.Errorf("match temporal placebos: insufficient candidates")
	}
	actualFeatures := make([]temporalPlaceboFeature, len(samples))
	candidateFeatures := make([]temporalPlaceboFeature, len(states))
	all := make([]temporalPlaceboFeature, 0, len(samples)+len(states))
	for i, sample := range samples {
		actualFeatures[i] = temporalPlaceboFeatureFromBurn(sample)
		all = append(all, actualFeatures[i])
	}
	for i, state := range states {
		candidateFeatures[i] = temporalPlaceboFeatureFromState(state)
		all = append(all, candidateFeatures[i])
	}
	scales := temporalPlaceboFeatureScales(all)
	type orderItem struct {
		index   int
		nearest float64
		key     string
	}
	order := make([]orderItem, len(samples))
	for i := range samples {
		nearest := math.Inf(1)
		for j := range states {
			d := temporalPlaceboDistance(actualFeatures[i], candidateFeatures[j], scales)
			if d < nearest {
				nearest = d
			}
		}
		order[i] = orderItem{i, nearest, samples[i].Burn.EventKey()}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].nearest == order[j].nearest {
			return order[i].key < order[j].key
		}
		return order[i].nearest > order[j].nearest
	})
	used := make([]bool, len(states))
	matches := make([]TemporalPlaceboMatch, 0, len(samples))
	for _, item := range order {
		best := -1
		bestDistance := math.Inf(1)
		for j := range states {
			if used[j] {
				continue
			}
			d := temporalPlaceboDistance(actualFeatures[item.index], candidateFeatures[j], scales)
			if d < bestDistance || (best >= 0 && d == bestDistance && states[j].AnchorBlock < states[best].AnchorBlock) {
				best = j
				bestDistance = d
			}
		}
		if best < 0 {
			return nil, fmt.Errorf("match temporal placebos: no candidate for %s", samples[item.index].Burn.EventKey())
		}
		used[best] = true
		sample := samples[item.index]
		state := states[best]
		matches = append(matches, TemporalPlaceboMatch{MatchID: fmt.Sprintf("placebo_%03d", len(matches)+1), Burn: cloneBurnCandidate(sample.Burn), BurnRangeActive: sample.BurnRangeActive, BurnRangeLocation: sample.RangeLocation, PlaceboAnchorBlock: state.AnchorBlock, PlaceboReferenceBlock: state.ReferenceBlock, MatchDistance: bestDistance, BlockDistance: absUint64Diff(sample.Burn.Cursor.BlockNumber, state.AnchorBlock), BurnCurrentTick: sample.CurrentTick, PlaceboCurrentTick: state.Pool.CurrentTick, BurnActiveLiquidity: new(big.Int).Set(sample.ActiveLiquidityBeforeBurn), PlaceboActiveLiquidity: new(big.Int).Set(state.Pool.Liquidity), BurnBaselineTotalAUCBps: sample.ZeroForOne.BaseAUCBps.Add(sample.OneForZero.BaseAUCBps), BurnImmediateTotalLSISBps: sample.TotalLSISBps, PlaceboBaselineTotalAUCBps: state.TotalAUCBps, BurnDirectionalImbalance: temporalBurnDirectionalImbalance(sample), PlaceboDirectionalImbalance: state.DirectionalImbalance})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Burn.Cursor.Before(matches[j].Burn.Cursor) })
	for i := range matches {
		matches[i].MatchID = fmt.Sprintf("placebo_%03d", i+1)
	}
	return matches, nil
}

func temporalPlaceboFeatureFromBurn(sample BurnEventSample) temporalPlaceboFeature {
	total := sample.ZeroForOne.BaseAUCBps.Add(sample.OneForZero.BaseAUCBps)
	return temporalPlaceboFeature{BlockNumber: float64(sample.Burn.Cursor.BlockNumber), Tick: float64(sample.CurrentTick), LogLiquidity: logBigInt(sample.ActiveLiquidityBeforeBurn), LogAUC: logPositiveDecimal(total), DirectionalImbalance: decimalToFloat(temporalBurnDirectionalImbalance(sample))}
}
func temporalPlaceboFeatureFromState(state temporalPlaceboState) temporalPlaceboFeature {
	return temporalPlaceboFeature{BlockNumber: float64(state.AnchorBlock), Tick: float64(state.Pool.CurrentTick), LogLiquidity: logBigInt(state.Pool.Liquidity), LogAUC: logPositiveDecimal(state.TotalAUCBps), DirectionalImbalance: decimalToFloat(state.DirectionalImbalance)}
}
func temporalBurnDirectionalImbalance(sample BurnEventSample) decimal.Decimal {
	total := sample.ZeroForOne.BaseAUCBps.Add(sample.OneForZero.BaseAUCBps)
	if total.IsZero() {
		return decimal.Zero
	}
	return sample.ZeroForOne.BaseAUCBps.Sub(sample.OneForZero.BaseAUCBps).Abs().Div(total)
}
func logBigInt(value *big.Int) float64 {
	if value == nil || value.Sign() <= 0 {
		return 0
	}
	f, _ := new(big.Float).SetInt(value).Float64()
	return math.Log1p(f)
}
func logPositiveDecimal(value decimal.Decimal) float64 {
	f := decimalToFloat(value)
	if f < 0 {
		f = 0
	}
	return math.Log1p(f)
}

type temporalPlaceboScales struct{ BlockNumber, Tick, LogLiquidity, LogAUC, DirectionalImbalance float64 }

func temporalPlaceboFeatureScales(values []temporalPlaceboFeature) temporalPlaceboScales {
	blocks := make([]float64, 0, len(values))
	ticks := make([]float64, 0, len(values))
	liquidity := make([]float64, 0, len(values))
	auc := make([]float64, 0, len(values))
	imbalance := make([]float64, 0, len(values))
	for _, value := range values {
		blocks = append(blocks, value.BlockNumber)
		ticks = append(ticks, value.Tick)
		liquidity = append(liquidity, value.LogLiquidity)
		auc = append(auc, value.LogAUC)
		imbalance = append(imbalance, value.DirectionalImbalance)
	}
	return temporalPlaceboScales{
		BlockNumber:          safeStd(blocks),
		Tick:                 safeStd(ticks),
		LogLiquidity:         safeStd(liquidity),
		LogAUC:               safeStd(auc),
		DirectionalImbalance: safeStd(imbalance),
	}
}
func safeStd(values []float64) float64 {
	if len(values) < 2 {
		return 1
	}
	m := mean(values)
	var sum float64
	for _, v := range values {
		d := v - m
		sum += d * d
	}
	s := math.Sqrt(sum / float64(len(values)-1))
	if s == 0 || math.IsNaN(s) || math.IsInf(s, 0) {
		return 1
	}
	return s
}
func temporalPlaceboDistance(left, right temporalPlaceboFeature, scales temporalPlaceboScales) float64 {
	blockDistance := (left.BlockNumber - right.BlockNumber) / scales.BlockNumber
	tickDistance := (left.Tick - right.Tick) / scales.Tick
	liquidityDistance := (left.LogLiquidity - right.LogLiquidity) / scales.LogLiquidity
	aucDistance := (left.LogAUC - right.LogAUC) / scales.LogAUC
	imbalanceDistance := (left.DirectionalImbalance - right.DirectionalImbalance) / scales.DirectionalImbalance
	return math.Sqrt(
		blockDistance*blockDistance +
			tickDistance*tickDistance +
			liquidityDistance*liquidityDistance +
			aucDistance*aucDistance +
			imbalanceDistance*imbalanceDistance,
	)
}

func buildTemporalPlaceboBalance(
	samples []BurnEventSample,
	states []temporalPlaceboState,
	matches []TemporalPlaceboMatch,
) []TemporalPlaceboBalance {
	actualFeatures := make([]temporalPlaceboFeature, 0, len(samples))
	for _, sample := range samples {
		actualFeatures = append(actualFeatures, temporalPlaceboFeatureFromBurn(sample))
	}
	candidateFeatures := make([]temporalPlaceboFeature, 0, len(states))
	for _, state := range states {
		candidateFeatures = append(candidateFeatures, temporalPlaceboFeatureFromState(state))
	}
	matchedActual := make([]temporalPlaceboFeature, 0, len(matches))
	matchedPlacebo := make([]temporalPlaceboFeature, 0, len(matches))
	for _, match := range matches {
		matchedActual = append(matchedActual, temporalPlaceboFeature{
			BlockNumber:          float64(match.Burn.Cursor.BlockNumber),
			Tick:                 float64(match.BurnCurrentTick),
			LogLiquidity:         logBigInt(match.BurnActiveLiquidity),
			LogAUC:               logPositiveDecimal(match.BurnBaselineTotalAUCBps),
			DirectionalImbalance: decimalToFloat(match.BurnDirectionalImbalance),
		})
		matchedPlacebo = append(matchedPlacebo, temporalPlaceboFeature{
			BlockNumber:          float64(match.PlaceboAnchorBlock),
			Tick:                 float64(match.PlaceboCurrentTick),
			LogLiquidity:         logBigInt(match.PlaceboActiveLiquidity),
			LogAUC:               logPositiveDecimal(match.PlaceboBaselineTotalAUCBps),
			DirectionalImbalance: decimalToFloat(match.PlaceboDirectionalImbalance),
		})
	}

	metrics := []struct {
		name  string
		value func(temporalPlaceboFeature) float64
	}{
		{"block_number", func(value temporalPlaceboFeature) float64 { return value.BlockNumber }},
		{"current_tick", func(value temporalPlaceboFeature) float64 { return value.Tick }},
		{"log_active_liquidity", func(value temporalPlaceboFeature) float64 { return value.LogLiquidity }},
		{"log_baseline_total_auc", func(value temporalPlaceboFeature) float64 { return value.LogAUC }},
		{"directional_imbalance", func(value temporalPlaceboFeature) float64 { return value.DirectionalImbalance }},
	}
	featureValues := func(values []temporalPlaceboFeature, value func(temporalPlaceboFeature) float64) []float64 {
		result := make([]float64, len(values))
		for index, item := range values {
			result[index] = value(item)
		}
		return result
	}

	result := make([]TemporalPlaceboBalance, 0, len(metrics))
	for _, metric := range metrics {
		actual := featureValues(actualFeatures, metric.value)
		candidates := featureValues(candidateFeatures, metric.value)
		matchedBurns := featureValues(matchedActual, metric.value)
		matchedControls := featureValues(matchedPlacebo, metric.value)
		beforeSMD := standardizedMeanDifference(actual, candidates)
		afterSMD := standardizedMeanDifference(matchedBurns, matchedControls)
		result = append(result, TemporalPlaceboBalance{
			Metric:               metric.name,
			ActualCount:          len(actual),
			CandidateCount:       len(candidates),
			MatchedCount:         len(matchedControls),
			ActualMean:           meanOrZero(actual),
			CandidateMean:        meanOrZero(candidates),
			SMDBefore:            beforeSMD,
			MatchedPlaceboMean:   meanOrZero(matchedControls),
			SMDAfter:             afterSMD,
			AbsoluteSMDReduction: math.Abs(beforeSMD) - math.Abs(afterSMD),
		})
	}
	return result
}

func (s *TemporalPlaceboService) buildObservations(ctx context.Context, poolAddress string, matches []TemporalPlaceboMatch, states []temporalPlaceboState, horizons []BurnOutcomeHorizon, zfAmounts, ofAmounts []*big.Int, thresholds []decimal.Decimal, actualByKey map[string]BurnRegressionObservation, cfByKey map[string]BurnCounterfactualObservation) []TemporalPlaceboObservation {
	stateByBlock := make(map[uint64]temporalPlaceboState, len(states))
	for _, state := range states {
		stateByBlock[state.AnchorBlock] = state
	}
	type result struct {
		index int
		rows  []TemporalPlaceboObservation
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > maxTemporalPlaceboWorkers {
		workers = maxTemporalPlaceboWorkers
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	results := make(chan result, len(matches))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				match := matches[index]
				state := stateByBlock[match.PlaceboAnchorBlock]
				rows := make([]TemporalPlaceboObservation, 0, len(horizons))
				maxBlock := match.PlaceboAnchorBlock + horizons[len(horizons)-1].Blocks
				start := domain.EventCursor{BlockNumber: match.PlaceboAnchorBlock - 1, LogIndex: math.MaxInt32}
				events, eventErr := s.provider.PoolEventsAfterCursorThroughBlock(ctx, poolAddress, start, maxBlock)
				for _, h := range horizons {
					row := TemporalPlaceboObservation{Match: cloneTemporalPlaceboMatch(match), HorizonLabel: h.Label, HorizonBlocks: h.Blocks, FutureBlock: match.PlaceboAnchorBlock + h.Blocks, ReferenceTick: state.Pool.CurrentTick, ReferenceSqrtPriceX96: new(big.Int).Set(state.Pool.SqrtPriceX96), ReferenceActiveLiquidity: new(big.Int).Set(state.Pool.Liquidity), PlaceboZeroForOneBaseAUCBps: state.ZeroForOne.PriceImpactAUCBps, PlaceboOneForZeroBaseAUCBps: state.OneForZero.PriceImpactAUCBps}
					key := burnCounterfactualObservationKey(match.Burn.EventKey(), h.Label)
					if actual, ok := actualByKey[key]; ok {
						row.ActualTotalRealizedDeteriorationBps = actual.TotalRealizedDeteriorationBps
					}
					if cf, ok := cfByKey[key]; ok {
						row.ActualMinMechanicalEffectPIAUCBps = cf.MinTotalMechanicalEffectPIAUCBps
						row.ActualMaxMechanicalEffectPIAUCBps = cf.MaxTotalMechanicalEffectPIAUCBps
						row.ActualCounterfactualAvailable = cf.ExactInputTieBreak.Available || cf.ExactOutputTieBreak.Available
					}
					if eventErr != nil {
						row.FailureDetail = eventErr.Error()
						rows = append(rows, row)
						continue
					}
					futurePool, err := s.provider.ReconstructedPoolAt(ctx, poolAddress, row.FutureBlock)
					if err != nil {
						row.FailureDetail = err.Error()
						rows = append(rows, row)
						continue
					}
					zf, of, err := summarizeTemporalPlaceboPool(ctx, s.curveService, futurePool, state.ZeroForOneAmountsIn, state.OneForZeroAmountsIn, thresholds)
					if err != nil {
						row.FailureDetail = err.Error()
						rows = append(rows, row)
						continue
					}
					row.PlaceboZeroForOneFutureAUCBps = zf.PriceImpactAUCBps
					row.PlaceboOneForZeroFutureAUCBps = of.PriceImpactAUCBps
					zfDelta := zf.PriceImpactAUCBps.Sub(state.ZeroForOne.PriceImpactAUCBps)
					ofDelta := of.PriceImpactAUCBps.Sub(state.OneForZero.PriceImpactAUCBps)
					row.PlaceboTotalRealizedDeltaPIAUCBps = zfDelta.Add(ofDelta)
					row.PlaceboTotalRealizedDeteriorationBps = positiveOnly(zfDelta).Add(positiveOnly(ofDelta))
					row.ExcessDeteriorationBps = row.ActualTotalRealizedDeteriorationBps.Sub(row.PlaceboTotalRealizedDeteriorationBps)
					row.FutureTick = futurePool.CurrentTick
					row.TickChange = futurePool.CurrentTick - state.Pool.CurrentTick
					row.AbsoluteTickChange = burnAbsInt(row.TickChange)
					row.FutureSqrtPriceX96 = new(big.Int).Set(futurePool.SqrtPriceX96)
					row.FutureActiveLiquidity = new(big.Int).Set(futurePool.Liquidity)
					priceReturn, priceErr := burnSqrtPriceReturnBps(state.Pool.SqrtPriceX96, futurePool.SqrtPriceX96)
					if priceErr != nil {
						row.FailureDetail = priceErr.Error()
						rows = append(rows, row)
						continue
					}
					row.PriceReturnBps = priceReturn
					row.AbsolutePriceReturnBps = priceReturn.Abs()
					liqDelta := new(big.Int).Sub(new(big.Int).Set(futurePool.Liquidity), state.Pool.Liquidity)
					row.ActiveLiquidityChangeBps = burnSignedBigIntRatio(liqDelta, state.Pool.Liquidity).Mul(decimal.NewFromInt(10000))
					row.Flow = summarizeTemporalPlaceboFlow(events, state.Pool.CurrentTick, row.FutureBlock)
					row.Available = true
					rows = append(rows, row)
				}
				results <- result{index, rows}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range matches {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	ordered := make([][]TemporalPlaceboObservation, len(matches))
	for item := range results {
		ordered[item.index] = item.rows
	}
	all := make([]TemporalPlaceboObservation, 0, len(matches)*len(horizons))
	for _, rows := range ordered {
		all = append(all, rows...)
	}
	return all
}

func cloneTemporalPlaceboMatch(value TemporalPlaceboMatch) TemporalPlaceboMatch {
	value.Burn = cloneBurnCandidate(value.Burn)
	if value.BurnActiveLiquidity != nil {
		value.BurnActiveLiquidity = new(big.Int).Set(value.BurnActiveLiquidity)
	}
	if value.PlaceboActiveLiquidity != nil {
		value.PlaceboActiveLiquidity = new(big.Int).Set(value.PlaceboActiveLiquidity)
	}
	return value
}

func summarizeTemporalPlaceboFlow(events []domain.PoolBlockEvent, referenceTick int, throughBlock uint64) TemporalPlaceboFlowControls {
	result := TemporalPlaceboFlowControls{GrossToken0VolumeRaw: big.NewInt(0), GrossToken1VolumeRaw: big.NewInt(0), GrossMintLiquidity: big.NewInt(0), GrossBurnLiquidity: big.NewInt(0), NetLiquidityFlow: big.NewInt(0)}
	previousTick := referenceTick
	minTick, maxTick := referenceTick, referenceTick
	for _, event := range events {
		if event.Cursor.BlockNumber > throughBlock {
			break
		}
		switch event.Type {
		case domain.PoolBlockEventMint, domain.PoolBlockEventBurn:
			change := event.LiquidityChange
			if change == nil || change.LiquidityDelta == nil {
				continue
			}
			result.LiquidityEventCount++
			result.NetLiquidityFlow.Add(result.NetLiquidityFlow, change.LiquidityDelta)
			if change.LiquidityDelta.Sign() > 0 {
				result.MintEventCount++
				result.GrossMintLiquidity.Add(result.GrossMintLiquidity, change.LiquidityDelta)
			} else {
				result.BurnEventCount++
				result.GrossBurnLiquidity.Add(result.GrossBurnLiquidity, new(big.Int).Abs(change.LiquidityDelta))
			}
		case domain.PoolBlockEventSwap:
			swap := event.Swap
			if swap == nil {
				continue
			}
			zero, amountIn, amountOut, err := burnRealizedFlowSwapAmounts(*swap)
			if err != nil {
				continue
			}
			result.SwapCount++
			if zero {
				result.ZeroForOneSwapCount++
				result.GrossToken0VolumeRaw.Add(result.GrossToken0VolumeRaw, amountIn)
				result.GrossToken1VolumeRaw.Add(result.GrossToken1VolumeRaw, amountOut)
			} else {
				result.OneForZeroSwapCount++
				result.GrossToken1VolumeRaw.Add(result.GrossToken1VolumeRaw, amountIn)
				result.GrossToken0VolumeRaw.Add(result.GrossToken0VolumeRaw, amountOut)
			}
			step := swap.TickAfter - previousTick
			abs := burnAbsInt(step)
			result.TickPathTotalVariation += uint64(abs)
			if abs > result.TickPathMaxAbsoluteStep {
				result.TickPathMaxAbsoluteStep = abs
			}
			if swap.TickAfter < minTick {
				minTick = swap.TickAfter
			}
			if swap.TickAfter > maxTick {
				maxTick = swap.TickAfter
			}
			previousTick = swap.TickAfter
		}
	}
	result.TickPathRange = maxTick - minTick
	return result
}

func buildTemporalPlaceboSummaries(observations []TemporalPlaceboObservation) []TemporalPlaceboHorizonSummary {
	type key struct {
		stratum, label string
		blocks         uint64
	}
	groups := make(map[key][]TemporalPlaceboObservation)
	for _, o := range observations {
		strata := []string{"all"}
		if o.Match.BurnRangeActive {
			strata = append(strata, "active")
		} else {
			strata = append(strata, "inactive")
		}
		for _, stratum := range strata {
			k := key{stratum, o.HorizonLabel, o.HorizonBlocks}
			groups[k] = append(groups[k], o)
		}
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].blocks == keys[j].blocks {
			return keys[i].stratum < keys[j].stratum
		}
		return keys[i].blocks < keys[j].blocks
	})
	result := make([]TemporalPlaceboHorizonSummary, 0, len(keys))
	for _, k := range keys {
		rows := groups[k]
		summary := TemporalPlaceboHorizonSummary{Stratum: k.stratum, HorizonLabel: k.label, HorizonBlocks: k.blocks, Pairs: len(rows)}
		actuals := make([]decimal.Decimal, 0)
		placebos := make([]decimal.Decimal, 0)
		excesses := make([]decimal.Decimal, 0)
		lsisFloat := make([]float64, 0)
		excessFloat := make([]float64, 0)
		for _, o := range rows {
			if !o.Available {
				summary.UnavailablePairs++
				continue
			}
			summary.AvailablePairs++
			actuals = append(actuals, o.ActualTotalRealizedDeteriorationBps)
			placebos = append(placebos, o.PlaceboTotalRealizedDeteriorationBps)
			excesses = append(excesses, o.ExcessDeteriorationBps)
			if o.ExcessDeteriorationBps.IsPositive() {
				summary.PositiveExcessPairs++
			}
			lsisFloat = append(lsisFloat, decimalToFloat(o.Match.BurnImmediateTotalLSISBps))
			excessFloat = append(excessFloat, decimalToFloat(o.ExcessDeteriorationBps))
		}
		summary.MeanActualDeteriorationBps = decimalMean(actuals)
		summary.MeanPlaceboDeteriorationBps = decimalMean(placebos)
		summary.MeanExcessDeteriorationBps = decimalMean(excesses)
		summary.MedianExcessDeteriorationBps = decimalMedian(excesses)
		if summary.AvailablePairs > 0 {
			summary.PositiveExcessShare = decimal.NewFromInt(int64(summary.PositiveExcessPairs)).Div(decimal.NewFromInt(int64(summary.AvailablePairs)))
		}
		summary.PearsonImmediateLSISVsExcess = pearson(lsisFloat, excessFloat)
		summary.SpearmanImmediateLSISVsExcess = spearman(lsisFloat, excessFloat)
		result = append(result, summary)
	}
	return result
}

func decimalMean(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}
	sum := decimal.Zero
	for _, v := range values {
		sum = sum.Add(v)
	}
	return sum.Div(decimal.NewFromInt(int64(len(values))))
}
func decimalMedian(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}
	copyValues := append([]decimal.Decimal(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i].LessThan(copyValues[j]) })
	mid := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[mid]
	}
	return copyValues[mid-1].Add(copyValues[mid]).Div(decimal.NewFromInt(2))
}
