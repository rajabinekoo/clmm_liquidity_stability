package domain

import (
	"math/big"
	"testing"
)

func TestNewLiquidityPoolBlockEvent(
	t *testing.T,
) {
	t.Parallel()

	mintChange := LiquidityChange{
		ID: "mint",

		BlockNumber: 100,

		LogIndex: 10,

		TickLower: -100,

		TickUpper: 100,

		LiquidityDelta: big.NewInt(1_000),
	}

	mint, err :=
		NewLiquidityPoolBlockEvent(
			mintChange,
		)
	if err != nil {
		t.Fatalf(
			"NewLiquidityPoolBlockEvent(mint) error = %v",
			err,
		)
	}

	if mint.Type !=
		PoolBlockEventMint {
		t.Fatalf(
			"mint Type = %s",
			mint.Type,
		)
	}

	burnChange := mintChange
	burnChange.ID = "burn"
	burnChange.LogIndex = 11
	burnChange.LiquidityDelta =
		big.NewInt(-500)

	burn, err :=
		NewLiquidityPoolBlockEvent(
			burnChange,
		)
	if err != nil {
		t.Fatalf(
			"NewLiquidityPoolBlockEvent(burn) error = %v",
			err,
		)
	}

	if burn.Type !=
		PoolBlockEventBurn {
		t.Fatalf(
			"burn Type = %s",
			burn.Type,
		)
	}
}

func TestPoolBlockEventClonesLiquidityDelta(
	t *testing.T,
) {
	t.Parallel()

	delta :=
		big.NewInt(1_000)

	event, err :=
		NewLiquidityPoolBlockEvent(
			LiquidityChange{
				ID: "mint",

				BlockNumber: 100,

				LogIndex: 10,

				TickLower: -100,

				TickUpper: 100,

				LiquidityDelta: delta,
			},
		)
	if err != nil {
		t.Fatalf(
			"NewLiquidityPoolBlockEvent() error = %v",
			err,
		)
	}

	delta.SetInt64(2_000)

	if event.
		LiquidityChange.
		LiquidityDelta.
		Cmp(
			big.NewInt(1_000),
		) != 0 {
		t.Fatalf(
			"event delta changed through alias: %s",
			event.LiquidityChange.LiquidityDelta,
		)
	}
}

func TestPoolBlockEventRejectsCursorMismatch(
	t *testing.T,
) {
	t.Parallel()

	event := PoolBlockEvent{
		Type: PoolBlockEventMint,

		Cursor: EventCursor{
			BlockNumber: 100,

			LogIndex: 10,
		},

		LiquidityChange: &LiquidityChange{
			ID: "mint",

			BlockNumber: 100,

			LogIndex: 11,

			TickLower: -100,

			TickUpper: 100,

			LiquidityDelta: big.NewInt(1_000),
		},
	}

	if err := event.Validate(); err == nil {
		t.Fatal(
			"Validate() expected cursor mismatch error",
		)
	}
}
