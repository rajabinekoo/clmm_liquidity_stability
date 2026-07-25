package services

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
)

const (
	maxBurnCollectionPageSize = 1_000
	maxBurnSwapPageSize       = 1_000
)

type BurnSampleSkipReason string

const (
	BurnSampleSkipZeroActiveLiquidityBefore BurnSampleSkipReason = "zero_active_liquidity_before_burn"

	BurnSampleSkipZeroActiveLiquidityAfter BurnSampleSkipReason = "zero_active_liquidity_after_burn"
)

type BurnSampleSkip struct {
	Burn domain.BurnCandidate

	Reason BurnSampleSkipReason
	Detail string
}

// BurnEventSample is the compact event-level observation used by the
// empirical dataset.
//
// It intentionally excludes:
//
//   - owner;
//   - origin;
//   - sender;
//   - NFT token identity;
//   - full reconstructed tick maps.
type BurnEventSample struct {
	Burn domain.BurnCandidate

	SnapshotBlock uint64

	PriorLiquidityEvents int
	PriorSwapEvents      int
	ReplayedEvents       int

	SwapReplays []BurnSwapReplayAudit

	LastReplayedCursor *domain.EventCursor

	CurrentTick int

	SqrtPriceX96BeforeBurn *big.Int

	RangeLocation   BurnRangeLocation
	BurnRangeActive bool

	RangeWidth int

	DistanceToLowerTick       int
	DistanceToUpperTick       int
	DistanceToNearestBoundary int
	DistanceOutsideRange      int

	NormalizedDistanceOutsideRange decimal.Decimal

	LiquidityRemoved *big.Int

	RangeLiquidityBeforeBurn *big.Int
	RangeLiquidityAfterBurn  *big.Int

	ActiveLiquidityBeforeBurn *big.Int
	ActiveLiquidityAfterBurn  *big.Int
	ActiveLiquidityRemoved    *big.Int

	RemovalFraction           decimal.Decimal
	RangeActiveLiquidityShare decimal.Decimal
	ActiveRemovalShare        decimal.Decimal

	RangeLiquidityDensity   decimal.Decimal
	RemovedLiquidityDensity decimal.Decimal

	ZeroForOne BurnDirectionalImpact
	OneForZero BurnDirectionalImpact

	TotalLSISBps          decimal.Decimal
	MaxDirectionalLSISBps decimal.Decimal
}

