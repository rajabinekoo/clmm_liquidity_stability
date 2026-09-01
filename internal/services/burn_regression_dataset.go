package services

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

// BurnRegressionObservation is one regression-ready observation:
//
//	one actual Burn event
//	×
//	one future outcome horizon
//
// It contains:
//
//   - event identity;
//   - pre-event controls;
//   - immediate LSIS;
//   - future realized PIAUC deterioration;
//   - future realized depth deterioration.
//
// It intentionally contains no owner, sender, origin or NFT identity.
type BurnRegressionObservation struct {
	Burn domain.BurnCandidate

	HorizonLabel  string
	HorizonBlocks uint64
	FutureBlock   uint64

	SnapshotBlock uint64

	PriorLiquidityEvents int
	PriorSwapEvents      int
	ReplayedEvents       int

	LastReplayedCursor *domain.EventCursor

	CurrentTick       int
	FutureCurrentTick int

	SqrtPriceX96BeforeBurn *big.Int
	FutureSqrtPriceX96     *big.Int

	RangeLocation   BurnRangeLocation
	BurnRangeActive bool

	RangeWidth int

	DistanceToLowerTick       int
	DistanceToUpperTick       int
	DistanceToNearestBoundary int
	DistanceOutsideRange      int

	NormalizedDistanceOutsideRange decimal.Decimal

	LiquidityRemoved *big.Int

	FlowControls BurnRealizedFlowControls

	RangeLiquidityBeforeBurn *big.Int
	RangeLiquidityAfterBurn  *big.Int

	ActiveLiquidityBeforeBurn *big.Int
	ActiveLiquidityAfterBurn  *big.Int
	ActiveLiquidityRemoved    *big.Int

	FutureActiveLiquidity *big.Int

	MarketControls BurnRealizedMarketControls

	RemovalFraction           decimal.Decimal
	RangeActiveLiquidityShare decimal.Decimal
	ActiveRemovalShare        decimal.Decimal

	RangeLiquidityDensity   decimal.Decimal
	RemovedLiquidityDensity decimal.Decimal

	ImmediateZeroForOne BurnDirectionalImpact
	ImmediateOneForZero BurnDirectionalImpact

	ImmediateTotalLSISBps          decimal.Decimal
	ImmediateMaxDirectionalLSISBps decimal.Decimal

	RealizedZeroForOne BurnRealizedDirectionalOutcome
	RealizedOneForZero BurnRealizedDirectionalOutcome

	TotalRealizedDeltaPIAUCBps decimal.Decimal

	TotalRealizedDeteriorationBps decimal.Decimal

	MaxDirectionalDeteriorationBps decimal.Decimal
}

