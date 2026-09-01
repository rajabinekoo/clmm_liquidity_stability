package services

import (
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestValidateBurnRegressionReadinessCardinalityAcceptsPartialSampleWhenMaximumIsNotRequired(
	t *testing.T,
) {
	t.Parallel()

	report := BurnRealizedDatasetReport{
		BurnSamples:           94,
		HorizonsPerSample:     4,
		CandidateHorizonPairs: 376,
		ObservedHorizonPairs:  376,
		SkippedHorizonPairs:   0,
	}

	actual, err :=
		validateBurnRegressionReadinessCardinality(
			report,
			100,
			false,
			4,
		)
	if err != nil {
		t.Fatalf(
			"validateBurnRegressionReadinessCardinality() error = %v",
			err,
		)
	}

	if actual != 94 {
		t.Fatalf(
			"actual samples = %d, want 94",
			actual,
		)
	}
}

func TestValidateBurnRegressionReadinessCardinalityRejectsPartialSampleWhenMaximumIsRequired(
	t *testing.T,
) {
	t.Parallel()

	report := BurnRealizedDatasetReport{
		BurnSamples:           94,
		HorizonsPerSample:     4,
		CandidateHorizonPairs: 376,
		ObservedHorizonPairs:  376,
		SkippedHorizonPairs:   0,
	}

	_, err :=
		validateBurnRegressionReadinessCardinality(
			report,
			100,
			true,
			4,
		)
	if err == nil {
		t.Fatal(
			"validation error = nil, want non-nil",
		)
	}
}

func TestValidateBurnRegressionReadinessCardinalityUsesActualSampleCountForPairs(
	t *testing.T,
) {
	t.Parallel()

	report := BurnRealizedDatasetReport{
		BurnSamples:           94,
		HorizonsPerSample:     4,
		CandidateHorizonPairs: 400,
		ObservedHorizonPairs:  400,
		SkippedHorizonPairs:   0,
	}

	_, err :=
		validateBurnRegressionReadinessCardinality(
			report,
			100,
			false,
			4,
		)
	if err == nil {
		t.Fatal(
			"validation error = nil, want non-nil",
		)
	}
}

func TestValidateBurnRegressionReadinessCardinalityRejectsSamplesAboveMaximum(
	t *testing.T,
) {
	t.Parallel()

	report := BurnRealizedDatasetReport{
		BurnSamples:           101,
		HorizonsPerSample:     4,
		CandidateHorizonPairs: 404,
		ObservedHorizonPairs:  404,
		SkippedHorizonPairs:   0,
	}

	_, err :=
		validateBurnRegressionReadinessCardinality(
			report,
			100,
			false,
			4,
		)
	if err == nil {
		t.Fatal(
			"validation error = nil, want non-nil",
		)
	}
}

func TestValidateBurnRegressionFlowPrefix(
	t *testing.T,
) {
	t.Parallel()

	cursor :=
		domain.EventCursor{
			BlockNumber: 100,
			LogIndex:    5,
		}

	previous :=
		BurnRegressionObservation{
			FlowControls: BurnRealizedFlowControls{
				Available: true,

				WindowStartCursor: cursor,

				WindowEndBlock: 150,

				SwapCount: 10,

				ZeroForOneSwapCount: 6,

				OneForZeroSwapCount: 4,

				Token0InputRaw: big.NewInt(100),

				Token0OutputRaw: big.NewInt(50),

				Token1InputRaw: big.NewInt(200),

				Token1OutputRaw: big.NewInt(80),

				GrossToken0VolumeRaw: big.NewInt(150),

				GrossToken1VolumeRaw: big.NewInt(280),

				LiquidityEventCount: 2,

				MintEventCount: 1,

				BurnEventCount: 1,

				GrossMintLiquidity: big.NewInt(100),

				GrossBurnLiquidity: big.NewInt(50),

				TickPathMinTick: 90,

				TickPathMaxTick: 110,

				TickPathRange: 20,

				TickPathTotalVariation: 30,

				TickPathQuadraticVariation: big.NewInt(500),

				TickPathMaxAbsoluteStep: 10,
			},
		}

	current :=
		previous

	current.FlowControls =
		cloneBurnRealizedFlowControls(
			previous.FlowControls,
		)

	current.FlowControls.WindowEndBlock =
		200

	current.FlowControls.SwapCount =
		20

	current.FlowControls.ZeroForOneSwapCount =
		11

	current.FlowControls.OneForZeroSwapCount =
		9

	current.FlowControls.Token0InputRaw =
		big.NewInt(200)

	current.FlowControls.Token0OutputRaw =
		big.NewInt(80)

	current.FlowControls.Token1InputRaw =
		big.NewInt(400)

	current.FlowControls.Token1OutputRaw =
		big.NewInt(150)

	current.FlowControls.GrossToken0VolumeRaw =
		big.NewInt(280)

	current.FlowControls.GrossToken1VolumeRaw =
		big.NewInt(550)

	current.FlowControls.LiquidityEventCount =
		4

	current.FlowControls.MintEventCount =
		2

	current.FlowControls.BurnEventCount =
		2

	current.FlowControls.GrossMintLiquidity =
		big.NewInt(180)

	current.FlowControls.GrossBurnLiquidity =
		big.NewInt(90)

	current.FlowControls.TickPathMinTick =
		80

	current.FlowControls.TickPathMaxTick =
		120

	current.FlowControls.TickPathRange =
		40

	current.FlowControls.TickPathTotalVariation =
		70

	current.FlowControls.TickPathQuadraticVariation =
		big.NewInt(1_200)

	current.FlowControls.TickPathMaxAbsoluteStep =
		15

	if err :=
		validateBurnRegressionFlowPrefix(
			previous,
			current,
		); err != nil {
		t.Fatalf(
			"validateBurnRegressionFlowPrefix() error = %v",
			err,
		)
	}
}

func TestValidateBurnRegressionFlowPrefixRejectsDecreasingVolume(
	t *testing.T,
) {
	t.Parallel()

	cursor :=
		domain.EventCursor{
			BlockNumber: 100,
			LogIndex:    5,
		}

	previous :=
		BurnRegressionObservation{
			FlowControls: BurnRealizedFlowControls{
				Available: true,

				WindowStartCursor: cursor,

				WindowEndBlock: 150,

				Token0InputRaw: big.NewInt(100),

				Token0OutputRaw: big.NewInt(0),

				Token1InputRaw: big.NewInt(0),

				Token1OutputRaw: big.NewInt(100),

				GrossToken0VolumeRaw: big.NewInt(100),

				GrossToken1VolumeRaw: big.NewInt(100),

				GrossMintLiquidity: big.NewInt(0),

				GrossBurnLiquidity: big.NewInt(0),

				TickPathQuadraticVariation: big.NewInt(0),
			},
		}

	current :=
		previous

	current.FlowControls =
		cloneBurnRealizedFlowControls(
			previous.FlowControls,
		)

	current.FlowControls.WindowEndBlock =
		200

	current.FlowControls.Token0InputRaw =
		big.NewInt(90)

	err :=
		validateBurnRegressionFlowPrefix(
			previous,
			current,
		)

	if err == nil {
		t.Fatal(
			"validation error = nil, want non-nil",
		)
	}
}
