package services

import (
	"context"
	"math/big"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

func TestTargetImpactAmountGridResolverFindsMinimalMonotonicAmounts(t *testing.T) {
	t.Parallel()

	pool := normalizedGridTestPool(t)
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatalf("NewSimulator() error = %v", err)
	}
	resolver, err := NewTargetImpactAmountGridResolver(
		simulator,
		AnalysisAmountGridSearchConfig{
			TargetImpactsBps: []decimal.Decimal{
				decimal.NewFromInt(1),
				decimal.NewFromInt(5),
				decimal.NewFromInt(10),
			},
			MaxExpansions: 128,
			MaxBisections: 128,
		},
	)
	if err != nil {
		t.Fatalf("NewTargetImpactAmountGridResolver() error = %v", err)
	}

	grid, err := resolver.Resolve(context.Background(), pool)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := grid.RequireComplete(); err != nil {
		t.Fatalf("RequireComplete() error = %v", err)
	}

	for directionIndex, direction := range [][]AnalysisAmountGridPoint{grid.ZeroForOne, grid.OneForZero} {
		zeroForOne := directionIndex == 0
		var previous *big.Int
		for index, point := range direction {
			if point.AchievedImpactBps.LessThan(point.TargetImpactBps) {
				t.Fatalf("point %d achieved=%s target=%s", index, point.AchievedImpactBps, point.TargetImpactBps)
			}
			if point.AmountOutRaw == nil || point.AmountOutRaw.Cmp(big.NewInt(100_000)) < 0 {
				t.Fatalf("point %d amount_out=%v, want at least 100000 raw units", index, point.AmountOutRaw)
			}
			if point.OutputQuantizationBoundBps.GreaterThan(decimal.RequireFromString("0.1")) {
				t.Fatalf("point %d quantization bound=%s, want at most 0.1 bps", index, point.OutputQuantizationBoundBps)
			}
			if previous != nil && previous.Cmp(point.AmountInRaw) >= 0 {
				t.Fatalf("point %d amount=%s previous=%s", index, point.AmountInRaw, previous)
			}
			if point.AmountInRaw.Cmp(big.NewInt(1)) > 0 {
				predecessor := new(big.Int).Sub(new(big.Int).Set(point.AmountInRaw), big.NewInt(1))
				result, simulateErr := simulator.SimulateExactInput(pool, uniswapv3.ExactInputRequest{AmountIn: predecessor, ZeroForOne: zeroForOne, AllowZeroOutput: true})
				predecessorIsPreviousPoint := previous != nil && predecessor.Cmp(previous) == 0
				predecessorMeetsPrecision := simulateErr == nil && result.AmountOut.Cmp(big.NewInt(100_000)) >= 0
				if !predecessorIsPreviousPoint && predecessorMeetsPrecision && !result.PriceImpactBps.LessThan(point.TargetImpactBps) {
					t.Fatalf("point %d amount=%s is not minimal above the previous grid point: predecessor impact=%s target=%s", index, point.AmountInRaw, result.PriceImpactBps, point.TargetImpactBps)
				}
			}
			previous = point.AmountInRaw
		}
	}

	cached, err := resolver.Resolve(context.Background(), pool)
	if err != nil {
		t.Fatalf("cached Resolve() error = %v", err)
	}
	if cached.StateID != grid.StateID {
		t.Fatalf("cached state id=%s want=%s", cached.StateID, grid.StateID)
	}
	summary := resolver.AuditSummary()
	if summary.UniqueStates != 1 || summary.CacheHits < 1 || summary.CompleteStates != 1 {
		t.Fatalf("audit summary = %+v", summary)
	}
}

func TestTargetImpactAmountGridResolverConcurrentCache(t *testing.T) {
	t.Parallel()

	pool := normalizedGridTestPool(t)
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewTargetImpactAmountGridResolver(
		simulator,
		AnalysisAmountGridSearchConfig{
			TargetImpactsBps: []decimal.Decimal{decimal.NewFromInt(1), decimal.NewFromInt(5)},
			MaxExpansions:    128,
			MaxBisections:    128,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			grid, resolveErr := resolver.Resolve(context.Background(), pool)
			if resolveErr == nil {
				resolveErr = grid.RequireComplete()
			}
			errors <- resolveErr
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent Resolve() error = %v", err)
		}
	}
	if resolver.AuditSummary().UniqueStates != 1 {
		t.Fatalf("unique states=%d, want 1", resolver.AuditSummary().UniqueStates)
	}
}