func (o BurnRegressionObservation) Validate() error {
	sample :=
		BurnEventSample{
			Burn: cloneBurnCandidate(
				o.Burn,
			),

			SnapshotBlock: o.SnapshotBlock,

			PriorLiquidityEvents: o.PriorLiquidityEvents,

			PriorSwapEvents: o.PriorSwapEvents,

			ReplayedEvents: o.ReplayedEvents,

			LastReplayedCursor: cloneEventCursorPointer(
				o.LastReplayedCursor,
			),

			CurrentTick: o.CurrentTick,

			SqrtPriceX96BeforeBurn: cloneRegressionBigInt(
				o.SqrtPriceX96BeforeBurn,
			),

			RangeLocation: o.RangeLocation,

			BurnRangeActive: o.BurnRangeActive,

			RangeWidth: o.RangeWidth,

			DistanceToLowerTick: o.DistanceToLowerTick,

			DistanceToUpperTick: o.DistanceToUpperTick,

			DistanceToNearestBoundary: o.DistanceToNearestBoundary,

			DistanceOutsideRange: o.DistanceOutsideRange,

			NormalizedDistanceOutsideRange: o.NormalizedDistanceOutsideRange,

			LiquidityRemoved: cloneRegressionBigInt(
				o.LiquidityRemoved,
			),

			RangeLiquidityBeforeBurn: cloneRegressionBigInt(
				o.RangeLiquidityBeforeBurn,
			),

			RangeLiquidityAfterBurn: cloneRegressionBigInt(
				o.RangeLiquidityAfterBurn,
			),

			ActiveLiquidityBeforeBurn: cloneRegressionBigInt(
				o.ActiveLiquidityBeforeBurn,
			),

			ActiveLiquidityAfterBurn: cloneRegressionBigInt(
				o.ActiveLiquidityAfterBurn,
			),

			ActiveLiquidityRemoved: cloneRegressionBigInt(
				o.ActiveLiquidityRemoved,
			),

			RemovalFraction: o.RemovalFraction,

			RangeActiveLiquidityShare: o.RangeActiveLiquidityShare,

			ActiveRemovalShare: o.ActiveRemovalShare,

			RangeLiquidityDensity: o.RangeLiquidityDensity,

			RemovedLiquidityDensity: o.RemovedLiquidityDensity,

			ZeroForOne: cloneBurnDirectionalImpact(
				o.ImmediateZeroForOne,
			),

			OneForZero: cloneBurnDirectionalImpact(
				o.ImmediateOneForZero,
			),

			TotalLSISBps: o.ImmediateTotalLSISBps,

			MaxDirectionalLSISBps: o.
				ImmediateMaxDirectionalLSISBps,
		}

	if err := sample.Validate(); err != nil {
		return fmt.Errorf(
			"burn regression observation: invalid immediate sample: %w",
			err,
		)
	}

	outcome :=
		BurnRealizedHorizonOutcome{
			HorizonLabel: o.HorizonLabel,

			HorizonBlocks: o.HorizonBlocks,

			FutureBlock: o.FutureBlock,

			FutureCurrentTick: o.FutureCurrentTick,

			FlowControls: cloneBurnRealizedFlowControls(
				o.FlowControls,
			),

			FutureSqrtPriceX96: cloneRegressionBigInt(
				o.FutureSqrtPriceX96,
			),

			FutureActiveLiquidity: cloneRegressionBigInt(
				o.FutureActiveLiquidity,
			),

			MarketControls: cloneBurnRealizedMarketControls(
				o.MarketControls,
			),

			ZeroForOne: cloneBurnRealizedDirectionalOutcome(
				o.RealizedZeroForOne,
			),

			OneForZero: cloneBurnRealizedDirectionalOutcome(
				o.RealizedOneForZero,
			),

			TotalRealizedDeltaPIAUCBps: o.TotalRealizedDeltaPIAUCBps,

			TotalRealizedDeteriorationBps: o.
				TotalRealizedDeteriorationBps,

			MaxDirectionalDeteriorationBps: o.
				MaxDirectionalDeteriorationBps,
		}

	if err :=
		validateBurnRealizedHorizonOutcome(
			o.Burn,
			outcome,
		); err != nil {
		return fmt.Errorf(
			"burn regression observation: invalid realized outcome: %w",
			err,
		)
	}

	if !o.
		RealizedZeroForOne.
		PreEventAUCBps.
		Equal(
			o.
				ImmediateZeroForOne.
				BaseAUCBps,
		) {
		return fmt.Errorf(
			"burn regression observation: zero_for_one pre-event AUC=%s does not match immediate base AUC=%s",
			o.
				RealizedZeroForOne.
				PreEventAUCBps,
			o.
				ImmediateZeroForOne.
				BaseAUCBps,
		)
	}

	if !o.
		RealizedOneForZero.
		PreEventAUCBps.
		Equal(
			o.
				ImmediateOneForZero.
				BaseAUCBps,
		) {
		return fmt.Errorf(
			"burn regression observation: one_for_zero pre-event AUC=%s does not match immediate base AUC=%s",
			o.
				RealizedOneForZero.
				PreEventAUCBps,
			o.
				ImmediateOneForZero.
				BaseAUCBps,
		)
	}

	if len(
		o.
			ImmediateZeroForOne.
			DepthDeltas,
	) != len(
		o.
			RealizedZeroForOne.
			DepthDeltas,
	) {
		return fmt.Errorf(
			"burn regression observation: zero_for_one immediate depth count=%d, realized depth count=%d",
			len(
				o.
					ImmediateZeroForOne.
					DepthDeltas,
			),
			len(
				o.
					RealizedZeroForOne.
					DepthDeltas,
			),
		)
	}

	if len(
		o.
			ImmediateOneForZero.
			DepthDeltas,
	) != len(
		o.
			RealizedOneForZero.
			DepthDeltas,
	) {
		return fmt.Errorf(
			"burn regression observation: one_for_zero immediate depth count=%d, realized depth count=%d",
			len(
				o.
					ImmediateOneForZero.
					DepthDeltas,
			),
			len(
				o.
					RealizedOneForZero.
					DepthDeltas,
			),
		)
	}

	if err :=
		validateRegressionDepthThresholds(
			o.
				ImmediateZeroForOne.
				DepthDeltas,
			o.
				RealizedZeroForOne.
				DepthDeltas,
		); err != nil {
		return fmt.Errorf(
			"burn regression observation: zero_for_one: %w",
			err,
		)
	}

	if err :=
		validateRegressionDepthThresholds(
			o.
				ImmediateOneForZero.
				DepthDeltas,
			o.
				RealizedOneForZero.
				DepthDeltas,
		); err != nil {
		return fmt.Errorf(
			"burn regression observation: one_for_zero: %w",
			err,
		)
	}

	if o.MarketControls.Available {
		if o.MarketControls.ReferenceTick !=
			o.CurrentTick {
			return fmt.Errorf(
				"burn regression observation: market-control reference tick=%d, current tick=%d",
				o.MarketControls.ReferenceTick,
				o.CurrentTick,
			)
		}

		if o.MarketControls.
			ReferenceSqrtPriceX96 == nil ||
			o.MarketControls.
				ReferenceSqrtPriceX96.
				Cmp(
					o.SqrtPriceX96BeforeBurn,
				) != 0 {
			return fmt.Errorf(
				"burn regression observation: market-control reference sqrt price does not match pre-burn sqrt price",
			)
		}

		if o.MarketControls.
			PostBurnActiveLiquidity == nil ||
			o.MarketControls.
				PostBurnActiveLiquidity.
				Cmp(
					o.ActiveLiquidityAfterBurn,
				) != 0 {
			return fmt.Errorf(
				"burn regression observation: market-control post-burn liquidity does not match observation",
			)
		}
	}

	if o.FlowControls.Available {
		if o.FlowControls.ReferenceTick !=
			o.CurrentTick {
			return fmt.Errorf(
				"burn regression observation: flow-control reference tick=%d, current tick=%d",
				o.FlowControls.ReferenceTick,
				o.CurrentTick,
			)
		}

		if o.FlowControls.
			ReferenceSqrtPriceX96 == nil ||
			o.FlowControls.
				ReferenceSqrtPriceX96.
				Cmp(
					o.SqrtPriceX96BeforeBurn,
				) != 0 {
			return fmt.Errorf(
				"burn regression observation: flow-control reference sqrt price does not match pre-burn sqrt price",
			)
		}

		if !o.FlowControls.
			WindowStartCursor.
			Equal(
				o.Burn.Cursor,
			) {
			return fmt.Errorf(
				"burn regression observation: flow-control start cursor=%s, burn cursor=%s",
				o.FlowControls.WindowStartCursor,
				o.Burn.Cursor,
			)
		}

		if o.FlowControls.WindowEndBlock !=
			o.FutureBlock {
			return fmt.Errorf(
				"burn regression observation: flow-control end block=%d, future block=%d",
				o.FlowControls.WindowEndBlock,
				o.FutureBlock,
			)
		}
	}

	return nil
}