func (s BurnEventSample) Validate() error {
	if err := s.Burn.Validate(); err != nil {
		return fmt.Errorf(
			"burn event sample: invalid burn: %w",
			err,
		)
	}

	if s.Burn.Cursor.BlockNumber <= 1 {
		return fmt.Errorf(
			"burn event sample: burn block %d has no valid preceding snapshot",
			s.Burn.Cursor.BlockNumber,
		)
	}

	expectedSnapshotBlock :=
		s.Burn.Cursor.BlockNumber - 1

	if s.SnapshotBlock !=
		expectedSnapshotBlock {
		return fmt.Errorf(
			"burn event sample: snapshot block=%d, expected=%d",
			s.SnapshotBlock,
			expectedSnapshotBlock,
		)
	}

	if s.PriorLiquidityEvents < 0 ||
		s.PriorSwapEvents < 0 ||
		s.ReplayedEvents < 0 {
		return fmt.Errorf(
			"burn event sample: replay counters must not be negative",
		)
	}

	expectedReplayedEvents :=
		s.PriorLiquidityEvents +
			s.PriorSwapEvents

	if s.ReplayedEvents !=
		expectedReplayedEvents {
		return fmt.Errorf(
			"burn event sample: replayed events=%d, expected=%d",
			s.ReplayedEvents,
			expectedReplayedEvents,
		)
	}

	if s.SwapReplays != nil {
		if len(s.SwapReplays) !=
			s.PriorSwapEvents {
			return fmt.Errorf(
				"burn event sample: swap replay audits=%d, prior swaps=%d",
				len(s.SwapReplays),
				s.PriorSwapEvents,
			)
		}

		var previousCursor *domain.EventCursor

		for index, replay := range s.SwapReplays {
			if err := replay.Validate(); err != nil {
				return fmt.Errorf(
					"burn event sample: swap replay audit %d: %w",
					index,
					err,
				)
			}

			if !replay.Cursor.Before(
				s.Burn.Cursor,
			) {
				return fmt.Errorf(
					"burn event sample: swap replay cursor %s is not before burn %s",
					replay.Cursor,
					s.Burn.Cursor,
				)
			}

			if previousCursor != nil &&
				!previousCursor.Before(
					replay.Cursor,
				) {
				return fmt.Errorf(
					"burn event sample: swap replay ordering is not strictly increasing: previous=%s current=%s",
					previousCursor,
					replay.Cursor,
				)
			}

			cursorCopy :=
				replay.Cursor

			previousCursor =
				&cursorCopy
		}
	}

	if s.LastReplayedCursor != nil {
		if err :=
			s.LastReplayedCursor.Validate(); err != nil {
			return fmt.Errorf(
				"burn event sample: invalid last replayed cursor: %w",
				err,
			)
		}

		if !s.LastReplayedCursor.Before(
			s.Burn.Cursor,
		) {
			return fmt.Errorf(
				"burn event sample: last replayed cursor %s is not before burn %s",
				s.LastReplayedCursor,
				s.Burn.Cursor,
			)
		}
	}

	if s.SqrtPriceX96BeforeBurn == nil ||
		s.SqrtPriceX96BeforeBurn.Sign() <= 0 {
		return fmt.Errorf(
			"burn event sample: invalid pre-burn sqrt price",
		)
	}

	if s.RangeWidth !=
		s.Burn.TickUpper-
			s.Burn.TickLower {
		return fmt.Errorf(
			"burn event sample: range width=%d is inconsistent with [%d,%d]",
			s.RangeWidth,
			s.Burn.TickLower,
			s.Burn.TickUpper,
		)
	}

	if s.RangeWidth <= 0 {
		return fmt.Errorf(
			"burn event sample: range width must be positive",
		)
	}

	expectedLocation,
		expectedOutsideDistance :=
		burnRangeLocationAtTick(
			s.CurrentTick,
			s.Burn.TickLower,
			s.Burn.TickUpper,
		)

	if s.RangeLocation !=
		expectedLocation {
		return fmt.Errorf(
			"burn event sample: range location=%q, expected=%q",
			s.RangeLocation,
			expectedLocation,
		)
	}

	expectedActive :=
		expectedLocation ==
			BurnRangeActive

	if s.BurnRangeActive !=
		expectedActive {
		return fmt.Errorf(
			"burn event sample: active-range flag=%t, expected=%t",
			s.BurnRangeActive,
			expectedActive,
		)
	}

	expectedDistanceToLower :=
		burnAbsIntDiff(
			s.CurrentTick,
			s.Burn.TickLower,
		)

	expectedDistanceToUpper :=
		burnAbsIntDiff(
			s.CurrentTick,
			s.Burn.TickUpper,
		)

	expectedNearestDistance :=
		burnMinInt(
			expectedDistanceToLower,
			expectedDistanceToUpper,
		)

	if s.DistanceToLowerTick !=
		expectedDistanceToLower ||
		s.DistanceToUpperTick !=
			expectedDistanceToUpper ||
		s.DistanceToNearestBoundary !=
			expectedNearestDistance ||
		s.DistanceOutsideRange !=
			expectedOutsideDistance {
		return fmt.Errorf(
			"burn event sample: inconsistent range distances",
		)
	}

	expectedNormalizedOutside :=
		burnIntRatio(
			expectedOutsideDistance,
			s.RangeWidth,
		)

	if !s.NormalizedDistanceOutsideRange.Equal(
		expectedNormalizedOutside,
	) {
		return fmt.Errorf(
			"burn event sample: normalized outside distance=%s, expected=%s",
			s.NormalizedDistanceOutsideRange,
			expectedNormalizedOutside,
		)
	}

	positiveValues := []struct {
		name  string
		value *big.Int
	}{
		{
			name: "liquidity removed",

			value: s.LiquidityRemoved,
		},
		{
			name: "range liquidity before burn",

			value: s.RangeLiquidityBeforeBurn,
		},
		{
			name: "active liquidity before burn",

			value: s.ActiveLiquidityBeforeBurn,
		},
	}

	for _, item := range positiveValues {
		if item.value == nil ||
			item.value.Sign() <= 0 {
			return fmt.Errorf(
				"burn event sample: %s must be positive",
				item.name,
			)
		}
	}

	nonNegativeValues := []struct {
		name  string
		value *big.Int
	}{
		{
			name: "range liquidity after burn",

			value: s.RangeLiquidityAfterBurn,
		},
		{
			name: "active liquidity after burn",

			value: s.ActiveLiquidityAfterBurn,
		},
		{
			name: "active liquidity removed",

			value: s.ActiveLiquidityRemoved,
		},
	}

	for _, item := range nonNegativeValues {
		if item.value == nil ||
			item.value.Sign() < 0 {
			return fmt.Errorf(
				"burn event sample: %s must not be negative",
				item.name,
			)
		}
	}

	if s.LiquidityRemoved.Cmp(
		s.Burn.LiquidityRemoved,
	) != 0 {
		return fmt.Errorf(
			"burn event sample: removed liquidity does not match burn event",
		)
	}

	expectedRangeAfter :=
		new(big.Int).Sub(
			new(big.Int).Set(
				s.RangeLiquidityBeforeBurn,
			),
			s.LiquidityRemoved,
		)

	if expectedRangeAfter.Sign() < 0 {
		return fmt.Errorf(
			"burn event sample: removed liquidity exceeds range liquidity",
		)
	}

	if s.RangeLiquidityAfterBurn.Cmp(
		expectedRangeAfter,
	) != 0 {
		return fmt.Errorf(
			"burn event sample: range liquidity after=%s, expected=%s",
			s.RangeLiquidityAfterBurn,
			expectedRangeAfter,
		)
	}

	expectedActiveRemoved :=
		big.NewInt(0)

	if s.BurnRangeActive {
		expectedActiveRemoved =
			new(big.Int).Set(
				s.LiquidityRemoved,
			)
	}

	if s.ActiveLiquidityRemoved.Cmp(
		expectedActiveRemoved,
	) != 0 {
		return fmt.Errorf(
			"burn event sample: active liquidity removed=%s, expected=%s",
			s.ActiveLiquidityRemoved,
			expectedActiveRemoved,
		)
	}

	expectedActiveAfter :=
		new(big.Int).Sub(
			new(big.Int).Set(
				s.ActiveLiquidityBeforeBurn,
			),
			expectedActiveRemoved,
		)

	if expectedActiveAfter.Sign() < 0 {
		return fmt.Errorf(
			"burn event sample: active liquidity after would be negative",
		)
	}

	if s.ActiveLiquidityAfterBurn.Cmp(
		expectedActiveAfter,
	) != 0 {
		return fmt.Errorf(
			"burn event sample: active liquidity after=%s, expected=%s",
			s.ActiveLiquidityAfterBurn,
			expectedActiveAfter,
		)
	}

	expectedRemovalFraction :=
		burnDecimalRatio(
			s.LiquidityRemoved,
			s.RangeLiquidityBeforeBurn,
		)

	if !s.RemovalFraction.Equal(
		expectedRemovalFraction,
	) {
		return fmt.Errorf(
			"burn event sample: removal fraction=%s, expected=%s",
			s.RemovalFraction,
			expectedRemovalFraction,
		)
	}

	expectedRangeActiveShare :=
		decimal.Zero

	expectedActiveRemovalShare :=
		decimal.Zero

	if s.BurnRangeActive {
		expectedRangeActiveShare =
			burnDecimalRatio(
				s.RangeLiquidityBeforeBurn,
				s.ActiveLiquidityBeforeBurn,
			)

		expectedActiveRemovalShare =
			burnDecimalRatio(
				s.LiquidityRemoved,
				s.ActiveLiquidityBeforeBurn,
			)
	}

	if !s.RangeActiveLiquidityShare.Equal(
		expectedRangeActiveShare,
	) {
		return fmt.Errorf(
			"burn event sample: range active-liquidity share=%s, expected=%s",
			s.RangeActiveLiquidityShare,
			expectedRangeActiveShare,
		)
	}

	if !s.ActiveRemovalShare.Equal(
		expectedActiveRemovalShare,
	) {
		return fmt.Errorf(
			"burn event sample: active removal share=%s, expected=%s",
			s.ActiveRemovalShare,
			expectedActiveRemovalShare,
		)
	}

	expectedRangeDensity :=
		burnLiquidityDensity(
			s.RangeLiquidityBeforeBurn,
			s.RangeWidth,
		)

	expectedRemovedDensity :=
		burnLiquidityDensity(
			s.LiquidityRemoved,
			s.RangeWidth,
		)

	if !s.RangeLiquidityDensity.Equal(
		expectedRangeDensity,
	) {
		return fmt.Errorf(
			"burn event sample: range liquidity density=%s, expected=%s",
			s.RangeLiquidityDensity,
			expectedRangeDensity,
		)
	}

	if !s.RemovedLiquidityDensity.Equal(
		expectedRemovedDensity,
	) {
		return fmt.Errorf(
			"burn event sample: removed liquidity density=%s, expected=%s",
			s.RemovedLiquidityDensity,
			expectedRemovedDensity,
		)
	}

	if err :=
		validateBurnDirectionalSample(
			s.ZeroForOne,
			true,
		); err != nil {
		return fmt.Errorf(
			"burn event sample: zero_for_one: %w",
			err,
		)
	}

	if err :=
		validateBurnDirectionalSample(
			s.OneForZero,
			false,
		); err != nil {
		return fmt.Errorf(
			"burn event sample: one_for_zero: %w",
			err,
		)
	}

	expectedTotalLSIS :=
		s.ZeroForOne.
			LSISBps.
			Add(
				s.OneForZero.
					LSISBps,
			)

	if !s.TotalLSISBps.Equal(
		expectedTotalLSIS,
	) {
		return fmt.Errorf(
			"burn event sample: total LSIS=%s, expected=%s",
			s.TotalLSISBps,
			expectedTotalLSIS,
		)
	}

	expectedMaximumLSIS :=
		burnMaxDecimal(
			s.ZeroForOne.LSISBps,
			s.OneForZero.LSISBps,
		)

	if !s.MaxDirectionalLSISBps.Equal(
		expectedMaximumLSIS,
	) {
		return fmt.Errorf(
			"burn event sample: maximum directional LSIS=%s, expected=%s",
			s.MaxDirectionalLSISBps,
			expectedMaximumLSIS,
		)
	}

	return nil
}

