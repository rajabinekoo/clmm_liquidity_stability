package services

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestSelectJointRemovalPositionsTopN(
	t *testing.T,
) {
	t.Parallel()

	positions :=
		jointRemovalTestPositions()

	selected, err :=
		selectJointRemovalPositions(
			positions,
			big.NewInt(100),
			jointRemovalScenarioSpec{
				ID: "top_3_by_lsis",

				Kind: JointRemovalScenarioTopNByLSIS,

				TopCount: 3,
			},
		)
	if err != nil {
		t.Fatalf(
			"selectJointRemovalPositions() error = %v",
			err,
		)
	}

	if len(selected) != 3 {
		t.Fatalf(
			"selected count = %d, want 3",
			len(selected),
		)
	}

	if selected[0].PositionKey != "position-1" ||
		selected[2].PositionKey != "position-3" {
		t.Fatalf(
			"unexpected selected positions: %+v",
			selected,
		)
	}
}

func TestSelectJointRemovalPositionsByLiquidityShare(
	t *testing.T,
) {
	t.Parallel()

	selected, err :=
		selectJointRemovalPositions(
			jointRemovalTestPositions(),
			big.NewInt(100),
			jointRemovalScenarioSpec{
				ID: "target_2500_bps",

				Kind: JointRemovalScenarioTargetActiveLiquidityShareByLSIS,

				TargetActiveLiquidityShareBps: 2_500,
			},
		)
	if err != nil {
		t.Fatalf(
			"selectJointRemovalPositions() error = %v",
			err,
		)
	}

	if len(selected) != 2 {
		t.Fatalf(
			"selected count = %d, want 2",
			len(selected),
		)
	}

	selectedLiquidity :=
		new(big.Int).Add(
			selected[0].Position.Liquidity,
			selected[1].Position.Liquidity,
		)

	if selectedLiquidity.Cmp(
		big.NewInt(30),
	) != 0 {
		t.Fatalf(
			"selected liquidity = %s, want 30",
			selectedLiquidity,
		)
	}
}

func TestJointRemovalAmplificationRatio(
	t *testing.T,
) {
	t.Parallel()

	result :=
		jointRemovalAmplificationRatio(
			decimal.NewFromInt(30),
			decimal.NewFromInt(20),
		)

	if !result.Equal(
		decimal.RequireFromString("1.5"),
	) {
		t.Fatalf(
			"ratio = %s, want 1.5",
			result,
		)
	}
}

func jointRemovalTestPositions() []BidirectionalPositionImpact {
	liquidities :=
		[]int64{
			20,
			10,
			8,
			7,
			5,
		}

	result := make(
		[]BidirectionalPositionImpact,
		0,
		len(liquidities),
	)

	for index, liquidity := range liquidities {
		result =
			append(
				result,
				BidirectionalPositionImpact{
					Position: domain.LiquidityPosition{
						PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

						Owner: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",

						TickLower: -100 - index,

						TickUpper: 100 + index,

						Liquidity: big.NewInt(
							liquidity,
						),
					},

					PositionKey: "position-" +
						decimal.
							NewFromInt(
								int64(index+1),
							).
							String(),

					TotalLSISBps: decimal.NewFromInt(
						int64(
							100 - index,
						),
					),
				},
			)
	}

	return result
}

func TestJointRemovalUnavailableScenarioKeepsSnapshotShape(
	t *testing.T,
) {
	t.Parallel()

	pool :=
		&domain.ReconstructedPool{
			Liquidity: big.NewInt(100),
		}

	result :=
		jointRemovalUnavailableScenario(
			JointRemovalRequest{
				Pool: pool,

				BaseReport: &BidirectionalLiquidityImpactReport{},
			},
			jointRemovalScenarioSpec{
				ID: "top_5_by_lsis",

				Kind: JointRemovalScenarioTopNByLSIS,

				TopCount: 5,
			},
			"requested top 5 positions but only 4 are available",
		)

	if !result.Skipped {
		t.Fatal(
			"Skipped = false, want true",
		)
	}

	if result.ScenarioID !=
		"top_5_by_lsis" {
		t.Fatalf(
			"ScenarioID = %q, want top_5_by_lsis",
			result.ScenarioID,
		)
	}

	if result.RequestedPositionCount != 5 {
		t.Fatalf(
			"RequestedPositionCount = %d, want 5",
			result.RequestedPositionCount,
		)
	}

	if result.RemovedLiquidity == nil ||
		result.RemovedLiquidity.Sign() != 0 {
		t.Fatalf(
			"RemovedLiquidity = %v, want zero",
			result.RemovedLiquidity,
		)
	}

	if result.CounterfactualActiveLiquidity == nil ||
		result.CounterfactualActiveLiquidity.Cmp(
			big.NewInt(100),
		) != 0 {
		t.Fatalf(
			"CounterfactualActiveLiquidity = %v, want 100",
			result.CounterfactualActiveLiquidity,
		)
	}
}
