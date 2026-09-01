package services

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type BurnOutcomeHorizon struct {
	Label  string
	Blocks uint64
}

type BurnOutcomeSkipReason string

const (
	BurnOutcomeSkipFutureBlockNotIndexed BurnOutcomeSkipReason = "future_block_not_indexed"

	BurnOutcomeSkipZeroFutureActiveLiquidity BurnOutcomeSkipReason = "zero_future_active_liquidity"
)

type BurnRealizedDirectionalOutcome struct {
	ZeroForOne bool

	PreEventAUCBps decimal.Decimal
	FutureAUCBps   decimal.Decimal

	// RealizedDeltaPIAUCBps is signed:
	//
	//	future PIAUC - exact pre-event PIAUC
	//
	// Positive means deterioration and negative means improvement.
	RealizedDeltaPIAUCBps decimal.Decimal

	// RealizedDeteriorationBps is the positive-only form of the signed delta.
	RealizedDeteriorationBps decimal.Decimal

	// BaseDepthAmount is the exact pre-event depth.
	// CounterfactualDepthAmount contains future realized depth in this context.
	DepthDeltas []DepthDelta
}

type BurnRealizedHorizonOutcome struct {
	HorizonLabel  string
	HorizonBlocks uint64

	FutureBlock uint64

	FutureCurrentTick int

	FutureSqrtPriceX96    *big.Int
	FutureActiveLiquidity *big.Int

	MarketControls BurnRealizedMarketControls

	FlowControls BurnRealizedFlowControls

	ZeroForOne BurnRealizedDirectionalOutcome
	OneForZero BurnRealizedDirectionalOutcome

	// Signed sum of the two directional realized PIAUC changes.
	TotalRealizedDeltaPIAUCBps decimal.Decimal

	// Sum of positive directional deterioration. Improvement in one direction
	// does not cancel deterioration in the other direction.
	TotalRealizedDeteriorationBps decimal.Decimal

	MaxDirectionalDeteriorationBps decimal.Decimal
}

type BurnRealizedOutcomeSkip struct {
	Burn domain.BurnCandidate

	HorizonLabel  string
	HorizonBlocks uint64
	FutureBlock   uint64

	Reason BurnOutcomeSkipReason
	Detail string
}

type BurnRealizedOutcomeReport struct {
	Burn domain.BurnCandidate

	IndexedThrough uint64

	Outcomes []BurnRealizedHorizonOutcome
	Skipped  []BurnRealizedOutcomeSkip
}

type BurnRealizedOutcomeProvider interface {
	IndexedHead(
		ctx context.Context,
	) (domain.IndexedHead, error)

	PoolSnapshotAt(
		ctx context.Context,
		poolAddress string,
		blockNumber uint64,
	) (domain.PoolSnapshot, error)
}

type BurnRealizedOutcomeRepository interface {
	LoadReconstructionInputFromSnapshot(
		ctx context.Context,
		snapshot domain.PoolSnapshot,
	) (domain.ReconstructionInput, error)
}

type BurnRealizedOutcomeRequest struct {
	Sample BurnEventSample

	// IndexedThrough pins all outcome analyses belonging to one regression
	// dataset to the same provider checkpoint.
	//
	// A zero value means that Analyze must resolve the provider's current
	// indexed head. A non-zero value means that the supplied checkpoint must
	// be used without reading a newer head.
	IndexedThrough uint64

	RequireFlowControls bool

	Horizons []BurnOutcomeHorizon

	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int

	ThresholdsBps []decimal.Decimal
}

type BurnRealizedOutcomeService struct {
	provider     BurnRealizedOutcomeProvider
	repository   BurnRealizedOutcomeRepository
	curveService *PriceImpactCurveService

	// The realized dataset and the no-burn counterfactual consume the exact same
	// burn-to-max-horizon event stream. Keep the PostgreSQL-loaded stream once in
	// memory and release it immediately after the counterfactual sample consumes it.
	flowCacheMu sync.Mutex
	flowCache   map[burnRealizedFlowCacheKey]burnRealizedFlowEventSet
}

