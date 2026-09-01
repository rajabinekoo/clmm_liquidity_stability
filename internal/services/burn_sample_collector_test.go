package services

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
)

type fakeBurnCandidatePageLoader struct {
	pages []repositories.BurnCandidatePage

	calls int
}

func (f *fakeBurnCandidatePageLoader) LoadBurnCandidatesPage(
	_ context.Context,
	_ repositories.BurnCandidatePageRequest,
) (repositories.BurnCandidatePage, error) {
	if f.calls >= len(f.pages) {
		return repositories.BurnCandidatePage{}, fmt.Errorf(
			"unexpected candidate page call %d",
			f.calls,
		)
	}

	page :=
		f.pages[f.calls]

	f.calls++

	return page, nil
}

type fakeBurnPreStateBuilder struct {
	results map[string]PreBurnStateResult

	calls int
}

func (f *fakeBurnPreStateBuilder) Build(
	_ context.Context,
	burn domain.BurnCandidate,
	_ int,
) (PreBurnStateResult, error) {
	f.calls++

	result, exists :=
		f.results[burn.EventKey()]
	if !exists {
		return PreBurnStateResult{}, fmt.Errorf(
			"missing pre-burn fixture for %s",
			burn.EventKey(),
		)
	}

	return cloneCollectorPreBurnResult(
		result,
	), nil
}

type fakeBurnImpactAnalyzer struct {
	results map[string]BurnEventImpactResult

	calls int
}

func (f *fakeBurnImpactAnalyzer) Analyze(
	_ context.Context,
	req BurnEventImpactRequest,
) (BurnEventImpactResult, error) {
	f.calls++

	result, exists :=
		f.results[req.PreBurn.Burn.EventKey()]
	if !exists {
		return BurnEventImpactResult{}, fmt.Errorf(
			"missing impact fixture for %s",
			req.PreBurn.Burn.EventKey(),
		)
	}

	return cloneCollectorImpactResult(
		result,
	), nil
}

