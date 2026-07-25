package services

import (
	"math/big"
	"strings"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestSelectActivePositionsByCoverageUsesSmallestPrefix(
	t *testing.T,
) {
	t.Parallel()

	positions := []domain.LiquidityPosition{
		coveragePosition(50),
		coveragePosition(25),
		coveragePosition(15),
		coveragePosition(10),
	}

	selection, err :=
		selectActivePositionsByCoverage(
			positions,
			big.NewInt(100),
			9_000,
		)
	if err != nil {
		t.Fatalf(
			"selectActivePositionsByCoverage() error = %v",
			err,
		)
	}

	if selection.LoadedPositionCount != 4 {
		t.Fatalf(
			"loaded position count = %d, want 4",
			selection.LoadedPositionCount,
		)
	}

	if selection.SelectedPositionCount != 3 {
		t.Fatalf(
			"selected position count = %d, want 3",
			selection.SelectedPositionCount,
		)
	}

	if selection.SelectedLiquidity.Cmp(
		big.NewInt(90),
	) != 0 {
		t.Fatalf(
			"selected liquidity = %s, want 90",
			selection.SelectedLiquidity,
		)
	}

	if selection.
		AchievedCoverageBps.
		String() != "9000" {
		t.Fatalf(
			"achieved coverage = %s, want 9000",
			selection.AchievedCoverageBps,
		)
	}
}

func TestSelectActivePositionsByCoverageIncludesNextPositionWhenNeeded(
	t *testing.T,
) {
	t.Parallel()

	selection, err :=
		selectActivePositionsByCoverage(
			[]domain.LiquidityPosition{
				coveragePosition(50),
				coveragePosition(25),
				coveragePosition(15),
				coveragePosition(10),
			},
			big.NewInt(100),
			9_500,
		)
	if err != nil {
		t.Fatalf(
			"selectActivePositionsByCoverage() error = %v",
			err,
		)
	}

	if selection.SelectedPositionCount != 4 {
		t.Fatalf(
			"selected position count = %d, want 4",
			selection.SelectedPositionCount,
		)
	}

	if selection.
		AchievedCoverageBps.
		String() != "10000" {
		t.Fatalf(
			"achieved coverage = %s, want 10000",
			selection.AchievedCoverageBps,
		)
	}
}

func TestSelectActivePositionsByCoverageRejectsInsufficientLoadedPositions(
	t *testing.T,
) {
	t.Parallel()

	_, err :=
		selectActivePositionsByCoverage(
			[]domain.LiquidityPosition{
				coveragePosition(50),
				coveragePosition(25),
			},
			big.NewInt(100),
			9_500,
		)
	if err == nil {
		t.Fatal(
			"selectActivePositionsByCoverage() error = nil, want non-nil",
		)
	}

	if !strings.Contains(
		err.Error(),
		"increase POSITION_LIMIT",
	) {
		t.Fatalf(
			"error = %q, want POSITION_LIMIT diagnostic",
			err,
		)
	}
}

func TestSelectActivePositionsByCoverageRejectsUnsortedPositions(
	t *testing.T,
) {
	t.Parallel()

	_, err :=
		selectActivePositionsByCoverage(
			[]domain.LiquidityPosition{
				coveragePosition(25),
				coveragePosition(50),
			},
			big.NewInt(100),
			5_000,
		)
	if err == nil {
		t.Fatal(
			"selectActivePositionsByCoverage() error = nil, want non-nil",
		)
	}

	if !strings.Contains(
		err.Error(),
		"not ordered by descending liquidity",
	) {
		t.Fatalf(
			"error = %q, want ordering diagnostic",
			err,
		)
	}
}

func coveragePosition(
	liquidity int64,
) domain.LiquidityPosition {
	return domain.LiquidityPosition{
		PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

		Owner: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",

		TickLower: -100,
		TickUpper: 100,

		Liquidity: big.NewInt(liquidity),
	}
}