func validateRegressionDepthThresholds(
	immediate []DepthDelta,
	realized []DepthDelta,
) error {
	if len(immediate) !=
		len(realized) {
		return fmt.Errorf(
			"depth counts differ: immediate=%d realized=%d",
			len(immediate),
			len(realized),
		)
	}

	for index := range immediate {
		if !immediate[index].
			ThresholdBps.
			Equal(
				realized[index].
					ThresholdBps,
			) {
			return fmt.Errorf(
				"depth threshold index %d differs: immediate=%s realized=%s",
				index,
				immediate[index].
					ThresholdBps,
				realized[index].
					ThresholdBps,
			)
		}

		if !immediate[index].
			BaseDepthAmount.
			Equal(
				realized[index].
					BaseDepthAmount,
			) {
			return fmt.Errorf(
				"depth base amount index %d differs: immediate=%s realized=%s",
				index,
				immediate[index].
					BaseDepthAmount,
				realized[index].
					BaseDepthAmount,
			)
		}
	}

	return nil
}

type BurnRealizedDatasetRequest struct {
	Collection BurnSampleCollectionReport

	Horizons []BurnOutcomeHorizon

	RequireFlowControls bool

	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int

	ThresholdsBps []decimal.Decimal
}

type BurnRealizedDatasetReport struct {
	PoolAddress string

	FromBlock uint64
	ToBlock   uint64

	BurnIndexedThrough uint64

	OutcomeIndexedThrough uint64

	BurnSamples int

	HorizonsPerSample int

	CandidateHorizonPairs int
	ObservedHorizonPairs  int
	SkippedHorizonPairs   int

	Observations []BurnRegressionObservation
	Skipped      []BurnRealizedOutcomeSkip
}