func TestBurnSampleCollectorContinuesAfterDeterministicSkip(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	first :=
		collectorBurnCandidate(
			poolAddress,
			100,
			10,
			100,
		)

	skipped :=
		collectorBurnCandidate(
			poolAddress,
			101,
			10,
			100,
		)

	third :=
		collectorBurnCandidate(
			poolAddress,
			102,
			10,
			100,
		)

	firstPageTail :=
		skipped.Cursor

	repository :=
		&fakeBurnCandidatePageLoader{
			pages: []repositories.BurnCandidatePage{
				{
					PoolAddress: poolAddress,

					FromBlock: 100,

					ToBlock: 200,

					IndexedThrough: 250,

					Candidates: []domain.BurnCandidate{
						first,
						skipped,
					},

					NextCursor: &firstPageTail,
				},
				{
					PoolAddress: poolAddress,

					FromBlock: 100,

					ToBlock: 200,

					IndexedThrough: 250,

					Candidates: []domain.BurnCandidate{
						third,
					},
				},
			},
		}

	firstPreBurn :=
		collectorPreBurnFixture(
			first,
			1_000,
		)

	skippedPreBurn :=
		collectorPreBurnFixture(
			skipped,
			0,
		)

	thirdPreBurn :=
		collectorPreBurnFixture(
			third,
			1_000,
		)

	preBurnBuilder :=
		&fakeBurnPreStateBuilder{
			results: map[string]PreBurnStateResult{
				first.EventKey(): firstPreBurn,

				skipped.EventKey(): skippedPreBurn,

				third.EventKey(): thirdPreBurn,
			},
		}

	impactAnalyzer :=
		&fakeBurnImpactAnalyzer{
			results: map[string]BurnEventImpactResult{
				first.EventKey(): collectorImpactFixture(
					firstPreBurn,
				),

				third.EventKey(): collectorImpactFixture(
					thirdPreBurn,
				),
			},
		}

	collector :=
		NewBurnSampleCollector(
			repository,
			preBurnBuilder,
			impactAnalyzer,
		)

	report, err :=
		collector.Collect(
			context.Background(),
			BurnSampleCollectionRequest{
				PoolAddress: poolAddress,

				FromBlock: 100,

				ToBlock: 200,

				PageSize: 2,

				SwapPageSize: 100,

				MaxCandidates: 10,

				MaxSamples: 2,

				ZeroForOneAmountsIn: collectorAmountGrid(),

				OneForZeroAmountsIn: collectorAmountGrid(),

				ThresholdsBps: collectorThresholds(),
			},
		)
	if err != nil {
		t.Fatalf(
			"Collect() error = %v",
			err,
		)
	}

	if report.Pages != 2 {
		t.Fatalf(
			"Pages = %d, want 2",
			report.Pages,
		)
	}

	if report.CandidateEvents != 3 ||
		report.AnalyzedEvents != 2 ||
		report.SkippedEvents != 1 {
		t.Fatalf(
			"unexpected counts: candidates=%d analyzed=%d skipped=%d",
			report.CandidateEvents,
			report.AnalyzedEvents,
			report.SkippedEvents,
		)
	}

	if len(report.Samples) != 2 {
		t.Fatalf(
			"len(Samples) = %d, want 2",
			len(report.Samples),
		)
	}

	if report.Samples[0].
		Burn.EventKey() !=
		first.EventKey() {
		t.Fatalf(
			"first sample = %s, want %s",
			report.Samples[0].Burn.EventKey(),
			first.EventKey(),
		)
	}

	if report.Samples[1].
		Burn.EventKey() !=
		third.EventKey() {
		t.Fatalf(
			"second sample = %s, want %s",
			report.Samples[1].Burn.EventKey(),
			third.EventKey(),
		)
	}

	if len(report.Skipped) != 1 ||
		report.Skipped[0].Reason !=
			BurnSampleSkipZeroActiveLiquidityBefore {
		t.Fatalf(
			"unexpected skip = %+v",
			report.Skipped,
		)
	}

	if repository.calls != 2 ||
		preBurnBuilder.calls != 3 ||
		impactAnalyzer.calls != 2 {
		t.Fatalf(
			"unexpected calls: pages=%d preburn=%d impact=%d",
			repository.calls,
			preBurnBuilder.calls,
			impactAnalyzer.calls,
		)
	}

	if report.IndexedThrough != 250 {
		t.Fatalf(
			"IndexedThrough = %d, want 250",
			report.IndexedThrough,
		)
	}

	if report.Truncated {
		t.Fatal(
			"Truncated = true, want false",
		)
	}
}

func TestBurnSampleCollectorRejectsNonIncreasingCandidates(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	later :=
		collectorBurnCandidate(
			poolAddress,
			101,
			20,
			100,
		)

	earlier :=
		collectorBurnCandidate(
			poolAddress,
			101,
			10,
			100,
		)

	collector :=
		NewBurnSampleCollector(
			&fakeBurnCandidatePageLoader{
				pages: []repositories.BurnCandidatePage{
					{
						PoolAddress: poolAddress,

						FromBlock: 100,

						ToBlock: 200,

						IndexedThrough: 250,

						Candidates: []domain.BurnCandidate{
							later,
							earlier,
						},
					},
				},
			},
			&fakeBurnPreStateBuilder{
				results: map[string]PreBurnStateResult{
					later.EventKey(): collectorPreBurnFixture(
						later,
						1_000,
					),
				},
			},
			&fakeBurnImpactAnalyzer{
				results: map[string]BurnEventImpactResult{
					later.EventKey(): collectorImpactFixture(
						collectorPreBurnFixture(
							later,
							1_000,
						),
					),
				},
			},
		)

	_, err :=
		collector.Collect(
			context.Background(),
			collectorRequestFixture(
				poolAddress,
			),
		)

	if err == nil {
		t.Fatal(
			"Collect() expected ordering error",
		)
	}
}

