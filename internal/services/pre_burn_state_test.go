package services

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

func TestMergePreBurnEventsOrdersByLogIndex(
	t *testing.T,
) {
	t.Parallel()

	burnCursor :=
		domain.EventCursor{
			BlockNumber: 101,

			LogIndex: 30,
		}

	events, err :=
		mergePreBurnEvents(
			burnCursor,
			[]domain.LiquidityChange{
				{
					ID: "mint",

					BlockNumber: 101,

					LogIndex: 20,

					TickLower: -50,

					TickUpper: 50,

					LiquidityDelta: big.NewInt(500),
				},
			},
			[]domain.SwapEvent{
				validPreBurnSwap(
					101,
					10,
				),
			},
		)
	if err != nil {
		t.Fatalf(
			"mergePreBurnEvents() error = %v",
			err,
		)
	}

	if len(events) != 2 {
		t.Fatalf(
			"len(events) = %d, want 2",
			len(events),
		)
	}

	if events[0].Type !=
		domain.PoolBlockEventSwap ||
		events[1].Type !=
			domain.PoolBlockEventMint {
		t.Fatalf(
			"unexpected event order: %s, %s",
			events[0].Type,
			events[1].Type,
		)
	}
}

func TestMergePreBurnEventsRejectsDuplicateCursor(
	t *testing.T,
) {
	t.Parallel()

	cursor :=
		domain.EventCursor{
			BlockNumber: 101,

			LogIndex: 10,
		}

	_, err :=
		mergePreBurnEvents(
			domain.EventCursor{
				BlockNumber: 101,

				LogIndex: 30,
			},
			[]domain.LiquidityChange{
				{
					ID: "mint",

					BlockNumber: cursor.BlockNumber,

					LogIndex: cursor.LogIndex,

					TickLower: -50,

					TickUpper: 50,

					LiquidityDelta: big.NewInt(500),
				},
			},
			[]domain.SwapEvent{
				validPreBurnSwap(
					cursor.BlockNumber,
					cursor.LogIndex,
				),
			},
		)

	if err == nil {
		t.Fatal(
			"mergePreBurnEvents() expected duplicate-cursor error",
		)
	}
}

func TestReplayPoolBlockEventsReplaysMintAndSwapExactly(
	t *testing.T,
) {
	t.Parallel()

	pool :=
		preBurnReplayPool()

	simulator, err :=
		uniswapv3.NewSimulator(
			500,
		)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	mint :=
		domain.LiquidityChange{
			ID: "mint",

			BlockNumber: 101,

			LogIndex: 10,

			TickLower: -50,

			TickUpper: 50,

			LiquidityDelta: big.NewInt(500_000_000),
		}

	poolAfterMint, err :=
		uniswapv3.ApplyLiquidityChange(
			pool,
			mint,
		)
	if err != nil {
		t.Fatalf(
			"ApplyLiquidityChange() error = %v",
			err,
		)
	}

	simulatedSwap, err :=
		simulator.SimulateExactInput(
			poolAfterMint,
			uniswapv3.ExactInputRequest{
				AmountIn: big.NewInt(1_000),

				ZeroForOne: true,
			},
		)
	if err != nil {
		t.Fatalf(
			"SimulateExactInput() error = %v",
			err,
		)
	}

	swap :=
		validPreBurnSwap(
			101,
			20,
		)

	swap.Amount0Raw =
		big.NewInt(1_000)

	swap.Amount1Raw =
		new(big.Int).Neg(
			new(big.Int).Set(
				simulatedSwap.AmountOut,
			),
		)

	swap.SqrtPriceX96After =
		new(big.Int).Set(
			simulatedSwap.
				SqrtPriceAfterX96,
		)

	swap.TickAfter =
		simulatedSwap.TickAfter

	events, err :=
		mergePreBurnEvents(
			domain.EventCursor{
				BlockNumber: 101,

				LogIndex: 30,
			},
			[]domain.LiquidityChange{
				mint,
			},
			[]domain.SwapEvent{
				swap,
			},
		)
	if err != nil {
		t.Fatalf(
			"mergePreBurnEvents() error = %v",
			err,
		)
	}

	result,
		lastCursor,
		swapReplays,
		err :=
		replayPoolBlockEvents(
			pool,
			events,
			simulator,
		)
	if err != nil {
		t.Fatalf(
			"replayPoolBlockEvents() error = %v",
			err,
		)
	}

	if len(swapReplays) != 1 {
		t.Fatalf(
			"swap replay audit count = %d, want 1",
			len(swapReplays),
		)
	}

	if lastCursor == nil ||
		lastCursor.LogIndex != 20 {
		t.Fatalf(
			"lastCursor = %v, want log 20",
			lastCursor,
		)
	}

	if result.SqrtPriceX96.Cmp(
		simulatedSwap.
			SqrtPriceAfterX96,
	) != 0 {
		t.Fatalf(
			"sqrt price = %s, want %s",
			result.SqrtPriceX96,
			simulatedSwap.SqrtPriceAfterX96,
		)
	}

	if result.CurrentTick !=
		simulatedSwap.TickAfter {
		t.Fatalf(
			"CurrentTick = %d, want %d",
			result.CurrentTick,
			simulatedSwap.TickAfter,
		)
	}

	if result.Liquidity.Cmp(
		simulatedSwap.LiquidityAfter,
	) != 0 {
		t.Fatalf(
			"Liquidity = %s, want %s",
			result.Liquidity,
			simulatedSwap.LiquidityAfter,
		)
	}
}

