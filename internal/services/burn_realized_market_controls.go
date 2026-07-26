package services

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const burnMarketControlDivisionPrecision int32 = 36

type BurnRealizedMarketControls struct {
	// Available=false برای Fixtureهای قدیمی مجاز است.
	// تمام Observationهای واقعی Analyzer باید Available=true داشته باشند.
	Available bool

	// Baselineهای داخلی برای اعتبارسنجی محاسبات.
	ReferenceTick           int
	ReferenceSqrtPriceX96   *big.Int
	PostBurnActiveLiquidity *big.Int

	TickChange         int
	AbsoluteTickChange int

	// PriceReturnBps بر اساس قیمت واقعی Uniswap محاسبه می‌شود:
	//
	// price ∝ sqrtPriceX96²
	//
	// بنابراین Decimalهای Token در بازده نسبی حذف می‌شوند.
	PriceReturnBps         decimal.Decimal
	AbsolutePriceReturnBps decimal.Decimal

	FutureRangeLocation   BurnRangeLocation
	FutureBurnRangeActive bool

	FutureActiveLiquidityDeltaFromPostBurn *big.Int

	FutureActiveLiquidityChangeFractionFromPostBurn decimal.Decimal
	FutureActiveLiquidityChangeBpsFromPostBurn      decimal.Decimal
}

func buildBurnRealizedMarketControls(
	sample BurnEventSample,
	futurePool *domain.ReconstructedPool,
) (BurnRealizedMarketControls, error) {
	if futurePool == nil {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: future pool is nil",
		)
	}

	if sample.SqrtPriceX96BeforeBurn == nil ||
		sample.SqrtPriceX96BeforeBurn.Sign() <= 0 {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: pre-burn sqrt price must be positive",
		)
	}

	if sample.ActiveLiquidityAfterBurn == nil ||
		sample.ActiveLiquidityAfterBurn.Sign() <= 0 {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: post-burn active liquidity must be positive",
		)
	}

	if futurePool.SqrtPriceX96 == nil ||
		futurePool.SqrtPriceX96.Sign() <= 0 {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: future sqrt price must be positive",
		)
	}

	if futurePool.Liquidity == nil ||
		futurePool.Liquidity.Sign() <= 0 {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: future active liquidity must be positive",
		)
	}

	priceReturnBps, err :=
		burnSqrtPriceReturnBps(
			sample.SqrtPriceX96BeforeBurn,
			futurePool.SqrtPriceX96,
		)
	if err != nil {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: calculate price return: %w",
			err,
		)
	}

	activeLiquidityDelta :=
		new(big.Int).Sub(
			new(big.Int).Set(
				futurePool.Liquidity,
			),
			sample.ActiveLiquidityAfterBurn,
		)

	activeLiquidityChangeFraction :=
		burnSignedBigIntRatio(
			activeLiquidityDelta,
			sample.ActiveLiquidityAfterBurn,
		)

	futureRangeLocation, _ :=
		burnRangeLocationAtTick(
			futurePool.CurrentTick,
			sample.Burn.TickLower,
			sample.Burn.TickUpper,
		)

	tickChange :=
		futurePool.CurrentTick -
			sample.CurrentTick

	controls :=
		BurnRealizedMarketControls{
			Available: true,

			ReferenceTick: sample.CurrentTick,

			ReferenceSqrtPriceX96: new(big.Int).Set(
				sample.
					SqrtPriceX96BeforeBurn,
			),

			PostBurnActiveLiquidity: new(big.Int).Set(
				sample.
					ActiveLiquidityAfterBurn,
			),

			TickChange: tickChange,

			AbsoluteTickChange: burnAbsInt(
				tickChange,
			),

			PriceReturnBps: priceReturnBps,

			AbsolutePriceReturnBps: priceReturnBps.Abs(),

			FutureRangeLocation: futureRangeLocation,

			FutureBurnRangeActive: futureRangeLocation ==
				BurnRangeActive,

			FutureActiveLiquidityDeltaFromPostBurn: new(big.Int).Set(
				activeLiquidityDelta,
			),

			FutureActiveLiquidityChangeFractionFromPostBurn: activeLiquidityChangeFraction,

			FutureActiveLiquidityChangeBpsFromPostBurn: activeLiquidityChangeFraction.Mul(
				decimal.NewFromInt(
					10_000,
				),
			),
		}

	if err :=
		validateBurnRealizedMarketControls(
			sample.Burn,
			futurePool.CurrentTick,
			futurePool.SqrtPriceX96,
			futurePool.Liquidity,
			controls,
		); err != nil {
		return BurnRealizedMarketControls{}, fmt.Errorf(
			"build burn realized market controls: %w",
			err,
		)
	}

	return controls, nil
}

