package services

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type fakeBurnRealizedOutcomeProvider struct {
	head domain.IndexedHead

	snapshots map[uint64]domain.PoolSnapshot

	snapshotCalls int
}

func (f *fakeBurnRealizedOutcomeProvider) IndexedHead(
	_ context.Context,
) (domain.IndexedHead, error) {
	return f.head, nil
}

func (f *fakeBurnRealizedOutcomeProvider) PoolSnapshotAt(
	_ context.Context,
	poolAddress string,
	blockNumber uint64,
) (domain.PoolSnapshot, error) {
	f.snapshotCalls++

	snapshot, exists :=
		f.snapshots[blockNumber]
	if !exists {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"missing snapshot for block %d",
			blockNumber,
		)
	}

	if normalizeAddress(
		snapshot.PoolAddress,
	) != normalizeAddress(
		poolAddress,
	) {
		return domain.PoolSnapshot{}, fmt.Errorf(
			"snapshot pool mismatch",
		)
	}

	return snapshot, nil
}

type fakeBurnRealizedOutcomeRepository struct {
	inputs map[uint64]domain.ReconstructionInput

	calls int
}

func (f *fakeBurnRealizedOutcomeRepository) LoadReconstructionInputFromSnapshot(
	_ context.Context,
	snapshot domain.PoolSnapshot,
) (domain.ReconstructionInput, error) {
	f.calls++

	input, exists :=
		f.inputs[snapshot.BlockNumber]
	if !exists {
		return domain.ReconstructionInput{}, fmt.Errorf(
			"missing reconstruction input for block %d",
			snapshot.BlockNumber,
		)
	}

	return input, nil
}

func TestBurnRealizedOutcomeAnalyzeMultipleHorizons(
	t *testing.T,
) {
	t.Parallel()

	sample, curveService :=
		burnRealizedOutcomeFixture(
			t,
		)

	poolAddress :=
		sample.Burn.PoolAddress

	firstBlock :=
		sample.Burn.Cursor.BlockNumber +
			10

	secondBlock :=
		sample.Burn.Cursor.BlockNumber +
			20

	provider :=
		&fakeBurnRealizedOutcomeProvider{
			head: domain.IndexedHead{
				BlockNumber: secondBlock + 100,
			},

			snapshots: map[uint64]domain.PoolSnapshot{
				firstBlock: burnOutcomeSnapshot(
					poolAddress,
					firstBlock,
					800_000_000_000,
				),

				secondBlock: burnOutcomeSnapshot(
					poolAddress,
					secondBlock,
					600_000_000_000,
				),
			},
		}

	repository :=
		&fakeBurnRealizedOutcomeRepository{
			inputs: map[uint64]domain.ReconstructionInput{
				firstBlock: burnOutcomeReconstructionInput(
					poolAddress,
					firstBlock,
					800_000_000_000,
				),

				secondBlock: burnOutcomeReconstructionInput(
					poolAddress,
					secondBlock,
					600_000_000_000,
				),
			},
		}

	service :=
		NewBurnRealizedOutcomeService(
			provider,
			repository,
			curveService,
		)

	report, err :=
		service.Analyze(
			context.Background(),
			BurnRealizedOutcomeRequest{
				Sample: sample,

				Horizons: []BurnOutcomeHorizon{
					{
						Label: "h10",

						Blocks: 10,
					},
					{
						Label: "h20",

						Blocks: 20,
					},
				},

				ZeroForOneAmountsIn: burnImpactAmountGrid(),

				OneForZeroAmountsIn: burnImpactAmountGrid(),

				ThresholdsBps: burnOutcomeThresholds(),
			},
		)
	if err != nil {
		t.Fatalf(
			"Analyze() error = %v",
			err,
		)
	}

	if len(report.Outcomes) != 2 ||
		len(report.Skipped) != 0 {
		t.Fatalf(
			"outcomes=%d skipped=%d, want 2 and 0",
			len(report.Outcomes),
			len(report.Skipped),
		)
	}

	if provider.snapshotCalls != 2 ||
		repository.calls != 2 {
		t.Fatalf(
			"snapshot calls=%d repository calls=%d",
			provider.snapshotCalls,
			repository.calls,
		)
	}

	if report.Outcomes[0].FutureBlock !=
		firstBlock {
		t.Fatalf(
			"first future block=%d, want=%d",
			report.Outcomes[0].FutureBlock,
			firstBlock,
		)
	}

	if report.Outcomes[1].FutureBlock !=
		secondBlock {
		t.Fatalf(
			"second future block=%d, want=%d",
			report.Outcomes[1].FutureBlock,
			secondBlock,
		)
	}

	if report.Outcomes[0].
		FutureActiveLiquidity.
		Cmp(
			big.NewInt(
				800_000_000_000,
			),
		) != 0 {
		t.Fatalf(
			"first future liquidity=%s",
			report.Outcomes[0].FutureActiveLiquidity,
		)
	}

	if !report.Outcomes[1].
		TotalRealizedDeteriorationBps.
		GreaterThan(
			report.Outcomes[0].
				TotalRealizedDeteriorationBps,
		) {
		t.Fatalf(
			"later lower-liquidity outcome should deteriorate more: first=%s second=%s",
			report.Outcomes[0].
				TotalRealizedDeteriorationBps,
			report.Outcomes[1].
				TotalRealizedDeteriorationBps,
		)
	}
}