func validateBurnDirectionalSample(
	impact BurnDirectionalImpact,
	expectedDirection bool,
) error {
	if impact.ZeroForOne !=
		expectedDirection {
		return fmt.Errorf(
			"direction=%t, expected=%t",
			impact.ZeroForOne,
			expectedDirection,
		)
	}

	if impact.BaseAUCBps.IsNegative() ||
		impact.PostBurnAUCBps.IsNegative() ||
		impact.LSISBps.IsNegative() {
		return fmt.Errorf(
			"AUC and LSIS values must not be negative",
		)
	}

	expectedDelta :=
		impact.
			PostBurnAUCBps.
			Sub(
				impact.BaseAUCBps,
			)

	if !impact.DeltaAUCBps.Equal(
		expectedDelta,
	) {
		return fmt.Errorf(
			"delta AUC=%s, expected=%s",
			impact.DeltaAUCBps,
			expectedDelta,
		)
	}

	expectedLSIS :=
		positiveOnly(
			expectedDelta,
		)

	if !impact.LSISBps.Equal(
		expectedLSIS,
	) {
		return fmt.Errorf(
			"LSIS=%s, expected=%s",
			impact.LSISBps,
			expectedLSIS,
		)
	}

	for index, depth := range impact.DepthDeltas {
		if depth.ThresholdBps.
			LessThanOrEqual(
				decimal.Zero,
			) {
			return fmt.Errorf(
				"depth %d has non-positive threshold %s",
				index,
				depth.ThresholdBps,
			)
		}

		if depth.BaseDepthAmount.IsNegative() ||
			depth.CounterfactualDepthAmount.IsNegative() {
			return fmt.Errorf(
				"depth %d has negative executable depth",
				index,
			)
		}

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
				"depth %d delta=%s, expected=%s",
				index,
				depth.DeltaDepthAmount,
				expectedDepthDelta,
			)
		}
	}

	return nil
}

