package services

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

func TestAttachBurnEventSampleRuntimeCopiesPools(t *testing.T) {
	t.Parallel()

	pre := preBurnReplayPool()
	post, err := uniswapv3.ApplyLiquidityChange(
		pre,
		domain.LiquidityChange{
			ID:             "burn",
			BlockNumber:    101,
			LogIndex:       10,
			TickLower:      -100,
			TickUpper:      100,
			LiquidityDelta: big.NewInt(-100),
		},
	)
	if err != nil {
		t.Fatalf("ApplyLiquidityChange() error = %v", err)
	}

	sample := BurnEventSample{
		Burn: domain.BurnCandidate{
			ID:               "burn",
			PoolAddress:      pre.PoolAddress,
			TxHash:           "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Cursor:           domain.EventCursor{BlockNumber: 101, LogIndex: 10},
			TickLower:        -100,
			TickUpper:        100,
			LiquidityRemoved: big.NewInt(100),
		},
		CurrentTick:               pre.CurrentTick,
		SqrtPriceX96BeforeBurn:    new(big.Int).Set(pre.SqrtPriceX96),
		ActiveLiquidityBeforeBurn: new(big.Int).Set(pre.Liquidity),
		ActiveLiquidityAfterBurn:  new(big.Int).Set(post.Liquidity),
	}

	if err := attachBurnEventSampleRuntime(&sample, pre, post); err != nil {
		t.Fatalf("attachBurnEventSampleRuntime() error = %v", err)
	}

	pre.SqrtPriceX96.Add(pre.SqrtPriceX96, big.NewInt(1))
	post.Liquidity.Add(post.Liquidity, big.NewInt(1))

	storedPre, storedPost, err := burnEventSampleRuntimePools(sample)
	if err != nil {
		t.Fatalf("burnEventSampleRuntimePools() error = %v", err)
	}
	if storedPre.SqrtPriceX96.Cmp(sample.SqrtPriceX96BeforeBurn) != 0 {
		t.Fatalf("stored pre-burn sqrt was mutated")
	}
	if storedPost.Liquidity.Cmp(sample.ActiveLiquidityAfterBurn) != 0 {
		t.Fatalf("stored post-burn liquidity was mutated")
	}
}

