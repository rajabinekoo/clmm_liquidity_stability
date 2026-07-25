package services

import (
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestFindBurnSampleSpacingConflict(
	t *testing.T,
) {
	t.Parallel()

	samples := []BurnEventSample{
		{
			Burn: collectorBurnCandidate(
				"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				10_000,
				1,
				100,
			),
		},
		{
			Burn: collectorBurnCandidate(
				"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				30_000,
				1,
				100,
			),
		},
	}

	candidate :=
		collectorBurnCandidate(
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			15_000,
			1,
			100,
		)

	conflict :=
		findBurnSampleSpacingConflict(
			candidate,
			samples,
			7_200,
		)

	if conflict == nil {
		t.Fatal(
			"spacing conflict = nil, want non-nil",
		)
	}

	if conflict.BlockNumber != 10_000 {
		t.Fatalf(
			"conflict block = %d, want 10000",
			conflict.BlockNumber,
		)
	}

	if conflict.Distance != 5_000 {
		t.Fatalf(
			"conflict distance = %d, want 5000",
			conflict.Distance,
		)
	}
}

func TestFindBurnSampleSpacingConflictAcceptsBoundary(
	t *testing.T,
) {
	t.Parallel()

	samples := []BurnEventSample{
		{
			Burn: domain.BurnCandidate{
				ID: "burn-1",

				PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

				Cursor: domain.EventCursor{
					BlockNumber: 10_000,
					LogIndex:    1,
				},

				TickLower: -100,
				TickUpper: 100,

				LiquidityRemoved: big.NewInt(100),
			},
		},
	}

	candidate :=
		collectorBurnCandidate(
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			17_200,
			1,
			100,
		)

	conflict :=
		findBurnSampleSpacingConflict(
			candidate,
			samples,
			7_200,
		)

	if conflict != nil {
		t.Fatalf(
			"spacing conflict = %+v, want nil",
			conflict,
		)
	}
}
