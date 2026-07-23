package services

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
	"github.com/shopspring/decimal"
)

type fakeValidationProvider struct {
	head domain.IndexedHead
	pool domain.Pool

	swaps []domain.SwapEvent

	snapshots map[uint64]domain.PoolSnapshot
}

func (f *fakeValidationProvider) IndexedHead(
	context.Context,
) (domain.IndexedHead, error) {
	return f.head, nil
}

func (f *fakeValidationProvider) PoolMetadata(
	context.Context,
	string,
) (domain.Pool, error) {
	return f.pool, nil
}

func (f *fakeValidationProvider) PoolSnapshotAt(
	_ context.Context,
	_ string,
	blockNumber uint64,
) (domain.PoolSnapshot, error) {
	snapshot, exists :=
		f.snapshots[blockNumber]

	if !exists {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"snapshot %d not found",
			blockNumber,
		)
	}

	return snapshot, nil
}

func (f *fakeValidationProvider) FetchSwapsPage(
	_ context.Context,
	_ string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	_ int,
	_ int,
	_ int,
) ([]domain.SwapEvent, error) {
	if afterID != "" {
		return nil, nil
	}

	result := make(
		[]domain.SwapEvent,
		0,
	)

	for _, swap := range f.swaps {
		if swap.BlockNumber >= fromBlock &&
			swap.BlockNumber <= toBlock {
			result = append(
				result,
				swap,
			)
		}
	}

	return result, nil
}

type fakeValidationRepository struct {
	contexts map[domain.EventCursor]repositories.SwapValidationContext
	input    domain.ReconstructionInput
}

func (f *fakeValidationRepository) LoadSwapValidationContext(
	_ context.Context,
	_ string,
	cursor domain.EventCursor,
) (repositories.SwapValidationContext, error) {
	value, exists :=
		f.contexts[cursor]

	if !exists {
		return repositories.SwapValidationContext{}, fmt.Errorf(
			"context %s not found",
			cursor,
		)
	}

	return value, nil
}

func (f *fakeValidationRepository) LoadReconstructionInputFromSnapshot(
	context.Context,
	domain.PoolSnapshot,
) (domain.ReconstructionInput, error) {
	return f.input, nil
}

type fakeValidationSimulator struct {
	result *uniswapv3.ExactInputResult

	requests []uniswapv3.ExactInputRequest
}

func (f *fakeValidationSimulator) SimulateExactInput(
	_ *domain.ReconstructedPool,
	req uniswapv3.ExactInputRequest,
) (*uniswapv3.ExactInputResult, error) {
	f.requests = append(
		f.requests,
		uniswapv3.ExactInputRequest{
			AmountIn: cloneBigInt(
				req.AmountIn,
			),

			ZeroForOne: req.ZeroForOne,
		},
	)

	return cloneValidationSimulationResult(
		f.result,
	), nil
}

func TestFirstSwapsPerBlockNewestFirst(
	t *testing.T,
) {
	t.Parallel()

	actual :=
		firstSwapsPerBlockNewestFirst(
			[]domain.SwapEvent{
				{
					ID: "101-late",

					BlockNumber: 101,

					LogIndex: 9,
				},
				{
					ID: "100-first",

					BlockNumber: 100,

					LogIndex: 2,
				},
				{
					ID: "102-first",

					BlockNumber: 102,

					LogIndex: 7,
				},
				{
					ID: "101-first",

					BlockNumber: 101,

					LogIndex: 3,
				},
				{
					ID: "100-late",

					BlockNumber: 100,

					LogIndex: 8,
				},
			},
		)

	expected := []string{
		"102-first",
		"101-first",
		"100-first",
	}

	if len(actual) != len(expected) {
		t.Fatalf(
			"len(first swaps) = %d, want %d",
			len(actual),
			len(expected),
		)
	}

	for index := range expected {
		if actual[index].ID != expected[index] {
			t.Fatalf(
				"first swap %d = %q, want %q",
				index,
				actual[index].ID,
				expected[index],
			)
		}
	}
}

