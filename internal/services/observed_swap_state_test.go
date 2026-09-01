package services

import (
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestApplyObservedSwapStateUsesAuthoritativePostState(t *testing.T) {
	current := &domain.ReconstructedPool{
		PoolAddress:  "0xpool",
		BlockNumber:  99,
		SqrtPriceX96: big.NewInt(1_000_000),
		CurrentTick:  0,
		Liquidity:    big.NewInt(1_000),
		Ticks: map[int]*domain.TickState{
			10: {
				Index:          10,
				LiquidityGross: big.NewInt(500),
				LiquidityNet:   big.NewInt(500),
			},
			20: {
				Index:          20,
				LiquidityGross: big.NewInt(200),
				LiquidityNet:   big.NewInt(-200),
			},
		},
		InitializedTicks: []int{10, 20},
	}

	// Amounts intentionally do not need to be protocol-replayable. Historical
	// state reconstruction consumes the authoritative post-state fields.
	swap := domain.SwapEvent{
		ID:                "swap:1",
		TxHash:            "0xtx",
		PoolAddress:       "0xpool",
		BlockNumber:       100,
		LogIndex:          7,
		Timestamp:         1,
		Amount0Raw:        big.NewInt(-1),
		Amount1Raw:        big.NewInt(1),
		SqrtPriceX96After: big.NewInt(1_200_000),
		TickAfter:         20,
	}

	next, err := applyObservedSwapState(current, swap)
	if err != nil {
		t.Fatalf("applyObservedSwapState() error = %v", err)
	}

	if next.BlockNumber != 100 {
		t.Fatalf("BlockNumber = %d, want 100", next.BlockNumber)
	}
	if next.CurrentTick != 20 {
		t.Fatalf("CurrentTick = %d, want 20", next.CurrentTick)
	}
	if next.SqrtPriceX96.Cmp(swap.SqrtPriceX96After) != 0 {
		t.Fatalf("SqrtPriceX96 = %s, want %s", next.SqrtPriceX96, swap.SqrtPriceX96After)
	}
	if next.Liquidity.Cmp(big.NewInt(1_300)) != 0 {
		t.Fatalf("Liquidity = %s, want 1300", next.Liquidity)
	}

	if current.CurrentTick != 0 || current.Liquidity.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("input pool was mutated")
	}
}

func TestApplyObservedSwapStateCrossesDownwardTicks(t *testing.T) {
	current := &domain.ReconstructedPool{
		PoolAddress:  "0xpool",
		BlockNumber:  100,
		SqrtPriceX96: big.NewInt(1_200_000),
		CurrentTick:  20,
		Liquidity:    big.NewInt(1_300),
		Ticks: map[int]*domain.TickState{
			10: {
				Index:          10,
				LiquidityGross: big.NewInt(500),
				LiquidityNet:   big.NewInt(500),
			},
			20: {
				Index:          20,
				LiquidityGross: big.NewInt(200),
				LiquidityNet:   big.NewInt(-200),
			},
		},
		InitializedTicks: []int{10, 20},
	}

	swap := domain.SwapEvent{
		ID:                "swap:2",
		TxHash:            "0xtx2",
		PoolAddress:       "0xpool",
		BlockNumber:       101,
		LogIndex:          9,
		Timestamp:         2,
		Amount0Raw:        big.NewInt(1),
		Amount1Raw:        big.NewInt(-1),
		SqrtPriceX96After: big.NewInt(900_000),
		TickAfter:         0,
	}

	next, err := applyObservedSwapState(current, swap)
	if err != nil {
		t.Fatalf("applyObservedSwapState() error = %v", err)
	}
	if next.Liquidity.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("Liquidity = %s, want 1000", next.Liquidity)
	}
}

