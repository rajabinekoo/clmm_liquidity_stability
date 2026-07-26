package services

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type burnFlowFetchCall struct {
	FromBlock uint64
	ToBlock   uint64
	AfterID   string
	Limit     int
}

type fakeBurnRealizedFlowProvider struct {
	calls []burnFlowFetchCall

	pages map[string][]domain.SwapEvent
}

func (
	f *fakeBurnRealizedFlowProvider,
) PoolMetadata(
	_ context.Context,
	poolAddress string,
) (domain.Pool, error) {
	return domain.Pool{
		Address: poolAddress,

		Token0Decimals: 6,

		Token1Decimals: 18,
	}, nil
}

func (
	f *fakeBurnRealizedFlowProvider,
) FetchSwapsPage(
	_ context.Context,
	poolAddress string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
	_ int,
	_ int,
) ([]domain.SwapEvent, error) {
	f.calls =
		append(
			f.calls,
			burnFlowFetchCall{
				FromBlock: fromBlock,

				ToBlock: toBlock,

				AfterID: afterID,

				Limit: limit,
			},
		)

	key :=
		burnFlowTestPageKey(
			fromBlock,
			toBlock,
			afterID,
		)

	values :=
		f.pages[key]

	result := make(
		[]domain.SwapEvent,
		len(values),
	)

	copy(
		result,
		values,
	)

	return result, nil
}

func TestFetchBurnRealizedFlowSwapsUsesBlockChunks(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	provider :=
		&fakeBurnRealizedFlowProvider{
			pages: map[string][]domain.SwapEvent{
				burnFlowTestPageKey(
					100,
					599,
					"",
				): {
					burnFlowTestSwap(
						poolAddress,
						110,
						1,
						"swap-1",
					),
				},

				burnFlowTestPageKey(
					600,
					1_099,
					"",
				): {
					burnFlowTestSwap(
						poolAddress,
						700,
						1,
						"swap-2",
					),
				},

				burnFlowTestPageKey(
					1_100,
					1_299,
					"",
				): {
					burnFlowTestSwap(
						poolAddress,
						1_200,
						1,
						"swap-3",
					),
				},
			},
		}

	swaps, err :=
		fetchBurnRealizedFlowSwaps(
			context.Background(),
			provider,
			domain.Pool{
				Address: poolAddress,

				Token0Decimals: 6,

				Token1Decimals: 18,
			},
			domain.EventCursor{
				BlockNumber: 100,

				LogIndex: 5,
			},
			1_299,
			1_000,
		)
	if err != nil {
		t.Fatalf(
			"fetchBurnRealizedFlowSwaps() error = %v",
			err,
		)
	}

	if len(provider.calls) != 3 {
		t.Fatalf(
			"provider call count = %d, want 3",
			len(provider.calls),
		)
	}

	expectedRanges :=
		[][2]uint64{
			{100, 599},
			{600, 1_099},
			{1_100, 1_299},
		}

	for index, expected := range expectedRanges {
		call :=
			provider.calls[index]

		if call.FromBlock !=
			expected[0] ||
			call.ToBlock !=
				expected[1] {
			t.Fatalf(
				"call %d range = [%d,%d], want [%d,%d]",
				index,
				call.FromBlock,
				call.ToBlock,
				expected[0],
				expected[1],
			)
		}

		if call.AfterID != "" {
			t.Fatalf(
				"call %d after ID = %q, want empty because each chunk has independent pagination",
				index,
				call.AfterID,
			)
		}
	}

	if len(swaps) != 3 {
		t.Fatalf(
			"swap count = %d, want 3",
			len(swaps),
		)
	}

	for index, expectedID := range []string{
		"swap-1",
		"swap-2",
		"swap-3",
	} {
		if swaps[index].ID !=
			expectedID {
			t.Fatalf(
				"swap %d ID = %q, want %q",
				index,
				swaps[index].ID,
				expectedID,
			)
		}
	}
}

func TestFetchBurnRealizedFlowSwapsExcludesEventsBeforeBurnInSameBlock(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	provider :=
		&fakeBurnRealizedFlowProvider{
			pages: map[string][]domain.SwapEvent{
				burnFlowTestPageKey(
					100,
					100,
					"",
				): {
					burnFlowTestSwap(
						poolAddress,
						100,
						4,
						"before-burn",
					),

					burnFlowTestSwap(
						poolAddress,
						100,
						6,
						"after-burn",
					),
				},
			},
		}

	swaps, err :=
		fetchBurnRealizedFlowSwaps(
			context.Background(),
			provider,
			domain.Pool{
				Address: poolAddress,

				Token0Decimals: 6,

				Token1Decimals: 18,
			},
			domain.EventCursor{
				BlockNumber: 100,

				LogIndex: 5,
			},
			100,
			1_000,
		)
	if err != nil {
		t.Fatalf(
			"fetchBurnRealizedFlowSwaps() error = %v",
			err,
		)
	}

	if len(swaps) != 1 {
		t.Fatalf(
			"swap count = %d, want 1",
			len(swaps),
		)
	}

	if swaps[0].ID !=
		"after-burn" {
		t.Fatalf(
			"swap ID = %q, want after-burn",
			swaps[0].ID,
		)
	}
}

func burnFlowTestPageKey(
	fromBlock uint64,
	toBlock uint64,
	afterID string,
) string {
	return fmt.Sprintf(
		"%d:%d:%s",
		fromBlock,
		toBlock,
		afterID,
	)
}

func burnFlowTestSwap(
	poolAddress string,
	blockNumber uint64,
	logIndex int,
	id string,
) domain.SwapEvent {
	return domain.SwapEvent{
		ID: id,

		TxHash: "0x" + id,

		PoolAddress: poolAddress,

		BlockNumber: blockNumber,

		LogIndex: logIndex,

		Timestamp: 1,

		Amount0Raw: big.NewInt(100),

		Amount1Raw: big.NewInt(-200),

		SqrtPriceX96After: big.NewInt(1_000),

		TickAfter: 100,
	}
}

func TestBurnRealizedFlowChunkEnd(
	t *testing.T,
) {
	t.Parallel()

	tests :=
		[]struct {
			name string

			fromBlock    uint64
			throughBlock uint64
			chunkSize    uint64

			want uint64
		}{
			{
				name: "full chunk",

				fromBlock: 100,

				throughBlock: 1_000,

				chunkSize: 500,

				want: 599,
			},
			{
				name: "partial final chunk",

				fromBlock: 600,

				throughBlock: 900,

				chunkSize: 500,

				want: 900,
			},
			{
				name: "single block",

				fromBlock: 100,

				throughBlock: 100,

				chunkSize: 500,

				want: 100,
			},
		}

	for _, test := range tests {
		test :=
			test

		t.Run(
			test.name,
			func(t *testing.T) {
				t.Parallel()

				got :=
					burnRealizedFlowChunkEnd(
						test.fromBlock,
						test.throughBlock,
						test.chunkSize,
					)

				if got != test.want {
					t.Fatalf(
						"burnRealizedFlowChunkEnd() = %d, want %d",
						got,
						test.want,
					)
				}
			},
		)
	}
}
