package services

import (
	"context"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type BurnRangeLocation string

const (
	BurnRangeBelowCurrentTick BurnRangeLocation = "below_current_tick"

	BurnRangeActive BurnRangeLocation = "active"

	BurnRangeAboveCurrentTick BurnRangeLocation = "above_current_tick"
)

type BurnEventImpactRepository interface {
	LoadRangeLiquidityBeforeCursor(
		ctx context.Context,
		poolAddress string,
		tickLower int,
		tickUpper int,
		cursor domain.EventCursor,
	) (*big.Int, error)
}

type BurnEventImpactRequest struct {
	PreBurn PreBurnStateResult

	AmountGridStateID string
	AmountGridMode    string
	TargetImpactsBps  []decimal.Decimal

	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int

	ThresholdsBps []decimal.Decimal
}

type BurnDirectionalImpact struct {
	ZeroForOne bool

	BaseAUCBps     decimal.Decimal
	PostBurnAUCBps decimal.Decimal

	DeltaAUCBps decimal.Decimal
	LSISBps     decimal.Decimal

	DepthDeltas []DepthDelta
}

type BurnEventImpactResult struct {
	Burn domain.BurnCandidate

	AmountGridStateID string
	AmountGridMode    string
	TargetImpactsBps  []decimal.Decimal

	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int

	PreBurnPool  *domain.ReconstructedPool
	PostBurnPool *domain.ReconstructedPool

	CurrentTick int

	RangeLocation   BurnRangeLocation
	BurnRangeActive bool

	RangeWidth int

	DistanceToLowerTick       int
	DistanceToUpperTick       int
	DistanceToNearestBoundary int
	DistanceOutsideRange      int

	NormalizedDistanceOutsideRange decimal.Decimal

	LiquidityRemoved *big.Int

	// Aggregate liquidity on the exact [tickLower, tickUpper] pair. This does
	// not represent an NFT position and does not use owner.
	RangeLiquidityBeforeBurn *big.Int
	RangeLiquidityAfterBurn  *big.Int

	ActiveLiquidityBeforeBurn *big.Int
	ActiveLiquidityAfterBurn  *big.Int
	ActiveLiquidityRemoved    *big.Int

	// LiquidityRemoved / RangeLiquidityBeforeBurn.
	RemovalFraction decimal.Decimal

	// RangeLiquidityBeforeBurn / ActiveLiquidityBeforeBurn when the range is
	// active; otherwise zero.
	RangeActiveLiquidityShare decimal.Decimal

	// LiquidityRemoved / ActiveLiquidityBeforeBurn when the range is active;
	// otherwise zero.
	ActiveRemovalShare decimal.Decimal

	RangeLiquidityDensity   decimal.Decimal
	RemovedLiquidityDensity decimal.Decimal

	ZeroForOne BurnDirectionalImpact
	OneForZero BurnDirectionalImpact

	TotalLSISBps          decimal.Decimal
	MaxDirectionalLSISBps decimal.Decimal
}

type BurnEventImpactService struct {
	repository   BurnEventImpactRepository
	curveService *PriceImpactCurveService
}

func NewBurnEventImpactService(
	repository BurnEventImpactRepository,
	curveService *PriceImpactCurveService,
) *BurnEventImpactService {
	return &BurnEventImpactService{
		repository: repository,

		curveService: curveService,
	}
}

// Analyze calculates the immediate execution-quality deterioration caused by
// removing the exact liquidity amount of one actual Pool Burn event.
//
// No owner, sender, origin or NFT identity participates in the calculation.
func (s *BurnEventImpactService) Analyze(
	ctx context.Context,
	req BurnEventImpactRequest,
) (BurnEventImpactResult, error) {
	if s == nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: service is nil",
		)
	}

	if s.repository == nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: repository is nil",
		)
	}

	if s.curveService == nil ||
		s.curveService.simulator == nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: curve service is nil",
		)
	}

	if err := validateBurnEventImpactRequest(
		req,
	); err != nil {
		return BurnEventImpactResult{}, err
	}

	preBurn :=
		req.PreBurn

	burn :=
		preBurn.Burn

	preBurnPool :=
		preBurn.Pool

	rangeLiquidityBefore, err :=
		s.repository.
			LoadRangeLiquidityBeforeCursor(
				ctx,
				burn.PoolAddress,
				burn.TickLower,
				burn.TickUpper,
				burn.Cursor,
			)
	if err != nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: load exact range liquidity before burn: %w",
			err,
		)
	}

	if rangeLiquidityBefore.Sign() <= 0 {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: exact range [%d,%d] has no positive liquidity before burn %s",
			burn.TickLower,
			burn.TickUpper,
			burn.EventKey(),
		)
	}

	if rangeLiquidityBefore.Cmp(
		burn.LiquidityRemoved,
	) < 0 {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: removed liquidity %s exceeds exact range liquidity %s before burn",
			burn.LiquidityRemoved,
			rangeLiquidityBefore,
		)
	}

	if err :=
		validateRangeLiquidityAgainstPool(
			preBurnPool,
			burn,
			rangeLiquidityBefore,
		); err != nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: %w",
			err,
		)
	}

	rangeLiquidityAfter :=
		new(big.Int).Sub(
			new(big.Int).Set(
				rangeLiquidityBefore,
			),
			burn.LiquidityRemoved,
		)

	postBurnPool, err :=
		uniswapv3.ApplyLiquidityChange(
			preBurnPool,
			domain.LiquidityChange{
				ID: burn.ID,

				BlockNumber: burn.Cursor.BlockNumber,

				LogIndex: burn.Cursor.LogIndex,

				TickLower: burn.TickLower,

				TickUpper: burn.TickUpper,

				LiquidityDelta: new(big.Int).Neg(
					burn.LiquidityRemovedCopy(),
				),
			},
		)
	if err != nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: apply actual burn: %w",
			err,
		)
	}

	rangeLocation,
		distanceOutsideRange :=
		burnRangeLocationAtTick(
			preBurnPool.CurrentTick,
			burn.TickLower,
			burn.TickUpper,
		)

	burnRangeIsActive :=
		rangeLocation ==
			BurnRangeActive

	if burnRangeIsActive !=
		preBurn.BurnRangeActive {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: pre-burn active-range flag mismatch: stored=%t computed=%t",
			preBurn.BurnRangeActive,
			burnRangeIsActive,
		)
	}

	activeLiquidityRemoved :=
		big.NewInt(0)

	if burnRangeIsActive {
		activeLiquidityRemoved =
			burn.LiquidityRemovedCopy()
	}

	expectedActiveLiquidityAfter :=
		new(big.Int).Sub(
			new(big.Int).Set(
				preBurnPool.Liquidity,
			),
			activeLiquidityRemoved,
		)

	if expectedActiveLiquidityAfter.Sign() < 0 {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: expected active liquidity became negative: %s",
			expectedActiveLiquidityAfter,
		)
	}

	if postBurnPool.Liquidity.Cmp(
		expectedActiveLiquidityAfter,
	) != 0 {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: post-burn active liquidity mismatch: actual=%s expected=%s",
			postBurnPool.Liquidity,
			expectedActiveLiquidityAfter,
		)
	}

	zeroForOneImpact, err :=
		s.analyzeDirection(
			ctx,
			preBurnPool,
			postBurnPool,
			req.ZeroForOneAmountsIn,
			req.ThresholdsBps,
			true,
		)
	if err != nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: zero_for_one: %w",
			err,
		)
	}

	oneForZeroImpact, err :=
		s.analyzeDirection(
			ctx,
			preBurnPool,
			postBurnPool,
			req.OneForZeroAmountsIn,
			req.ThresholdsBps,
			false,
		)
	if err != nil {
		return BurnEventImpactResult{}, fmt.Errorf(
			"analyze burn event impact: one_for_zero: %w",
			err,
		)
	}

	rangeWidth :=
		burn.TickUpper -
			burn.TickLower

	distanceToLower :=
		burnAbsIntDiff(
			preBurnPool.CurrentTick,
			burn.TickLower,
		)

	distanceToUpper :=
		burnAbsIntDiff(
			preBurnPool.CurrentTick,
			burn.TickUpper,
		)

	distanceToNearest :=
		burnMinInt(
			distanceToLower,
			distanceToUpper,
		)

	rangeActiveLiquidityShare :=
		decimal.Zero

	activeRemovalShare :=
		decimal.Zero

	if burnRangeIsActive {
		if rangeLiquidityBefore.Cmp(
			preBurnPool.Liquidity,
		) > 0 {
			return BurnEventImpactResult{}, fmt.Errorf(
				"analyze burn event impact: active exact-range liquidity %s exceeds pool active liquidity %s",
				rangeLiquidityBefore,
				preBurnPool.Liquidity,
			)
		}

		rangeActiveLiquidityShare =
			burnDecimalRatio(
				rangeLiquidityBefore,
				preBurnPool.Liquidity,
			)

		activeRemovalShare =
			burnDecimalRatio(
				burn.LiquidityRemoved,
				preBurnPool.Liquidity,
			)
	}

	totalLSIS :=
		zeroForOneImpact.
			LSISBps.
			Add(
				oneForZeroImpact.
					LSISBps,
			)

	return BurnEventImpactResult{
		Burn: burn,

		AmountGridStateID: req.AmountGridStateID,

		AmountGridMode: req.AmountGridMode,

		TargetImpactsBps: append(
			[]decimal.Decimal(nil),
			req.TargetImpactsBps...,
		),

		ZeroForOneAmountsIn: cloneBurnAmountGrid(
			req.ZeroForOneAmountsIn,
		),

		OneForZeroAmountsIn: cloneBurnAmountGrid(
			req.OneForZeroAmountsIn,
		),

		PreBurnPool: preBurnPool,

		PostBurnPool: postBurnPool,

		CurrentTick: preBurnPool.CurrentTick,

		RangeLocation: rangeLocation,

		BurnRangeActive: burnRangeIsActive,

		RangeWidth: rangeWidth,

		DistanceToLowerTick: distanceToLower,

		DistanceToUpperTick: distanceToUpper,

		DistanceToNearestBoundary: distanceToNearest,

		DistanceOutsideRange: distanceOutsideRange,

		NormalizedDistanceOutsideRange: burnIntRatio(
			distanceOutsideRange,
			rangeWidth,
		),

		LiquidityRemoved: burn.LiquidityRemovedCopy(),

		RangeLiquidityBeforeBurn: new(big.Int).Set(
			rangeLiquidityBefore,
		),

		RangeLiquidityAfterBurn: rangeLiquidityAfter,

		ActiveLiquidityBeforeBurn: new(big.Int).Set(
			preBurnPool.Liquidity,
		),

		ActiveLiquidityAfterBurn: new(big.Int).Set(
			postBurnPool.Liquidity,
		),

		ActiveLiquidityRemoved: new(big.Int).Set(
			activeLiquidityRemoved,
		),

		RemovalFraction: burnDecimalRatio(
			burn.LiquidityRemoved,
			rangeLiquidityBefore,
		),

		RangeActiveLiquidityShare: rangeActiveLiquidityShare,

		ActiveRemovalShare: activeRemovalShare,

		RangeLiquidityDensity: burnLiquidityDensity(
			rangeLiquidityBefore,
			rangeWidth,
		),

		RemovedLiquidityDensity: burnLiquidityDensity(
			burn.LiquidityRemoved,
			rangeWidth,
		),

		ZeroForOne: zeroForOneImpact,

		OneForZero: oneForZeroImpact,

		TotalLSISBps: totalLSIS,

		MaxDirectionalLSISBps: burnMaxDecimal(
			zeroForOneImpact.LSISBps,
			oneForZeroImpact.LSISBps,
		),
	}, nil
}