func validateBurnRealizedMarketControls(
	burn domain.BurnCandidate,
	futureTick int,
	futureSqrtPriceX96 *big.Int,
	futureActiveLiquidity *big.Int,
	controls BurnRealizedMarketControls,
) error {
	// Backward compatibility برای Fixtureهای قدیمی.
	if !controls.Available {
		if controls.ReferenceSqrtPriceX96 != nil ||
			controls.PostBurnActiveLiquidity != nil ||
			controls.FutureActiveLiquidityDeltaFromPostBurn != nil ||
			controls.ReferenceTick != 0 ||
			controls.TickChange != 0 ||
			controls.AbsoluteTickChange != 0 ||
			!controls.PriceReturnBps.IsZero() ||
			!controls.AbsolutePriceReturnBps.IsZero() ||
			controls.FutureRangeLocation != "" ||
			controls.FutureBurnRangeActive ||
			!controls.
				FutureActiveLiquidityChangeFractionFromPostBurn.
				IsZero() ||
			!controls.
				FutureActiveLiquidityChangeBpsFromPostBurn.
				IsZero() {
			return fmt.Errorf(
				"market controls are unavailable but contain values",
			)
		}

		return nil
	}

	if burn.TickLower >= burn.TickUpper {
		return fmt.Errorf(
			"invalid burn range [%d,%d)",
			burn.TickLower,
			burn.TickUpper,
		)
	}

	if controls.ReferenceSqrtPriceX96 == nil ||
		controls.ReferenceSqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"reference sqrt price must be positive",
		)
	}

	if controls.PostBurnActiveLiquidity == nil ||
		controls.PostBurnActiveLiquidity.Sign() <= 0 {
		return fmt.Errorf(
			"post-burn active liquidity must be positive",
		)
	}

	if futureSqrtPriceX96 == nil ||
		futureSqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"future sqrt price must be positive",
		)
	}

	if futureActiveLiquidity == nil ||
		futureActiveLiquidity.Sign() <= 0 {
		return fmt.Errorf(
			"future active liquidity must be positive",
		)
	}

	expectedTickChange :=
		futureTick -
			controls.ReferenceTick

	if controls.TickChange !=
		expectedTickChange {
		return fmt.Errorf(
			"tick change=%d, expected=%d",
			controls.TickChange,
			expectedTickChange,
		)
	}

	expectedAbsoluteTickChange :=
		burnAbsInt(
			expectedTickChange,
		)

	if controls.AbsoluteTickChange !=
		expectedAbsoluteTickChange {
		return fmt.Errorf(
			"absolute tick change=%d, expected=%d",
			controls.AbsoluteTickChange,
			expectedAbsoluteTickChange,
		)
	}

	expectedPriceReturnBps, err :=
		burnSqrtPriceReturnBps(
			controls.ReferenceSqrtPriceX96,
			futureSqrtPriceX96,
		)
	if err != nil {
		return err
	}

	if !controls.PriceReturnBps.Equal(
		expectedPriceReturnBps,
	) {
		return fmt.Errorf(
			"price return=%s, expected=%s",
			controls.PriceReturnBps,
			expectedPriceReturnBps,
		)
	}

	if !controls.
		AbsolutePriceReturnBps.
		Equal(
			expectedPriceReturnBps.Abs(),
		) {
		return fmt.Errorf(
			"absolute price return=%s, expected=%s",
			controls.AbsolutePriceReturnBps,
			expectedPriceReturnBps.Abs(),
		)
	}

	expectedRangeLocation, _ :=
		burnRangeLocationAtTick(
			futureTick,
			burn.TickLower,
			burn.TickUpper,
		)

	if controls.FutureRangeLocation !=
		expectedRangeLocation {
		return fmt.Errorf(
			"future range location=%q, expected=%q",
			controls.FutureRangeLocation,
			expectedRangeLocation,
		)
	}

	expectedRangeActive :=
		expectedRangeLocation ==
			BurnRangeActive

	if controls.FutureBurnRangeActive !=
		expectedRangeActive {
		return fmt.Errorf(
			"future burn-range active=%t, expected=%t",
			controls.FutureBurnRangeActive,
			expectedRangeActive,
		)
	}

	expectedLiquidityDelta :=
		new(big.Int).Sub(
			new(big.Int).Set(
				futureActiveLiquidity,
			),
			controls.PostBurnActiveLiquidity,
		)

	if controls.
		FutureActiveLiquidityDeltaFromPostBurn == nil ||
		controls.
			FutureActiveLiquidityDeltaFromPostBurn.
			Cmp(
				expectedLiquidityDelta,
			) != 0 {
		return fmt.Errorf(
			"future active-liquidity delta=%v, expected=%s",
			controls.
				FutureActiveLiquidityDeltaFromPostBurn,
			expectedLiquidityDelta,
		)
	}

	expectedLiquidityFraction :=
		burnSignedBigIntRatio(
			expectedLiquidityDelta,
			controls.PostBurnActiveLiquidity,
		)

	if !controls.
		FutureActiveLiquidityChangeFractionFromPostBurn.
		Equal(
			expectedLiquidityFraction,
		) {
		return fmt.Errorf(
			"future active-liquidity change fraction=%s, expected=%s",
			controls.
				FutureActiveLiquidityChangeFractionFromPostBurn,
			expectedLiquidityFraction,
		)
	}

	expectedLiquidityBps :=
		expectedLiquidityFraction.Mul(
			decimal.NewFromInt(
				10_000,
			),
		)

	if !controls.
		FutureActiveLiquidityChangeBpsFromPostBurn.
		Equal(
			expectedLiquidityBps,
		) {
		return fmt.Errorf(
			"future active-liquidity change bps=%s, expected=%s",
			controls.
				FutureActiveLiquidityChangeBpsFromPostBurn,
			expectedLiquidityBps,
		)
	}

	return nil
}