func TestBurnSampleCollectorRejectsCheckpointDrift(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	first :=
		collectorBurnCandidate(
			poolAddress,
			100,
			10,
			100,
		)

	second :=
		collectorBurnCandidate(
			poolAddress,
			101,
			10,
			100,
		)

	firstTail :=
		first.Cursor

	preBurnFirst :=
		collectorPreBurnFixture(
			first,
			1_000,
		)

	collector :=
		NewBurnSampleCollector(
			&fakeBurnCandidatePageLoader{
				pages: []repositories.BurnCandidatePage{
					{
						PoolAddress: poolAddress,

						FromBlock: 100,

						ToBlock: 200,

						IndexedThrough: 250,

						Candidates: []domain.BurnCandidate{
							first,
						},

						NextCursor: &firstTail,
					},
					{
						PoolAddress: poolAddress,

						FromBlock: 100,

						ToBlock: 200,

						IndexedThrough: 251,

						Candidates: []domain.BurnCandidate{
							second,
						},
					},
				},
			},
			&fakeBurnPreStateBuilder{
				results: map[string]PreBurnStateResult{
					first.EventKey(): preBurnFirst,
				},
			},
			&fakeBurnImpactAnalyzer{
				results: map[string]BurnEventImpactResult{
					first.EventKey(): collectorImpactFixture(
						preBurnFirst,
					),
				},
			},
		)

	request :=
		collectorRequestFixture(
			poolAddress,
		)

	request.PageSize = 1
	request.MaxSamples = 10
	request.MaxCandidates = 10

	_, err :=
		collector.Collect(
			context.Background(),
			request,
		)

	if err == nil {
		t.Fatal(
			"Collect() expected checkpoint-drift error",
		)
	}
}

func TestNewBurnEventSampleDoesNotAliasInput(
	t *testing.T,
) {
	t.Parallel()

	burn :=
		collectorBurnCandidate(
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			100,
			10,
			100,
		)

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

	impact.LiquidityRemoved.SetInt64(
		999,
	)

	impact.
		RangeLiquidityBeforeBurn.
		SetInt64(
			999,
		)

	impact.
		PreBurnPool.
		SqrtPriceX96.
		SetInt64(
			1,
		)

	if sample.LiquidityRemoved.Cmp(
		big.NewInt(100),
	) != 0 {
		t.Fatalf(
			"sample removed liquidity changed: %s",
			sample.LiquidityRemoved,
		)
	}

	if sample.RangeLiquidityBeforeBurn.Cmp(
		big.NewInt(500),
	) != 0 {
		t.Fatalf(
			"sample range liquidity changed: %s",
			sample.RangeLiquidityBeforeBurn,
		)
	}

	expectedQ96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	if sample.
		SqrtPriceX96BeforeBurn.
		Cmp(
			expectedQ96,
		) != 0 {
		t.Fatalf(
			"sample sqrt price changed: %s",
			sample.SqrtPriceX96BeforeBurn,
		)
	}
}

func collectorRequestFixture(
	poolAddress string,
) BurnSampleCollectionRequest {
	return BurnSampleCollectionRequest{
		PoolAddress: poolAddress,

		FromBlock: 100,

		ToBlock: 200,

		PageSize: 10,

		SwapPageSize: 100,

		MaxCandidates: 10,

		MaxSamples: 1,

		ZeroForOneAmountsIn: collectorAmountGrid(),

		OneForZeroAmountsIn: collectorAmountGrid(),

		ThresholdsBps: collectorThresholds(),
	}
}