func NewBurnRealizedOutcomeService(
	provider BurnRealizedOutcomeProvider,
	repository BurnRealizedOutcomeRepository,
	curveService *PriceImpactCurveService,
) *BurnRealizedOutcomeService {
	return &BurnRealizedOutcomeService{
		provider: provider,

		repository: repository,

		curveService: curveService,

		flowCache: make(
			map[burnRealizedFlowCacheKey]burnRealizedFlowEventSet,
		),
	}
}

func (s *BurnRealizedOutcomeService) Analyze(
	ctx context.Context,
	req BurnRealizedOutcomeRequest,
) (BurnRealizedOutcomeReport, error) {
	if s == nil {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: service is nil",
		)
	}

	if s.provider == nil {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: provider is nil",
		)
	}

	if s.repository == nil {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: repository is nil",
		)
	}

	if s.curveService == nil ||
		s.curveService.simulator == nil {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: curve service is nil",
		)
	}

	if err := req.Sample.Validate(); err != nil {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: invalid sample: %w",
			err,
		)
	}

	horizons, err :=
		normalizeBurnOutcomeHorizons(
			req.Sample.Burn.Cursor.BlockNumber,
			req.Horizons,
		)
	if err != nil {
		return BurnRealizedOutcomeReport{}, err
	}

	zeroForOneAmounts, err :=
		normalizeBurnAmountGrid(
			"realized zero_for_one",
			req.ZeroForOneAmountsIn,
		)
	if err != nil {
		return BurnRealizedOutcomeReport{}, err
	}

	oneForZeroAmounts, err :=
		normalizeBurnAmountGrid(
			"realized one_for_zero",
			req.OneForZeroAmountsIn,
		)
	if err != nil {
		return BurnRealizedOutcomeReport{}, err
	}

	thresholds, err :=
		normalizeBurnThresholds(
			req.ThresholdsBps,
		)
	if err != nil {
		return BurnRealizedOutcomeReport{}, err
	}

	if err :=
		validateBurnOutcomeBaseThresholds(
			req.Sample,
			thresholds,
		); err != nil {
		return BurnRealizedOutcomeReport{}, err
	}

	indexedThrough :=
		req.IndexedThrough

	if indexedThrough == 0 {
		head, err :=
			s.provider.IndexedHead(
				ctx,
			)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: read indexed head: %w",
				err,
			)
		}

		indexedThrough =
			head.BlockNumber
	}

	if indexedThrough == 0 {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: indexed-through checkpoint is zero",
		)
	}

	if indexedThrough <
		req.Sample.Burn.Cursor.BlockNumber {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"analyze burn realized outcome: indexed-through checkpoint %d is before burn block %d",
			indexedThrough,
			req.Sample.Burn.Cursor.BlockNumber,
		)
	}

	report :=
		BurnRealizedOutcomeReport{
			Burn: cloneBurnCandidate(
				req.Sample.Burn,
			),

			IndexedThrough: indexedThrough,

			Outcomes: make(
				[]BurnRealizedHorizonOutcome,
				0,
				len(horizons),
			),

			Skipped: make(
				[]BurnRealizedOutcomeSkip,
				0,
			),
		}

	maximumEligibleFutureBlock :=
		req.Sample.Burn.Cursor.BlockNumber

	for _, horizon := range horizons {
		futureBlock :=
			req.Sample.Burn.Cursor.BlockNumber +
				horizon.Blocks

		if futureBlock <= indexedThrough &&
			futureBlock >
				maximumEligibleFutureBlock {
			maximumEligibleFutureBlock =
				futureBlock
		}
	}

	flowEvents :=
		burnRealizedFlowEventSet{}

	if maximumEligibleFutureBlock >
		req.Sample.Burn.Cursor.BlockNumber {
		flowEvents, err =
			s.loadBurnRealizedFlowEvents(
				ctx,
				req.Sample.Burn,
				maximumEligibleFutureBlock,
				req.RequireFlowControls,
			)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: load flow events: %w",
				err,
			)
		}
	}

	for _, horizon := range horizons {
		if err := ctx.Err(); err != nil {
			return BurnRealizedOutcomeReport{}, err
		}

		futureBlock :=
			req.Sample.
				Burn.
				Cursor.
				BlockNumber +
				horizon.Blocks

		if futureBlock >
			indexedThrough {
			report.Skipped =
				append(
					report.Skipped,
					newBurnRealizedOutcomeSkip(
						req.Sample.Burn,
						horizon,
						futureBlock,
						BurnOutcomeSkipFutureBlockNotIndexed,
						fmt.Sprintf(
							"future block %d is after provider indexed head %d",
							futureBlock,
							indexedThrough,
						),
					),
				)

			continue
		}

		futurePool, err := loadHistoricalPoolAt(
			ctx,
			s.provider,
			s.repository,
			req.Sample.Burn.PoolAddress,
			futureBlock,
		)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: load local future pool at block %d: %w",
				futureBlock,
				err,
			)
		}

		if err :=
			validateBurnOutcomeFuturePool(
				futurePool,
				req.Sample.Burn.PoolAddress,
				futureBlock,
			); err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: %w",
				err,
			)
		}

		if futurePool.Liquidity.Sign() == 0 {
			report.Skipped =
				append(
					report.Skipped,
					newBurnRealizedOutcomeSkip(
						req.Sample.Burn,
						horizon,
						futureBlock,
						BurnOutcomeSkipZeroFutureActiveLiquidity,
						"future realized price-impact curve is undefined because active liquidity is zero",
					),
				)

			continue
		}

		marketControls, err :=
			buildBurnRealizedMarketControls(
				req.Sample,
				futurePool,
			)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: horizon=%s build market controls: %w",
				horizon.Label,
				err,
			)
		}

		flowControls, err :=
			buildBurnRealizedFlowControls(
				req.Sample,
				futurePool,
				flowEvents,
			)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: horizon=%s build flow controls: %w",
				horizon.Label,
				err,
			)
		}

		zeroForOne, err :=
			s.analyzeDirection(
				ctx,
				req.Sample.ZeroForOne,
				futurePool,
				zeroForOneAmounts,
				thresholds,
				true,
			)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: horizon=%s zero_for_one: %w",
				horizon.Label,
				err,
			)
		}

		oneForZero, err :=
			s.analyzeDirection(
				ctx,
				req.Sample.OneForZero,
				futurePool,
				oneForZeroAmounts,
				thresholds,
				false,
			)
		if err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: horizon=%s one_for_zero: %w",
				horizon.Label,
				err,
			)
		}

		totalSignedDelta :=
			zeroForOne.
				RealizedDeltaPIAUCBps.
				Add(
					oneForZero.
						RealizedDeltaPIAUCBps,
				)

		totalDeterioration :=
			zeroForOne.
				RealizedDeteriorationBps.
				Add(
					oneForZero.
						RealizedDeteriorationBps,
				)

		outcome :=
			BurnRealizedHorizonOutcome{
				HorizonLabel: horizon.Label,

				HorizonBlocks: horizon.Blocks,

				FutureBlock: futureBlock,

				FutureCurrentTick: futurePool.CurrentTick,

				FutureSqrtPriceX96: new(big.Int).Set(
					futurePool.SqrtPriceX96,
				),

				FutureActiveLiquidity: new(big.Int).Set(
					futurePool.Liquidity,
				),

				MarketControls: cloneBurnRealizedMarketControls(
					marketControls,
				),

				FlowControls: cloneBurnRealizedFlowControls(
					flowControls,
				),

				ZeroForOne: zeroForOne,

				OneForZero: oneForZero,

				TotalRealizedDeltaPIAUCBps: totalSignedDelta,

				TotalRealizedDeteriorationBps: totalDeterioration,

				MaxDirectionalDeteriorationBps: burnMaxDecimal(
					zeroForOne.
						RealizedDeteriorationBps,
					oneForZero.
						RealizedDeteriorationBps,
				),
			}

		if err :=
			validateBurnRealizedHorizonOutcome(
				req.Sample.Burn,
				outcome,
			); err != nil {
			return BurnRealizedOutcomeReport{}, fmt.Errorf(
				"analyze burn realized outcome: horizon=%s: %w",
				horizon.Label,
				err,
			)
		}

		report.Outcomes =
			append(
				report.Outcomes,
				outcome,
			)
	}

	if err :=
		validateBurnRealizedOutcomeReport(
			report,
			horizons,
		); err != nil {
		return BurnRealizedOutcomeReport{}, err
	}

	return report, nil
}

