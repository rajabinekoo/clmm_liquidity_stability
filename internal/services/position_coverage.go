package services

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const (
	defaultPositionCoverageBps  int64 = 9_500
	positionCoverageDenominator int64 = 10_000
)

type ActivePositionSelection struct {
	Positions []domain.LiquidityPosition

	LoadedPositionCount   int
	SelectedPositionCount int

	SelectedLiquidity *big.Int

	TargetCoverageBps   int64
	AchievedCoverageBps decimal.Decimal
}

func selectActivePositionsByCoverage(
	positions []domain.LiquidityPosition,
	activeLiquidity *big.Int,
	targetCoverageBps int64,
) (ActivePositionSelection, error) {
	if activeLiquidity == nil ||
		activeLiquidity.Sign() <= 0 {
		return ActivePositionSelection{}, fmt.Errorf(
			"select active positions by coverage: active liquidity must be positive",
		)
	}

	if targetCoverageBps <= 0 ||
		targetCoverageBps >
			positionCoverageDenominator {
		return ActivePositionSelection{}, fmt.Errorf(
			"select active positions by coverage: target coverage bps %d must be inside [1,%d]",
			targetCoverageBps,
			positionCoverageDenominator,
		)
	}

	if len(positions) == 0 {
		return ActivePositionSelection{}, fmt.Errorf(
			"select active positions by coverage: no active positions were loaded",
		)
	}

	// ceil(activeLiquidity * targetCoverageBps / 10000)
	requiredLiquidity := new(big.Int).Mul(
		new(big.Int).Set(
			activeLiquidity,
		),
		big.NewInt(
			targetCoverageBps,
		),
	)

	requiredLiquidity.Add(
		requiredLiquidity,
		big.NewInt(
			positionCoverageDenominator-1,
		),
	)

	requiredLiquidity.Quo(
		requiredLiquidity,
		big.NewInt(
			positionCoverageDenominator,
		),
	)

	selected := make(
		[]domain.LiquidityPosition,
		0,
		len(positions),
	)

	selectedLiquidity :=
		big.NewInt(0)

	var previousLiquidity *big.Int

	for index, position := range positions {
		if position.Liquidity == nil ||
			position.Liquidity.Sign() <= 0 {
			return ActivePositionSelection{}, fmt.Errorf(
				"select active positions by coverage: position %d has invalid liquidity %v",
				index,
				position.Liquidity,
			)
		}

		if previousLiquidity != nil &&
			position.Liquidity.Cmp(
				previousLiquidity,
			) > 0 {
			return ActivePositionSelection{}, fmt.Errorf(
				"select active positions by coverage: positions are not ordered by descending liquidity at index %d: previous=%s current=%s",
				index,
				previousLiquidity,
				position.Liquidity,
			)
		}

		previousLiquidity =
			new(big.Int).Set(
				position.Liquidity,
			)

		selectedLiquidity.Add(
			selectedLiquidity,
			position.Liquidity,
		)

		if selectedLiquidity.Cmp(
			activeLiquidity,
		) > 0 {
			return ActivePositionSelection{}, fmt.Errorf(
				"select active positions by coverage: selected liquidity %s exceeds pool active liquidity %s at position %d",
				selectedLiquidity,
				activeLiquidity,
				index,
			)
		}

		selected = append(
			selected,
			cloneLiquidityPosition(
				position,
			),
		)

		if selectedLiquidity.Cmp(
			requiredLiquidity,
		) >= 0 {
			break
		}
	}

	achievedCoverageBps :=
		decimal.NewFromBigInt(
			selectedLiquidity,
			0,
		).Mul(
			decimal.NewFromInt(
				positionCoverageDenominator,
			),
		).Div(
			decimal.NewFromBigInt(
				activeLiquidity,
				0,
			),
		)

	if selectedLiquidity.Cmp(
		requiredLiquidity,
	) < 0 {
		return ActivePositionSelection{}, fmt.Errorf(
			"select active positions by coverage: loaded %d positions reached only %s bps, below target %d bps; increase POSITION_LIMIT or verify indexed liquidity completeness",
			len(positions),
			achievedCoverageBps,
			targetCoverageBps,
		)
	}

	return ActivePositionSelection{
		Positions: selected,

		LoadedPositionCount: len(
			positions,
		),

		SelectedPositionCount: len(
			selected,
		),

		SelectedLiquidity: new(big.Int).Set(
			selectedLiquidity,
		),

		TargetCoverageBps: targetCoverageBps,

		AchievedCoverageBps: achievedCoverageBps,
	}, nil
}

func cloneLiquidityPosition(
	position domain.LiquidityPosition,
) domain.LiquidityPosition {
	result :=
		position

	if position.Liquidity != nil {
		result.Liquidity =
			new(big.Int).Set(
				position.Liquidity,
			)
	}

	return result
}