func TestMergeBurnCounterfactualEventsOrdersByCursor(t *testing.T) {
	t.Parallel()

	q96 := new(big.Int).Lsh(big.NewInt(1), 96)
	events, err := mergeBurnCounterfactualEvents(
		burnRealizedFlowEventSet{
			Available:    true,
			BurnCursor:   domain.EventCursor{BlockNumber: 100, LogIndex: 5},
			ThroughBlock: 102,
			LiquidityChanges: []domain.LiquidityChange{
				{
					ID:             "mint",
					BlockNumber:    101,
					LogIndex:       20,
					TickLower:      -100,
					TickUpper:      100,
					LiquidityDelta: big.NewInt(10),
				},
			},
			Swaps: []domain.SwapEvent{
				{
					ID:                "swap",
					TxHash:            "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					PoolAddress:       "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					BlockNumber:       101,
					LogIndex:          10,
					Timestamp:         1,
					Amount0Raw:        big.NewInt(1),
					Amount1Raw:        big.NewInt(-1),
					SqrtPriceX96After: q96,
					TickAfter:         0,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("mergeBurnCounterfactualEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Type != burnCounterfactualEventSwap ||
		events[1].Type != burnCounterfactualEventLiquidity {
		t.Fatalf("unexpected event order")
	}
}

func TestMergeBurnCounterfactualEventsRejectsDuplicateCursor(t *testing.T) {
	t.Parallel()

	q96 := new(big.Int).Lsh(big.NewInt(1), 96)
	_, err := mergeBurnCounterfactualEvents(
		burnRealizedFlowEventSet{
			Available:    true,
			BurnCursor:   domain.EventCursor{BlockNumber: 100, LogIndex: 5},
			ThroughBlock: 101,
			LiquidityChanges: []domain.LiquidityChange{
				{
					ID:             "mint",
					BlockNumber:    101,
					LogIndex:       10,
					TickLower:      -100,
					TickUpper:      100,
					LiquidityDelta: big.NewInt(10),
				},
			},
			Swaps: []domain.SwapEvent{
				{
					ID:                "swap",
					TxHash:            "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					PoolAddress:       "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					BlockNumber:       101,
					LogIndex:          10,
					Timestamp:         1,
					Amount0Raw:        big.NewInt(1),
					Amount1Raw:        big.NewInt(-1),
					SqrtPriceX96After: q96,
					TickAfter:         0,
				},
			},
		},
	)
	if err == nil {
		t.Fatal("mergeBurnCounterfactualEvents() error = nil, want duplicate cursor error")
	}
}

func TestApplyCounterfactualSwapExactInput(t *testing.T) {
	t.Parallel()

	pool := preBurnReplayPool()
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatalf("NewSimulator() error = %v", err)
	}
	amountIn := big.NewInt(1_000_000)
	result, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{AmountIn: amountIn, ZeroForOne: true},
	)
	if err != nil {
		t.Fatalf("SimulateExactInput() error = %v", err)
	}

	swap := domain.SwapEvent{
		ID:                "swap",
		TxHash:            "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		PoolAddress:       pool.PoolAddress,
		BlockNumber:       101,
		LogIndex:          10,
		Timestamp:         1,
		Amount0Raw:        new(big.Int).Set(amountIn),
		Amount1Raw:        new(big.Int).Neg(new(big.Int).Set(result.AmountOut)),
		SqrtPriceX96After: new(big.Int).Set(result.SqrtPriceAfterX96),
		TickAfter:         result.TickAfter,
	}
	branch := burnCounterfactualBranchState{
		TieBreak:  BurnCounterfactualExactInputTieBreak,
		Available: true,
		Pool:      cloneBurnRuntimePool(pool),
	}

	applyCounterfactualSwap(
		&branch,
		swap,
		observedSwapReplayExactInput,
		simulator,
	)
	if !branch.Available {
		t.Fatalf("branch failed: %s", branch.FailureDetail)
	}
	if branch.Pool.SqrtPriceX96.Cmp(result.SqrtPriceAfterX96) != 0 ||
		branch.Pool.CurrentTick != result.TickAfter ||
		branch.Pool.Liquidity.Cmp(result.LiquidityAfter) != 0 {
		t.Fatalf("counterfactual exact-input state mismatch")
	}
}

func TestBurnCounterfactualReplaySupportsObservedZeroOutputSwap(
	t *testing.T,
) {
	t.Parallel()

	pool := preBurnReplayPool()
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatalf("NewSimulator() error = %v", err)
	}

	// One raw input unit with a non-zero fee is a protocol-valid fee-only
	// historical swap: usable input and output both round to zero.
	amountIn := big.NewInt(1)
	result, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:        amountIn,
			ZeroForOne:      true,
			AllowZeroOutput: true,
		},
	)
	if err != nil {
		t.Fatalf("SimulateExactInput() error = %v", err)
	}
	if result.AmountOut.Sign() != 0 {
		t.Fatalf("test fixture AmountOut = %s, want 0", result.AmountOut)
	}

	swap := domain.SwapEvent{
		ID:                "zero-output-swap",
		TxHash:            "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		PoolAddress:       pool.PoolAddress,
		BlockNumber:       101,
		LogIndex:          10,
		Timestamp:         1,
		Amount0Raw:        new(big.Int).Set(amountIn),
		Amount1Raw:        big.NewInt(0),
		SqrtPriceX96After: new(big.Int).Set(result.SqrtPriceAfterX96),
		TickAfter:         result.TickAfter,
	}

	service := NewBurnCounterfactualFutureService(
		&BurnRealizedOutcomeService{},
	)
	actual, mode, _, err := service.replayBurnCounterfactualActualSwap(
		cloneBurnRuntimePool(pool),
		swap,
		simulator,
	)
	if err != nil {
		t.Fatalf("replayBurnCounterfactualActualSwap() error = %v", err)
	}
	if mode != observedSwapReplayExactInput {
		t.Fatalf("mode = %q, want %q", mode, observedSwapReplayExactInput)
	}
	if actual.SqrtPriceX96.Cmp(swap.SqrtPriceX96After) != 0 ||
		actual.CurrentTick != swap.TickAfter ||
		actual.Liquidity.Cmp(result.LiquidityAfter) != 0 {
		t.Fatal("actual zero-output replay state mismatch")
	}

	branch := burnCounterfactualBranchState{
		TieBreak:  BurnCounterfactualExactInputTieBreak,
		Available: true,
		Pool:      cloneBurnRuntimePool(pool),
	}
	applyCounterfactualSwap(
		&branch,
		swap,
		observedSwapReplayExactInput,
		simulator,
	)
	if !branch.Available {
		t.Fatalf("counterfactual branch failed: %s", branch.FailureDetail)
	}
	if branch.Pool.SqrtPriceX96.Cmp(result.SqrtPriceAfterX96) != 0 ||
		branch.Pool.CurrentTick != result.TickAfter ||
		branch.Pool.Liquidity.Cmp(result.LiquidityAfter) != 0 {
		t.Fatal("counterfactual zero-output branch state mismatch")
	}
}