type BurnRealizedOutcomeAnalyzer interface {
	Analyze(
		ctx context.Context,
		req BurnRealizedOutcomeRequest,
	) (BurnRealizedOutcomeReport, error)
}

type BurnRealizedDatasetService struct {
	analyzer BurnRealizedOutcomeAnalyzer
}

func NewBurnRealizedDatasetService(
	analyzer BurnRealizedOutcomeAnalyzer,
) *BurnRealizedDatasetService {
	return &BurnRealizedDatasetService{
		analyzer: analyzer,
	}
}

func (s *BurnRealizedDatasetService) Build(
	ctx context.Context,
	req BurnRealizedDatasetRequest,
) (BurnRealizedDatasetReport, error) {
	if s == nil {
		return BurnRealizedDatasetReport{}, fmt.Errorf(
			"build burn realized dataset: service is nil",
		)
	}

	if s.analyzer == nil {
		return BurnRealizedDatasetReport{}, fmt.Errorf(
			"build burn realized dataset: analyzer is nil",
		)
	}

	collectionSummary, err :=
		BuildBurnSampleCollectionSummary(
			req.Collection,
		)
	if err != nil {
		return BurnRealizedDatasetReport{}, fmt.Errorf(
			"build burn realized dataset: invalid collection: %w",
			err,
		)
	}

	horizons, err :=
		normalizeBurnDatasetHorizons(
			req.Horizons,
		)
	if err != nil {
		return BurnRealizedDatasetReport{}, err
	}

	zeroForOneAmounts, err :=
		normalizeBurnAmountGrid(
			"dataset zero_for_one",
			req.ZeroForOneAmountsIn,
		)
	if err != nil {
		return BurnRealizedDatasetReport{}, err
	}

	oneForZeroAmounts, err :=
		normalizeBurnAmountGrid(
			"dataset one_for_zero",
			req.OneForZeroAmountsIn,
		)
	if err != nil {
		return BurnRealizedDatasetReport{}, err
	}

	thresholds, err :=
		normalizeBurnThresholds(
			req.ThresholdsBps,
		)
	if err != nil {
		return BurnRealizedDatasetReport{}, err
	}

	report :=
		BurnRealizedDatasetReport{
			PoolAddress: collectionSummary.
				PoolAddress,

			FromBlock: collectionSummary.
				FromBlock,

			ToBlock: collectionSummary.
				ToBlock,

			BurnIndexedThrough: collectionSummary.
				IndexedThrough,

			BurnSamples: len(
				req.
					Collection.
					Samples,
			),

			HorizonsPerSample: len(horizons),

			CandidateHorizonPairs: len(
				req.
					Collection.
					Samples,
			) *
				len(horizons),

			Observations: make(
				[]BurnRegressionObservation,
				0,
				len(
					req.
						Collection.
						Samples,
				)*
					len(horizons),
			),

			Skipped: make(
				[]BurnRealizedOutcomeSkip,
				0,
			),
		}

	var previousBurnCursor *domain.EventCursor

	for sampleIndex, sample := range req.Collection.Samples {
		if err := ctx.Err(); err != nil {
			return BurnRealizedDatasetReport{}, err
		}

		if err := sample.Validate(); err != nil {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: sample %d: %w",
				sampleIndex,
				err,
			)
		}

		if previousBurnCursor != nil &&
			!previousBurnCursor.Before(
				sample.Burn.Cursor,
			) {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: sample ordering is not strictly increasing: previous=%s current=%s",
				previousBurnCursor,
				sample.Burn.Cursor,
			)
		}

		currentCursor :=
			sample.Burn.Cursor

		previousBurnCursor =
			&currentCursor

		sampleZeroForOneAmounts, sampleOneForZeroAmounts, _, _, _, gridErr := burnEventSampleRuntimeAmountGrids(
			sample,
			zeroForOneAmounts,
			oneForZeroAmounts,
		)
		if gridErr != nil {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: sample %s amount grid: %w",
				sample.Burn.EventKey(),
				gridErr,
			)
		}

		outcomeReport, err :=
			s.analyzer.Analyze(
				ctx,
				BurnRealizedOutcomeRequest{
					Sample: sample,

					IndexedThrough: report.
						OutcomeIndexedThrough,

					RequireFlowControls: req.RequireFlowControls,

					Horizons: append(
						[]BurnOutcomeHorizon(nil),
						horizons...,
					),

					ZeroForOneAmountsIn: cloneBurnAmountGrid(
						sampleZeroForOneAmounts,
					),

					OneForZeroAmountsIn: cloneBurnAmountGrid(
						sampleOneForZeroAmounts,
					),

					ThresholdsBps: append(
						[]decimal.Decimal(nil),
						thresholds...,
					),
				},
			)
		if err != nil {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: analyze sample %s: %w",
				sample.Burn.EventKey(),
				err,
			)
		}

		if !sameBurnEvent(
			outcomeReport.Burn,
			sample.Burn,
		) {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: outcome report belongs to a different burn than sample %s",
				sample.Burn.EventKey(),
			)
		}

		if err :=
			validateBurnRealizedOutcomeReport(
				outcomeReport,
				horizons,
			); err != nil {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: invalid outcome report for %s: %w",
				sample.Burn.EventKey(),
				err,
			)
		}

		if report.OutcomeIndexedThrough == 0 {
			report.OutcomeIndexedThrough =
				outcomeReport.IndexedThrough
		} else if report.OutcomeIndexedThrough !=
			outcomeReport.IndexedThrough {
			return BurnRealizedDatasetReport{}, fmt.Errorf(
				"build burn realized dataset: outcome indexed head changed during dataset construction: previous=%d current=%d",
				report.OutcomeIndexedThrough,
				outcomeReport.IndexedThrough,
			)
		}

		for outcomeIndex, outcome := range outcomeReport.Outcomes {
			observation, err :=
				newBurnRegressionObservation(
					sample,
					outcome,
				)
			if err != nil {
				return BurnRealizedDatasetReport{}, fmt.Errorf(
					"build burn realized dataset: sample=%s outcome=%d: %w",
					sample.Burn.EventKey(),
					outcomeIndex,
					err,
				)
			}

			report.Observations =
				append(
					report.Observations,
					observation,
				)
		}

		for skipIndex, skipped := range outcomeReport.Skipped {
			if !sameBurnEvent(
				skipped.Burn,
				sample.Burn,
			) {
				return BurnRealizedDatasetReport{}, fmt.Errorf(
					"build burn realized dataset: sample=%s skip=%d belongs to another burn",
					sample.Burn.EventKey(),
					skipIndex,
				)
			}

			report.Skipped =
				append(
					report.Skipped,
					cloneBurnRealizedOutcomeSkip(
						skipped,
					),
				)
		}
	}

	report.ObservedHorizonPairs =
		len(
			report.Observations,
		)

	report.SkippedHorizonPairs =
		len(
			report.Skipped,
		)

	if err :=
		validateBurnRealizedDatasetReport(
			report,
		); err != nil {
		return BurnRealizedDatasetReport{}, err
	}

	return report, nil
}

