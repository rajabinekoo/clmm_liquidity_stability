package services

import (
	"fmt"
	"math/big"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type BurnRealizedFlowControls struct {
	Available bool

	WindowStartCursor domain.EventCursor
	WindowEndBlock    uint64

	ReferenceTick         int
	ReferenceSqrtPriceX96 *big.Int

	SwapCount           int
	ZeroForOneSwapCount int
	OneForZeroSwapCount int

	Token0InputRaw  *big.Int
	Token0OutputRaw *big.Int
	Token1InputRaw  *big.Int
	Token1OutputRaw *big.Int

	GrossToken0VolumeRaw *big.Int
	GrossToken1VolumeRaw *big.Int

	LiquidityEventCount int
	MintEventCount      int
	BurnEventCount      int

	GrossMintLiquidity *big.Int
	GrossBurnLiquidity *big.Int
	NetLiquidityFlow   *big.Int

	TickPathStartTick int
	TickPathEndTick   int
	TickPathMinTick   int
	TickPathMaxTick   int
	TickPathRange     int

	TickPathTotalVariation uint64

	TickPathQuadraticVariation *big.Int

	TickPathMaxAbsoluteStep int

	LastSwapSqrtPriceX96 *big.Int
}

func buildBurnRealizedFlowControls(
	sample BurnEventSample,
	futurePool *domain.ReconstructedPool,
	events burnRealizedFlowEventSet,
) (BurnRealizedFlowControls, error) {
	if !events.Available {
		return BurnRealizedFlowControls{}, nil
	}

	if futurePool == nil {
		return BurnRealizedFlowControls{}, fmt.Errorf(
			"build burn realized flow controls: future pool is nil",
		)
	}

	if futurePool.BlockNumber >
		events.ThroughBlock {
		return BurnRealizedFlowControls{}, fmt.Errorf(
			"build burn realized flow controls: future block %d exceeds loaded-through block %d",
			futurePool.BlockNumber,
			events.ThroughBlock,
		)
	}

	if !events.BurnCursor.Equal(
		sample.Burn.Cursor,
	) {
		return BurnRealizedFlowControls{}, fmt.Errorf(
			"build burn realized flow controls: event-set burn cursor %s does not match sample burn cursor %s",
			events.BurnCursor,
			sample.Burn.Cursor,
		)
	}

	controls :=
		BurnRealizedFlowControls{
			Available: true,

			WindowStartCursor: sample.Burn.Cursor,

			WindowEndBlock: futurePool.BlockNumber,

			ReferenceTick: sample.CurrentTick,

			ReferenceSqrtPriceX96: new(big.Int).Set(
				sample.
					SqrtPriceX96BeforeBurn,
			),

			Token0InputRaw: big.NewInt(0),

			Token0OutputRaw: big.NewInt(0),

			Token1InputRaw: big.NewInt(0),

			Token1OutputRaw: big.NewInt(0),

			GrossToken0VolumeRaw: big.NewInt(0),

			GrossToken1VolumeRaw: big.NewInt(0),

			GrossMintLiquidity: big.NewInt(0),

			GrossBurnLiquidity: big.NewInt(0),

			NetLiquidityFlow: big.NewInt(0),

			TickPathStartTick: sample.CurrentTick,

			TickPathEndTick: sample.CurrentTick,

			TickPathMinTick: sample.CurrentTick,

			TickPathMaxTick: sample.CurrentTick,

			TickPathQuadraticVariation: big.NewInt(0),
		}

	for _, change := range events.LiquidityChanges {
		if change.BlockNumber >
			futurePool.BlockNumber {
			break
		}

		cursor :=
			domain.EventCursor{
				BlockNumber: change.BlockNumber,

				LogIndex: change.LogIndex,
			}

		if !cursor.After(
			sample.Burn.Cursor,
		) {
			return BurnRealizedFlowControls{}, fmt.Errorf(
				"build burn realized flow controls: liquidity cursor %s is not after burn %s",
				cursor,
				sample.Burn.Cursor,
			)
		}

		if change.LiquidityDelta == nil ||
			change.LiquidityDelta.Sign() == 0 {
			return BurnRealizedFlowControls{}, fmt.Errorf(
				"build burn realized flow controls: liquidity event %s has invalid delta",
				change.ID,
			)
		}

		controls.LiquidityEventCount++

		controls.NetLiquidityFlow.Add(
			controls.NetLiquidityFlow,
			change.LiquidityDelta,
		)

		if change.LiquidityDelta.Sign() > 0 {
			controls.MintEventCount++

			controls.GrossMintLiquidity.Add(
				controls.GrossMintLiquidity,
				change.LiquidityDelta,
			)
		} else {
			controls.BurnEventCount++

			controls.GrossBurnLiquidity.Add(
				controls.GrossBurnLiquidity,
				new(big.Int).Abs(
					new(big.Int).Set(
						change.LiquidityDelta,
					),
				),
			)
		}
	}

	previousTick :=
		sample.CurrentTick

	for _, swap := range events.Swaps {
		if swap.BlockNumber >
			futurePool.BlockNumber {
			break
		}

		if !swap.Cursor().After(
			sample.Burn.Cursor,
		) {
			return BurnRealizedFlowControls{}, fmt.Errorf(
				"build burn realized flow controls: swap cursor %s is not after burn %s",
				swap.Cursor(),
				sample.Burn.Cursor,
			)
		}

		zeroForOne,
			amountIn,
			amountOut,
			err :=
			burnRealizedFlowSwapAmounts(
				swap,
			)
		if err != nil {
			return BurnRealizedFlowControls{}, fmt.Errorf(
				"build burn realized flow controls: swap %s: %w",
				swap.ID,
				err,
			)
		}

		controls.SwapCount++

		if zeroForOne {
			controls.ZeroForOneSwapCount++

			controls.Token0InputRaw.Add(
				controls.Token0InputRaw,
				amountIn,
			)

			controls.Token1OutputRaw.Add(
				controls.Token1OutputRaw,
				amountOut,
			)
		} else {
			controls.OneForZeroSwapCount++

			controls.Token1InputRaw.Add(
				controls.Token1InputRaw,
				amountIn,
			)

			controls.Token0OutputRaw.Add(
				controls.Token0OutputRaw,
				amountOut,
			)
		}

		step :=
			swap.TickAfter -
				previousTick

		absoluteStep :=
			burnAbsInt(
				step,
			)

		controls.TickPathTotalVariation +=
			uint64(
				absoluteStep,
			)

		stepBig :=
			big.NewInt(
				int64(step),
			)

		stepSquared :=
			new(big.Int).Mul(
				new(big.Int).Set(
					stepBig,
				),
				stepBig,
			)

		controls.
			TickPathQuadraticVariation.
			Add(
				controls.
					TickPathQuadraticVariation,
				stepSquared,
			)

		if absoluteStep >
			controls.TickPathMaxAbsoluteStep {
			controls.TickPathMaxAbsoluteStep =
				absoluteStep
		}

		if swap.TickAfter <
			controls.TickPathMinTick {
			controls.TickPathMinTick =
				swap.TickAfter
		}

		if swap.TickAfter >
			controls.TickPathMaxTick {
			controls.TickPathMaxTick =
				swap.TickAfter
		}

		previousTick =
			swap.TickAfter

		controls.TickPathEndTick =
			swap.TickAfter

		controls.LastSwapSqrtPriceX96 =
			new(big.Int).Set(
				swap.SqrtPriceX96After,
			)
	}

	controls.GrossToken0VolumeRaw.Add(
		controls.Token0InputRaw,
		controls.Token0OutputRaw,
	)

	controls.GrossToken1VolumeRaw.Add(
		controls.Token1InputRaw,
		controls.Token1OutputRaw,
	)

	controls.TickPathRange =
		controls.TickPathMaxTick -
			controls.TickPathMinTick

	if err :=
		validateBurnRealizedFlowControls(
			sample.Burn,
			futurePool.BlockNumber,
			futurePool.CurrentTick,
			futurePool.SqrtPriceX96,
			controls,
		); err != nil {
		return BurnRealizedFlowControls{}, fmt.Errorf(
			"build burn realized flow controls: %w",
			err,
		)
	}

	return controls, nil
}

func validateBurnRealizedFlowControls(
	burn domain.BurnCandidate,
	futureBlock uint64,
	futureTick int,
	futureSqrtPriceX96 *big.Int,
	controls BurnRealizedFlowControls,
) error {
	if !controls.Available {
		return nil
	}

	if !controls.WindowStartCursor.Equal(
		burn.Cursor,
	) {
		return fmt.Errorf(
			"flow-control window start=%s, burn cursor=%s",
			controls.WindowStartCursor,
			burn.Cursor,
		)
	}

	if controls.WindowEndBlock !=
		futureBlock {
		return fmt.Errorf(
			"flow-control window end=%d, future block=%d",
			controls.WindowEndBlock,
			futureBlock,
		)
	}

	if controls.ReferenceSqrtPriceX96 == nil ||
		controls.ReferenceSqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"flow-control reference sqrt price must be positive",
		)
	}

	if futureSqrtPriceX96 == nil ||
		futureSqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf(
			"flow-control future sqrt price must be positive",
		)
	}

	if controls.SwapCount !=
		controls.ZeroForOneSwapCount+
			controls.OneForZeroSwapCount {
		return fmt.Errorf(
			"flow-control swap count=%d, directional count=%d",
			controls.SwapCount,
			controls.ZeroForOneSwapCount+
				controls.OneForZeroSwapCount,
		)
	}

	if controls.LiquidityEventCount !=
		controls.MintEventCount+
			controls.BurnEventCount {
		return fmt.Errorf(
			"flow-control liquidity-event count=%d, categorized count=%d",
			controls.LiquidityEventCount,
			controls.MintEventCount+
				controls.BurnEventCount,
		)
	}

	requiredNonNegative :=
		[]struct {
			name  string
			value *big.Int
		}{
			{"token0 input", controls.Token0InputRaw},
			{"token0 output", controls.Token0OutputRaw},
			{"token1 input", controls.Token1InputRaw},
			{"token1 output", controls.Token1OutputRaw},
			{"gross token0 volume", controls.GrossToken0VolumeRaw},
			{"gross token1 volume", controls.GrossToken1VolumeRaw},
			{"gross mint liquidity", controls.GrossMintLiquidity},
			{"gross burn liquidity", controls.GrossBurnLiquidity},
			{
				"tick-path quadratic variation",
				controls.TickPathQuadraticVariation,
			},
		}

	for _, item := range requiredNonNegative {
		if item.value == nil ||
			item.value.Sign() < 0 {
			return fmt.Errorf(
				"flow-control %s must be non-negative",
				item.name,
			)
		}
	}

	expectedToken0Volume :=
		new(big.Int).Add(
			controls.Token0InputRaw,
			controls.Token0OutputRaw,
		)

	if controls.GrossToken0VolumeRaw.Cmp(
		expectedToken0Volume,
	) != 0 {
		return fmt.Errorf(
			"flow-control gross token0 volume=%s, expected=%s",
			controls.GrossToken0VolumeRaw,
			expectedToken0Volume,
		)
	}

	expectedToken1Volume :=
		new(big.Int).Add(
			controls.Token1InputRaw,
			controls.Token1OutputRaw,
		)

	if controls.GrossToken1VolumeRaw.Cmp(
		expectedToken1Volume,
	) != 0 {
		return fmt.Errorf(
			"flow-control gross token1 volume=%s, expected=%s",
			controls.GrossToken1VolumeRaw,
			expectedToken1Volume,
		)
	}

	if controls.NetLiquidityFlow == nil {
		return fmt.Errorf(
			"flow-control net liquidity flow is nil",
		)
	}

	expectedNetLiquidityFlow :=
		new(big.Int).Sub(
			controls.GrossMintLiquidity,
			controls.GrossBurnLiquidity,
		)

	if controls.NetLiquidityFlow.Cmp(
		expectedNetLiquidityFlow,
	) != 0 {
		return fmt.Errorf(
			"flow-control net liquidity flow=%s, expected=%s",
			controls.NetLiquidityFlow,
			expectedNetLiquidityFlow,
		)
	}

	if controls.TickPathMinTick >
		controls.TickPathMaxTick {
		return fmt.Errorf(
			"flow-control tick minimum=%d exceeds maximum=%d",
			controls.TickPathMinTick,
			controls.TickPathMaxTick,
		)
	}

	if controls.TickPathRange !=
		controls.TickPathMaxTick-
			controls.TickPathMinTick {
		return fmt.Errorf(
			"flow-control tick range=%d, expected=%d",
			controls.TickPathRange,
			controls.TickPathMaxTick-
				controls.TickPathMinTick,
		)
	}

	if controls.TickPathEndTick !=
		futureTick {
		return fmt.Errorf(
			"flow-control end tick=%d, future tick=%d; swap stream may be incomplete",
			controls.TickPathEndTick,
			futureTick,
		)
	}

	if controls.SwapCount == 0 {
		if controls.LastSwapSqrtPriceX96 != nil {
			return fmt.Errorf(
				"flow-control has zero swaps but a last-swap sqrt price",
			)
		}

		if controls.TickPathTotalVariation != 0 ||
			controls.
				TickPathQuadraticVariation.
				Sign() != 0 ||
			controls.TickPathMaxAbsoluteStep != 0 {
			return fmt.Errorf(
				"flow-control has zero swaps but non-zero tick-path movement",
			)
		}

		if controls.ReferenceSqrtPriceX96.Cmp(
			futureSqrtPriceX96,
		) != 0 {
			return fmt.Errorf(
				"flow-control has zero swaps but reference sqrt price %s differs from future sqrt price %s",
				controls.ReferenceSqrtPriceX96,
				futureSqrtPriceX96,
			)
		}
	} else {
		if controls.LastSwapSqrtPriceX96 == nil ||
			controls.LastSwapSqrtPriceX96.Sign() <= 0 {
			return fmt.Errorf(
				"flow-control last-swap sqrt price must be positive",
			)
		}

		if controls.LastSwapSqrtPriceX96.Cmp(
			futureSqrtPriceX96,
		) != 0 {
			return fmt.Errorf(
				"flow-control last-swap sqrt price %s differs from future sqrt price %s; swap stream may be incomplete",
				controls.LastSwapSqrtPriceX96,
				futureSqrtPriceX96,
			)
		}
	}

	return nil
}