func TestBurnCounterfactualWorkerCountBounds(
	t *testing.T,
) {
	t.Parallel()

	service := &BurnCounterfactualFutureService{
		outcomeService: &BurnRealizedOutcomeService{
			flowCache: make(
				map[burnRealizedFlowCacheKey]burnRealizedFlowEventSet,
			),
		},
	}

	horizons := []BurnOutcomeHorizon{{Label: "b50", Blocks: 50}}
	samples := make([]BurnEventSample, 12)
	for index := range samples {
		samples[index].Burn = domain.BurnCandidate{
			PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Cursor: domain.EventCursor{
				BlockNumber: uint64(100 + index*100),
				LogIndex:    1,
			},
		}

		throughBlock := samples[index].Burn.Cursor.BlockNumber + 50
		service.outcomeService.storeBurnRealizedFlowEvents(
			newBurnRealizedFlowCacheKey(samples[index].Burn, throughBlock),
			burnRealizedFlowEventSet{Available: true},
		)
	}

	workers := service.workerCount(samples, horizons)
	if workers < 1 {
		t.Fatalf("worker count = %d, want positive", workers)
	}
	if workers > maxBurnCounterfactualWorkers {
		t.Fatalf(
			"worker count = %d, exceeds cap %d",
			workers,
			maxBurnCounterfactualWorkers,
		)
	}
	if workers > len(samples) {
		t.Fatalf(
			"worker count = %d, exceeds samples %d",
			workers,
			len(samples),
		)
	}

	delete(
		service.outcomeService.flowCache,
		newBurnRealizedFlowCacheKey(
			samples[0].Burn,
			samples[0].Burn.Cursor.BlockNumber+50,
		),
	)

	if got := service.workerCount(samples, horizons); got != 1 {
		t.Fatalf(
			"worker count with missing cache = %d, want 1",
			got,
		)
	}
}

func TestSetBurnCounterfactualEffectBounds(t *testing.T) {
	t.Parallel()

	observation := BurnCounterfactualObservation{
		ExactInputTieBreak: BurnCounterfactualBranchResult{
			Available:                       true,
			TotalMechanicalEffectPIAUCBps:   decimal.NewFromInt(-2),
			TotalMechanicalDeteriorationBps: decimal.NewFromInt(1),
		},
		ExactOutputTieBreak: BurnCounterfactualBranchResult{
			Available:                       true,
			TotalMechanicalEffectPIAUCBps:   decimal.NewFromInt(3),
			TotalMechanicalDeteriorationBps: decimal.NewFromInt(4),
		},
	}

	setBurnCounterfactualEffectBounds(&observation)
	if !observation.MinTotalMechanicalEffectPIAUCBps.Equal(decimal.NewFromInt(-2)) ||
		!observation.MaxTotalMechanicalEffectPIAUCBps.Equal(decimal.NewFromInt(3)) ||
		!observation.MinTotalMechanicalDeteriorationBps.Equal(decimal.NewFromInt(1)) ||
		!observation.MaxTotalMechanicalDeteriorationBps.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("unexpected counterfactual bounds")
	}
}