func TestMinimumAnalysisGridOutputRawBoundsQuantization(t *testing.T) {
	t.Parallel()

	minimum, err := minimumAnalysisGridOutputRaw(decimal.RequireFromString("0.1"))
	if err != nil {
		t.Fatalf("minimumAnalysisGridOutputRaw() error = %v", err)
	}
	if minimum.Cmp(big.NewInt(100_000)) != 0 {
		t.Fatalf("minimum output = %s, want 100000", minimum)
	}
	if bound := outputQuantizationBoundBps(minimum); !bound.Equal(decimal.RequireFromString("0.1")) {
		t.Fatalf("quantization bound = %s, want 0.1", bound)
	}
}

func TestTargetImpactAmountGridResolverRejectsOneUnitOutputArtifacts(t *testing.T) {
	t.Parallel()

	pool := normalizedGridTestPool(t)
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewTargetImpactAmountGridResolver(
		simulator,
		AnalysisAmountGridSearchConfig{
			TargetImpactsBps: []decimal.Decimal{
				decimal.NewFromInt(1),
				decimal.NewFromInt(5),
			},
			MaxExpansions:            128,
			MaxBisections:            128,
			MaxOutputQuantizationBps: decimal.RequireFromString("0.1"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	grid, err := resolver.Resolve(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := grid.RequireComplete(); err != nil {
		t.Fatal(err)
	}
	for _, direction := range [][]AnalysisAmountGridPoint{grid.ZeroForOne, grid.OneForZero} {
		for _, point := range direction {
			if point.AmountOutRaw.Cmp(big.NewInt(100_000)) < 0 {
				t.Fatalf("resolved output = %s, want precision-safe output", point.AmountOutRaw)
			}
		}
	}
}

func TestBurnEventSampleRuntimeAmountGridOverridesFallback(t *testing.T) {
	t.Parallel()

	sample := BurnEventSample{runtime: &burnEventSampleRuntime{}}
	if err := attachBurnEventSampleAmountGridRuntime(
		&sample,
		"state-1",
		AnalysisAmountGridModeTargetImpact,
		[]decimal.Decimal{decimal.NewFromInt(1), decimal.NewFromInt(5)},
		[]*big.Int{big.NewInt(10), big.NewInt(20)},
		[]*big.Int{big.NewInt(30), big.NewInt(40)},
	); err != nil {
		t.Fatalf("attachBurnEventSampleAmountGridRuntime() error = %v", err)
	}

	zf, of, stateID, mode, targets, err := burnEventSampleRuntimeAmountGrids(
		sample,
		[]*big.Int{big.NewInt(1), big.NewInt(2)},
		[]*big.Int{big.NewInt(3), big.NewInt(4)},
	)
	if err != nil {
		t.Fatalf("burnEventSampleRuntimeAmountGrids() error = %v", err)
	}
	if stateID != "state-1" || mode != AnalysisAmountGridModeTargetImpact || len(targets) != 2 {
		t.Fatalf("metadata state=%s mode=%s targets=%v", stateID, mode, targets)
	}
	if zf[0].Cmp(big.NewInt(10)) != 0 || of[0].Cmp(big.NewInt(30)) != 0 {
		t.Fatalf("runtime grids zf=%v of=%v", zf, of)
	}
}

func normalizedGridTestPool(t *testing.T) *domain.ReconstructedPool {
	t.Helper()
	sqrtPrice, err := uniswapv3.SqrtRatioAtTick(0)
	if err != nil {
		t.Fatalf("SqrtRatioAtTick() error = %v", err)
	}
	liquidity := big.NewInt(1_000_000_000_000)
	return &domain.ReconstructedPool{
		PoolAddress:  "0x0000000000000000000000000000000000000001",
		BlockNumber:  100,
		SqrtPriceX96: sqrtPrice,
		CurrentTick:  0,
		Liquidity:    new(big.Int).Set(liquidity),
		Ticks: map[int]*domain.TickState{
			-1000: {Index: -1000, LiquidityGross: new(big.Int).Set(liquidity), LiquidityNet: new(big.Int).Set(liquidity)},
			1000:  {Index: 1000, LiquidityGross: new(big.Int).Set(liquidity), LiquidityNet: new(big.Int).Neg(new(big.Int).Set(liquidity))},
		},
		InitializedTicks: []int{-1000, 1000},
	}
}