func validateBurnEventImpactRequest(
	req BurnEventImpactRequest,
) error {
	preBurn :=
		req.PreBurn

	if err := preBurn.Burn.Validate(); err != nil {
		return fmt.Errorf(
			"analyze burn event impact: invalid burn: %w",
			err,
		)
	}

	if preBurn.Pool == nil {
		return fmt.Errorf(
			"analyze burn event impact: pre-burn pool is nil",
		)
	}

	if normalizeAddress(
		preBurn.Pool.PoolAddress,
	) != normalizeAddress(
		preBurn.Burn.PoolAddress,
	) {
		return fmt.Errorf(
			"analyze burn event impact: pool address %s does not match burn pool %s",
			preBurn.Pool.PoolAddress,
			preBurn.Burn.PoolAddress,
		)
	}

	if preBurn.Pool.BlockNumber !=
		preBurn.Burn.Cursor.BlockNumber {
		return fmt.Errorf(
			"analyze burn event impact: pre-burn pool block=%d does not match burn block=%d",
			preBurn.Pool.BlockNumber,
			preBurn.Burn.Cursor.BlockNumber,
		)
	}

	if preBurn.Pool.SqrtPriceX96 == nil ||
		preBurn.Pool.SqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"analyze burn event impact: invalid pre-burn sqrt price",
		)
	}

	if preBurn.Pool.Liquidity == nil ||
		preBurn.Pool.Liquidity.Sign() <= 0 {
		return fmt.Errorf(
			"analyze burn event impact: invalid pre-burn active liquidity",
		)
	}

	if preBurn.ActiveLiquidityBeforeBurn == nil ||
		preBurn.ActiveLiquidityBeforeBurn.Cmp(
			preBurn.Pool.Liquidity,
		) != 0 {
		return fmt.Errorf(
			"analyze burn event impact: stored active liquidity before burn is inconsistent",
		)
	}

	if preBurn.LastReplayedCursor != nil &&
		!preBurn.LastReplayedCursor.Before(
			preBurn.Burn.Cursor,
		) {
		return fmt.Errorf(
			"analyze burn event impact: last replayed cursor %s is not before burn %s",
			preBurn.LastReplayedCursor,
			preBurn.Burn.Cursor,
		)
	}

	if len(req.ZeroForOneAmountsIn) == 0 {
		return fmt.Errorf(
			"analyze burn event impact: zero_for_one amount grid is empty",
		)
	}

	if len(req.OneForZeroAmountsIn) == 0 {
		return fmt.Errorf(
			"analyze burn event impact: one_for_zero amount grid is empty",
		)
	}

	if len(req.ThresholdsBps) == 0 {
		return fmt.Errorf(
			"analyze burn event impact: thresholds are empty",
		)
	}

	return nil
}

