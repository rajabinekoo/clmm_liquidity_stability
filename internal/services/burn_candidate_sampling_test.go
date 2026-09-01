package services

import (
	"context"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
)

func TestBuildBurnCandidateSamplingOrderCoversEachNonEmptyBinBeforeRepeating(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	candidates := []domain.BurnCandidate{
		collectorBurnCandidate(poolAddress, 101, 1, 100),
		collectorBurnCandidate(poolAddress, 102, 1, 100),

		collectorBurnCandidate(poolAddress, 126, 1, 100),
		collectorBurnCandidate(poolAddress, 127, 1, 100),

		collectorBurnCandidate(poolAddress, 151, 1, 100),
		collectorBurnCandidate(poolAddress, 152, 1, 100),

		collectorBurnCandidate(poolAddress, 176, 1, 100),
		collectorBurnCandidate(poolAddress, 177, 1, 100),
	}

	ordered, diagnostics, err :=
		buildBurnCandidateSamplingOrder(
			candidates,
			100,
			199,
			4,
			20260725,
		)
	if err != nil {
		t.Fatalf(
			"buildBurnCandidateSamplingOrder() error = %v",
			err,
		)
	}

	if diagnostics.BinCount != 4 ||
		diagnostics.NonEmptyBinCount != 4 {
		t.Fatalf(
			"diagnostics = %+v, want 4 bins and 4 non-empty bins",
			diagnostics,
		)
	}

	if len(ordered) != len(candidates) {
		t.Fatalf(
			"ordered candidate count = %d, want %d",
			len(ordered),
			len(candidates),
		)
	}

	seenBins :=
		make(
			map[int]struct{},
			4,
		)

	for _, candidate := range ordered[:4] {
		binIndex, err :=
			burnSamplingBinIndex(
				candidate.Cursor.BlockNumber,
				100,
				199,
				4,
			)
		if err != nil {
			t.Fatalf(
				"burnSamplingBinIndex() error = %v",
				err,
			)
		}

		seenBins[binIndex] =
			struct{}{}
	}

	if len(seenBins) != 4 {
		t.Fatalf(
			"first sampling round covered %d bins, want 4",
			len(seenBins),
		)
	}

	repeated, _, err :=
		buildBurnCandidateSamplingOrder(
			candidates,
			100,
			199,
			4,
			20260725,
		)
	if err != nil {
		t.Fatalf(
			"second buildBurnCandidateSamplingOrder() error = %v",
			err,
		)
	}

	for index := range ordered {
		if ordered[index].EventKey() !=
			repeated[index].EventKey() {
			t.Fatalf(
				"sampling order is not deterministic at index %d: first=%s second=%s",
				index,
				ordered[index].EventKey(),
				repeated[index].EventKey(),
			)
		}
	}
}

func TestBurnSampleCollectorSamplesAcrossBlockBins(
	t *testing.T,
) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	candidates := []domain.BurnCandidate{
		collectorBurnCandidate(poolAddress, 101, 1, 100),
		collectorBurnCandidate(poolAddress, 102, 1, 100),

		collectorBurnCandidate(poolAddress, 151, 1, 100),
		collectorBurnCandidate(poolAddress, 152, 1, 100),
	}

	preBurnResults :=
		make(
			map[string]PreBurnStateResult,
			len(candidates),
		)

	impactResults :=
		make(
			map[string]BurnEventImpactResult,
			len(candidates),
		)

	for _, candidate := range candidates {
		preBurn :=
			collectorPreBurnFixture(
				candidate,
				1_000,
			)

		preBurnResults[candidate.EventKey()] = preBurn

		impactResults[candidate.EventKey()] = collectorImpactFixture(
			preBurn,
		)
	}

	collector :=
		NewBurnSampleCollector(
			&fakeBurnCandidatePageLoader{
				pages: []repositories.BurnCandidatePage{
					{
						PoolAddress: poolAddress,

						FromBlock: 100,
						ToBlock:   199,

						IndexedThrough: 250,

						Candidates: candidates,
					},
				},
			},
			&fakeBurnPreStateBuilder{
				results: preBurnResults,
			},
			&fakeBurnImpactAnalyzer{
				results: impactResults,
			},
		)

	report, err :=
		collector.Collect(
			context.Background(),
			BurnSampleCollectionRequest{
				PoolAddress: poolAddress,

				FromBlock: 100,
				ToBlock:   199,

				PageSize:     10,
				SwapPageSize: 100,

				MaxCandidates: 10,
				MaxSamples:    2,

				SamplingBins: 2,
				SamplingSeed: 20260725,

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

	if report.DiscoveredCandidates != 4 ||
		report.CandidateEvents != 2 ||
		report.AnalyzedEvents != 2 ||
		report.SkippedEvents != 0 {
		t.Fatalf(
			"unexpected counts: discovered=%d attempted=%d analyzed=%d skipped=%d",
			report.DiscoveredCandidates,
			report.CandidateEvents,
			report.AnalyzedEvents,
			report.SkippedEvents,
		)
	}

	if report.SamplingBins != 2 ||
		report.NonEmptySamplingBins != 2 {
		t.Fatalf(
			"unexpected sampling diagnostics: bins=%d non_empty=%d",
			report.SamplingBins,
			report.NonEmptySamplingBins,
		)
	}

	if !report.Truncated {
		t.Fatal(
			"Truncated = false, want true because two of four discovered candidates were sampled",
		)
	}

	if len(report.Samples) != 2 {
		t.Fatalf(
			"sample count = %d, want 2",
			len(report.Samples),
		)
	}

	firstBin, err :=
		burnSamplingBinIndex(
			report.Samples[0].
				Burn.
				Cursor.
				BlockNumber,
			100,
			199,
			2,
		)
	if err != nil {
		t.Fatalf(
			"first burnSamplingBinIndex() error = %v",
			err,
		)
	}

	secondBin, err :=
		burnSamplingBinIndex(
			report.Samples[1].
				Burn.
				Cursor.
				BlockNumber,
			100,
			199,
			2,
		)
	if err != nil {
		t.Fatalf(
			"second burnSamplingBinIndex() error = %v",
			err,
		)
	}

	if firstBin == secondBin {
		t.Fatalf(
			"sampled bins = %d and %d, want distinct bins",
			firstBin,
			secondBin,
		)
	}
}