func TestValidateCleanFirstSwapsPerBlockSkipsUncleanAndContinues(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0x0000000000000000000000000000000000000001"

	q96 := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	clean := validationSwap(
		poolAddress,
		100,
		2,
		"clean",
		q96,
	)

	incomplete := validationSwap(
		poolAddress,
		101,
		3,
		"incomplete",
		q96,
	)

	priorAction := validationSwap(
		poolAddress,
		102,
		5,
		"prior-action",
		q96,
	)

	laterInCleanBlock := validationSwap(
		poolAddress,
		100,
		8,
		"clean-later",
		q96,
	)

	provider := &fakeValidationProvider{
		head: domain.IndexedHead{
			BlockNumber: 102,
		},

		pool: domain.Pool{
			Address: poolAddress,

			Token0Decimals: 18,

			Token1Decimals: 18,

			CreatedBlock: 1,
		},

		swaps: []domain.SwapEvent{
			laterInCleanBlock,
			priorAction,
			clean,
			incomplete,
		},

		snapshots: map[uint64]domain.PoolSnapshot{
			99: {
				PoolAddress: poolAddress,

				BlockNumber: 99,

				Tick: "0",

				SqrtPriceX96: decimal.NewFromBigInt(
					q96,
					0,
				),

				ActiveLiquidity: decimal.NewFromInt(
					1_000,
				),
			},
		},
	}

	repository := &fakeValidationRepository{
		contexts: map[domain.EventCursor]repositories.SwapValidationContext{
			clean.Cursor(): {
				Cursor: clean.Cursor(),

				IndexedThrough: 102,
			},

			incomplete.Cursor(): {
				Cursor: incomplete.Cursor(),

				IndexedThrough: 100,
			},

			priorAction.Cursor(): {
				Cursor: priorAction.Cursor(),

				IndexedThrough: 102,

				PriorLiquidityActionCount: 1,
			},
		},

		input: validationReconstructionInput(
			poolAddress,
			99,
			q96,
		),
	}

	simulator := &fakeValidationSimulator{
		result: exactValidationSimulationResult(
			q96,
		),
	}

	service := NewSwapValidationService(
		provider,
		repository,
		simulator,
	)

	report, err :=
		service.ValidateCleanFirstSwapsPerBlock(
			context.Background(),
			SwapValidationRequest{
				PoolAddress: poolAddress,

				FromBlock: 100,

				ToBlock: 102,

				PageSize: 100,

				MaxSamples: 1,

				BlockWindowSize: 3,

				MaxCandidateBlocks: 3,
			},
		)
	if err != nil {
		t.Fatalf(
			"ValidateCleanFirstSwapsPerBlock() error = %v",
			err,
		)
	}

	if report.CandidateBlocks != 3 {
		t.Fatalf(
			"CandidateBlocks = %d, want 3",
			report.CandidateBlocks,
		)
	}

	if len(report.Skipped) != 2 {
		t.Fatalf(
			"len(Skipped) = %d, want 2",
			len(report.Skipped),
		)
	}

	if report.Skipped[0].Reason !=
		SwapValidationSkipIncompleteLPIndex {
		t.Fatalf(
			"skip[0] reason = %q",
			report.Skipped[0].Reason,
		)
	}

	if report.Skipped[1].Reason !=
		SwapValidationSkipPriorLiquidityAction {
		t.Fatalf(
			"skip[1] reason = %q",
			report.Skipped[1].Reason,
		)
	}

	if len(report.Results) != 1 ||
		report.Results[0].SwapID != clean.ID {
		t.Fatalf(
			"unexpected results: %+v",
			report.Results,
		)
	}

	if !report.Results[0].ExactMatch {
		t.Fatal(
			"ExactMatch = false, want true",
		)
	}

	if len(simulator.requests) != 1 ||
		!simulator.requests[0].ZeroForOne {
		t.Fatalf(
			"unexpected simulator requests: %+v",
			simulator.requests,
		)
	}

	assertValidationBigIntEqual(
		t,
		simulator.requests[0].AmountIn,
		big.NewInt(1_000),
	)
}

