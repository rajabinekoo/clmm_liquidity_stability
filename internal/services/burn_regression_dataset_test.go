package services

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type fakeBurnRealizedDatasetAnalyzer struct {
	reports map[string]BurnRealizedOutcomeReport

	calls int

	requestedIndexedThrough []uint64
}

func (f *fakeBurnRealizedDatasetAnalyzer) Analyze(
	_ context.Context,
	req BurnRealizedOutcomeRequest,
) (BurnRealizedOutcomeReport, error) {
	f.calls++

	f.requestedIndexedThrough =
		append(
			f.requestedIndexedThrough,
			req.IndexedThrough,
		)

	report, exists :=
		f.reports[req.Sample.Burn.EventKey()]
	if !exists {
		return BurnRealizedOutcomeReport{}, fmt.Errorf(
			"missing outcome report for %s",
			req.Sample.Burn.EventKey(),
		)
	}

	return cloneBurnRealizedReport(
		report,
	), nil
}

func TestBurnRealizedDatasetBuildsEventHorizonPairs(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	firstSample :=
		regressionSampleFixture(
			t,
			collectorBurnCandidate(
				poolAddress,
				100,
				10,
				100,
			),
		)

	secondSample :=
		regressionSampleFixture(
			t,
			collectorBurnCandidate(
				poolAddress,
				110,
				10,
				100,
			),
		)

	horizons :=
		[]BurnOutcomeHorizon{
			{
				Label: "h10",

				Blocks: 10,
			},
			{
				Label: "h20",

				Blocks: 20,
			},
		}

	firstSkip :=
		BurnRealizedOutcomeSkip{
			Burn: cloneBurnCandidate(
				firstSample.Burn,
			),

			HorizonLabel: "h20",

			HorizonBlocks: 20,

			FutureBlock: firstSample.
				Burn.
				Cursor.
				BlockNumber +
				20,

			Reason: BurnOutcomeSkipFutureBlockNotIndexed,

			Detail: "future block is not indexed",
		}

	analyzer :=
		&fakeBurnRealizedDatasetAnalyzer{
			reports: map[string]BurnRealizedOutcomeReport{
				firstSample.Burn.EventKey(): {
					Burn: cloneBurnCandidate(
						firstSample.Burn,
					),

					IndexedThrough: 1_000,

					Outcomes: []BurnRealizedHorizonOutcome{
						regressionOutcomeFixture(
							firstSample,
							horizons[0],
							900,
						),
					},

					Skipped: []BurnRealizedOutcomeSkip{
						firstSkip,
					},
				},

				secondSample.Burn.EventKey(): {
					Burn: cloneBurnCandidate(
						secondSample.Burn,
					),

					IndexedThrough: 1_000,

					Outcomes: []BurnRealizedHorizonOutcome{
						regressionOutcomeFixture(
							secondSample,
							horizons[0],
							900,
						),
						regressionOutcomeFixture(
							secondSample,
							horizons[1],
							800,
						),
					},
				},
			},
		}

	service :=
		NewBurnRealizedDatasetService(
			analyzer,
		)

	report, err :=
		service.Build(
			context.Background(),
			BurnRealizedDatasetRequest{
				Collection: regressionCollectionFixture(
					poolAddress,
					[]BurnEventSample{
						firstSample,
						secondSample,
					},
				),

				Horizons: horizons,

				ZeroForOneAmountsIn: collectorAmountGrid(),

				OneForZeroAmountsIn: collectorAmountGrid(),

				ThresholdsBps: collectorThresholds(),
			},
		)
	if err != nil {
		t.Fatalf(
			"Build() error = %v",
			err,
		)
	}

	if analyzer.calls != 2 {
		t.Fatalf(
			"analyzer calls = %d, want 2",
			analyzer.calls,
		)
	}

	if len(
		analyzer.requestedIndexedThrough,
	) != 2 {
		t.Fatalf(
			"indexed-through request count = %d, want 2",
			len(
				analyzer.requestedIndexedThrough,
			),
		)
	}

	if analyzer.requestedIndexedThrough[0] != 0 {
		t.Fatalf(
			"first requested indexed-through = %d, want 0",
			analyzer.requestedIndexedThrough[0],
		)
	}

	if analyzer.requestedIndexedThrough[1] != 1_000 {
		t.Fatalf(
			"second requested indexed-through = %d, want 1000",
			analyzer.requestedIndexedThrough[1],
		)
	}

	if report.BurnSamples != 2 ||
		report.HorizonsPerSample != 2 ||
		report.CandidateHorizonPairs != 4 ||
		report.ObservedHorizonPairs != 3 ||
		report.SkippedHorizonPairs != 1 {
		t.Fatalf(
			"unexpected counters: burns=%d horizons=%d candidate=%d observed=%d skipped=%d",
			report.BurnSamples,
			report.HorizonsPerSample,
			report.CandidateHorizonPairs,
			report.ObservedHorizonPairs,
			report.SkippedHorizonPairs,
		)
	}

	if len(report.Observations) != 3 ||
		len(report.Skipped) != 1 {
		t.Fatalf(
			"observations=%d skipped=%d",
			len(report.Observations),
			len(report.Skipped),
		)
	}

	if report.OutcomeIndexedThrough !=
		1_000 {
		t.Fatalf(
			"OutcomeIndexedThrough = %d, want 1000",
			report.OutcomeIndexedThrough,
		)
	}

	if report.Observations[0].
		HorizonLabel !=
		"h10" {
		t.Fatalf(
			"first horizon = %q, want h10",
			report.Observations[0].
				HorizonLabel,
		)
	}

	if report.Skipped[0].Reason !=
		BurnOutcomeSkipFutureBlockNotIndexed {
		t.Fatalf(
			"skip reason = %q",
			report.Skipped[0].Reason,
		)
	}
}