func collectorBurnCandidate(
	poolAddress string,
	blockNumber uint64,
	logIndex int,
	removed int64,
) domain.BurnCandidate {
	return domain.BurnCandidate{
		ID: fmt.Sprintf(
			"burn-%d-%d",
			blockNumber,
			logIndex,
		),

		PoolAddress: poolAddress,

		TxHash: fmt.Sprintf(
			"0x%064x",
			blockNumber*1_000+
				uint64(logIndex),
		),

		Cursor: domain.EventCursor{
			BlockNumber: blockNumber,

			LogIndex: logIndex,
		},

		Timestamp: time.Unix(
			1_700_000_000+
				int64(blockNumber),
			0,
		).UTC(),

		TickLower: -100,

		TickUpper: 100,

		LiquidityRemoved: big.NewInt(
			removed,
		),
	}
}

func collectorPreBurnFixture(
	burn domain.BurnCandidate,
	activeLiquidity int64,
) PreBurnStateResult {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	pool :=
		&domain.ReconstructedPool{
			PoolAddress: burn.PoolAddress,

			BlockNumber: burn.Cursor.BlockNumber,

			SqrtPriceX96: q96,

			CurrentTick: 0,

			Liquidity: big.NewInt(
				activeLiquidity,
			),

			Ticks: map[int]*domain.TickState{},

			InitializedTicks: []int{},
		}

	return PreBurnStateResult{
		Burn: cloneBurnCandidate(
			burn,
		),

		Pool: pool,

		SnapshotBlock: burn.Cursor.BlockNumber - 1,

		PriorLiquidityEvents: 1,

		PriorSwapEvents: 1,

		ReplayedEvents: 2,

		BurnRangeActive: true,

		ActiveLiquidityBeforeBurn: big.NewInt(
			activeLiquidity,
		),
	}
}

func collectorImpactFixture(
	preBurn PreBurnStateResult,
) BurnEventImpactResult {
	rangeBefore :=
		big.NewInt(500)

	removed :=
		preBurn.
			Burn.
			LiquidityRemovedCopy()

	rangeAfter :=
		new(big.Int).Sub(
			new(big.Int).Set(
				rangeBefore,
			),
			removed,
		)

	activeAfter :=
		new(big.Int).Sub(
			new(big.Int).Set(
				preBurn.Pool.Liquidity,
			),
			removed,
		)

	postBurnPool :=
		*preBurn.Pool

	postBurnPool.Liquidity =
		activeAfter

	zeroForOne :=
		BurnDirectionalImpact{
			ZeroForOne: true,

			BaseAUCBps: decimal.NewFromInt(10),

			PostBurnAUCBps: decimal.NewFromInt(11),

			DeltaAUCBps: decimal.NewFromInt(1),

			LSISBps: decimal.NewFromInt(1),

			DepthDeltas: collectorDepthDeltas(),
		}

	oneForZero :=
		BurnDirectionalImpact{
			ZeroForOne: false,

			BaseAUCBps: decimal.NewFromInt(20),

			PostBurnAUCBps: decimal.NewFromInt(22),

			DeltaAUCBps: decimal.NewFromInt(2),

			LSISBps: decimal.NewFromInt(2),

			DepthDeltas: collectorDepthDeltas(),
		}

	return BurnEventImpactResult{
		Burn: cloneBurnCandidate(
			preBurn.Burn,
		),

		PreBurnPool: preBurn.Pool,

		PostBurnPool: &postBurnPool,

		CurrentTick: 0,

		RangeLocation: BurnRangeActive,

		BurnRangeActive: true,

		RangeWidth: 200,

		DistanceToLowerTick: 100,

		DistanceToUpperTick: 100,

		DistanceToNearestBoundary: 100,

		DistanceOutsideRange: 0,

		NormalizedDistanceOutsideRange: decimal.Zero,

		LiquidityRemoved: removed,

		RangeLiquidityBeforeBurn: rangeBefore,

		RangeLiquidityAfterBurn: rangeAfter,

		ActiveLiquidityBeforeBurn: new(big.Int).Set(
			preBurn.Pool.Liquidity,
		),

		ActiveLiquidityAfterBurn: activeAfter,

		ActiveLiquidityRemoved: preBurn.
			Burn.
			LiquidityRemovedCopy(),

		RemovalFraction: decimal.RequireFromString(
			"0.2",
		),

		RangeActiveLiquidityShare: burnDecimalRatio(
			rangeBefore,
			preBurn.Pool.Liquidity,
		),

		ActiveRemovalShare: burnDecimalRatio(
			removed,
			preBurn.Pool.Liquidity,
		),

		RangeLiquidityDensity: decimal.RequireFromString(
			"2.5",
		),

		RemovedLiquidityDensity: decimal.RequireFromString(
			"0.5",
		),

		ZeroForOne: zeroForOne,

		OneForZero: oneForZero,

		TotalLSISBps: decimal.NewFromInt(3),

		MaxDirectionalLSISBps: decimal.NewFromInt(2),
	}
}