type BurnSampleCollectionRequest struct {
	PoolAddress string

	FromBlock uint64
	ToBlock   uint64

	PageSize     int
	SwapPageSize int

	MaxCandidates int
	MaxSamples    int

	// SamplingBins enables deterministic block-stratified sampling.
	//
	// A zero value preserves chronological candidate ordering.
	// Production studies should use a positive value.
	SamplingBins int

	// SamplingSeed makes the candidate selection reproducible.
	SamplingSeed uint64

	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int

	ThresholdsBps []decimal.Decimal
}

type BurnSampleCollectionReport struct {
	PoolAddress string

	FromBlock uint64
	ToBlock   uint64

	IndexedThrough uint64

	Pages int

	// DiscoveredCandidates is the complete candidate universe found in the
	// requested block interval before expensive pre-burn reconstruction.
	DiscoveredCandidates int

	// CandidateEvents is the number of candidates actually attempted after
	// deterministic sampling.
	//
	// CandidateEvents = AnalyzedEvents + SkippedEvents.
	CandidateEvents int
	AnalyzedEvents  int
	SkippedEvents   int

	SamplingBins         int
	NonEmptySamplingBins int

	// Truncated means the successful sample target was reached before every
	// discovered candidate had to be attempted.
	//
	// Candidate discovery itself is never silently truncated.
	Truncated bool

	LastProcessedCursor *domain.EventCursor

	Samples []BurnEventSample
	Skipped []BurnSampleSkip
}

type BurnCandidatePageLoader interface {
	LoadBurnCandidatesPage(
		ctx context.Context,
		req repositories.BurnCandidatePageRequest,
	) (repositories.BurnCandidatePage, error)
}

type BurnPreStateBuilder interface {
	Build(
		ctx context.Context,
		burn domain.BurnCandidate,
		pageSize int,
	) (PreBurnStateResult, error)
}

type BurnImpactAnalyzer interface {
	Analyze(
		ctx context.Context,
		req BurnEventImpactRequest,
	) (BurnEventImpactResult, error)
}

type BurnSampleCollector struct {
	candidateRepository BurnCandidatePageLoader
	preBurnBuilder      BurnPreStateBuilder
	impactAnalyzer      BurnImpactAnalyzer
}

func NewBurnSampleCollector(
	candidateRepository BurnCandidatePageLoader,
	preBurnBuilder BurnPreStateBuilder,
	impactAnalyzer BurnImpactAnalyzer,
) *BurnSampleCollector {
	return &BurnSampleCollector{
		candidateRepository: candidateRepository,

		preBurnBuilder: preBurnBuilder,

		impactAnalyzer: impactAnalyzer,
	}
}