func validPreBurnSwap(
	blockNumber uint64,
	logIndex int,
) domain.SwapEvent {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	return domain.SwapEvent{
		ID: "swap",

		TxHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

		PoolAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",

		BlockNumber: blockNumber,

		LogIndex: logIndex,

		Timestamp: 1_700_000_000,

		Amount0Raw: big.NewInt(1_000),

		Amount1Raw: big.NewInt(-999),

		SqrtPriceX96After: q96,

		TickAfter: 0,
	}
}

func preBurnReplayPool() *domain.ReconstructedPool {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	liquidity :=
		big.NewInt(
			1_000_000_000_000,
		)

	return &domain.ReconstructedPool{
		PoolAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",

		BlockNumber: 100,

		SqrtPriceX96: q96,

		CurrentTick: 0,

		Liquidity: new(big.Int).Set(
			liquidity,
		),

		Ticks: map[int]*domain.TickState{
			-100: {
				Index: -100,

				LiquidityGross: new(big.Int).Set(
					liquidity,
				),

				LiquidityNet: new(big.Int).Set(
					liquidity,
				),
			},

			100: {
				Index: 100,

				LiquidityGross: new(big.Int).Set(
					liquidity,
				),

				LiquidityNet: new(big.Int).Neg(
					new(big.Int).Set(
						liquidity,
					),
				),
			},
		},

		InitializedTicks: []int{
			-100,
			100,
		},
	}
}