func normalizeBurnDatasetHorizons(
	values []BurnOutcomeHorizon,
) ([]BurnOutcomeHorizon, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf(
			"build burn realized dataset: horizons are empty",
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
				"build burn realized dataset: horizon %d has empty label",
				index,
			)
		}

		if horizon.Blocks == 0 {
			return nil, fmt.Errorf(
				"build burn realized dataset: horizon %q has zero blocks",
				horizon.Label,
			)
		}

		if _, exists :=
			seenLabels[horizon.Label]; exists {
			return nil, fmt.Errorf(
				"build burn realized dataset: duplicate horizon label %q",
				horizon.Label,
			)
		}

		if index > 0 &&
			result[index-1].Blocks >=
				horizon.Blocks {
			return nil, fmt.Errorf(
				"build burn realized dataset: horizons must be strictly increasing at index %d",
				index,
			)
		}

		seenLabels[horizon.Label] = struct{}{}

		result[index] =
			horizon
	}

	return result, nil
}

func newBurnRegressionObservation(
	sample BurnEventSample,
	outcome BurnRealizedHorizonOutcome,
) (BurnRegressionObservation, error) {
	if err := sample.Validate(); err != nil {
		return BurnRegressionObservation{}, err
	}

	if err :=
		validateBurnRealizedHorizonOutcome(
			sample.Burn,
			outcome,
		); err != nil {
		return BurnRegressionObservation{}, err
	}

	observation :=
		BurnRegressionObservation{
			Burn: cloneBurnCandidate(
				sample.Burn,
			),

			FlowControls: cloneBurnRealizedFlowControls(
				outcome.FlowControls,
			),

			HorizonLabel: outcome.HorizonLabel,

			HorizonBlocks: outcome.HorizonBlocks,

			FutureBlock: outcome.FutureBlock,

			SnapshotBlock: sample.SnapshotBlock,

			PriorLiquidityEvents: sample.PriorLiquidityEvents,

			PriorSwapEvents: sample.PriorSwapEvents,

			ReplayedEvents: sample.ReplayedEvents,

			LastReplayedCursor: cloneEventCursorPointer(
				sample.
					LastReplayedCursor,
			),

			CurrentTick: sample.CurrentTick,

			FutureCurrentTick: outcome.FutureCurrentTick,

			SqrtPriceX96BeforeBurn: cloneRegressionBigInt(
				sample.
					SqrtPriceX96BeforeBurn,
			),

			FutureSqrtPriceX96: cloneRegressionBigInt(
				outcome.
					FutureSqrtPriceX96,
			),

			RangeLocation: sample.RangeLocation,

			BurnRangeActive: sample.BurnRangeActive,

			RangeWidth: sample.RangeWidth,

			DistanceToLowerTick: sample.DistanceToLowerTick,

			DistanceToUpperTick: sample.DistanceToUpperTick,

			DistanceToNearestBoundary: sample.
				DistanceToNearestBoundary,

			DistanceOutsideRange: sample.DistanceOutsideRange,

			NormalizedDistanceOutsideRange: sample.
				NormalizedDistanceOutsideRange,

			LiquidityRemoved: cloneRegressionBigInt(
				sample.LiquidityRemoved,
			),

			RangeLiquidityBeforeBurn: cloneRegressionBigInt(
				sample.
					RangeLiquidityBeforeBurn,
			),

			RangeLiquidityAfterBurn: cloneRegressionBigInt(
				sample.
					RangeLiquidityAfterBurn,
			),

			ActiveLiquidityBeforeBurn: cloneRegressionBigInt(
				sample.
					ActiveLiquidityBeforeBurn,
			),

			ActiveLiquidityAfterBurn: cloneRegressionBigInt(
				sample.
					ActiveLiquidityAfterBurn,
			),

			ActiveLiquidityRemoved: cloneRegressionBigInt(
				sample.
					ActiveLiquidityRemoved,
			),

			FutureActiveLiquidity: cloneRegressionBigInt(
				outcome.
					FutureActiveLiquidity,
			),

			MarketControls: cloneBurnRealizedMarketControls(
				outcome.MarketControls,
			),

			RemovalFraction: sample.RemovalFraction,

			RangeActiveLiquidityShare: sample.
				RangeActiveLiquidityShare,

			ActiveRemovalShare: sample.ActiveRemovalShare,

			RangeLiquidityDensity: sample.RangeLiquidityDensity,

			RemovedLiquidityDensity: sample.RemovedLiquidityDensity,

			ImmediateZeroForOne: cloneBurnDirectionalImpact(
				sample.ZeroForOne,
			),

			ImmediateOneForZero: cloneBurnDirectionalImpact(
				sample.OneForZero,
			),

			ImmediateTotalLSISBps: sample.TotalLSISBps,

			ImmediateMaxDirectionalLSISBps: sample.
				MaxDirectionalLSISBps,

			RealizedZeroForOne: cloneBurnRealizedDirectionalOutcome(
				outcome.ZeroForOne,
			),

			RealizedOneForZero: cloneBurnRealizedDirectionalOutcome(
				outcome.OneForZero,
			),

			TotalRealizedDeltaPIAUCBps: outcome.
				TotalRealizedDeltaPIAUCBps,

			TotalRealizedDeteriorationBps: outcome.
				TotalRealizedDeteriorationBps,

			MaxDirectionalDeteriorationBps: outcome.
				MaxDirectionalDeteriorationBps,
		}

	if err := observation.Validate(); err != nil {
		return BurnRegressionObservation{}, err
	}

	return observation, nil
}