func TestBurnRealizedDatasetRejectsIndexedHeadDrift(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	firstSample :=
		regressionSampleFixture(
			t,
			collectorBurnCandidate(
				poolAddress,
				100,
				10,
				100,
			),
		)

	secondSample :=
		regressionSampleFixture(
			t,
			collectorBurnCandidate(
				poolAddress,
				110,
				10,
				100,
			),
		)

	horizon :=
		BurnOutcomeHorizon{
			Label: "h10",

			Blocks: 10,
		}

	service :=
		NewBurnRealizedDatasetService(
			&fakeBurnRealizedDatasetAnalyzer{
				reports: map[string]BurnRealizedOutcomeReport{
					firstSample.Burn.EventKey(): {
						Burn: cloneBurnCandidate(
							firstSample.Burn,
						),

						IndexedThrough: 1_000,

						Outcomes: []BurnRealizedHorizonOutcome{
							regressionOutcomeFixture(
								firstSample,
								horizon,
								900,
							),
						},
					},

					secondSample.Burn.EventKey(): {
						Burn: cloneBurnCandidate(
							secondSample.Burn,
						),

						IndexedThrough: 1_001,

						Outcomes: []BurnRealizedHorizonOutcome{
							regressionOutcomeFixture(
								secondSample,
								horizon,
								900,
							),
						},
					},
				},
			},
		)

	_, err :=
		service.Build(
			context.Background(),
			BurnRealizedDatasetRequest{
				Collection: regressionCollectionFixture(
					poolAddress,
					[]BurnEventSample{
						firstSample,
						secondSample,
					},
				),

				Horizons: []BurnOutcomeHorizon{
					horizon,
				},

				ZeroForOneAmountsIn: collectorAmountGrid(),

				OneForZeroAmountsIn: collectorAmountGrid(),

				ThresholdsBps: collectorThresholds(),
			},
		)

	if err == nil {
		t.Fatal(
			"Build() expected indexed-head drift error",
		)
	}
}

func TestNewBurnRegressionObservationDoesNotAliasInputs(
	t *testing.T,
) {
	t.Parallel()

	sample :=
		regressionSampleFixture(
			t,
			collectorBurnCandidate(
				"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				100,
				10,
				100,
			),
		)

	outcome :=
		regressionOutcomeFixture(
			sample,
			BurnOutcomeHorizon{
				Label: "h10",

				Blocks: 10,
			},
			900,
		)

	observation, err :=
		newBurnRegressionObservation(
			sample,
			outcome,
		)
	if err != nil {
		t.Fatalf(
			"newBurnRegressionObservation() error = %v",
			err,
		)
	}

	sample.
		LiquidityRemoved.
		SetInt64(
			999,
		)

	outcome.
		FutureActiveLiquidity.
		SetInt64(
			999,
		)

	outcome.
		FutureSqrtPriceX96.
		SetInt64(
			1,
		)

	if observation.
		LiquidityRemoved.
		Cmp(
			big.NewInt(100),
		) != 0 {
		t.Fatalf(
			"observation liquidity removed changed: %s",
			observation.LiquidityRemoved,
		)
	}

	if observation.
		FutureActiveLiquidity.
		Cmp(
			big.NewInt(900),
		) != 0 {
		t.Fatalf(
			"observation future liquidity changed: %s",
			observation.FutureActiveLiquidity,
		)
	}

	expectedQ96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	if observation.
		FutureSqrtPriceX96.
		Cmp(
			expectedQ96,
		) != 0 {
		t.Fatalf(
			"observation future sqrt price changed: %s",
			observation.FutureSqrtPriceX96,
		)
	}
}

func TestNormalizeBurnDatasetHorizonsRejectsDuplicateLabels(
	t *testing.T,
) {
	t.Parallel()

	_, err :=
		normalizeBurnDatasetHorizons(
			[]BurnOutcomeHorizon{
				{
					Label: "short",

					Blocks: 10,
				},
				{
					Label: "short",

					Blocks: 20,
				},
			},
		)

	if err == nil {
		t.Fatal(
			"normalizeBurnDatasetHorizons() expected duplicate-label error",
		)
	}
}

