package services

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestBuildBurnRealizedMarketControls(
	t *testing.T,
) {
	t.Parallel()

	sample :=
		BurnEventSample{
			Burn: domain.BurnCandidate{
				TickLower: 90,
				TickUpper: 110,
			},

			CurrentTick: 100,

			SqrtPriceX96BeforeBurn: big.NewInt(1_000),

			ActiveLiquidityAfterBurn: big.NewInt(800),
		}

	futurePool :=
		&domain.ReconstructedPool{
			CurrentTick: 120,

			SqrtPriceX96: big.NewInt(1_100),

			Liquidity: big.NewInt(1_000),
		}

	controls, err :=
		buildBurnRealizedMarketControls(
			sample,
			futurePool,
		)
	if err != nil {
		t.Fatalf(
			"buildBurnRealizedMarketControls() error = %v",
			err,
		)
	}

	if !controls.Available {
		t.Fatal(
			"Available = false, want true",
		)
	}

	if controls.TickChange != 20 {
		t.Fatalf(
			"TickChange = %d, want 20",
			controls.TickChange,
		)
	}

	if controls.AbsoluteTickChange != 20 {
		t.Fatalf(
			"AbsoluteTickChange = %d, want 20",
			controls.AbsoluteTickChange,
		)
	}

	// (1100² / 1000² - 1) × 10000 = 2100 bps
	if !controls.PriceReturnBps.Equal(
		decimal.NewFromInt(2_100),
	) {
		t.Fatalf(
			"PriceReturnBps = %s, want 2100",
			controls.PriceReturnBps,
		)
	}

	if controls.FutureRangeLocation !=
		BurnRangeAboveCurrentTick {
		t.Fatalf(
			"FutureRangeLocation = %q, want %q",
			controls.FutureRangeLocation,
			BurnRangeAboveCurrentTick,
		)
	}

	if controls.FutureBurnRangeActive {
		t.Fatal(
			"FutureBurnRangeActive = true, want false",
		)
	}

	if controls.
		FutureActiveLiquidityDeltaFromPostBurn.
		Cmp(
			big.NewInt(200),
		) != 0 {
		t.Fatalf(
			"liquidity delta = %s, want 200",
			controls.
				FutureActiveLiquidityDeltaFromPostBurn,
		)
	}

	if !controls.
		FutureActiveLiquidityChangeFractionFromPostBurn.
		Equal(
			decimal.RequireFromString("0.25"),
		) {
		t.Fatalf(
			"liquidity fraction = %s, want 0.25",
			controls.
				FutureActiveLiquidityChangeFractionFromPostBurn,
		)
	}

	if !controls.
		FutureActiveLiquidityChangeBpsFromPostBurn.
		Equal(
			decimal.NewFromInt(2_500),
		) {
		t.Fatalf(
			"liquidity change bps = %s, want 2500",
			controls.
				FutureActiveLiquidityChangeBpsFromPostBurn,
		)
	}
}

func TestBurnSqrtPriceReturnBpsSupportsNegativeReturn(
	t *testing.T,
) {
	t.Parallel()

	result, err :=
		burnSqrtPriceReturnBps(
			big.NewInt(1_000),
			big.NewInt(900),
		)
	if err != nil {
		t.Fatalf(
			"burnSqrtPriceReturnBps() error = %v",
			err,
		)
	}

	// (900² / 1000² - 1) × 10000 = -1900 bps
	if !result.Equal(
		decimal.NewFromInt(-1_900),
	) {
		t.Fatalf(
			"result = %s, want -1900",
			result,
		)
	}
}

func TestValidateBurnRealizedMarketControlsRejectsTampering(
	t *testing.T,
) {
	t.Parallel()

	sample :=
		BurnEventSample{
			Burn: domain.BurnCandidate{
				TickLower: 90,
				TickUpper: 110,
			},

			CurrentTick: 100,

			SqrtPriceX96BeforeBurn: big.NewInt(1_000),

			ActiveLiquidityAfterBurn: big.NewInt(800),
		}

	futurePool :=
		&domain.ReconstructedPool{
			CurrentTick: 120,

			SqrtPriceX96: big.NewInt(1_100),

			Liquidity: big.NewInt(1_000),
		}

	controls, err :=
		buildBurnRealizedMarketControls(
			sample,
			futurePool,
		)
	if err != nil {
		t.Fatalf(
			"build controls: %v",
			err,
		)
	}

	controls.TickChange++

	err =
		validateBurnRealizedMarketControls(
			sample.Burn,
			futurePool.CurrentTick,
			futurePool.SqrtPriceX96,
			futurePool.Liquidity,
			controls,
		)

	if err == nil {
		t.Fatal(
			"validation error = nil, want non-nil",
		)
	}
}