func TestValidateSwapSimulationResultRejectsBrokenFeeAccounting(
	t *testing.T,
) {
	t.Parallel()

	q96 := new(big.Int).Lsh(
		big.NewInt(1),
		96,
	)

	result :=
		exactValidationSimulationResult(
			q96,
		)

	result.FeeAmount =
		big.NewInt(2)

	if err := validateSwapSimulationResult(
		result,
		big.NewInt(1_000),
	); err == nil {
		t.Fatal(
			"validateSwapSimulationResult() " +
				"expected accounting error",
		)
	}
}

func validationSwap(
	poolAddress string,
	blockNumber uint64,
	logIndex int,
	id string,
	sqrtPriceX96 *big.Int,
) domain.SwapEvent {
	return domain.SwapEvent{
		ID: id,

		TxHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

		PoolAddress: poolAddress,

		BlockNumber: blockNumber,

		LogIndex: logIndex,

		Timestamp: 1_700_000_000,

		Amount0Raw: big.NewInt(1_000),

		Amount1Raw: big.NewInt(-900),

		SqrtPriceX96After: cloneBigInt(
			sqrtPriceX96,
		),

		TickAfter: 0,
	}
}

func validationReconstructionInput(
	poolAddress string,
	blockNumber uint64,
	sqrtPriceX96 *big.Int,
) domain.ReconstructionInput {
	currentTick := 0

	return domain.ReconstructionInput{
		Snapshot: domain.ReconstructionSnapshot{
			PoolAddress: poolAddress,

			BlockNumber: blockNumber,

			SqrtPriceX96: cloneBigInt(
				sqrtPriceX96,
			),

			CurrentTick: &currentTick,

			Liquidity: big.NewInt(1_000),
		},

		Changes: []domain.LiquidityChange{
			{
				ID: "mint",

				BlockNumber: 1,

				LogIndex: 1,

				TickLower: -100,

				TickUpper: 100,

				LiquidityDelta: big.NewInt(1_000),
			},
		},
	}
}

func exactValidationSimulationResult(
	sqrtPriceX96 *big.Int,
) *uniswapv3.ExactInputResult {
	return &uniswapv3.ExactInputResult{
		AmountIn: big.NewInt(1_000),

		AmountInLessFee: big.NewInt(999),

		AmountOut: big.NewInt(900),

		FeeAmount: big.NewInt(1),

		LiquidityAfter: big.NewInt(1_000),

		TickBefore: 0,

		TickAfter: 0,

		TickAfterApprox: 0,

		CrossedTicks: 0,

		SwapSteps: 1,

		SqrtPriceBeforeX96: cloneBigInt(
			sqrtPriceX96,
		),

		SqrtPriceAfterX96: cloneBigInt(
			sqrtPriceX96,
		),
	}
}

func cloneValidationSimulationResult(
	value *uniswapv3.ExactInputResult,
) *uniswapv3.ExactInputResult {
	if value == nil {
		return nil
	}

	result := *value

	result.AmountIn =
		cloneBigInt(
			value.AmountIn,
		)

	result.AmountInLessFee =
		cloneBigInt(
			value.AmountInLessFee,
		)

	result.AmountOut =
		cloneBigInt(
			value.AmountOut,
		)

	result.FeeAmount =
		cloneBigInt(
			value.FeeAmount,
		)

	result.LiquidityAfter =
		cloneBigInt(
			value.LiquidityAfter,
		)

	result.SqrtPriceBeforeX96 =
		cloneBigInt(
			value.SqrtPriceBeforeX96,
		)

	result.SqrtPriceAfterX96 =
		cloneBigInt(
			value.SqrtPriceAfterX96,
		)

	return &result
}

func assertValidationBigIntEqual(
	t *testing.T,
	actual *big.Int,
	expected *big.Int,
) {
	t.Helper()

	if actual == nil ||
		expected == nil ||
		actual.Cmp(expected) != 0 {
		t.Fatalf(
			"actual = %v, want %v",
			actual,
			expected,
		)
	}
}