func (s *BurnRealizedOutcomeService) analyzeDirection(
	ctx context.Context,
	preEventImpact BurnDirectionalImpact,
	futurePool *domain.ReconstructedPool,
	amountsIn []*big.Int,
	thresholds []decimal.Decimal,
	zeroForOne bool,
) (BurnRealizedDirectionalOutcome, error) {
	if preEventImpact.ZeroForOne !=
		zeroForOne {
		return BurnRealizedDirectionalOutcome{}, fmt.Errorf(
			"pre-event direction=%t, expected=%t",
			preEventImpact.ZeroForOne,
			zeroForOne,
		)
	}

	baseDepths, err :=
		burnOutcomeBaseDepths(
			preEventImpact,
			thresholds,
		)
	if err != nil {
		return BurnRealizedDirectionalOutcome{}, err
	}

	futureCurve, err :=
		s.curveService.Build(
			ctx,
			PriceImpactCurveRequest{
				Pool: futurePool,

				AmountsIn: cloneBurnAmountGrid(
					amountsIn,
				),

				ZeroForOne: zeroForOne,
			},
		)
	if err != nil {
		return BurnRealizedDirectionalOutcome{}, fmt.Errorf(
			"build future realized curve: %w",
			err,
		)
	}

	futureSummary, err :=
		s.curveService.Summarize(
			futureCurve,
			thresholds,
		)
	if err != nil {
		return BurnRealizedDirectionalOutcome{}, fmt.Errorf(
			"summarize future realized curve: %w",
			err,
		)
	}

	if len(futureSummary.ThresholdDepths) !=
		len(baseDepths) {
		return BurnRealizedDirectionalOutcome{}, fmt.Errorf(
			"future depth count=%d, pre-event depth count=%d",
			len(futureSummary.ThresholdDepths),
			len(baseDepths),
		)
	}

	realizedDelta :=
		futureSummary.
			PriceImpactAUCBps.
			Sub(
				preEventImpact.BaseAUCBps,
			)

	outcome :=
		BurnRealizedDirectionalOutcome{
			ZeroForOne: zeroForOne,

			PreEventAUCBps: preEventImpact.BaseAUCBps,

			FutureAUCBps: futureSummary.
				PriceImpactAUCBps,

			RealizedDeltaPIAUCBps: realizedDelta,

			RealizedDeteriorationBps: positiveOnly(
				realizedDelta,
			),

			DepthDeltas: compareDepths(
				baseDepths,
				futureSummary.
					ThresholdDepths,
			),
		}

	if err :=
		validateBurnRealizedDirectionalOutcome(
			outcome,
			zeroForOne,
		); err != nil {
		return BurnRealizedDirectionalOutcome{}, err
	}

	return outcome, nil
}

