package services

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func ValidateBurnRegressionReadiness(
	report BurnRealizedDatasetReport,
	expectedBurnSamples int,
	horizons []BurnOutcomeHorizon,
	minimumSpacingBlocks uint64,
) error {
	if err :=
		validateBurnRealizedDatasetReport(
			report,
		); err != nil {
		return fmt.Errorf(
			"validate burn regression readiness: invalid dataset: %w",
			err,
		)
	}

	if expectedBurnSamples <= 0 {
		return fmt.Errorf(
			"validate burn regression readiness: expected burn samples must be positive",
		)
	}

	normalizedHorizons, err :=
		normalizeBurnDatasetHorizons(
			horizons,
		)
	if err != nil {
		return fmt.Errorf(
			"validate burn regression readiness: normalize horizons: %w",
			err,
		)
	}

	maximumHorizon :=
		normalizedHorizons[len(normalizedHorizons)-1].Blocks

	// Since outcome windows include their final block, strict separation
	// requires spacing to be greater than the maximum horizon.
	if minimumSpacingBlocks <=
		maximumHorizon {
		return fmt.Errorf(
			"validate burn regression readiness: minimum spacing %d must be greater than maximum horizon %d",
			minimumSpacingBlocks,
			maximumHorizon,
		)
	}

	if report.BurnSamples !=
		expectedBurnSamples {
		return fmt.Errorf(
			"validate burn regression readiness: burn samples=%d, expected=%d",
			report.BurnSamples,
			expectedBurnSamples,
		)
	}

	if report.HorizonsPerSample !=
		len(normalizedHorizons) {
		return fmt.Errorf(
			"validate burn regression readiness: horizons per sample=%d, expected=%d",
			report.HorizonsPerSample,
			len(normalizedHorizons),
		)
	}

	expectedObservations :=
		expectedBurnSamples *
			len(normalizedHorizons)

	if report.CandidateHorizonPairs !=
		expectedObservations {
		return fmt.Errorf(
			"validate burn regression readiness: candidate pairs=%d, expected=%d",
			report.CandidateHorizonPairs,
			expectedObservations,
		)
	}

	if report.ObservedHorizonPairs !=
		expectedObservations {
		return fmt.Errorf(
			"validate burn regression readiness: observed pairs=%d, expected=%d",
			report.ObservedHorizonPairs,
			expectedObservations,
		)
	}

	if report.SkippedHorizonPairs != 0 ||
		len(report.Skipped) != 0 {
		return fmt.Errorf(
			"validate burn regression readiness: dataset contains %d skipped horizon pairs",
			report.SkippedHorizonPairs,
		)
	}

	expectedHorizonBlocks :=
		make(
			map[string]uint64,
			len(normalizedHorizons),
		)

	for _, horizon := range normalizedHorizons {
		expectedHorizonBlocks[horizon.Label] = horizon.Blocks
	}

	observationGroups :=
		make(
			map[string][]BurnRegressionObservation,
			expectedBurnSamples,
		)

	burns :=
		make(
			map[string]domain.BurnCandidate,
			expectedBurnSamples,
		)

	for index, observation := range report.Observations {
		if !observation.
			MarketControls.
			Available {
			return fmt.Errorf(
				"validate burn regression readiness: observation %d event=%s horizon=%s has unavailable market controls",
				index,
				observation.Burn.EventKey(),
				observation.HorizonLabel,
			)
		}

		if !observation.
			FlowControls.
			Available {
			return fmt.Errorf(
				"validate burn regression readiness: observation %d event=%s horizon=%s has unavailable flow controls",
				index,
				observation.Burn.EventKey(),
				observation.HorizonLabel,
			)
		}

		expectedBlocks, exists :=
			expectedHorizonBlocks[observation.HorizonLabel]

		if !exists {
			return fmt.Errorf(
				"validate burn regression readiness: observation %d has unexpected horizon %q",
				index,
				observation.HorizonLabel,
			)
		}

		if observation.HorizonBlocks !=
			expectedBlocks {
			return fmt.Errorf(
				"validate burn regression readiness: observation %d horizon %q blocks=%d, expected=%d",
				index,
				observation.HorizonLabel,
				observation.HorizonBlocks,
				expectedBlocks,
			)
		}

		expectedFutureBlock :=
			observation.
				Burn.
				Cursor.
				BlockNumber +
				observation.
					HorizonBlocks

		if observation.FutureBlock !=
			expectedFutureBlock {
			return fmt.Errorf(
				"validate burn regression readiness: observation %d future block=%d, expected=%d",
				index,
				observation.FutureBlock,
				expectedFutureBlock,
			)
		}

		eventKey :=
			observation.Burn.EventKey()

		observationGroups[eventKey] =
			append(
				observationGroups[eventKey],
				observation,
			)

		if _, exists :=
			burns[eventKey]; !exists {
			burns[eventKey] =
				cloneBurnCandidate(
					observation.Burn,
				)
		}
	}

	if len(observationGroups) !=
		expectedBurnSamples {
		return fmt.Errorf(
			"validate burn regression readiness: unique burns=%d, expected=%d",
			len(observationGroups),
			expectedBurnSamples,
		)
	}

	for eventKey, observations := range observationGroups {
		sort.Slice(
			observations,
			func(
				left int,
				right int,
			) bool {
				return observations[left].
					HorizonBlocks <
					observations[right].
						HorizonBlocks
			},
		)

		if len(observations) !=
			len(normalizedHorizons) {
			return fmt.Errorf(
				"validate burn regression readiness: event %s observations=%d, expected=%d",
				eventKey,
				len(observations),
				len(normalizedHorizons),
			)
		}

		for index, expected := range normalizedHorizons {
			observation :=
				observations[index]

			if observation.HorizonLabel !=
				expected.Label ||
				observation.HorizonBlocks !=
					expected.Blocks {
				return fmt.Errorf(
					"validate burn regression readiness: event %s horizon index=%d got=%s/%d, expected=%s/%d",
					eventKey,
					index,
					observation.HorizonLabel,
					observation.HorizonBlocks,
					expected.Label,
					expected.Blocks,
				)
			}

			if index == 0 {
				continue
			}

			if err :=
				validateBurnRegressionFlowPrefix(
					observations[index-1],
					observation,
				); err != nil {
				return fmt.Errorf(
					"validate burn regression readiness: event %s horizons %s→%s: %w",
					eventKey,
					observations[index-1].
						HorizonLabel,
					observation.
						HorizonLabel,
					err,
				)
			}
		}
	}

	sortedBurns :=
		make(
			[]domain.BurnCandidate,
			0,
			len(burns),
		)

	for _, burn := range burns {
		sortedBurns =
			append(
				sortedBurns,
				burn,
			)
	}

	sort.Slice(
		sortedBurns,
		func(
			left int,
			right int,
		) bool {
			leftCursor :=
				sortedBurns[left].Cursor

			rightCursor :=
				sortedBurns[right].Cursor

			if leftCursor.BlockNumber !=
				rightCursor.BlockNumber {
				return leftCursor.BlockNumber <
					rightCursor.BlockNumber
			}

			return leftCursor.LogIndex <
				rightCursor.LogIndex
		},
	)

	for index := 1; index <
		len(sortedBurns); index++ {
		previous :=
			sortedBurns[index-1]

		current :=
			sortedBurns[index]

		blockGap :=
			current.Cursor.BlockNumber -
				previous.Cursor.BlockNumber

		if blockGap <
			minimumSpacingBlocks {
			return fmt.Errorf(
				"validate burn regression readiness: burn spacing=%d below configured minimum=%d: previous=%s current=%s",
				blockGap,
				minimumSpacingBlocks,
				previous.EventKey(),
				current.EventKey(),
			)
		}

		if blockGap <=
			maximumHorizon {
			return fmt.Errorf(
				"validate burn regression readiness: outcome windows overlap: spacing=%d maximum_horizon=%d previous=%s current=%s",
				blockGap,
				maximumHorizon,
				previous.EventKey(),
				current.EventKey(),
			)
		}
	}

	return nil
}