func validateRangeLiquidityAgainstPool(
	pool *domain.ReconstructedPool,
	burn domain.BurnCandidate,
	rangeLiquidity *big.Int,
) error {
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

	if rangeLiquidity.Cmp(
		lower.LiquidityGross,
	) > 0 {
		return fmt.Errorf(
			"exact range liquidity %s exceeds lower tick gross liquidity %s",
			rangeLiquidity,
			lower.LiquidityGross,
		)
	}

	if rangeLiquidity.Cmp(
		upper.LiquidityGross,
	) > 0 {
		return fmt.Errorf(
			"exact range liquidity %s exceeds upper tick gross liquidity %s",
			rangeLiquidity,
			upper.LiquidityGross,
		)
	}

	return nil
}

func (s *BurnEventImpactService) analyzeDirection(
	ctx context.Context,
	preBurnPool *domain.ReconstructedPool,
	postBurnPool *domain.ReconstructedPool,
	amountsIn []*big.Int,
	thresholds []decimal.Decimal,
	zeroForOne bool,
) (BurnDirectionalImpact, error) {
	baseCurve, err :=
		s.curveService.Build(
			ctx,
			PriceImpactCurveRequest{
				Pool: preBurnPool,

				AmountsIn: amountsIn,

				ZeroForOne: zeroForOne,
			},
		)
	if err != nil {
		return BurnDirectionalImpact{}, fmt.Errorf(
			"build pre-burn curve: %w",
			err,
		)
	}

	baseSummary, err :=
		s.curveService.Summarize(
			baseCurve,
			thresholds,
		)
	if err != nil {
		return BurnDirectionalImpact{}, fmt.Errorf(
			"summarize pre-burn curve: %w",
			err,
		)
	}

	postBurnCurve, err :=
		s.curveService.Build(
			ctx,
			PriceImpactCurveRequest{
				Pool: postBurnPool,

				AmountsIn: amountsIn,

				ZeroForOne: zeroForOne,
			},
		)
	if err != nil {
		return BurnDirectionalImpact{}, fmt.Errorf(
			"build post-burn counterfactual curve: %w",
			err,
		)
	}

	postBurnSummary, err :=
		s.curveService.Summarize(
			postBurnCurve,
			thresholds,
		)
	if err != nil {
		return BurnDirectionalImpact{}, fmt.Errorf(
			"summarize post-burn counterfactual curve: %w",
			err,
		)
	}

	deltaAUC :=
		postBurnSummary.
			PriceImpactAUCBps.
			Sub(
				baseSummary.
					PriceImpactAUCBps,
			)

	return BurnDirectionalImpact{
		ZeroForOne: zeroForOne,

		BaseAUCBps: baseSummary.
			PriceImpactAUCBps,

		PostBurnAUCBps: postBurnSummary.
			PriceImpactAUCBps,

		DeltaAUCBps: deltaAUC,

		LSISBps: positiveOnly(
			deltaAUC,
		),

		DepthDeltas: compareDepths(
			baseSummary.
				ThresholdDepths,
			postBurnSummary.
				ThresholdDepths,
		),
	}, nil
}