func cloneBurnRealizedFlowControls(
	value BurnRealizedFlowControls,
) BurnRealizedFlowControls {
	result :=
		value

	clone :=
		func(input *big.Int) *big.Int {
			if input == nil {
				return nil
			}

			return new(big.Int).Set(
				input,
			)
		}

	result.ReferenceSqrtPriceX96 =
		clone(
			value.ReferenceSqrtPriceX96,
		)

	result.Token0InputRaw =
		clone(
			value.Token0InputRaw,
		)

	result.Token0OutputRaw =
		clone(
			value.Token0OutputRaw,
		)

	result.Token1InputRaw =
		clone(
			value.Token1InputRaw,
		)

	result.Token1OutputRaw =
		clone(
			value.Token1OutputRaw,
		)

	result.GrossToken0VolumeRaw =
		clone(
			value.GrossToken0VolumeRaw,
		)

	result.GrossToken1VolumeRaw =
		clone(
			value.GrossToken1VolumeRaw,
		)

	result.GrossMintLiquidity =
		clone(
			value.GrossMintLiquidity,
		)

	result.GrossBurnLiquidity =
		clone(
			value.GrossBurnLiquidity,
		)

	result.NetLiquidityFlow =
		clone(
			value.NetLiquidityFlow,
		)

	result.TickPathQuadraticVariation =
		clone(
			value.TickPathQuadraticVariation,
		)

	result.LastSwapSqrtPriceX96 =
		clone(
			value.LastSwapSqrtPriceX96,
		)

	return result
}

func burnRealizedFlowSwapAmounts(
	swap domain.SwapEvent,
) (
	zeroForOne bool,
	amountIn *big.Int,
	amountOut *big.Int,
	err error,
) {
	if err :=
		swap.ValidateForObservation(); err != nil {
		return false, nil, nil, err
	}

	switch {
	case swap.Amount0Raw.Sign() > 0 &&
		swap.Amount1Raw.Sign() <= 0:
		return true,
			new(big.Int).Set(
				swap.Amount0Raw,
			),
			new(big.Int).Abs(
				new(big.Int).Set(
					swap.Amount1Raw,
				),
			),
			nil

	case swap.Amount1Raw.Sign() > 0 &&
		swap.Amount0Raw.Sign() <= 0:
		return false,
			new(big.Int).Set(
				swap.Amount1Raw,
			),
			new(big.Int).Abs(
				new(big.Int).Set(
					swap.Amount0Raw,
				),
			),
			nil

	default:
		return false, nil, nil, fmt.Errorf(
			"unsupported swap direction: amount0=%s amount1=%s",
			swap.Amount0Raw,
			swap.Amount1Raw,
		)
	}
}