func normalizeBurnOutcomeHorizons(
	eventBlock uint64,
	values []BurnOutcomeHorizon,
) ([]BurnOutcomeHorizon, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf(
			"analyze burn realized outcome: horizons are empty",
		)
	}

	result :=
		make(
			[]BurnOutcomeHorizon,
			len(values),
		)

	seenLabels :=
		make(
			map[string]struct{},
			len(values),
		)

	for index, horizon := range values {
		horizon.Label =
			strings.TrimSpace(
				horizon.Label,
			)

		if horizon.Label == "" {
			return nil, fmt.Errorf(
				"analyze burn realized outcome: horizon %d has empty label",
				index,
			)
		}

		if horizon.Blocks == 0 {
			return nil, fmt.Errorf(
				"analyze burn realized outcome: horizon %q has zero block distance",
				horizon.Label,
			)
		}

		if horizon.Blocks >
			^uint64(0)-eventBlock {
			return nil, fmt.Errorf(
				"analyze burn realized outcome: horizon %q overflows block number",
				horizon.Label,
			)
		}

		if _, exists :=
			seenLabels[horizon.Label]; exists {
			return nil, fmt.Errorf(
				"analyze burn realized outcome: duplicate horizon label %q",
				horizon.Label,
			)
		}

		if index > 0 &&
			values[index-1].Blocks >=
				horizon.Blocks {
			return nil, fmt.Errorf(
				"analyze burn realized outcome: horizons must be strictly increasing at index %d",
				index,
			)
		}

		seenLabels[horizon.Label] = struct{}{}

		result[index] =
			horizon
	}

	return result, nil
}

