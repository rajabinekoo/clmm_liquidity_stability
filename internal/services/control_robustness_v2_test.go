package services

import (
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func robustControlTestSample(block uint64, logIndex int, tick int, liquidity int64, removed int64, lsis string, location BurnRangeLocation) BurnEventSample {
	active := location == BurnRangeActive
	return BurnEventSample{
		Burn: domain.BurnCandidate{
			ID:               "burn",
			PoolAddress:      "0xpool",
			TxHash:           "0xtx",
			Cursor:           domain.EventCursor{BlockNumber: block, LogIndex: logIndex},
			Timestamp:        time.Unix(int64(block), 0).UTC(),
			TickLower:        tick - 10,
			TickUpper:        tick + 10,
			LiquidityRemoved: big.NewInt(removed),
		},
		CurrentTick:                    tick,
		RangeLocation:                  location,
		BurnRangeActive:                active,
		RangeWidth:                     20,
		NormalizedDistanceOutsideRange: decimal.Zero,
		LiquidityRemoved:               big.NewInt(removed),
		ActiveLiquidityBeforeBurn:      big.NewInt(liquidity),
		RemovalFraction:                decimal.RequireFromString("0.5"),
		ActiveRemovalShare:             decimal.RequireFromString("0.1"),
		TotalLSISBps:                   decimal.RequireFromString(lsis),
		ZeroForOne: BurnDirectionalImpact{
			BaseAUCBps: decimal.RequireFromString("10"),
		},
		OneForZero: BurnDirectionalImpact{
			BaseAUCBps: decimal.RequireFromString("12"),
		},
	}
}

func robustControlTestState(block uint64, tick int, liquidity int64, auc string) temporalPlaceboState {
	return temporalPlaceboState{
		AnchorBlock:    block,
		ReferenceBlock: block - 1,
		Pool: &domain.ReconstructedPool{
			PoolAddress:  "0xpool",
			BlockNumber:  block - 1,
			SqrtPriceX96: big.NewInt(1),
			CurrentTick:  tick,
			Liquidity:    big.NewInt(liquidity),
		},
		TotalAUCBps:          decimal.RequireFromString(auc),
		DirectionalImbalance: decimal.RequireFromString("0.1"),
	}
}

func TestBuildDenseTemporalPlaceboCandidateBlocksExcludesLiquidityActions(t *testing.T) {
	blocks := buildDenseTemporalPlaceboCandidateBlocks(100, 1_000, 200, 10, []uint64{400, 705})
	want := []uint64{200, 600, 800, 1_000}
	if !reflect.DeepEqual(blocks, want) {
		t.Fatalf("blocks = %v, want %v", blocks, want)
	}
}

func TestMatchRobustTemporalPlacebosRespectsSeparationReuseAndDeterminism(t *testing.T) {
	samples := []BurnEventSample{
		robustControlTestSample(10_000, 1, 100, 1_000_000, 100, "5", BurnRangeActive),
		robustControlTestSample(30_000, 2, 110, 1_100_000, 110, "4", BurnRangeActive),
	}
	states := []temporalPlaceboState{
		robustControlTestState(1_000, 99, 990_000, "21"),
		robustControlTestState(20_000, 105, 1_050_000, "22"),
		robustControlTestState(40_000, 111, 1_110_000, "23"),
	}
	config := DefaultControlRobustnessV2Config(7_200)
	config.ControlsPerBurn = 2
	config.MaximumCandidateReuse = 2
	config.TemporalPlaceboCaliper = 100

	first, exclusions := matchRobustTemporalPlacebos(samples, states, config)
	second, _ := matchRobustTemporalPlacebos(samples, states, config)
	if len(exclusions) != 0 {
		t.Fatalf("unexpected exclusions: %+v", exclusions)
	}
	if len(first) != 4 {
		t.Fatalf("matches = %d, want 4", len(first))
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("matching is not deterministic")
	}
	uses := map[uint64]int{}
	for _, match := range first {
		if match.Match.BlockDistance < config.BurnPlaceboSeparationBlocks {
			t.Fatalf("overlapping burn/placebo windows: %+v", match)
		}
		uses[match.Match.PlaceboAnchorBlock]++
		if !match.WithinCaliper {
			t.Fatalf("match unexpectedly outside caliper: %+v", match)
		}
	}
	for block, count := range uses {
		if count > config.MaximumCandidateReuse {
			t.Fatalf("candidate %d reused %d times", block, count)
		}
	}
}

func TestRefineMatchedBurnPairsDoesNotIncreaseCostAndCommonSupportUsesCaliper(t *testing.T) {
	samples := []BurnEventSample{
		robustControlTestSample(10_000, 1, 100, 1_000_000, 100, "10", BurnRangeActive),
		robustControlTestSample(10_100, 2, 101, 1_010_000, 101, "1", BurnRangeActive),
		robustControlTestSample(20_000, 3, 200, 2_000_000, 200, "9", BurnRangeActive),
		robustControlTestSample(20_100, 4, 201, 2_010_000, 201, "2", BurnRangeActive),
	}
	base, err := matchRobustBurnStratum(BurnRangeActive, append([]BurnEventSample(nil), samples...))
	if err != nil {
		t.Fatal(err)
	}
	baseCost := 0.0
	for _, pair := range base {
		baseCost += pair.MatchDistance
	}
	refined := refineMatchedBurnPairs(BurnRangeActive, samples, base)
	refinedCost := 0.0
	for _, pair := range refined {
		refinedCost += pair.MatchDistance
		if !pair.High.TotalLSISBps.GreaterThan(pair.Low.TotalLSISBps) {
			t.Fatalf("pair has no positive LSIS contrast: %+v", pair)
		}
	}
	if refinedCost > baseCost+1e-12 {
		t.Fatalf("refinement increased cost: base=%f refined=%f", baseCost, refinedCost)
	}
	caliper := 1.25
	for _, pair := range refined {
		within := pair.MatchDistance <= caliper
		if within && pair.MatchDistance > caliper {
			t.Fatalf("common-support pair exceeds caliper: %f", pair.MatchDistance)
		}
	}
}

func TestRobustInferenceIsDeterministicAndHolmNeverShrinksPValues(t *testing.T) {
	config := DefaultControlRobustnessV2Config(7_200)
	config.BootstrapIterations = 1_000
	config.PermutationIterations = 1_000
	values := []float64{1, 2, 3, 4, 5, -0.5}
	first := buildRobustInferenceRow("test", "common", "effect", "all", "b50", 50, values, config)
	second := buildRobustInferenceRow("test", "common", "effect", "all", "b50", 50, values, config)
	if first != second {
		t.Fatalf("inference is not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	rows := []RobustControlInference{
		first,
		buildRobustInferenceRow("test", "common", "effect", "all", "b300", 300, []float64{1, 1, 1, 1}, config),
	}
	applyControlHolm(rows)
	for _, row := range rows {
		if row.WilcoxonHolmPValue+1e-15 < row.WilcoxonPValue {
			t.Fatalf("Holm-adjusted Wilcoxon p-value shrank: %+v", row)
		}
		if row.PermutationHolmPValue+1e-15 < row.PermutationPValue {
			t.Fatalf("Holm-adjusted permutation p-value shrank: %+v", row)
		}
	}
}

func TestControlBalanceStatusThresholds(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0.05, ControlBalanceBalanced},
		{0.15, ControlBalanceWarning},
		{0.25, ControlBalanceFailed},
	}
	for _, tc := range cases {
		if got := controlBalanceStatus(tc.value); got != tc.want {
			t.Fatalf("status(%f) = %s, want %s", tc.value, got, tc.want)
		}
	}
}