func burnSqrtPriceReturnBps(
	beforeSqrtPriceX96 *big.Int,
	afterSqrtPriceX96 *big.Int,
) (decimal.Decimal, error) {
	if beforeSqrtPriceX96 == nil ||
		beforeSqrtPriceX96.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf(
			"before sqrt price must be positive",
		)
	}

	if afterSqrtPriceX96 == nil ||
		afterSqrtPriceX96.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf(
			"after sqrt price must be positive",
		)
	}

	beforeSquared :=
		new(big.Int).Mul(
			new(big.Int).Set(
				beforeSqrtPriceX96,
			),
			new(big.Int).Set(
				beforeSqrtPriceX96,
			),
		)

	afterSquared :=
		new(big.Int).Mul(
			new(big.Int).Set(
				afterSqrtPriceX96,
			),
			new(big.Int).Set(
				afterSqrtPriceX96,
			),
		)

	priceRatio :=
		decimal.NewFromBigInt(
			afterSquared,
			0,
		).DivRound(
			decimal.NewFromBigInt(
				beforeSquared,
				0,
			),
			burnMarketControlDivisionPrecision,
		)

	return priceRatio.Sub(
		decimal.NewFromInt(1),
	).Mul(
		decimal.NewFromInt(
			10_000,
		),
	), nil
}

func burnSignedBigIntRatio(
	numerator *big.Int,
	denominator *big.Int,
) decimal.Decimal {
	if numerator == nil ||
		denominator == nil ||
		denominator.Sign() <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromBigInt(
		numerator,
		0,
	).DivRound(
		decimal.NewFromBigInt(
			denominator,
			0,
		),
		burnMarketControlDivisionPrecision,
	)
}

func cloneBurnRealizedMarketControls(
	value BurnRealizedMarketControls,
) BurnRealizedMarketControls {
	result :=
		value

	if value.ReferenceSqrtPriceX96 != nil {
		result.ReferenceSqrtPriceX96 =
			new(big.Int).Set(
				value.ReferenceSqrtPriceX96,
			)
	}

	if value.PostBurnActiveLiquidity != nil {
		result.PostBurnActiveLiquidity =
			new(big.Int).Set(
				value.PostBurnActiveLiquidity,
			)
	}

	if value.
		FutureActiveLiquidityDeltaFromPostBurn != nil {
		result.
			FutureActiveLiquidityDeltaFromPostBurn =
			new(big.Int).Set(
				value.
					FutureActiveLiquidityDeltaFromPostBurn,
			)
	}

	return result
}

func burnAbsInt(
	value int,
) int {
	if value < 0 {
		return -value
	}

	return value
}
