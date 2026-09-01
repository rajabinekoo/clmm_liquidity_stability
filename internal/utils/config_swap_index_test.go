package utils

import "testing"

func TestRequiredSwapIndexStartBlockDerivedCoverage(t *testing.T) {
	t.Parallel()

	cfg := Config{
		LookbackBlocks:            1_000_000,
		WindowSize:                10_000,
		BurnMinimumSpacingBlocks:  7_201,
		ValidationBlockWindowSize: 8,
		ConfirmationDepth:         20,
	}

	got, err := cfg.RequiredSwapIndexStartBlock(
		25_206_728,
		12_369_821,
	)
	if err != nil {
		t.Fatalf("RequiredSwapIndexStartBlock() error = %v", err)
	}

	const want = uint64(24_189_499)
	if got != want {
		t.Fatalf("RequiredSwapIndexStartBlock() = %d, want %d", got, want)
	}
}

func TestRequiredSwapIndexStartBlockHonorsExplicitBoundary(t *testing.T) {
	t.Parallel()

	cfg := Config{SwapIndexStartBlock: 24_000_000}

	got, err := cfg.RequiredSwapIndexStartBlock(
		25_000_000,
		12_000_000,
	)
	if err != nil {
		t.Fatalf("RequiredSwapIndexStartBlock() error = %v", err)
	}

	if got != 24_000_000 {
		t.Fatalf("RequiredSwapIndexStartBlock() = %d, want 24000000", got)
	}
}

func TestRequiredSwapIndexStartBlockNeverPrecedesPoolCreation(t *testing.T) {
	t.Parallel()

	cfg := Config{
		LookbackBlocks:            1_000_000,
		WindowSize:                10_000,
		BurnMinimumSpacingBlocks:  7_201,
		ValidationBlockWindowSize: 8,
		ConfirmationDepth:         20,
	}

	got, err := cfg.RequiredSwapIndexStartBlock(
		1_000,
		900,
	)
	if err != nil {
		t.Fatalf("RequiredSwapIndexStartBlock() error = %v", err)
	}

	if got != 900 {
		t.Fatalf("RequiredSwapIndexStartBlock() = %d, want pool creation block 900", got)
	}
}