func validateBurnOutcomeBaseThresholds(
	sample BurnEventSample,
	thresholds []decimal.Decimal,
) error {
	directions :=
		[]BurnDirectionalImpact{
			sample.ZeroForOne,
			sample.OneForZero,
		}

	for _, direction := range directions {
		if len(direction.DepthDeltas) !=
			len(thresholds) {
			return fmt.Errorf(
				"analyze burn realized outcome: pre-event depth count=%d, threshold count=%d for zero_for_one=%t",
				len(direction.DepthDeltas),
				len(thresholds),
				direction.ZeroForOne,
			)
		}

		for index, depth := range direction.DepthDeltas {
			if !depth.ThresholdBps.Equal(
				thresholds[index],
			) {
				return fmt.Errorf(
					"analyze burn realized outcome: pre-event threshold index %d is %s, expected %s",
					index,
					depth.ThresholdBps,
					thresholds[index],
				)
			}
		}
	}

	return nil
}

func burnOutcomeBaseDepths(
	impact BurnDirectionalImpact,
	thresholds []decimal.Decimal,
) ([]ThresholdDepth, error) {
	if len(impact.DepthDeltas) !=
		len(thresholds) {
		return nil, fmt.Errorf(
			"pre-event depth count=%d, threshold count=%d",
			len(impact.DepthDeltas),
			len(thresholds),
		)
	}

	result :=
		make(
			[]ThresholdDepth,
			len(impact.DepthDeltas),
		)

	for index, depth := range impact.DepthDeltas {
		if !depth.ThresholdBps.Equal(
			thresholds[index],
		) {
			return nil, fmt.Errorf(
				"pre-event threshold index %d is %s, expected %s",
				index,
				depth.ThresholdBps,
				thresholds[index],
			)
		}

		result[index] =
			ThresholdDepth{
				ThresholdBps: depth.ThresholdBps,

				DepthAmount: depth.BaseDepthAmount,

				Breached: depth.BaseBreached,
			}
	}

	return result, nil
}

func validateBurnOutcomeSnapshot(
	snapshot domain.PoolSnapshot,
	poolAddress string,
	futureBlock uint64,
) error {
	if normalizeAddress(
		snapshot.PoolAddress,
	) != normalizeAddress(
		poolAddress,
	) {
		return fmt.Errorf(
			"future snapshot pool=%s, expected=%s",
			snapshot.PoolAddress,
			poolAddress,
		)
	}

	if snapshot.BlockNumber !=
		futureBlock {
		return fmt.Errorf(
			"future snapshot block=%d, expected=%d",
			snapshot.BlockNumber,
			futureBlock,
		)
	}

	return nil
}

func validateBurnOutcomeFuturePool(
	pool *domain.ReconstructedPool,
	poolAddress string,
	futureBlock uint64,
) error {
	if pool == nil {
		return fmt.Errorf(
			"future reconstructed pool is nil",
		)
	}

	if normalizeAddress(
		pool.PoolAddress,
	) != normalizeAddress(
		poolAddress,
	) {
		return fmt.Errorf(
			"future reconstructed pool=%s, expected=%s",
			pool.PoolAddress,
			poolAddress,
		)
	}

	if pool.BlockNumber !=
		futureBlock {
		return fmt.Errorf(
			"future reconstructed block=%d, expected=%d",
			pool.BlockNumber,
			futureBlock,
		)
	}

	if pool.SqrtPriceX96 == nil ||
		pool.SqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"future reconstructed sqrt price is invalid",
		)
	}

	if pool.Liquidity == nil ||
		pool.Liquidity.Sign() < 0 {
		return fmt.Errorf(
			"future reconstructed active liquidity is invalid",
		)
	}

	return nil
}