func regressionSampleFixture(
	t *testing.T,
	burn domain.BurnCandidate,
) BurnEventSample {
	t.Helper()

	preBurn :=
		collectorPreBurnFixture(
			burn,
			1_000,
		)

	impact :=
		collectorImpactFixture(
			preBurn,
		)

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

	return sample
}

func regressionCollectionFixture(
	poolAddress string,
	samples []BurnEventSample,
) BurnSampleCollectionReport {
	lastCursor :=
		samples[len(samples)-1].Burn.Cursor

	return BurnSampleCollectionReport{
		PoolAddress: poolAddress,

		FromBlock: 100,

		ToBlock: 200,

		IndexedThrough: 500,

		Pages: 1,

		CandidateEvents: len(samples),

		AnalyzedEvents: len(samples),

		SkippedEvents: 0,

		LastProcessedCursor: &lastCursor,

		Samples: samples,

		Skipped: nil,
	}
}

func regressionOutcomeFixture(
	sample BurnEventSample,
	horizon BurnOutcomeHorizon,
	futureLiquidity int64,
) BurnRealizedHorizonOutcome {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	zeroForOne :=
		regressionDirectionalOutcomeFixture(
			sample.ZeroForOne,
			true,
			decimal.NewFromInt(1),
		)

	oneForZero :=
		regressionDirectionalOutcomeFixture(
			sample.OneForZero,
			false,
			decimal.NewFromInt(2),
		)

	return BurnRealizedHorizonOutcome{
		HorizonLabel: horizon.Label,

		HorizonBlocks: horizon.Blocks,

		FutureBlock: sample.
			Burn.
			Cursor.
			BlockNumber +
			horizon.Blocks,

		FutureCurrentTick: sample.CurrentTick,

		FutureSqrtPriceX96: q96,

		FutureActiveLiquidity: big.NewInt(
			futureLiquidity,
		),

		ZeroForOne: zeroForOne,

		OneForZero: oneForZero,

		TotalRealizedDeltaPIAUCBps: decimal.NewFromInt(3),

		TotalRealizedDeteriorationBps: decimal.NewFromInt(3),

		MaxDirectionalDeteriorationBps: decimal.NewFromInt(2),
	}
}

func regressionDirectionalOutcomeFixture(
	immediate BurnDirectionalImpact,
	zeroForOne bool,
	delta decimal.Decimal,
) BurnRealizedDirectionalOutcome {
	futureAUC :=
		immediate.
			BaseAUCBps.
			Add(
				delta,
			)

	depths :=
		make(
			[]DepthDelta,
			len(
				immediate.
					DepthDeltas,
			),
		)

	for index, depth := range immediate.DepthDeltas {
		futureDepth :=
			depth.
				BaseDepthAmount.
				Sub(
					decimal.NewFromInt(1),
				)

		if futureDepth.IsNegative() {
			futureDepth =
				decimal.Zero
		}

		depths[index] =
			DepthDelta{
				ThresholdBps: depth.ThresholdBps,

				BaseDepthAmount: depth.BaseDepthAmount,

				CounterfactualDepthAmount: futureDepth,

				DeltaDepthAmount: depth.
					BaseDepthAmount.
					Sub(
						futureDepth,
					),

				BaseBreached: depth.BaseBreached,

				CounterfactualBreached: depth.BaseBreached,
			}
	}

	return BurnRealizedDirectionalOutcome{
		ZeroForOne: zeroForOne,

		PreEventAUCBps: immediate.BaseAUCBps,

		FutureAUCBps: futureAUC,

		RealizedDeltaPIAUCBps: delta,

		RealizedDeteriorationBps: delta,

		DepthDeltas: depths,
	}
}

func cloneBurnRealizedReport(
	value BurnRealizedOutcomeReport,
) BurnRealizedOutcomeReport {
	result :=
		value

	result.Burn =
		cloneBurnCandidate(
			value.Burn,
		)

	result.Outcomes =
		make(
			[]BurnRealizedHorizonOutcome,
			len(value.Outcomes),
		)

	for index, outcome := range value.Outcomes {
		result.Outcomes[index] =
			outcome

		result.Outcomes[index].
			FutureSqrtPriceX96 =
			cloneRegressionBigInt(
				outcome.
					FutureSqrtPriceX96,
			)

		result.Outcomes[index].
			FutureActiveLiquidity =
			cloneRegressionBigInt(
				outcome.
					FutureActiveLiquidity,
			)

		result.Outcomes[index].
			ZeroForOne =
			cloneBurnRealizedDirectionalOutcome(
				outcome.ZeroForOne,
			)

		result.Outcomes[index].
			OneForZero =
			cloneBurnRealizedDirectionalOutcome(
				outcome.OneForZero,
			)
	}

	result.Skipped =
		make(
			[]BurnRealizedOutcomeSkip,
			len(value.Skipped),
		)

	for index, skipped := range value.Skipped {
		result.Skipped[index] =
			cloneBurnRealizedOutcomeSkip(
				skipped,
			)
	}

	return result
}
