package services

import (
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestBuildBurnRealizedFlowControls(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	sample :=
		BurnEventSample{
			Burn: domain.BurnCandidate{
				ID: "burn-1",

				PoolAddress: poolAddress,

				Cursor: domain.EventCursor{
					BlockNumber: 100,
					LogIndex:    5,
				},
			},

			CurrentTick: 100,

			SqrtPriceX96BeforeBurn: big.NewInt(1_000),
		}

	events :=
		burnRealizedFlowEventSet{
			Available: true,

			BurnCursor: sample.Burn.Cursor,

			ThroughBlock: 200,

			LiquidityChanges: []domain.LiquidityChange{
				{
					ID: "mint-1",

					BlockNumber: 110,
					LogIndex:    1,

					TickLower: 90,
					TickUpper: 110,

					LiquidityDelta: big.NewInt(100),
				},
				{
					ID: "burn-2",

					BlockNumber: 120,
					LogIndex:    1,

					TickLower: 90,
					TickUpper: 110,

					LiquidityDelta: big.NewInt(-40),
				},
			},

			Swaps: []domain.SwapEvent{
				{
					ID:          "swap-1",
					TxHash:      "0xswap1",
					PoolAddress: poolAddress,

					BlockNumber: 130,
					LogIndex:    1,
					Timestamp:   1,

					Amount0Raw: big.NewInt(10),

					Amount1Raw: big.NewInt(-20),

					SqrtPriceX96After: big.NewInt(900),

					TickAfter: 95,
				},
				{
					ID:          "swap-2",
					TxHash:      "0xswap2",
					PoolAddress: poolAddress,

					BlockNumber: 140,
					LogIndex:    1,
					Timestamp:   2,

					Amount0Raw: big.NewInt(-15),

					Amount1Raw: big.NewInt(8),

					SqrtPriceX96After: big.NewInt(1_100),

					TickAfter: 105,
				},
			},
		}

	futurePool :=
		&domain.ReconstructedPool{
			PoolAddress: poolAddress,

			BlockNumber: 150,

			SqrtPriceX96: big.NewInt(1_100),

			CurrentTick: 105,

			Liquidity: big.NewInt(1_000),
		}

	controls, err :=
		buildBurnRealizedFlowControls(
			sample,
			futurePool,
			events,
		)
	if err != nil {
		t.Fatalf(
			"buildBurnRealizedFlowControls() error = %v",
			err,
		)
	}

	if controls.SwapCount != 2 ||
		controls.ZeroForOneSwapCount != 1 ||
		controls.OneForZeroSwapCount != 1 {
		t.Fatalf(
			"unexpected swap counts: total=%d zero_for_one=%d one_for_zero=%d",
			controls.SwapCount,
			controls.ZeroForOneSwapCount,
			controls.OneForZeroSwapCount,
		)
	}

	if controls.GrossToken0VolumeRaw.Cmp(
		big.NewInt(25),
	) != 0 {
		t.Fatalf(
			"gross token0 volume = %s, want 25",
			controls.GrossToken0VolumeRaw,
		)
	}

	if controls.GrossToken1VolumeRaw.Cmp(
		big.NewInt(28),
	) != 0 {
		t.Fatalf(
			"gross token1 volume = %s, want 28",
			controls.GrossToken1VolumeRaw,
		)
	}

	if controls.GrossMintLiquidity.Cmp(
		big.NewInt(100),
	) != 0 ||
		controls.GrossBurnLiquidity.Cmp(
			big.NewInt(40),
		) != 0 ||
		controls.NetLiquidityFlow.Cmp(
			big.NewInt(60),
		) != 0 {
		t.Fatalf(
			"unexpected liquidity flow: mint=%s burn=%s net=%s",
			controls.GrossMintLiquidity,
			controls.GrossBurnLiquidity,
			controls.NetLiquidityFlow,
		)
	}

	if controls.TickPathTotalVariation != 15 {
		t.Fatalf(
			"tick-path total variation = %d, want 15",
			controls.TickPathTotalVariation,
		)
	}

	if controls.
		TickPathQuadraticVariation.
		Cmp(
			big.NewInt(125),
		) != 0 {
		t.Fatalf(
			"tick-path quadratic variation = %s, want 125",
			controls.TickPathQuadraticVariation,
		)
	}

	if controls.TickPathRange != 10 {
		t.Fatalf(
			"tick-path range = %d, want 10",
			controls.TickPathRange,
		)
	}

	if controls.TickPathMaxAbsoluteStep != 10 {
		t.Fatalf(
			"tick-path max absolute step = %d, want 10",
			controls.TickPathMaxAbsoluteStep,
		)
	}
}

func TestBuildBurnRealizedFlowControlsWithoutSwaps(
	t *testing.T,
) {
	t.Parallel()

	sample :=
		BurnEventSample{
			Burn: domain.BurnCandidate{
				ID: "burn-1",

				PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

				Cursor: domain.EventCursor{
					BlockNumber: 100,
					LogIndex:    5,
				},
			},

			CurrentTick: 100,

			SqrtPriceX96BeforeBurn: big.NewInt(1_000),
		}

	futurePool :=
		&domain.ReconstructedPool{
			PoolAddress: sample.Burn.PoolAddress,

			BlockNumber: 150,

			SqrtPriceX96: big.NewInt(1_000),

			CurrentTick: 100,

			Liquidity: big.NewInt(1_000),
		}

	controls, err :=
		buildBurnRealizedFlowControls(
			sample,
			futurePool,
			burnRealizedFlowEventSet{
				Available:    true,
				BurnCursor:   sample.Burn.Cursor,
				ThroughBlock: 200,
			},
		)
	if err != nil {
		t.Fatalf(
			"buildBurnRealizedFlowControls() error = %v",
			err,
		)
	}

	if controls.SwapCount != 0 {
		t.Fatalf(
			"swap count = %d, want 0",
			controls.SwapCount,
		)
	}

	if controls.TickPathEndTick !=
		futurePool.CurrentTick {
		t.Fatalf(
			"tick-path end = %d, want %d",
			controls.TickPathEndTick,
			futurePool.CurrentTick,
		)
	}
}

func TestBurnRealizedFlowSwapAmountsAllowsZeroOutput(
	t *testing.T,
) {
	t.Parallel()

	swap :=
		domain.SwapEvent{
			ID: "zero-output-swap",

			TxHash: "0xzerooutput",

			PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

			BlockNumber: 100,

			LogIndex: 1,

			Timestamp: 1,

			Amount0Raw: big.NewInt(100),

			Amount1Raw: big.NewInt(0),

			SqrtPriceX96After: big.NewInt(1_000),

			TickAfter: 100,
		}

	zeroForOne,
		amountIn,
		amountOut,
		err :=
		burnRealizedFlowSwapAmounts(
			swap,
		)
	if err != nil {
		t.Fatalf(
			"burnRealizedFlowSwapAmounts() error = %v",
			err,
		)
	}

	if !zeroForOne {
		t.Fatal(
			"zeroForOne = false, want true",
		)
	}

	if amountIn.Cmp(
		big.NewInt(100),
	) != 0 {
		t.Fatalf(
			"amountIn = %s, want 100",
			amountIn,
		)
	}

	if amountOut.Sign() != 0 {
		t.Fatalf(
			"amountOut = %s, want 0",
			amountOut,
		)
	}

	if err :=
		swap.ValidateForSimulation(); err == nil {
		t.Fatal(
			"ValidateForSimulation() error = nil, want zero-output swap to remain non-replayable",
		)
	}
}