func validateBurnRealizedDirectionalOutcome(
	outcome BurnRealizedDirectionalOutcome,
	expectedDirection bool,
) error {
	if outcome.ZeroForOne !=
		expectedDirection {
		return fmt.Errorf(
			"realized direction=%t, expected=%t",
			outcome.ZeroForOne,
			expectedDirection,
		)
	}

	if outcome.PreEventAUCBps.IsNegative() ||
		outcome.FutureAUCBps.IsNegative() ||
		outcome.RealizedDeteriorationBps.IsNegative() {
		return fmt.Errorf(
			"realized AUC and deterioration values must not be negative",
		)
	}

	expectedDelta :=
		outcome.
			FutureAUCBps.
			Sub(
				outcome.PreEventAUCBps,
			)

	if !outcome.RealizedDeltaPIAUCBps.Equal(
		expectedDelta,
	) {
		return fmt.Errorf(
			"realized delta=%s, expected=%s",
			outcome.RealizedDeltaPIAUCBps,
			expectedDelta,
		)
	}

	expectedDeterioration :=
		positiveOnly(
			expectedDelta,
		)

	if !outcome.
		RealizedDeteriorationBps.
		Equal(
			expectedDeterioration,
		) {
		return fmt.Errorf(
			"realized deterioration=%s, expected=%s",
			outcome.RealizedDeteriorationBps,
			expectedDeterioration,
		)
	}

	for index, depth := range outcome.DepthDeltas {
		expectedDepthDelta :=
			depth.
				BaseDepthAmount.
				Sub(
					depth.
						CounterfactualDepthAmount,
				)

		if !depth.DeltaDepthAmount.Equal(
			expectedDepthDelta,
		) {
			return fmt.Errorf(
				"realized depth %d delta=%s, expected=%s",
				index,
				depth.DeltaDepthAmount,
				expectedDepthDelta,
			)
		}
	}

	return nil
}

func validateBurnRealizedHorizonOutcome(
	burn domain.BurnCandidate,
	outcome BurnRealizedHorizonOutcome,
) error {
	if strings.TrimSpace(
		outcome.HorizonLabel,
	) == "" {
		return fmt.Errorf(
			"realized horizon label is empty",
		)
	}

	if outcome.HorizonBlocks == 0 {
		return fmt.Errorf(
			"realized horizon blocks is zero",
		)
	}

	expectedFutureBlock :=
		burn.Cursor.BlockNumber +
			outcome.HorizonBlocks

	if outcome.FutureBlock !=
		expectedFutureBlock {
		return fmt.Errorf(
			"future block=%d, expected=%d",
			outcome.FutureBlock,
			expectedFutureBlock,
		)
	}

	if outcome.FutureSqrtPriceX96 == nil ||
		outcome.FutureSqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"future sqrt price is invalid",
		)
	}

	if outcome.FutureActiveLiquidity == nil ||
		outcome.FutureActiveLiquidity.Sign() <= 0 {
		return fmt.Errorf(
			"future active liquidity must be positive",
		)
	}

	if err :=
		validateBurnRealizedMarketControls(
			burn,
			outcome.FutureCurrentTick,
			outcome.FutureSqrtPriceX96,
			outcome.FutureActiveLiquidity,
			outcome.MarketControls,
		); err != nil {
		return fmt.Errorf(
			"market controls: %w",
			err,
		)
	}

	if err :=
		validateBurnRealizedFlowControls(
			burn,
			outcome.FutureBlock,
			outcome.FutureCurrentTick,
			outcome.FutureSqrtPriceX96,
			outcome.FlowControls,
		); err != nil {
		return fmt.Errorf(
			"flow controls: %w",
			err,
		)
	}

	if err :=
		validateBurnRealizedDirectionalOutcome(
			outcome.ZeroForOne,
			true,
		); err != nil {
		return fmt.Errorf(
			"zero_for_one: %w",
			err,
		)
	}

	if err :=
		validateBurnRealizedDirectionalOutcome(
			outcome.OneForZero,
			false,
		); err != nil {
		return fmt.Errorf(
			"one_for_zero: %w",
			err,
		)
	}

	expectedSignedTotal :=
		outcome.
			ZeroForOne.
			RealizedDeltaPIAUCBps.
			Add(
				outcome.
					OneForZero.
					RealizedDeltaPIAUCBps,
			)

	if !outcome.
		TotalRealizedDeltaPIAUCBps.
		Equal(
			expectedSignedTotal,
		) {
		return fmt.Errorf(
			"total signed realized delta=%s, expected=%s",
			outcome.TotalRealizedDeltaPIAUCBps,
			expectedSignedTotal,
		)
	}

	expectedDeterioration :=
		outcome.
			ZeroForOne.
			RealizedDeteriorationBps.
			Add(
				outcome.
					OneForZero.
					RealizedDeteriorationBps,
			)

	if !outcome.
		TotalRealizedDeteriorationBps.
		Equal(
			expectedDeterioration,
		) {
		return fmt.Errorf(
			"total realized deterioration=%s, expected=%s",
			outcome.TotalRealizedDeteriorationBps,
			expectedDeterioration,
		)
	}

	expectedMaximum :=
		burnMaxDecimal(
			outcome.
				ZeroForOne.
				RealizedDeteriorationBps,
			outcome.
				OneForZero.
				RealizedDeteriorationBps,
		)

	if !outcome.
		MaxDirectionalDeteriorationBps.
		Equal(
			expectedMaximum,
		) {
		return fmt.Errorf(
			"maximum directional deterioration=%s, expected=%s",
			outcome.MaxDirectionalDeteriorationBps,
			expectedMaximum,
		)
	}

	return nil
}