func TestBurnRealizedOutcomeSkipsUnindexedHorizon(
	t *testing.T,
) {
	t.Parallel()

	sample, curveService :=
		burnRealizedOutcomeFixture(
			t,
		)

	service :=
		NewBurnRealizedOutcomeService(
			&fakeBurnRealizedOutcomeProvider{
				head: domain.IndexedHead{
					BlockNumber: sample.
						Burn.
						Cursor.
						BlockNumber +
						5,
				},

				snapshots: map[uint64]domain.PoolSnapshot{},
			},
			&fakeBurnRealizedOutcomeRepository{
				inputs: map[uint64]domain.ReconstructionInput{},
			},
			curveService,
		)

	report, err :=
		service.Analyze(
			context.Background(),
			BurnRealizedOutcomeRequest{
				Sample: sample,

				Horizons: []BurnOutcomeHorizon{
					{
						Label: "h10",

						Blocks: 10,
					},
				},

				ZeroForOneAmountsIn: burnImpactAmountGrid(),

				OneForZeroAmountsIn: burnImpactAmountGrid(),

				ThresholdsBps: burnOutcomeThresholds(),
			},
		)
	if err != nil {
		t.Fatalf(
			"Analyze() error = %v",
			err,
		)
	}

	if len(report.Outcomes) != 0 ||
		len(report.Skipped) != 1 {
		t.Fatalf(
			"outcomes=%d skipped=%d, want 0 and 1",
			len(report.Outcomes),
			len(report.Skipped),
		)
	}

	if report.Skipped[0].Reason !=
		BurnOutcomeSkipFutureBlockNotIndexed {
		t.Fatalf(
			"skip reason=%q",
			report.Skipped[0].Reason,
		)
	}
}

func TestBurnRealizedOutcomeSkipsZeroFutureLiquidity(
	t *testing.T,
) {
	t.Parallel()

	sample, curveService :=
		burnRealizedOutcomeFixture(
			t,
		)

	futureBlock :=
		sample.Burn.Cursor.BlockNumber +
			10

	service :=
		NewBurnRealizedOutcomeService(
			&fakeBurnRealizedOutcomeProvider{
				head: domain.IndexedHead{
					BlockNumber: futureBlock + 100,
				},

				snapshots: map[uint64]domain.PoolSnapshot{
					futureBlock: burnOutcomeSnapshot(
						sample.Burn.PoolAddress,
						futureBlock,
						0,
					),
				},
			},
			&fakeBurnRealizedOutcomeRepository{
				inputs: map[uint64]domain.ReconstructionInput{
					futureBlock: burnOutcomeReconstructionInput(
						sample.Burn.PoolAddress,
						futureBlock,
						0,
					),
				},
			},
			curveService,
		)

	report, err :=
		service.Analyze(
			context.Background(),
			BurnRealizedOutcomeRequest{
				Sample: sample,

				Horizons: []BurnOutcomeHorizon{
					{
						Label: "h10",

						Blocks: 10,
					},
				},

				ZeroForOneAmountsIn: burnImpactAmountGrid(),

				OneForZeroAmountsIn: burnImpactAmountGrid(),

				ThresholdsBps: burnOutcomeThresholds(),
			},
		)
	if err != nil {
		t.Fatalf(
			"Analyze() error = %v",
			err,
		)
	}

	if len(report.Outcomes) != 0 ||
		len(report.Skipped) != 1 {
		t.Fatalf(
			"outcomes=%d skipped=%d, want 0 and 1",
			len(report.Outcomes),
			len(report.Skipped),
		)
	}

	if report.Skipped[0].Reason !=
		BurnOutcomeSkipZeroFutureActiveLiquidity {
		t.Fatalf(
			"skip reason=%q",
			report.Skipped[0].Reason,
		)
	}
}