func (s *BurnSampleCollector) Collect(
	ctx context.Context,
	req BurnSampleCollectionRequest,
) (BurnSampleCollectionReport, error) {
	if s == nil {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: collector is nil",
		)
	}

	if s.candidateRepository == nil {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: candidate repository is nil",
		)
	}

	if s.preBurnBuilder == nil {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: pre-burn builder is nil",
		)
	}

	if s.impactAnalyzer == nil {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: impact analyzer is nil",
		)
	}

	normalizedRequest, err :=
		normalizeBurnSampleCollectionRequest(
			req,
		)
	if err != nil {
		return BurnSampleCollectionReport{}, err
	}

	// Phase 1: load the complete cheap candidate universe.
	discovery, err :=
		s.discoverBurnCandidates(
			ctx,
			normalizedRequest,
		)
	if err != nil {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: %w",
			err,
		)
	}

	// Phase 2: generate a deterministic time-stratified order.
	candidateOrder,
		samplingDiagnostics,
		err :=
		buildBurnCandidateSamplingOrder(
			discovery.Candidates,
			normalizedRequest.FromBlock,
			normalizedRequest.ToBlock,
			normalizedRequest.SamplingBins,
			normalizedRequest.SamplingSeed,
		)
	if err != nil {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: build sampling order: %w",
			err,
		)
	}

	report :=
		BurnSampleCollectionReport{
			PoolAddress: normalizedRequest.
				PoolAddress,

			FromBlock: normalizedRequest.
				FromBlock,

			ToBlock: normalizedRequest.
				ToBlock,

			IndexedThrough: discovery.
				IndexedThrough,

			Pages: discovery.Pages,

			DiscoveredCandidates: len(
				discovery.Candidates,
			),

			SamplingBins: samplingDiagnostics.
				BinCount,

			NonEmptySamplingBins: samplingDiagnostics.
				NonEmptyBinCount,

			Samples: make(
				[]BurnEventSample,
				0,
				normalizedRequest.
					MaxSamples,
			),

			Skipped: make(
				[]BurnSampleSkip,
				0,
			),
		}

	// Phase 3: perform expensive pre-burn reconstruction only for candidates
	// selected by the stratified order.
	//
	// Deterministic skips do not consume the successful sample quota.
	for _, candidate := range candidateOrder {
		if err := ctx.Err(); err != nil {
			return BurnSampleCollectionReport{}, err
		}

		if report.AnalyzedEvents >=
			normalizedRequest.MaxSamples {
			break
		}

		report.CandidateEvents++

		report.LastProcessedCursor =
			laterBurnCursor(
				report.LastProcessedCursor,
				candidate.Cursor,
			)

		sample, skipped, err :=
			s.analyzeBurnCandidate(
				ctx,
				normalizedRequest,
				candidate,
			)
		if err != nil {
			return BurnSampleCollectionReport{}, fmt.Errorf(
				"collect burn samples: candidate %s: %w",
				candidate.EventKey(),
				err,
			)
		}

		if skipped != nil {
			report.Skipped =
				append(
					report.Skipped,
					*skipped,
				)

			report.SkippedEvents++

			continue
		}

		if sample == nil {
			return BurnSampleCollectionReport{}, fmt.Errorf(
				"collect burn samples: candidate %s produced neither sample nor exclusion",
				candidate.EventKey(),
			)
		}

		report.Samples =
			append(
				report.Samples,
				*sample,
			)

		report.AnalyzedEvents++
	}

	report.Truncated =
		report.CandidateEvents <
			report.DiscoveredCandidates

	// Sampling order is intentionally not chronological.
	// CSV observations are restored to blockchain order before export.
	sortBurnCollectionObservations(
		&report,
	)

	return finalizeBurnCollectionReport(
		report,
	)
}

func (s *BurnSampleCollector) analyzeBurnCandidate(
	ctx context.Context,
	req BurnSampleCollectionRequest,
	candidate domain.BurnCandidate,
) (
	*BurnEventSample,
	*BurnSampleSkip,
	error,
) {
	preBurn, err :=
		s.preBurnBuilder.Build(
			ctx,
			candidate,
			req.SwapPageSize,
		)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"build pre-burn state: %w",
			err,
		)
	}

	if err := validateCollectorPreBurnState(
		preBurn,
		candidate,
	); err != nil {
		return nil, nil, fmt.Errorf(
			"invalid pre-burn state: %w",
			err,
		)
	}

	if preBurn.Pool.Liquidity.Sign() == 0 {
		skipped :=
			newBurnSampleSkip(
				candidate,
				BurnSampleSkipZeroActiveLiquidityBefore,
				"price-impact curves are undefined because active liquidity before the burn is zero",
			)

		return nil, &skipped, nil
	}

	burnIsActive :=
		candidate.TickLower <=
			preBurn.Pool.CurrentTick &&
			preBurn.Pool.CurrentTick <
				candidate.TickUpper

	if burnIsActive &&
		preBurn.Pool.Liquidity.Cmp(
			candidate.LiquidityRemoved,
		) == 0 {
		skipped :=
			newBurnSampleSkip(
				candidate,
				BurnSampleSkipZeroActiveLiquidityAfter,
				"the burn removes all active liquidity, so the fixed-grid post-burn price-impact curve is undefined",
			)

		return nil, &skipped, nil
	}

	impact, err :=
		s.impactAnalyzer.Analyze(
			ctx,
			BurnEventImpactRequest{
				PreBurn: preBurn,

				ZeroForOneAmountsIn: cloneBurnAmountGrid(
					req.ZeroForOneAmountsIn,
				),

				OneForZeroAmountsIn: cloneBurnAmountGrid(
					req.OneForZeroAmountsIn,
				),

				ThresholdsBps: append(
					[]decimal.Decimal(nil),
					req.ThresholdsBps...,
				),
			},
		)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"analyze burn impact: %w",
			err,
		)
	}

	sample, err :=
		newBurnEventSample(
			preBurn,
			impact,
		)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"build sample: %w",
			err,
		)
	}

	return &sample, nil, nil
}