func validateBurnRegressionFlowPrefix(
	previous BurnRegressionObservation,
	current BurnRegressionObservation,
) error {
	previousFlow :=
		previous.FlowControls

	currentFlow :=
		current.FlowControls

	if !previousFlow.Available ||
		!currentFlow.Available {
		return fmt.Errorf(
			"flow controls are unavailable",
		)
	}

	if !previousFlow.
		WindowStartCursor.
		Equal(
			currentFlow.WindowStartCursor,
		) {
		return fmt.Errorf(
			"flow window start cursor changed: previous=%s current=%s",
			previousFlow.WindowStartCursor,
			currentFlow.WindowStartCursor,
		)
	}

	if currentFlow.WindowEndBlock <=
		previousFlow.WindowEndBlock {
		return fmt.Errorf(
			"flow window end did not increase: previous=%d current=%d",
			previousFlow.WindowEndBlock,
			currentFlow.WindowEndBlock,
		)
	}

	integerCounters :=
		[]struct {
			name     string
			previous int
			current  int
		}{
			{
				"swap count",
				previousFlow.SwapCount,
				currentFlow.SwapCount,
			},
			{
				"zero_for_one swap count",
				previousFlow.ZeroForOneSwapCount,
				currentFlow.ZeroForOneSwapCount,
			},
			{
				"one_for_zero swap count",
				previousFlow.OneForZeroSwapCount,
				currentFlow.OneForZeroSwapCount,
			},
			{
				"liquidity event count",
				previousFlow.LiquidityEventCount,
				currentFlow.LiquidityEventCount,
			},
			{
				"mint event count",
				previousFlow.MintEventCount,
				currentFlow.MintEventCount,
			},
			{
				"burn event count",
				previousFlow.BurnEventCount,
				currentFlow.BurnEventCount,
			},
		}

	for _, counter := range integerCounters {
		if counter.current <
			counter.previous {
			return fmt.Errorf(
				"%s decreased: previous=%d current=%d",
				counter.name,
				counter.previous,
				counter.current,
			)
		}
	}

	bigIntegerCounters :=
		[]struct {
			name     string
			previous *big.Int
			current  *big.Int
		}{
			{
				"token0 input",
				previousFlow.Token0InputRaw,
				currentFlow.Token0InputRaw,
			},
			{
				"token0 output",
				previousFlow.Token0OutputRaw,
				currentFlow.Token0OutputRaw,
			},
			{
				"token1 input",
				previousFlow.Token1InputRaw,
				currentFlow.Token1InputRaw,
			},
			{
				"token1 output",
				previousFlow.Token1OutputRaw,
				currentFlow.Token1OutputRaw,
			},
			{
				"gross token0 volume",
				previousFlow.GrossToken0VolumeRaw,
				currentFlow.GrossToken0VolumeRaw,
			},
			{
				"gross token1 volume",
				previousFlow.GrossToken1VolumeRaw,
				currentFlow.GrossToken1VolumeRaw,
			},
			{
				"gross mint liquidity",
				previousFlow.GrossMintLiquidity,
				currentFlow.GrossMintLiquidity,
			},
			{
				"gross burn liquidity",
				previousFlow.GrossBurnLiquidity,
				currentFlow.GrossBurnLiquidity,
			},
			{
				"tick quadratic variation",
				previousFlow.TickPathQuadraticVariation,
				currentFlow.TickPathQuadraticVariation,
			},
		}

	for _, counter := range bigIntegerCounters {
		if counter.previous == nil ||
			counter.current == nil {
			return fmt.Errorf(
				"%s is nil",
				counter.name,
			)
		}

		if counter.current.Cmp(
			counter.previous,
		) < 0 {
			return fmt.Errorf(
				"%s decreased: previous=%s current=%s",
				counter.name,
				counter.previous,
				counter.current,
			)
		}
	}

	if currentFlow.TickPathMinTick >
		previousFlow.TickPathMinTick {
		return fmt.Errorf(
			"tick path minimum increased: previous=%d current=%d",
			previousFlow.TickPathMinTick,
			currentFlow.TickPathMinTick,
		)
	}

	if currentFlow.TickPathMaxTick <
		previousFlow.TickPathMaxTick {
		return fmt.Errorf(
			"tick path maximum decreased: previous=%d current=%d",
			previousFlow.TickPathMaxTick,
			currentFlow.TickPathMaxTick,
		)
	}

	if currentFlow.TickPathRange <
		previousFlow.TickPathRange {
		return fmt.Errorf(
			"tick path range decreased: previous=%d current=%d",
			previousFlow.TickPathRange,
			currentFlow.TickPathRange,
		)
	}

	if currentFlow.TickPathTotalVariation <
		previousFlow.TickPathTotalVariation {
		return fmt.Errorf(
			"tick path total variation decreased: previous=%d current=%d",
			previousFlow.TickPathTotalVariation,
			currentFlow.TickPathTotalVariation,
		)
	}

	if currentFlow.TickPathMaxAbsoluteStep <
		previousFlow.TickPathMaxAbsoluteStep {
		return fmt.Errorf(
			"tick path maximum step decreased: previous=%d current=%d",
			previousFlow.TickPathMaxAbsoluteStep,
			currentFlow.TickPathMaxAbsoluteStep,
		)
	}

	return nil
}