func collectorDepthDeltas() []DepthDelta {
	return []DepthDelta{
		{
			ThresholdBps: decimal.NewFromInt(10),

			BaseDepthAmount: decimal.NewFromInt(100),

			CounterfactualDepthAmount: decimal.NewFromInt(90),

			DeltaDepthAmount: decimal.NewFromInt(10),

			BaseBreached: true,

			CounterfactualBreached: true,
		},
	}
}

func collectorAmountGrid() []*big.Int {
	return []*big.Int{
		big.NewInt(100),
		big.NewInt(1_000),
		big.NewInt(10_000),
	}
}

func collectorThresholds() []decimal.Decimal {
	return []decimal.Decimal{
		decimal.NewFromInt(10),
		decimal.NewFromInt(50),
	}
}

func cloneCollectorPreBurnResult(
	value PreBurnStateResult,
) PreBurnStateResult {
	result :=
		value

	result.Burn =
		cloneBurnCandidate(
			value.Burn,
		)

	if value.Pool != nil {
		poolCopy :=
			*value.Pool

		poolCopy.SqrtPriceX96 =
			new(big.Int).Set(
				value.Pool.SqrtPriceX96,
			)

		poolCopy.Liquidity =
			new(big.Int).Set(
				value.Pool.Liquidity,
			)

		result.Pool =
			&poolCopy
	}

	if value.ActiveLiquidityBeforeBurn != nil {
		result.ActiveLiquidityBeforeBurn =
			new(big.Int).Set(
				value.
					ActiveLiquidityBeforeBurn,
			)
	}

	result.LastReplayedCursor =
		cloneEventCursorPointer(
			value.LastReplayedCursor,
		)

	return result
}

func cloneCollectorImpactResult(
	value BurnEventImpactResult,
) BurnEventImpactResult {
	result :=
		value

	result.Burn =
		cloneBurnCandidate(
			value.Burn,
		)

	result.LiquidityRemoved =
		new(big.Int).Set(
			value.LiquidityRemoved,
		)

	result.RangeLiquidityBeforeBurn =
		new(big.Int).Set(
			value.RangeLiquidityBeforeBurn,
		)

	result.RangeLiquidityAfterBurn =
		new(big.Int).Set(
			value.RangeLiquidityAfterBurn,
		)

	result.ActiveLiquidityBeforeBurn =
		new(big.Int).Set(
			value.ActiveLiquidityBeforeBurn,
		)

	result.ActiveLiquidityAfterBurn =
		new(big.Int).Set(
			value.ActiveLiquidityAfterBurn,
		)

	result.ActiveLiquidityRemoved =
		new(big.Int).Set(
			value.ActiveLiquidityRemoved,
		)

	result.ZeroForOne =
		cloneBurnDirectionalImpact(
			value.ZeroForOne,
		)

	result.OneForZero =
		cloneBurnDirectionalImpact(
			value.OneForZero,
		)

	return result
}
