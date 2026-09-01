package controlfreeze

import (
	"fmt"
	"testing"
)

func TestDefaultConfigFreezesPrimaryCalipers(t *testing.T) {
	config := DefaultConfig()
	if config.TemporalPrimaryCaliper != 1.25 {
		t.Fatalf("temporal primary caliper = %v, want 1.25", config.TemporalPrimaryCaliper)
	}
	if config.MatchedPrimaryCaliper != 1.25 {
		t.Fatalf("matched primary caliper = %v, want 1.25", config.MatchedPrimaryCaliper)
	}
	if config.TemporalSensitivityCaliper != 2 || config.MatchedSensitivityCaliper != 2 {
		t.Fatalf("sensitivity calipers = (%v,%v), want 2", config.TemporalSensitivityCaliper, config.MatchedSensitivityCaliper)
	}
}

func TestAnalyzeFreezesSupportBalanceAndInferenceDeterministically(t *testing.T) {
	config := DefaultConfig()
	config.BootstrapIterations = 250
	config.PermutationIterations = 250

	inputs := syntheticInputs()
	first, err := Analyze(inputs, config)
	if err != nil {
		t.Fatalf("first analyze: %v", err)
	}
	second, err := Analyze(inputs, config)
	if err != nil {
		t.Fatalf("second analyze: %v", err)
	}
	if fmt.Sprintf("%#v", first) != fmt.Sprintf("%#v", second) {
		t.Fatal("control freeze analysis is not deterministic")
	}
	if first.Manifest.TemporalPrimarySupportBurns != 3 {
		t.Fatalf("temporal primary support = %d, want 3", first.Manifest.TemporalPrimarySupportBurns)
	}
	if first.Manifest.MatchedPrimarySupportPairs != 2 {
		t.Fatalf("matched primary support = %d, want 2", first.Manifest.MatchedPrimarySupportPairs)
	}
	if first.Manifest.PrimaryBlockedRows != 0 {
		t.Fatalf("primary blocked rows = %d, want 0", first.Manifest.PrimaryBlockedRows)
	}
	foundExtremeTail := false
	for _, row := range first.TemporalSupport {
		if row.BurnEventKey == "burn-c" {
			if row.PrimaryIncluded {
				t.Fatal("burn-c unexpectedly entered primary common support")
			}
			foundExtremeTail = row.ExtremeTailEvent
		}
	}
	if !foundExtremeTail {
		t.Fatal("excluded highest-LSIS burn was not marked as an extreme tail event")
	}
	for _, gate := range first.BalanceGates {
		if gate.AnalysisSet == AnalysisPrimaryCommonSupport && (gate.Stratum == "all" || gate.Stratum == "active") && gate.GateStatus != GatePassed {
			t.Fatalf("primary gate unexpectedly failed: %+v", gate)
		}
	}
}

func syntheticInputs() parsedInputs {
	burns := []struct {
		key       string
		block     uint64
		lsis      float64
		distances []float64
		actual    float64
	}{
		{"burn-a", 100, 1, []float64{0.4, 0.8, 1.2}, 3},
		{"burn-b", 200, 2, []float64{0.5, 1.0, 1.3}, 4},
		{"burn-c", 300, 100, []float64{1.3, 1.4, 1.5}, 10},
		{"burn-d", 400, 3, []float64{0.6, 0.9, 1.1}, 5},
	}
	matches := make([]temporalMatch, 0, len(burns)*3)
	observations := make([]temporalObservation, 0, len(burns)*3)
	for burnIndex, burn := range burns {
		for rank, distance := range burn.distances {
			matchID := burn.key + "-m" + string(rune('1'+rank))
			matches = append(matches, temporalMatch{
				MatchID: matchID, BurnEventKey: burn.key, BurnBlock: burn.block, BurnLogIndex: burnIndex,
				Stratum: "active", BurnRangeActive: true, PlaceboAnchorBlock: burn.block,
				MatchDistance: distance, BurnCurrentTick: 1000, PlaceboCurrentTick: 1000,
				BurnActiveLiquidity: 1_000_000, PlaceboActiveLiquidity: 1_000_000,
				BurnBaselineTotalAUCBps: 10, PlaceboBaselineTotalAUCBps: 10,
				BurnImmediateLSISBps: burn.lsis, BurnDirectionalImbalance: 0.1, PlaceboDirectionalImbalance: 0.1,
			})
			observations = append(observations, temporalObservation{
				MatchID: matchID, BurnEventKey: burn.key, Stratum: "active", BurnRangeActive: true,
				HorizonLabel: "b50", HorizonBlocks: 50, Available: true,
				ActualRealizedDeteriorationBps:  burn.actual,
				PlaceboRealizedDeteriorationBps: 1 + float64(rank)*0.1,
			})
		}
	}
	pairs := []matchedPair{
		syntheticPair("pair-1", 1.0, "burn-a", "burn-b", 5, 1),
		syntheticPair("pair-2", 1.2, "burn-c", "burn-d", 100, 3),
		syntheticPair("pair-3", 1.6, "burn-b", "burn-d", 4, 2),
	}
	outcomes := []matchedOutcome{
		{PairID: "pair-1", Stratum: "active", HorizonLabel: "b50", HorizonBlocks: 50, RealizedDifferenceBps: 2, MechanicalAvailable: true, MechanicalDifferenceBps: 1},
		{PairID: "pair-2", Stratum: "active", HorizonLabel: "b50", HorizonBlocks: 50, RealizedDifferenceBps: 3, MechanicalAvailable: true, MechanicalDifferenceBps: 2},
		{PairID: "pair-3", Stratum: "active", HorizonLabel: "b50", HorizonBlocks: 50, RealizedDifferenceBps: 1, MechanicalAvailable: true, MechanicalDifferenceBps: 0.5},
	}
	return parsedInputs{
		Manifest:        SourceManifest{Status: "completed", PoolAddress: "pool", FromBlock: 1, ToBlock: 500, IndexedThrough: 600},
		TemporalMatches: matches, TemporalObservations: observations, MatchedPairs: pairs, MatchedOutcomes: outcomes,
	}
}

func syntheticPair(id string, distance float64, highKey, lowKey string, highLSIS, lowLSIS float64) matchedPair {
	return matchedPair{
		PairID: id, Stratum: "active", MatchDistance: distance, HighEventKey: highKey, LowEventKey: lowKey,
		HighImmediateLSISBps: highLSIS, LowImmediateLSISBps: lowLSIS,
		HighBlock: 100, LowBlock: 100, HighCurrentTick: 1000, LowCurrentTick: 1000,
		HighActiveLiquidity: 1_000_000, LowActiveLiquidity: 1_000_000,
		HighLiquidityRemoved: 1000, LowLiquidityRemoved: 1000,
		HighRemovalFraction: 1, LowRemovalFraction: 1,
		HighActiveRemovalShare: 0.1, LowActiveRemovalShare: 0.1,
		HighRangeWidth: 10, LowRangeWidth: 10,
		HighDistanceOutside: 0, LowDistanceOutside: 0,
	}
}