func burnRangeLocationAtTick(
	currentTick int,
	tickLower int,
	tickUpper int,
) (BurnRangeLocation, int) {
	switch {
	case currentTick < tickLower:
		return BurnRangeBelowCurrentTick,
			tickLower - currentTick

	case currentTick >= tickUpper:
		return BurnRangeAboveCurrentTick,
			currentTick - tickUpper

	default:
		return BurnRangeActive, 0
	}
}

func burnDecimalRatio(
	numerator *big.Int,
	denominator *big.Int,
) decimal.Decimal {
	if numerator == nil ||
		denominator == nil ||
		numerator.Sign() < 0 ||
		denominator.Sign() <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(
		numerator,
		0,
	).Div(
		decimal.NewFromBigInt(
			denominator,
			0,
		),
	)
}

func burnIntRatio(
	numerator int,
	denominator int,
) decimal.Decimal {
	if numerator <= 0 ||
		denominator <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromInt(
		int64(numerator),
	).Div(
		decimal.NewFromInt(
			int64(denominator),
		),
	)
}

func burnLiquidityDensity(
	liquidity *big.Int,
	rangeWidth int,
) decimal.Decimal {
	if liquidity == nil ||
		liquidity.Sign() <= 0 ||
		rangeWidth <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(
		liquidity,
		0,
	).Div(
		decimal.NewFromInt(
			int64(rangeWidth),
		),
	)
}

func burnAbsIntDiff(
	a int,
	b int,
) int {
	if a >= b {
		return a - b
	}

	return b - a
}

func burnMinInt(
	a int,
	b int,
) int {
	if a < b {
		return a
	}

	return b
}

func burnMaxDecimal(
	a decimal.Decimal,
	b decimal.Decimal,
) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}

	return b
}