func normalizeBurnSampleCollectionRequest(
	req BurnSampleCollectionRequest,
) (BurnSampleCollectionRequest, error) {
	req.PoolAddress =
		strings.ToLower(
			strings.TrimSpace(
				req.PoolAddress,
			),
		)

	if req.PoolAddress == "" {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: pool address is required",
		)
	}

	if req.FromBlock == 0 ||
		req.ToBlock == 0 ||
		req.FromBlock >
			req.ToBlock {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: invalid block range [%d,%d]",
			req.FromBlock,
			req.ToBlock,
		)
	}

	if req.PageSize <= 0 ||
		req.PageSize >
			maxBurnCollectionPageSize {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: page size %d must be inside [1,%d]",
			req.PageSize,
			maxBurnCollectionPageSize,
		)
	}

	if req.SwapPageSize <= 0 ||
		req.SwapPageSize >
			maxBurnSwapPageSize {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: swap page size %d must be inside [1,%d]",
			req.SwapPageSize,
			maxBurnSwapPageSize,
		)
	}

	if req.MaxCandidates <= 0 {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: max candidates must be positive",
		)
	}

	if req.MaxSamples <= 0 {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: max samples must be positive",
		)
	}

	if req.MaxSamples >
		req.MaxCandidates {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: max samples %d exceeds max candidates %d",
			req.MaxSamples,
			req.MaxCandidates,
		)
	}

	if req.SamplingBins < 0 {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: sampling bins %d must not be negative",
			req.SamplingBins,
		)
	}

	if req.SamplingBins > req.MaxSamples {
		return BurnSampleCollectionRequest{}, fmt.Errorf(
			"collect burn samples: sampling bins %d exceed max samples %d",
			req.SamplingBins,
			req.MaxSamples,
		)
	}

	zeroForOneAmounts, err :=
		normalizeBurnAmountGrid(
			"zero_for_one",
			req.ZeroForOneAmountsIn,
		)
	if err != nil {
		return BurnSampleCollectionRequest{}, err
	}

	oneForZeroAmounts, err :=
		normalizeBurnAmountGrid(
			"one_for_zero",
			req.OneForZeroAmountsIn,
		)
	if err != nil {
		return BurnSampleCollectionRequest{}, err
	}

	thresholds, err :=
		normalizeBurnThresholds(
			req.ThresholdsBps,
		)
	if err != nil {
		return BurnSampleCollectionRequest{}, err
	}

	req.ZeroForOneAmountsIn =
		zeroForOneAmounts

	req.OneForZeroAmountsIn =
		oneForZeroAmounts

	req.ThresholdsBps =
		thresholds

	return req, nil
}

func normalizeBurnAmountGrid(
	name string,
	values []*big.Int,
) ([]*big.Int, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf(
			"collect burn samples: %s amount grid is empty",
			name,
		)
	}

	result :=
		make(
			[]*big.Int,
			len(values),
		)

	for index, value := range values {
		if value == nil ||
			value.Sign() <= 0 {
			return nil, fmt.Errorf(
				"collect burn samples: %s amount grid index %d must be positive",
				name,
				index,
			)
		}

		if value.BitLen() > 256 {
			return nil, fmt.Errorf(
				"collect burn samples: %s amount grid index %d exceeds uint256",
				name,
				index,
			)
		}

		if index > 0 &&
			values[index-1].Cmp(
				value,
			) >= 0 {
			return nil, fmt.Errorf(
				"collect burn samples: %s amount grid must be strictly increasing at index %d",
				name,
				index,
			)
		}

		result[index] =
			new(big.Int).Set(
				value,
			)
	}

	return result, nil
}

func normalizeBurnThresholds(
	values []decimal.Decimal,
) ([]decimal.Decimal, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf(
			"collect burn samples: thresholds are empty",
		)
	}

	result :=
		append(
			[]decimal.Decimal(nil),
			values...,
		)

	for index, value := range result {
		if value.LessThanOrEqual(
			decimal.Zero,
		) {
			return nil, fmt.Errorf(
				"collect burn samples: threshold index %d must be positive",
				index,
			)
		}

		if index > 0 &&
			!result[index-1].
				LessThan(
					value,
				) {
			return nil, fmt.Errorf(
				"collect burn samples: thresholds must be strictly increasing at index %d",
				index,
			)
		}
	}

	return result, nil
}

func validateBurnCandidatePage(
	page repositories.BurnCandidatePage,
	req BurnSampleCollectionRequest,
) error {
	if normalizeAddress(
		page.PoolAddress,
	) != req.PoolAddress {
		return fmt.Errorf(
			"page pool=%s, expected=%s",
			page.PoolAddress,
			req.PoolAddress,
		)
	}

	if page.FromBlock !=
		req.FromBlock ||
		page.ToBlock !=
			req.ToBlock {
		return fmt.Errorf(
			"page range=[%d,%d], expected=[%d,%d]",
			page.FromBlock,
			page.ToBlock,
			req.FromBlock,
			req.ToBlock,
		)
	}

	if page.IndexedThrough <
		req.ToBlock {
		return fmt.Errorf(
			"page indexed through %d before requested to-block %d",
			page.IndexedThrough,
			req.ToBlock,
		)
	}

	return nil
}

func validateCollectedBurnCandidate(
	candidate domain.BurnCandidate,
	req BurnSampleCollectionRequest,
	previous *domain.EventCursor,
) error {
	if err := candidate.Validate(); err != nil {
		return err
	}

	if normalizeAddress(
		candidate.PoolAddress,
	) != req.PoolAddress {
		return fmt.Errorf(
			"candidate pool=%s, expected=%s",
			candidate.PoolAddress,
			req.PoolAddress,
		)
	}

	if candidate.Cursor.BlockNumber <
		req.FromBlock ||
		candidate.Cursor.BlockNumber >
			req.ToBlock {
		return fmt.Errorf(
			"candidate cursor %s outside block range [%d,%d]",
			candidate.Cursor,
			req.FromBlock,
			req.ToBlock,
		)
	}

	if previous != nil &&
		!previous.Before(
			candidate.Cursor,
		) {
		return fmt.Errorf(
			"candidate ordering is not strictly increasing: previous=%s current=%s",
			previous,
			candidate.Cursor,
		)
	}

	return nil
}

