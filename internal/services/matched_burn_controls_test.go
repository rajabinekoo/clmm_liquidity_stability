package services

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestBuildMatchedBurnPairsUsesCovariateNearestPairsWithoutReuse(t *testing.T) {
	t.Parallel()

	samples := make([]BurnEventSample, 0, 8)
	for index := 0; index < 8; index++ {
		samples = append(samples, matchedBurnSample(uint64(100+index*10), int64(index+1), BurnRangeActive))
	}

	pairs, before, err := buildMatchedBurnPairs(samples)
	if err != nil {
		t.Fatalf("buildMatchedBurnPairs() error = %v", err)
	}
	if len(pairs) != 4 {
		t.Fatalf("pairs = %d, want 4", len(pairs))
	}
	if len(before) != 1 || len(before[0].High) != 4 || len(before[0].Low) != 4 {
		t.Fatalf("unexpected pre-match groups: %#v", before)
	}

	used := make(map[string]struct{}, len(samples))
	for _, pair := range pairs {
		if !pair.High.TotalLSISBps.GreaterThan(pair.Low.TotalLSISBps) {
			t.Fatalf("high LSIS %s is not greater than low %s", pair.High.TotalLSISBps, pair.Low.TotalLSISBps)
		}
		for _, sample := range []BurnEventSample{pair.High, pair.Low} {
			key := sample.Burn.EventKey()
			if _, exists := used[key]; exists {
				t.Fatalf("sample %s reused", key)
			}
			used[key] = struct{}{}
		}
	}
	if len(used) != 8 {
		t.Fatalf("used samples = %d, want 8", len(used))
	}
}

func TestBuildMatchedBurnBalanceReportsPairBalance(t *testing.T) {
	t.Parallel()

	samples := make([]BurnEventSample, 0, 8)
	for index := 0; index < 8; index++ {
		samples = append(samples, matchedBurnSample(uint64(100+index*10), int64(index+1), BurnRangeActive))
	}
	pairs, before, err := buildMatchedBurnPairs(samples)
	if err != nil {
		t.Fatal(err)
	}
	balance := buildMatchedBurnBalance(before, pairs)
	if len(balance) == 0 {
		t.Fatal("balance rows are empty")
	}
	foundBlock := false
	for _, row := range balance {
		if row.Stratum == string(BurnRangeActive) && row.PairsAfter != 4 {
			t.Fatalf("pairs after = %d, want 4", row.PairsAfter)
		}
		if row.Stratum == string(BurnRangeActive) && row.Metric == "block_number" {
			foundBlock = true
		}
	}
	if !foundBlock {
		t.Fatal("block-number balance row is missing")
	}
}

func TestMatchBurnStratumSkipsEqualLSISPairs(t *testing.T) {
	t.Parallel()

	samples := []BurnEventSample{
		matchedBurnSample(100, 1, BurnRangeActive),
		matchedBurnSample(110, 1, BurnRangeActive),
		matchedBurnSample(120, 2, BurnRangeActive),
		matchedBurnSample(130, 3, BurnRangeActive),
	}
	pairs, err := matchBurnStratum(BurnRangeActive, samples)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range pairs {
		if pair.High.TotalLSISBps.Equal(pair.Low.TotalLSISBps) {
			t.Fatalf("equal-LSIS pair selected: %s", pair.High.TotalLSISBps)
		}
	}
}

func matchedBurnSample(block uint64, lsis int64, location BurnRangeLocation) BurnEventSample {
	active := location == BurnRangeActive
	activeShare := decimal.Zero
	if active {
		activeShare = decimal.NewFromInt(lsis).Div(decimal.NewFromInt(100))
	}
	return BurnEventSample{
		Burn: domain.BurnCandidate{
			ID: "burn", PoolAddress: localAnalysisTestPool,
			TxHash:    "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Cursor:    domain.EventCursor{BlockNumber: block, LogIndex: int(block % 97)},
			TickLower: -10, TickUpper: 10, LiquidityRemoved: big.NewInt(100 + lsis),
		},
		CurrentTick:                    0,
		RangeLocation:                  location,
		BurnRangeActive:                active,
		RangeWidth:                     20,
		NormalizedDistanceOutsideRange: decimal.Zero,
		LiquidityRemoved:               big.NewInt(100 + lsis),
		ActiveLiquidityBeforeBurn:      big.NewInt(10_000 + lsis*100),
		RemovalFraction:                decimal.NewFromInt(5).Div(decimal.NewFromInt(10)),
		ActiveRemovalShare:             activeShare,
		TotalLSISBps:                   decimal.NewFromInt(lsis),
	}
}