func validateBurnRealizedDatasetReport(
	report BurnRealizedDatasetReport,
) error {
	if normalizeAddress(
		report.PoolAddress,
	) == "" {
		return fmt.Errorf(
			"build burn realized dataset: report pool address is required",
		)
	}

	if report.FromBlock == 0 ||
		report.ToBlock == 0 ||
		report.FromBlock >
			report.ToBlock {
		return fmt.Errorf(
			"build burn realized dataset: invalid report block range [%d,%d]",
			report.FromBlock,
			report.ToBlock,
		)
	}

	if report.BurnIndexedThrough <
		report.ToBlock {
		return fmt.Errorf(
			"build burn realized dataset: burn index checkpoint %d is before to-block %d",
			report.BurnIndexedThrough,
			report.ToBlock,
		)
	}

	if report.BurnSamples < 0 ||
		report.HorizonsPerSample <= 0 ||
		report.CandidateHorizonPairs < 0 ||
		report.ObservedHorizonPairs < 0 ||
		report.SkippedHorizonPairs < 0 {
		return fmt.Errorf(
			"build burn realized dataset: invalid report counters",
		)
	}

	expectedPairs :=
		report.BurnSamples *
			report.HorizonsPerSample

	if report.CandidateHorizonPairs !=
		expectedPairs {
		return fmt.Errorf(
			"build burn realized dataset: candidate pairs=%d, expected=%d",
			report.CandidateHorizonPairs,
			expectedPairs,
		)
	}

	if report.ObservedHorizonPairs !=
		len(report.Observations) {
		return fmt.Errorf(
			"build burn realized dataset: observed pairs=%d, observations=%d",
			report.ObservedHorizonPairs,
			len(report.Observations),
		)
	}

	if report.SkippedHorizonPairs !=
		len(report.Skipped) {
		return fmt.Errorf(
			"build burn realized dataset: skipped pairs=%d, skips=%d",
			report.SkippedHorizonPairs,
			len(report.Skipped),
		)
	}

	if report.CandidateHorizonPairs !=
		report.ObservedHorizonPairs+
			report.SkippedHorizonPairs {
		return fmt.Errorf(
			"build burn realized dataset: candidate pairs=%d, observed+skipped=%d",
			report.CandidateHorizonPairs,
			report.ObservedHorizonPairs+
				report.SkippedHorizonPairs,
		)
	}

	if report.BurnSamples > 0 &&
		report.OutcomeIndexedThrough == 0 {
		return fmt.Errorf(
			"build burn realized dataset: non-empty dataset has zero outcome indexed head",
		)
	}

	seen :=
		make(
			map[string]struct{},
			report.CandidateHorizonPairs,
		)

	for index, observation := range report.Observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf(
				"build burn realized dataset: observation %d: %w",
				index,
				err,
			)
		}

		if err :=
			validateRegressionBurnScope(
				observation.Burn,
				report,
			); err != nil {
			return fmt.Errorf(
				"build burn realized dataset: observation %d: %w",
				index,
				err,
			)
		}

		key :=
			burnRegressionPairKey(
				observation.Burn,
				observation.HorizonLabel,
			)

		if _, exists := seen[key]; exists {
			return fmt.Errorf(
				"build burn realized dataset: duplicate event-horizon pair %s",
				key,
			)
		}

		seen[key] =
			struct{}{}
	}

	for index, skipped := range report.Skipped {
		if err := skipped.Burn.Validate(); err != nil {
			return fmt.Errorf(
				"build burn realized dataset: skipped pair %d: %w",
				index,
				err,
			)
		}

		if err :=
			validateRegressionBurnScope(
				skipped.Burn,
				report,
			); err != nil {
			return fmt.Errorf(
				"build burn realized dataset: skipped pair %d: %w",
				index,
				err,
			)
		}

		if strings.TrimSpace(
			skipped.HorizonLabel,
		) == "" ||
			skipped.HorizonBlocks == 0 ||
			strings.TrimSpace(
				skipped.Detail,
			) == "" {
			return fmt.Errorf(
				"build burn realized dataset: invalid skipped pair %d",
				index,
			)
		}

		switch skipped.Reason {
		case BurnOutcomeSkipFutureBlockNotIndexed,
			BurnOutcomeSkipZeroFutureActiveLiquidity:

		default:
			return fmt.Errorf(
				"build burn realized dataset: skipped pair %d has unknown reason %q",
				index,
				skipped.Reason,
			)
		}

		key :=
			burnRegressionPairKey(
				skipped.Burn,
				skipped.HorizonLabel,
			)

		if _, exists := seen[key]; exists {
			return fmt.Errorf(
				"build burn realized dataset: duplicate event-horizon pair %s",
				key,
			)
		}

		seen[key] =
			struct{}{}
	}

	if len(seen) !=
		report.CandidateHorizonPairs {
		return fmt.Errorf(
			"build burn realized dataset: unique pairs=%d, expected=%d",
			len(seen),
			report.CandidateHorizonPairs,
		)
	}

	return nil
}