func validateBurnCandidatePageOrdering(
	candidates []domain.BurnCandidate,
	req BurnSampleCollectionRequest,
	previousPageTail *domain.EventCursor,
) error {
	var previous *domain.EventCursor

	if previousPageTail != nil {
		previous =
			&domain.EventCursor{
				BlockNumber: previousPageTail.BlockNumber,

				LogIndex: previousPageTail.LogIndex,
			}
	}

	for index, candidate := range candidates {
		if err :=
			validateCollectedBurnCandidate(
				candidate,
				req,
				previous,
			); err != nil {
			return fmt.Errorf(
				"candidate %d: %w",
				index,
				err,
			)
		}

		cursorCopy :=
			candidate.Cursor

		previous =
			&cursorCopy
	}

	return nil
}

func validateCollectorPreBurnState(
	preBurn PreBurnStateResult,
	candidate domain.BurnCandidate,
) error {
	if preBurn.Pool == nil {
		return fmt.Errorf(
			"pre-burn pool is nil",
		)
	}

	if !sameBurnEvent(
		preBurn.Burn,
		candidate,
	) {
		return fmt.Errorf(
			"pre-burn result belongs to a different burn event",
		)
	}

	if normalizeAddress(
		preBurn.Pool.PoolAddress,
	) != normalizeAddress(
		candidate.PoolAddress,
	) {
		return fmt.Errorf(
			"pre-burn pool address does not match candidate",
		)
	}

	if preBurn.Pool.BlockNumber !=
		candidate.Cursor.BlockNumber {
		return fmt.Errorf(
			"pre-burn pool block=%d, expected=%d",
			preBurn.Pool.BlockNumber,
			candidate.Cursor.BlockNumber,
		)
	}

	if preBurn.Pool.SqrtPriceX96 == nil ||
		preBurn.Pool.SqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"invalid pre-burn sqrt price",
		)
	}

	if preBurn.Pool.Liquidity == nil ||
		preBurn.Pool.Liquidity.Sign() < 0 {
		return fmt.Errorf(
			"invalid pre-burn active liquidity",
		)
	}

	if preBurn.ActiveLiquidityBeforeBurn == nil ||
		preBurn.ActiveLiquidityBeforeBurn.Cmp(
			preBurn.Pool.Liquidity,
		) != 0 {
		return fmt.Errorf(
			"stored pre-burn active liquidity is inconsistent",
		)
	}

	return nil
}

func shouldStopBurnCollection(
	report *BurnSampleCollectionReport,
	page repositories.BurnCandidatePage,
	candidateIndex int,
	req BurnSampleCollectionRequest,
) bool {
	reachedSampleLimit :=
		report.AnalyzedEvents >=
			req.MaxSamples

	reachedCandidateLimit :=
		report.CandidateEvents >=
			req.MaxCandidates

	if !reachedSampleLimit &&
		!reachedCandidateLimit {
		return false
	}

	hasUnprocessedPageCandidates :=
		candidateIndex+1 <
			len(page.Candidates)

	hasAnotherPage :=
		page.NextCursor != nil

	report.Truncated =
		hasUnprocessedPageCandidates ||
			hasAnotherPage

	return true
}

func finalizeBurnCollectionReport(
	report BurnSampleCollectionReport,
) (BurnSampleCollectionReport, error) {
	report.AnalyzedEvents =
		len(report.Samples)

	report.SkippedEvents =
		len(report.Skipped)

	if report.CandidateEvents !=
		report.AnalyzedEvents+
			report.SkippedEvents {
		return BurnSampleCollectionReport{}, fmt.Errorf(
			"collect burn samples: candidate count=%d, analyzed+skipped=%d",
			report.CandidateEvents,
			report.AnalyzedEvents+
				report.SkippedEvents,
		)
	}

	for index, sample := range report.Samples {
		if err := sample.Validate(); err != nil {
			return BurnSampleCollectionReport{}, fmt.Errorf(
				"collect burn samples: sample %d: %w",
				index,
				err,
			)
		}
	}

	return report, nil
}