func validateBurnRealizedOutcomeReport(
	report BurnRealizedOutcomeReport,
	horizons []BurnOutcomeHorizon,
) error {
	if err := report.Burn.Validate(); err != nil {
		return fmt.Errorf(
			"analyze burn realized outcome: invalid report burn: %w",
			err,
		)
	}

	if len(report.Outcomes)+
		len(report.Skipped) !=
		len(horizons) {
		return fmt.Errorf(
			"analyze burn realized outcome: outcomes+skips=%d, horizons=%d",
			len(report.Outcomes)+
				len(report.Skipped),
			len(horizons),
		)
	}

	for index, outcome := range report.Outcomes {
		if err :=
			validateBurnRealizedHorizonOutcome(
				report.Burn,
				outcome,
			); err != nil {
			return fmt.Errorf(
				"analyze burn realized outcome: outcome %d: %w",
				index,
				err,
			)
		}
	}

	for index, skipped := range report.Skipped {
		if strings.TrimSpace(
			skipped.HorizonLabel,
		) == "" ||
			skipped.HorizonBlocks == 0 ||
			strings.TrimSpace(
				skipped.Detail,
			) == "" {
			return fmt.Errorf(
				"analyze burn realized outcome: invalid skip %d",
				index,
			)
		}

		switch skipped.Reason {
		case BurnOutcomeSkipFutureBlockNotIndexed,
			BurnOutcomeSkipZeroFutureActiveLiquidity:

		default:
			return fmt.Errorf(
				"analyze burn realized outcome: skip %d has unknown reason %q",
				index,
				skipped.Reason,
			)
		}
	}

	return nil
}

func newBurnRealizedOutcomeSkip(
	burn domain.BurnCandidate,
	horizon BurnOutcomeHorizon,
	futureBlock uint64,
	reason BurnOutcomeSkipReason,
	detail string,
) BurnRealizedOutcomeSkip {
	return BurnRealizedOutcomeSkip{
		Burn: cloneBurnCandidate(
			burn,
		),

		HorizonLabel: horizon.Label,

		HorizonBlocks: horizon.Blocks,

		FutureBlock: futureBlock,

		Reason: reason,

		Detail: detail,
	}
}