func validateRegressionBurnScope(
	burn domain.BurnCandidate,
	report BurnRealizedDatasetReport,
) error {
	if normalizeAddress(
		burn.PoolAddress,
	) != normalizeAddress(
		report.PoolAddress,
	) {
		return fmt.Errorf(
			"burn pool=%s, expected=%s",
			burn.PoolAddress,
			report.PoolAddress,
		)
	}

	if burn.Cursor.BlockNumber <
		report.FromBlock ||
		burn.Cursor.BlockNumber >
			report.ToBlock {
		return fmt.Errorf(
			"burn cursor %s outside report range [%d,%d]",
			burn.Cursor,
			report.FromBlock,
			report.ToBlock,
		)
	}

	return nil
}

func burnRegressionPairKey(
	burn domain.BurnCandidate,
	horizonLabel string,
) string {
	return burn.EventKey() +
		":" +
		strings.TrimSpace(
			horizonLabel,
		)
}

func cloneBurnRealizedDirectionalOutcome(
	value BurnRealizedDirectionalOutcome,
) BurnRealizedDirectionalOutcome {
	result :=
		value

	result.DepthDeltas =
		append(
			[]DepthDelta(nil),
			value.DepthDeltas...,
		)

	return result
}

func cloneBurnRealizedOutcomeSkip(
	value BurnRealizedOutcomeSkip,
) BurnRealizedOutcomeSkip {
	result :=
		value

	result.Burn =
		cloneBurnCandidate(
			value.Burn,
		)

	return result
}

func cloneRegressionBigInt(
	value *big.Int,
) *big.Int {
	if value == nil {
		return nil
	}

	return new(big.Int).Set(
		value,
	)
}