func TestNewBurnCounterfactualSwapReplayAuditRecordsTieBreakModes(t *testing.T) {
	t.Parallel()

	pool := preBurnReplayPool()
	swap := domain.SwapEvent{
		ID:                "swap-audit",
		TxHash:            "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		PoolAddress:       pool.PoolAddress,
		BlockNumber:       101,
		LogIndex:          12,
		Timestamp:         1,
		Amount0Raw:        big.NewInt(100),
		Amount1Raw:        big.NewInt(-50),
		SqrtPriceX96After: new(big.Int).Set(pool.SqrtPriceX96),
		TickAfter:         pool.CurrentTick,
	}
	burn := domain.BurnCandidate{
		ID:               "burn-audit",
		PoolAddress:      pool.PoolAddress,
		TxHash:           "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Cursor:           domain.EventCursor{BlockNumber: 100, LogIndex: 5},
		TickLower:        -100,
		TickUpper:        100,
		LiquidityRemoved: big.NewInt(1),
	}

	audit := newBurnCounterfactualSwapReplayAudit(
		burn,
		swap,
		observedSwapReplayBoth,
		"both modes matched",
		pool,
		burnCounterfactualBranchState{
			TieBreak:  BurnCounterfactualExactInputTieBreak,
			Available: true,
			Pool:      cloneBurnRuntimePool(pool),
		},
		burnCounterfactualBranchState{
			TieBreak:  BurnCounterfactualExactOutputTieBreak,
			Available: true,
			Pool:      cloneBurnRuntimePool(pool),
		},
	)

	if !audit.ActualAmbiguous {
		t.Fatal("ActualAmbiguous = false, want true")
	}
	if audit.ExactInputTieBreakMode != observedSwapReplayExactInput {
		t.Fatalf(
			"ExactInputTieBreakMode = %q, want %q",
			audit.ExactInputTieBreakMode,
			observedSwapReplayExactInput,
		)
	}
	if audit.ExactOutputTieBreakMode != observedSwapReplayExactOutput {
		t.Fatalf(
			"ExactOutputTieBreakMode = %q, want %q",
			audit.ExactOutputTieBreakMode,
			observedSwapReplayExactOutput,
		)
	}
}

func TestBuildBurnCounterfactualFutureSummaryExcludesUnavailableObservationsFromMean(
	t *testing.T,
) {
	t.Parallel()

	report := BurnCounterfactualFutureReport{
		PoolAddress:          "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FromBlock:            100,
		ToBlock:              200,
		BurnSamples:          1,
		HorizonsPerSample:    2,
		ExpectedObservations: 2,
		Observations: []BurnCounterfactualObservation{
			{
				ExactInputTieBreak:                 BurnCounterfactualBranchResult{Available: true},
				MinTotalMechanicalEffectPIAUCBps:   decimal.NewFromInt(10),
				MaxTotalMechanicalEffectPIAUCBps:   decimal.NewFromInt(20),
				MinTotalMechanicalDeteriorationBps: decimal.NewFromInt(3),
				MaxTotalMechanicalDeteriorationBps: decimal.NewFromInt(7),
			},
			{},
		},
		SingleBranchAvailable: 1,
		NoBranchAvailable:     1,
	}

	summary, err := BuildBurnCounterfactualFutureSummary(report)
	if err != nil {
		t.Fatalf("BuildBurnCounterfactualFutureSummary() error = %v", err)
	}
	if summary.AvailableObservations != 1 {
		t.Fatalf(
			"AvailableObservations = %d, want 1",
			summary.AvailableObservations,
		)
	}
	if !summary.MeanMinTotalMechanicalEffectPIAUCBps.Equal(decimal.NewFromInt(10)) ||
		!summary.MeanMaxTotalMechanicalEffectPIAUCBps.Equal(decimal.NewFromInt(20)) ||
		!summary.MeanMinTotalMechanicalDeteriorationBps.Equal(decimal.NewFromInt(3)) ||
		!summary.MeanMaxTotalMechanicalDeteriorationBps.Equal(decimal.NewFromInt(7)) {
		t.Fatal("summary means include unavailable observations")
	}
}

