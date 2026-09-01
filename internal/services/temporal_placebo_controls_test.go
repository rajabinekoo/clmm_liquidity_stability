package services

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestBuildTemporalPlaceboCandidateBlocksAreSpacedAndExcludeLiquidityActions(t *testing.T) {
	t.Parallel()

	actions := []uint64{250, 550, 850}
	blocks := buildTemporalPlaceboCandidateBlocks(100, 1_000, 100, 10, actions)
	if len(blocks) == 0 {
		t.Fatal("candidate blocks are empty")
	}
	for index, block := range blocks {
		if temporalPlaceboNearLiquidityAction(block, actions, 10) {
			t.Fatalf("candidate block %d is inside liquidity-action exclusion", block)
		}
		if index > 0 && block-blocks[index-1] < 100 {
			t.Fatalf("candidate spacing = %d, want at least 100", block-blocks[index-1])
		}
	}
}

func TestTemporalPlaceboLiquidityActionBlocksIncludesMintsAndBurns(t *testing.T) {
	t.Parallel()

	mint, err := domain.NewLiquidityPoolBlockEvent(domain.LiquidityChange{
		ID: "mint", BlockNumber: 100, LogIndex: 1,
		TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	burn, err := domain.NewLiquidityPoolBlockEvent(domain.LiquidityChange{
		ID: "burn", BlockNumber: 200, LogIndex: 2,
		TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(-10),
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := temporalPlaceboLiquidityActionBlocks([]domain.PoolBlockEvent{mint, burn}, nil)
	if len(blocks) != 2 || blocks[0] != 100 || blocks[1] != 200 {
		t.Fatalf("liquidity action blocks = %v, want [100 200]", blocks)
	}
}

func TestMatchTemporalPlacebosUsesUniqueCandidatesAndTemporalFeature(t *testing.T) {
	t.Parallel()

	samples := []BurnEventSample{
		temporalPlaceboSample(100, 0, 1_000, 2, 1),
		temporalPlaceboSample(900, 0, 1_000, 2, 1),
	}
	states := []temporalPlaceboState{
		temporalPlaceboStateFixture(110, 0, 1_000, 2, 1),
		temporalPlaceboStateFixture(890, 0, 1_000, 2, 1),
		temporalPlaceboStateFixture(500, 100, 9_000, 50, 1),
	}

	matches, err := matchTemporalPlacebos(samples, states)
	if err != nil {
		t.Fatalf("matchTemporalPlacebos() error = %v", err)
	}
	if len(matches) != len(samples) {
		t.Fatalf("matches = %d, want %d", len(matches), len(samples))
	}
	if matches[0].PlaceboAnchorBlock == matches[1].PlaceboAnchorBlock {
		t.Fatalf("placebo candidate reused at block %d", matches[0].PlaceboAnchorBlock)
	}
	if matches[0].Burn.Cursor.BlockNumber >= matches[1].Burn.Cursor.BlockNumber {
		t.Fatalf("matches are not sorted by burn cursor")
	}
	if matches[0].PlaceboAnchorBlock != 110 || matches[1].PlaceboAnchorBlock != 890 {
		t.Fatalf("temporal matches = %d/%d, want 110/890", matches[0].PlaceboAnchorBlock, matches[1].PlaceboAnchorBlock)
	}
}

func TestBuildTemporalPlaceboBalanceReportsMatchedCovariates(t *testing.T) {
	t.Parallel()

	samples := []BurnEventSample{
		temporalPlaceboSample(100, 0, 1_000, 2, 1),
		temporalPlaceboSample(900, 10, 2_000, 4, 3),
	}
	states := []temporalPlaceboState{
		temporalPlaceboStateFixture(110, 0, 1_100, 3, 1),
		temporalPlaceboStateFixture(890, 10, 2_100, 5, 3),
		temporalPlaceboStateFixture(500, 100, 9_000, 50, 1),
	}
	matches, err := matchTemporalPlacebos(samples, states)
	if err != nil {
		t.Fatal(err)
	}
	balance := buildTemporalPlaceboBalance(samples, states, matches)
	if len(balance) != 5 {
		t.Fatalf("balance rows = %d, want 5", len(balance))
	}
	for _, row := range balance {
		if row.ActualCount != 2 || row.CandidateCount != 3 || row.MatchedCount != 2 {
			t.Fatalf("unexpected counts for %s: %#v", row.Metric, row)
		}
	}
}

func TestSummarizeTemporalPlaceboFlow(t *testing.T) {
	t.Parallel()

	sqrt := new(big.Int).Lsh(big.NewInt(1), 96)
	mint, err := domain.NewLiquidityPoolBlockEvent(domain.LiquidityChange{
		ID: "mint", BlockNumber: 101, LogIndex: 1,
		TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(50),
	})
	if err != nil {
		t.Fatal(err)
	}
	swap, err := domain.NewSwapPoolBlockEvent(domain.SwapEvent{
		ID: "swap", TxHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PoolAddress: localAnalysisTestPool, BlockNumber: 102, LogIndex: 2,
		Amount0Raw: big.NewInt(10), Amount1Raw: big.NewInt(-20),
		SqrtPriceX96After: sqrt, TickAfter: -2,
	})
	if err != nil {
		t.Fatal(err)
	}
	burn, err := domain.NewLiquidityPoolBlockEvent(domain.LiquidityChange{
		ID: "burn", BlockNumber: 103, LogIndex: 3,
		TickLower: -10, TickUpper: 10, LiquidityDelta: big.NewInt(-15),
	})
	if err != nil {
		t.Fatal(err)
	}

	flow := summarizeTemporalPlaceboFlow([]domain.PoolBlockEvent{mint, swap, burn}, 0, 103)
	if flow.SwapCount != 1 || flow.MintEventCount != 1 || flow.BurnEventCount != 1 {
		t.Fatalf("unexpected counts: swaps=%d mints=%d burns=%d", flow.SwapCount, flow.MintEventCount, flow.BurnEventCount)
	}
	if flow.NetLiquidityFlow.Cmp(big.NewInt(35)) != 0 {
		t.Fatalf("net liquidity = %s, want 35", flow.NetLiquidityFlow)
	}
	if flow.TickPathTotalVariation != 2 || flow.TickPathRange != 2 {
		t.Fatalf("tick path variation=%d range=%d, want 2/2", flow.TickPathTotalVariation, flow.TickPathRange)
	}
}

func TestBuildTemporalPlaceboSummariesUsesImmediateLSIS(t *testing.T) {
	t.Parallel()

	rows := []TemporalPlaceboObservation{
		{Match: TemporalPlaceboMatch{BurnRangeActive: true, BurnImmediateTotalLSISBps: decimal.NewFromInt(1)}, HorizonLabel: "b50", HorizonBlocks: 50, Available: true, ExcessDeteriorationBps: decimal.NewFromInt(2)},
		{Match: TemporalPlaceboMatch{BurnRangeActive: true, BurnImmediateTotalLSISBps: decimal.NewFromInt(2)}, HorizonLabel: "b50", HorizonBlocks: 50, Available: true, ExcessDeteriorationBps: decimal.NewFromInt(4)},
	}
	summaries := buildTemporalPlaceboSummaries(rows)
	var all *TemporalPlaceboHorizonSummary
	for index := range summaries {
		if summaries[index].Stratum == "all" {
			all = &summaries[index]
			break
		}
	}
	if all == nil {
		t.Fatal("all-stratum summary missing")
	}
	if all.PearsonImmediateLSISVsExcess < 0.99 {
		t.Fatalf("pearson = %f, want near 1", all.PearsonImmediateLSISVsExcess)
	}
}

func temporalPlaceboSample(block uint64, tick int, liquidity int64, zeroForOneAUC int64, oneForZeroAUC int64) BurnEventSample {
	return BurnEventSample{
		Burn: domain.BurnCandidate{
			ID: "burn", PoolAddress: localAnalysisTestPool,
			TxHash:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Cursor:    domain.EventCursor{BlockNumber: block, LogIndex: int(block % 97)},
			TickLower: tick - 10, TickUpper: tick + 10, LiquidityRemoved: big.NewInt(10),
		},
		CurrentTick: tick, RangeLocation: BurnRangeActive, BurnRangeActive: true,
		RangeWidth: 20, ActiveLiquidityBeforeBurn: big.NewInt(liquidity),
		ZeroForOne:   BurnDirectionalImpact{BaseAUCBps: decimal.NewFromInt(zeroForOneAUC)},
		OneForZero:   BurnDirectionalImpact{BaseAUCBps: decimal.NewFromInt(oneForZeroAUC)},
		TotalLSISBps: decimal.NewFromInt(zeroForOneAUC + oneForZeroAUC),
	}
}

func temporalPlaceboStateFixture(block uint64, tick int, liquidity int64, zeroForOneAUC int64, oneForZeroAUC int64) temporalPlaceboState {
	total := decimal.NewFromInt(zeroForOneAUC + oneForZeroAUC)
	imbalance := decimal.NewFromInt(zeroForOneAUC - oneForZeroAUC).Abs().Div(total)
	return temporalPlaceboState{
		AnchorBlock: block, ReferenceBlock: block - 1,
		Pool:        &domain.ReconstructedPool{CurrentTick: tick, Liquidity: big.NewInt(liquidity)},
		ZeroForOne:  PriceImpactCurveSummary{PriceImpactAUCBps: decimal.NewFromInt(zeroForOneAUC)},
		OneForZero:  PriceImpactCurveSummary{PriceImpactAUCBps: decimal.NewFromInt(oneForZeroAUC)},
		TotalAUCBps: total, DirectionalImbalance: imbalance,
	}
}