func TestReplayObservedSwapAcceptsExactOutputEvent(
	t *testing.T,
) {
	t.Parallel()

	for _, zeroForOne := range []bool{
		true,
		false,
	} {
		zeroForOne := zeroForOne

		t.Run(
			fmt.Sprintf(
				"zero_for_one_%t",
				zeroForOne,
			),
			func(t *testing.T) {
				t.Parallel()

				pool := preBurnReplayPool()

				simulator, err := uniswapv3.NewSimulator(500)
				if err != nil {
					t.Fatalf(
						"NewSimulator() error = %v",
						err,
					)
				}

				exactOutput, err := simulator.SimulateExactOutput(
					pool,
					uniswapv3.ExactOutputRequest{
						AmountOut: big.NewInt(1),

						ZeroForOne: zeroForOne,
					},
				)
				if err != nil {
					t.Fatalf(
						"SimulateExactOutput() error = %v",
						err,
					)
				}

				grossAsExactInput, err := simulator.SimulateExactInput(
					pool,
					uniswapv3.ExactInputRequest{
						AmountIn: new(big.Int).Set(
							exactOutput.AmountIn,
						),

						ZeroForOne: zeroForOne,
					},
				)
				if err != nil {
					t.Fatalf(
						"SimulateExactInput() error = %v",
						err,
					)
				}

				if grossAsExactInput.SqrtPriceAfterX96.Cmp(
					exactOutput.SqrtPriceAfterX96,
				) == 0 {
					t.Fatal(
						"test fixture is not exact-output-specific",
					)
				}

				swap := validPreBurnSwap(
					101,
					20,
				)

				if zeroForOne {
					swap.Amount0Raw = new(big.Int).Set(
						exactOutput.AmountIn,
					)

					swap.Amount1Raw = new(big.Int).Neg(
						new(big.Int).Set(
							exactOutput.AmountOut,
						),
					)
				} else {
					swap.Amount0Raw = new(big.Int).Neg(
						new(big.Int).Set(
							exactOutput.AmountOut,
						),
					)

					swap.Amount1Raw = new(big.Int).Set(
						exactOutput.AmountIn,
					)
				}

				swap.SqrtPriceX96After = new(big.Int).Set(
					exactOutput.SqrtPriceAfterX96,
				)

				swap.TickAfter = exactOutput.TickAfter

				next, audit, err := replayObservedSwap(
					pool,
					swap,
					simulator,
				)
				if err != nil {
					t.Fatalf(
						"replayObservedSwap() error = %v",
						err,
					)
				}

				if audit.Mode !=
					SwapReplayModeExactOutput {
					t.Fatalf(
						"replay mode = %q, want %q",
						audit.Mode,
						SwapReplayModeExactOutput,
					)
				}

				if audit.SwapSteps !=
					exactOutput.SwapSteps {
					t.Fatalf(
						"swap steps = %d, want %d",
						audit.SwapSteps,
						exactOutput.SwapSteps,
					)
				}

				if audit.CrossedTicks !=
					exactOutput.CrossedTicks {
					t.Fatalf(
						"crossed ticks = %d, want %d",
						audit.CrossedTicks,
						exactOutput.CrossedTicks,
					)
				}

				if next.SqrtPriceX96.Cmp(
					exactOutput.SqrtPriceAfterX96,
				) != 0 {
					t.Fatalf(
						"sqrt price = %s, want %s",
						next.SqrtPriceX96,
						exactOutput.SqrtPriceAfterX96,
					)
				}

				if next.CurrentTick != exactOutput.TickAfter {
					t.Fatalf(
						"current tick = %d, want %d",
						next.CurrentTick,
						exactOutput.TickAfter,
					)
				}

				if next.Liquidity.Cmp(
					exactOutput.LiquidityAfter,
				) != 0 {
					t.Fatalf(
						"liquidity = %s, want %s",
						next.Liquidity,
						exactOutput.LiquidityAfter,
					)
				}
			},
		)
	}
}

func TestReplayObservedSwapRejectsStateThatMatchesNeitherMode(
	t *testing.T,
) {
	t.Parallel()

	pool := preBurnReplayPool()

	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	exactInput, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn: big.NewInt(1_000),

			ZeroForOne: true,
		},
	)
	if err != nil {
		t.Fatalf(
			"SimulateExactInput() error = %v",
			err,
		)
	}

	swap := validPreBurnSwap(
		101,
		20,
	)

	swap.Amount0Raw =
		big.NewInt(1_000)

	swap.Amount1Raw =
		new(big.Int).Neg(
			new(big.Int).Set(
				exactInput.AmountOut,
			),
		)

	swap.SqrtPriceX96After =
		new(big.Int).Add(
			new(big.Int).Set(
				exactInput.SqrtPriceAfterX96,
			),
			big.NewInt(1),
		)

	swap.TickAfter =
		exactInput.TickAfter

	if _, _, err := replayObservedSwap(
		pool,
		swap,
		simulator,
	); err == nil {
		t.Fatal(
			"replayObservedSwap() expected protocol-mode mismatch error",
		)
	}
}