func TestApplyObservedSwapStateUsesObservedTickMovementWhenAmountsDisagree(t *testing.T) {
	current := &domain.ReconstructedPool{
		PoolAddress:  "0xpool",
		BlockNumber:  99,
		SqrtPriceX96: big.NewInt(1_000_000),
		CurrentTick:  0,
		Liquidity:    big.NewInt(1_000),
		Ticks: map[int]*domain.TickState{
			1: {
				Index:          1,
				LiquidityGross: big.NewInt(250),
				LiquidityNet:   big.NewInt(250),
			},
		},
		InitializedTicks: []int{1},
	}

	// The token deltas classify this as zero-for-one, but the authoritative
	// observed post-state moved upward. Offline timeline reconstruction must
	// follow the observed tick transition rather than reject the event or infer
	// crossed liquidity from token-amount signs.
	swap := domain.SwapEvent{
		ID:                "swap:3",
		TxHash:            "0xtx3",
		PoolAddress:       "0xpool",
		BlockNumber:       100,
		LogIndex:          1,
		Timestamp:         3,
		Amount0Raw:        big.NewInt(1),
		Amount1Raw:        big.NewInt(-1),
		SqrtPriceX96After: big.NewInt(1_100_000),
		TickAfter:         1,
	}

	next, err := applyObservedSwapState(current, swap)
	if err != nil {
		t.Fatalf("applyObservedSwapState() error = %v", err)
	}
	if next.CurrentTick != 1 {
		t.Fatalf("CurrentTick = %d, want 1", next.CurrentTick)
	}
	if next.SqrtPriceX96.Cmp(big.NewInt(1_100_000)) != 0 {
		t.Fatalf("SqrtPriceX96 = %s, want 1100000", next.SqrtPriceX96)
	}
	if next.Liquidity.Cmp(big.NewInt(1_250)) != 0 {
		t.Fatalf("Liquidity = %s, want 1250", next.Liquidity)
	}
}

func TestApplyObservedSwapStateHistoricalDirectionDisagreement(t *testing.T) {
	current := &domain.ReconstructedPool{
		PoolAddress: "0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640",
		BlockNumber: 24412637,
		SqrtPriceX96: mustObservedSwapBigInt(
			t,
			"1721817549143520094040614249686610",
		),
		CurrentTick:      199787,
		Liquidity:        big.NewInt(1_000),
		Ticks:            map[int]*domain.TickState{},
		InitializedTicks: nil,
	}

	swap := domain.SwapEvent{
		ID:          "0xhistorical-direction#event",
		TxHash:      "0xhistorical-direction",
		PoolAddress: current.PoolAddress,
		BlockNumber: 24412638,
		LogIndex:    108,
		Timestamp:   1,
		Amount0Raw:  big.NewInt(1),
		Amount1Raw:  big.NewInt(-1),
		SqrtPriceX96After: mustObservedSwapBigInt(
			t,
			"1721818643891266617685602460460101",
		),
		TickAfter: 199787,
	}

	next, err := applyObservedSwapState(current, swap)
	if err != nil {
		t.Fatalf("applyObservedSwapState() error = %v", err)
	}
	if next.SqrtPriceX96.Cmp(swap.SqrtPriceX96After) != 0 {
		t.Fatalf("sqrtPriceX96 = %s, want %s", next.SqrtPriceX96, swap.SqrtPriceX96After)
	}
	if next.CurrentTick != swap.TickAfter {
		t.Fatalf("tick = %d, want %d", next.CurrentTick, swap.TickAfter)
	}
	if next.Liquidity.Cmp(current.Liquidity) != 0 {
		t.Fatalf("liquidity = %s, want %s", next.Liquidity, current.Liquidity)
	}
}

func TestApplyObservedSwapStateHistoricalAmountParityMismatch(t *testing.T) {
	current := &domain.ReconstructedPool{
		PoolAddress: "0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640",
		BlockNumber: 24365199,
		SqrtPriceX96: new(big.Int).Sub(
			mustObservedSwapBigInt(t, "1678789849177930175842034046157516"),
			big.NewInt(1),
		),
		CurrentTick:      199230,
		Liquidity:        big.NewInt(537362928130176165),
		Ticks:            map[int]*domain.TickState{},
		InitializedTicks: nil,
	}

	swap := domain.SwapEvent{
		ID:          "0xhistorical#event",
		TxHash:      "0xhistorical",
		PoolAddress: current.PoolAddress,
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

	next, err := applyObservedSwapState(current, swap)
	if err != nil {
		t.Fatalf("applyObservedSwapState() error = %v", err)
	}
	if next.SqrtPriceX96.Cmp(swap.SqrtPriceX96After) != 0 {
		t.Fatalf("sqrtPriceX96 = %s, want %s", next.SqrtPriceX96, swap.SqrtPriceX96After)
	}
	if next.CurrentTick != swap.TickAfter {
		t.Fatalf("tick = %d, want %d", next.CurrentTick, swap.TickAfter)
	}
}

func mustObservedSwapBigInt(t *testing.T, value string) *big.Int {
	t.Helper()
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		t.Fatalf("invalid integer fixture %q", value)
	}
	return result
}