func TestNormalizeBurnOutcomeHorizonsRejectsNonIncreasingValues(
	t *testing.T,
) {
	t.Parallel()

	_, err :=
		normalizeBurnOutcomeHorizons(
			100,
			[]BurnOutcomeHorizon{
				{
					Label: "h20",

					Blocks: 20,
				},
				{
					Label: "h10",

					Blocks: 10,
				},
			},
		)

	if err == nil {
		t.Fatal(
			"normalizeBurnOutcomeHorizons() expected ordering error",
		)
	}
}

func burnRealizedOutcomeFixture(
	t *testing.T,
) (BurnEventSample, *PriceImpactCurveService) {
	t.Helper()

	preBurn :=
		burnImpactPreBurnFixture(
			1_000_000_000_000,
			100_000_000_000,
		)

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

	curveService :=
		NewPriceImpactCurveService(
			simulator,
		)

	impactService :=
		NewBurnEventImpactService(
			&fakeBurnEventImpactRepository{
				rangeLiquidity: big.NewInt(
					1_000_000_000_000,
				),
			},
			curveService,
		)

	impact, err :=
		impactService.Analyze(
			context.Background(),
			BurnEventImpactRequest{
				PreBurn: preBurn,

				ZeroForOneAmountsIn: burnImpactAmountGrid(),

				OneForZeroAmountsIn: burnImpactAmountGrid(),

				ThresholdsBps: burnOutcomeThresholds(),
			},
		)
	if err != nil {
		t.Fatalf(
			"BurnEventImpactService.Analyze() error = %v",
			err,
		)
	}

	sample, err :=
		newBurnEventSample(
			preBurn,
			impact,
		)
	if err != nil {
		t.Fatalf(
			"newBurnEventSample() error = %v",
			err,
		)
	}

	return sample, curveService
}

func burnOutcomeThresholds() []decimal.Decimal {
	return []decimal.Decimal{
		decimal.NewFromInt(1),
		decimal.NewFromInt(10),
		decimal.NewFromInt(50),
	}
}

func burnOutcomeSnapshot(
	poolAddress string,
	blockNumber uint64,
	liquidity int64,
) domain.PoolSnapshot {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	return domain.PoolSnapshot{
		PoolAddress: poolAddress,

		BlockNumber: blockNumber,

		Tick: "0",

		SqrtPriceX96: decimal.NewFromBigInt(
			q96,
			0,
		),

		ActiveLiquidity: decimal.NewFromInt(
			liquidity,
		),
	}
}

func burnOutcomeReconstructionInput(
	poolAddress string,
	blockNumber uint64,
	liquidity int64,
) domain.ReconstructionInput {
	currentTick :=
		0

	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	input :=
		domain.ReconstructionInput{
			Snapshot: domain.ReconstructionSnapshot{
				PoolAddress: poolAddress,

				BlockNumber: blockNumber,

				SqrtPriceX96: q96,

				CurrentTick: &currentTick,

				Liquidity: big.NewInt(
					liquidity,
				),
			},
		}

	if liquidity > 0 {
		input.Changes =
			[]domain.LiquidityChange{
				{
					ID: "initial-liquidity",

					BlockNumber: 1,

					LogIndex: 1,

					TickLower: -100,

					TickUpper: 100,

					LiquidityDelta: big.NewInt(
						liquidity,
					),
				},
			}
	}

	return input
}