func TestApplyCounterfactualSwapPairSharesUndivergedReplay(
	t *testing.T,
) {
	t.Parallel()

	pool := preBurnReplayPool()
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatalf("NewSimulator() error = %v", err)
	}

	amountIn := big.NewInt(1_000_000)
	result, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:   amountIn,
			ZeroForOne: true,
		},
	)
	if err != nil {
		t.Fatalf("SimulateExactInput() error = %v", err)
	}

	swap := domain.SwapEvent{
		ID:                "shared-undiverged-swap",
		TxHash:            "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PoolAddress:       pool.PoolAddress,
		BlockNumber:       101,
		LogIndex:          10,
		Timestamp:         1,
		Amount0Raw:        new(big.Int).Set(amountIn),
		Amount1Raw:        new(big.Int).Neg(new(big.Int).Set(result.AmountOut)),
		SqrtPriceX96After: new(big.Int).Set(result.SqrtPriceAfterX96),
		TickAfter:         result.TickAfter,
	}

	exactInput := burnCounterfactualBranchState{
		TieBreak:  BurnCounterfactualExactInputTieBreak,
		Available: true,
		Pool:      cloneBurnRuntimePool(pool),
	}
	exactOutput := burnCounterfactualBranchState{
		TieBreak:  BurnCounterfactualExactOutputTieBreak,
		Available: true,
		Pool:      cloneBurnRuntimePool(pool),
	}

	applyCounterfactualSwapPair(
		&exactInput,
		&exactOutput,
		swap,
		observedSwapReplayExactInput,
		simulator,
		false,
	)

	if !exactInput.Available || !exactOutput.Available {
		t.Fatalf(
			"branches unavailable: input=%q output=%q",
			exactInput.FailureDetail,
			exactOutput.FailureDetail,
		)
	}
	if exactInput.Pool != exactOutput.Pool {
		t.Fatal("undiverged branches should share the immutable post-swap runtime")
	}
	if exactInput.Pool.SqrtPriceX96.Cmp(exactOutput.Pool.SqrtPriceX96) != 0 ||
		exactInput.Pool.CurrentTick != exactOutput.Pool.CurrentTick ||
		exactInput.Pool.Liquidity.Cmp(exactOutput.Pool.Liquidity) != 0 {
		t.Fatal("undiverged branches do not have identical post-swap state")
	}
}

func TestCloneBurnCounterfactualBranchResultForTieBreakDeepCopies(
	t *testing.T,
) {
	t.Parallel()

	cursor := domain.EventCursor{BlockNumber: 100, LogIndex: 5}
	original := BurnCounterfactualBranchResult{
		TieBreak:              BurnCounterfactualExactInputTieBreak,
		Available:             true,
		FailureCursor:         &cursor,
		FutureSqrtPriceX96:    big.NewInt(1_000),
		FutureActiveLiquidity: big.NewInt(2_000),
		ZeroForOne: BurnCounterfactualDirectionalResult{
			ActualDepths: []ThresholdDepth{{DepthAmount: decimal.NewFromInt(1)}},
			NoBurnDepths: []ThresholdDepth{{DepthAmount: decimal.NewFromInt(2)}},
		},
	}

	cloned := cloneBurnCounterfactualBranchResultForTieBreak(
		original,
		BurnCounterfactualExactOutputTieBreak,
	)

	original.FutureSqrtPriceX96.Add(original.FutureSqrtPriceX96, big.NewInt(1))
	original.FutureActiveLiquidity.Add(original.FutureActiveLiquidity, big.NewInt(1))
	original.ZeroForOne.ActualDepths[0].DepthAmount = decimal.NewFromInt(99)

	if cloned.TieBreak != BurnCounterfactualExactOutputTieBreak {
		t.Fatalf("tie break = %q, want exact-output", cloned.TieBreak)
	}
	if cloned.FutureSqrtPriceX96.Cmp(big.NewInt(1_000)) != 0 ||
		cloned.FutureActiveLiquidity.Cmp(big.NewInt(2_000)) != 0 ||
		!cloned.ZeroForOne.ActualDepths[0].DepthAmount.Equal(decimal.NewFromInt(1)) {
		t.Fatal("counterfactual branch clone shares mutable state")
	}
}

