package domain

import (
	"fmt"
	"math/big"
)

type PoolBlockEventType uint8

const (
	PoolBlockEventMint PoolBlockEventType = iota + 1
	PoolBlockEventBurn
	PoolBlockEventSwap
)

func (t PoolBlockEventType) String() string {
	switch t {
	case PoolBlockEventMint:
		return "mint"

	case PoolBlockEventBurn:
		return "burn"

	case PoolBlockEventSwap:
		return "swap"

	default:
		return "unknown"
	}
}

// PoolBlockEvent is one pool state-changing event placed in deterministic
// Ethereum log order.
//
// Exactly one payload must be present:
//
//   - LiquidityChange for Mint/Burn
//   - Swap for Swap
type PoolBlockEvent struct {
	Type   PoolBlockEventType
	Cursor EventCursor

	LiquidityChange *LiquidityChange
	Swap            *SwapEvent
}

func NewLiquidityPoolBlockEvent(
	change LiquidityChange,
) (PoolBlockEvent, error) {
	eventType := PoolBlockEventMint

	if change.LiquidityDelta != nil &&
		change.LiquidityDelta.Sign() < 0 {
		eventType = PoolBlockEventBurn
	}

	event := PoolBlockEvent{
		Type: eventType,

		Cursor: EventCursor{
			BlockNumber: change.BlockNumber,

			LogIndex: change.LogIndex,
		},

		LiquidityChange: cloneLiquidityChange(
			change,
		),
	}

	if err := event.Validate(); err != nil {
		return PoolBlockEvent{}, err
	}

	return event, nil
}

func NewSwapPoolBlockEvent(
	swap SwapEvent,
) (PoolBlockEvent, error) {
	event := PoolBlockEvent{
		Type: PoolBlockEventSwap,

		Cursor: swap.Cursor(),

		Swap: cloneSwapEvent(
			swap,
		),
	}

	if err := event.Validate(); err != nil {
		return PoolBlockEvent{}, err
	}

	return event, nil
}

func (e PoolBlockEvent) Validate() error {
	if err := e.Cursor.Validate(); err != nil {
		return fmt.Errorf(
			"pool block event cursor: %w",
			err,
		)
	}

	switch e.Type {
	case PoolBlockEventMint,
		PoolBlockEventBurn:
		if e.LiquidityChange == nil {
			return fmt.Errorf(
				"%s event has nil liquidity change",
				e.Type,
			)
		}

		if e.Swap != nil {
			return fmt.Errorf(
				"%s event unexpectedly contains a swap",
				e.Type,
			)
		}

		change :=
			e.LiquidityChange

		if change.BlockNumber !=
			e.Cursor.BlockNumber ||
			change.LogIndex !=
				e.Cursor.LogIndex {
			return fmt.Errorf(
				"%s event cursor %s does not match liquidity change cursor %d:%d",
				e.Type,
				e.Cursor,
				change.BlockNumber,
				change.LogIndex,
			)
		}

		if change.TickLower >=
			change.TickUpper {
			return fmt.Errorf(
				"%s event has invalid tick range [%d,%d]",
				e.Type,
				change.TickLower,
				change.TickUpper,
			)
		}

		if change.LiquidityDelta == nil {
			return fmt.Errorf(
				"%s event has nil liquidity delta",
				e.Type,
			)
		}

		if change.LiquidityDelta.Sign() == 0 {
			return fmt.Errorf(
				"%s event has zero liquidity delta",
				e.Type,
			)
		}

		absoluteDelta :=
			new(big.Int).Abs(
				new(big.Int).Set(
					change.LiquidityDelta,
				),
			)

		if absoluteDelta.BitLen() > 128 {
			return fmt.Errorf(
				"%s event liquidity delta exceeds uint128: %s",
				e.Type,
				change.LiquidityDelta,
			)
		}

		if e.Type == PoolBlockEventMint &&
			change.LiquidityDelta.Sign() <= 0 {
			return fmt.Errorf(
				"mint event has non-positive liquidity delta: %s",
				change.LiquidityDelta,
			)
		}

		if e.Type == PoolBlockEventBurn &&
			change.LiquidityDelta.Sign() >= 0 {
			return fmt.Errorf(
				"burn event has non-negative liquidity delta: %s",
				change.LiquidityDelta,
			)
		}

	case PoolBlockEventSwap:
		if e.Swap == nil {
			return fmt.Errorf(
				"swap event has nil swap payload",
			)
		}

		if e.LiquidityChange != nil {
			return fmt.Errorf(
				"swap event unexpectedly contains a liquidity change",
			)
		}

		if !e.Swap.Cursor().Equal(
			e.Cursor,
		) {
			return fmt.Errorf(
				"swap event cursor %s does not match swap cursor %s",
				e.Cursor,
				e.Swap.Cursor(),
			)
		}

		if err :=
			e.Swap.ValidateForSimulation(); err != nil {
			return fmt.Errorf(
				"swap event validation: %w",
				err,
			)
		}

	default:
		return fmt.Errorf(
			"unknown pool block event type: %d",
			e.Type,
		)
	}

	return nil
}

func cloneLiquidityChange(
	change LiquidityChange,
) *LiquidityChange {
	cloned := change

	if change.LiquidityDelta != nil {
		cloned.LiquidityDelta =
			new(big.Int).Set(
				change.LiquidityDelta,
			)
	}

	return &cloned
}

func cloneSwapEvent(
	swap SwapEvent,
) *SwapEvent {
	cloned := swap

	if swap.Amount0Raw != nil {
		cloned.Amount0Raw =
			new(big.Int).Set(
				swap.Amount0Raw,
			)
	}

	if swap.Amount1Raw != nil {
		cloned.Amount1Raw =
			new(big.Int).Set(
				swap.Amount1Raw,
			)
	}

	if swap.SqrtPriceX96After != nil {
		cloned.SqrtPriceX96After =
			new(big.Int).Set(
				swap.SqrtPriceX96After,
			)
	}

	return &cloned
}