func newBurnEventSample(
	preBurn PreBurnStateResult,
	impact BurnEventImpactResult,
) (BurnEventSample, error) {
	if preBurn.Pool == nil {
		return BurnEventSample{}, fmt.Errorf(
			"pre-burn pool is nil",
		)
	}

	if !sameBurnEvent(
		preBurn.Burn,
		impact.Burn,
	) {
		return BurnEventSample{}, fmt.Errorf(
			"pre-burn state and impact refer to different burn events",
		)
	}

	if impact.PreBurnPool == nil ||
		impact.PostBurnPool == nil {
		return BurnEventSample{}, fmt.Errorf(
			"impact pool state is incomplete",
		)
	}

	sample :=
		BurnEventSample{
			Burn: cloneBurnCandidate(
				impact.Burn,
			),

			SnapshotBlock: preBurn.SnapshotBlock,

			PriorLiquidityEvents: preBurn.PriorLiquidityEvents,

			PriorSwapEvents: preBurn.PriorSwapEvents,

			ReplayedEvents: preBurn.ReplayedEvents,

			SwapReplays: cloneBurnSwapReplayAudits(
				preBurn.SwapReplays,
			),

			LastReplayedCursor: cloneEventCursorPointer(
				preBurn.
					LastReplayedCursor,
			),

			CurrentTick: impact.CurrentTick,

			SqrtPriceX96BeforeBurn: new(big.Int).Set(
				impact.
					PreBurnPool.
					SqrtPriceX96,
			),

			RangeLocation: impact.RangeLocation,

			BurnRangeActive: impact.BurnRangeActive,

			RangeWidth: impact.RangeWidth,

			DistanceToLowerTick: impact.DistanceToLowerTick,

			DistanceToUpperTick: impact.DistanceToUpperTick,

			DistanceToNearestBoundary: impact.
				DistanceToNearestBoundary,

			DistanceOutsideRange: impact.DistanceOutsideRange,

			NormalizedDistanceOutsideRange: impact.
				NormalizedDistanceOutsideRange,

			LiquidityRemoved: new(big.Int).Set(
				impact.
					LiquidityRemoved,
			),

			RangeLiquidityBeforeBurn: new(big.Int).Set(
				impact.
					RangeLiquidityBeforeBurn,
			),

			RangeLiquidityAfterBurn: new(big.Int).Set(
				impact.
					RangeLiquidityAfterBurn,
			),

			ActiveLiquidityBeforeBurn: new(big.Int).Set(
				impact.
					ActiveLiquidityBeforeBurn,
			),

			ActiveLiquidityAfterBurn: new(big.Int).Set(
				impact.
					ActiveLiquidityAfterBurn,
			),

			ActiveLiquidityRemoved: new(big.Int).Set(
				impact.
					ActiveLiquidityRemoved,
			),

			RemovalFraction: impact.RemovalFraction,

			RangeActiveLiquidityShare: impact.
				RangeActiveLiquidityShare,

			ActiveRemovalShare: impact.ActiveRemovalShare,

			RangeLiquidityDensity: impact.RangeLiquidityDensity,

			RemovedLiquidityDensity: impact.RemovedLiquidityDensity,

			ZeroForOne: cloneBurnDirectionalImpact(
				impact.ZeroForOne,
			),

			OneForZero: cloneBurnDirectionalImpact(
				impact.OneForZero,
			),

			TotalLSISBps: impact.TotalLSISBps,

			MaxDirectionalLSISBps: impact.
				MaxDirectionalLSISBps,
		}

	if err := sample.Validate(); err != nil {
		return BurnEventSample{}, err
	}

	return sample, nil
}

func newBurnSampleSkip(
	burn domain.BurnCandidate,
	reason BurnSampleSkipReason,
	detail string,
) BurnSampleSkip {
	return BurnSampleSkip{
		Burn: cloneBurnCandidate(
			burn,
		),

		Reason: reason,

		Detail: detail,
	}
}

func cloneBurnCandidate(
	burn domain.BurnCandidate,
) domain.BurnCandidate {
	cloned :=
		burn

	cloned.PoolAddress =
		normalizeAddress(
			burn.PoolAddress,
		)

	cloned.TxHash =
		strings.ToLower(
			strings.TrimSpace(
				burn.TxHash,
			),
		)

	cloned.LiquidityRemoved =
		burn.LiquidityRemovedCopy()

	return cloned
}

func cloneBurnDirectionalImpact(
	impact BurnDirectionalImpact,
) BurnDirectionalImpact {
	cloned :=
		impact

	cloned.DepthDeltas =
		append(
			[]DepthDelta(nil),
			impact.DepthDeltas...,
		)

	return cloned
}

func cloneEventCursorPointer(
	cursor *domain.EventCursor,
) *domain.EventCursor {
	if cursor == nil {
		return nil
	}

	cloned :=
		*cursor

	return &cloned
}

func cloneBurnAmountGrid(
	values []*big.Int,
) []*big.Int {
	result :=
		make(
			[]*big.Int,
			len(values),
		)

	for index, value := range values {
		result[index] =
			new(big.Int).Set(
				value,
			)
	}

	return result
}

func sameBurnEvent(
	left domain.BurnCandidate,
	right domain.BurnCandidate,
) bool {
	if left.EventKey() !=
		right.EventKey() {
		return false
	}

	if !strings.EqualFold(
		strings.TrimSpace(
			left.TxHash,
		),
		strings.TrimSpace(
			right.TxHash,
		),
	) {
		return false
	}

	if left.ID !=
		right.ID ||
		left.TickLower !=
			right.TickLower ||
		left.TickUpper !=
			right.TickUpper {
		return false
	}

	if !left.Timestamp.Equal(
		right.Timestamp,
	) {
		return false
	}

	if left.LiquidityRemoved == nil ||
		right.LiquidityRemoved == nil {
		return false
	}

	return left.LiquidityRemoved.Cmp(
		right.LiquidityRemoved,
	) == 0
}