func TestClassifyBurnCounterfactualObservedModeFallsBackToSensitivityEnvelope(
	t *testing.T,
) {
	t.Parallel()

	swap := domain.SwapEvent{
		ID:          "0x31d998fe575717668a8dd5e1aa883b658281ef5a7a7b53314351919a68e6d583#10721548",
		TxHash:      "0x31d998fe575717668a8dd5e1aa883b658281ef5a7a7b53314351919a68e6d583",
		PoolAddress: "0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640",
		BlockNumber: 24365200,
		LogIndex:    787,
		Timestamp:   1,
		Amount0Raw:  mustObservedSwapBigInt(t, "-12286934057"),
		Amount1Raw:  mustObservedSwapBigInt(t, "5516756978581046696"),
		SqrtPriceX96After: mustObservedSwapBigInt(
			t,
			"1678789849177930175842034046157516",
		),
		TickAfter: 199234,
	}

	exactInput := observedSwapSimulation{
		Mode:              observedSwapReplayExactInput,
		AmountIn:          mustObservedSwapBigInt(t, "5516756978581046696"),
		AmountOut:         mustObservedSwapBigInt(t, "12286190950"),
		SqrtPriceAfterX96: mustObservedSwapBigInt(t, "1678840599027935627201596484186080"),
		TickAfter:         199235,
		LiquidityAfter:    mustObservedSwapBigInt(t, "537362928130176165"),
		CrossedTicks:      1,
		SwapSteps:         2,
	}
	exactOutput := observedSwapSimulation{
		Mode:              observedSwapReplayExactOutput,
		AmountIn:          mustObservedSwapBigInt(t, "5517090810779686808"),
		AmountOut:         mustObservedSwapBigInt(t, "12286934057"),
		SqrtPriceAfterX96: mustObservedSwapBigInt(t, "1678840648223155182998182457209776"),
		TickAfter:         199235,
		LiquidityAfter:    mustObservedSwapBigInt(t, "537362928130176165"),
		CrossedTicks:      1,
		SwapSteps:         2,
	}

	resolution := classifyBurnCounterfactualObservedMode(
		exactInput,
		nil,
		exactOutput,
		nil,
		swap,
	)

	if resolution.Mode != observedSwapReplayUnresolved {
		t.Fatalf(
			"mode = %q, want %q",
			resolution.Mode,
			observedSwapReplayUnresolved,
		)
	}
	if !observedSwapReplayModeRequiresTieBreak(resolution.Mode) {
		t.Fatal("unresolved mode must preserve both sensitivity branches")
	}
	if resolution.Detail == "" {
		t.Fatal("unresolved mode detail is empty")
	}
}

func TestReplayBurnCounterfactualActualSwapUsesObservedPostStateWhenModeUnresolved(
	t *testing.T,
) {
	t.Parallel()

	pool := preBurnReplayPool()
	simulator, err := uniswapv3.NewSimulator(500)
	if err != nil {
		t.Fatalf("NewSimulator() error = %v", err)
	}

	// Deliberately use token deltas that do not reproduce this observed
	// post-state under either protocol mode. The actual branch must still advance
	// from the authoritative on-chain post-state while the no-burn analysis keeps
	// both interpretations as a sensitivity envelope.
	swap := domain.SwapEvent{
		ID:          "unresolved-actual-swap",
		TxHash:      "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PoolAddress: pool.PoolAddress,
		BlockNumber: pool.BlockNumber + 1,
		LogIndex:    1,
		Timestamp:   1,
		Amount0Raw:  big.NewInt(-7),
		Amount1Raw:  big.NewInt(11),
		SqrtPriceX96After: new(big.Int).Add(
			new(big.Int).Set(pool.SqrtPriceX96),
			big.NewInt(1),
		),
		TickAfter: pool.CurrentTick,
	}

	service := NewBurnCounterfactualFutureService(
		&BurnRealizedOutcomeService{},
	)
	next, mode, detail, err := service.replayBurnCounterfactualActualSwap(
		cloneBurnRuntimePool(pool),
		swap,
		simulator,
	)
	if err != nil {
		t.Fatalf("replayBurnCounterfactualActualSwap() error = %v", err)
	}
	if mode != observedSwapReplayUnresolved {
		t.Fatalf("mode = %q, want unresolved", mode)
	}
	if detail == "" {
		t.Fatal("mode detail is empty")
	}
	if next.SqrtPriceX96.Cmp(swap.SqrtPriceX96After) != 0 {
		t.Fatalf(
			"actual sqrt = %s, want observed %s",
			next.SqrtPriceX96,
			swap.SqrtPriceX96After,
		)
	}
	if next.CurrentTick != swap.TickAfter {
		t.Fatalf("actual tick = %d, want %d", next.CurrentTick, swap.TickAfter)
	}
}

func TestBurnCounterfactualAppliedModeMapsUnresolvedToTieBreak(t *testing.T) {
	t.Parallel()

	if got := burnCounterfactualAppliedMode(
		observedSwapReplayUnresolved,
		BurnCounterfactualExactInputTieBreak,
	); got != observedSwapReplayExactInput {
		t.Fatalf("exact-input tie-break mode = %q", got)
	}
	if got := burnCounterfactualAppliedMode(
		observedSwapReplayUnresolved,
		BurnCounterfactualExactOutputTieBreak,
	); got != observedSwapReplayExactOutput {
		t.Fatalf("exact-output tie-break mode = %q", got)
	}
}
