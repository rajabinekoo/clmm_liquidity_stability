package services

import (
	"context"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type JointRemovalScenarioKind string

const (
	JointRemovalScenarioTopNByLSIS JointRemovalScenarioKind = "top_n_by_lsis"

	JointRemovalScenarioTargetActiveLiquidityShareByLSIS JointRemovalScenarioKind = "target_active_liquidity_share_by_lsis"
)

type JointRemovalRequest struct {
	Pool *domain.ReconstructedPool

	BaseReport *BidirectionalLiquidityImpactReport

	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int

	ThresholdsBps []decimal.Decimal

	TopCounts []int

	TargetActiveLiquidityShareBps []int64
}

type JointRemovalReport struct {
	PoolAddress string
	BlockNumber uint64
	CurrentTick int

	ThresholdsBps []decimal.Decimal

	Scenarios []JointRemovalScenarioResult
}

type JointRemovalScenarioResult struct {
	ScenarioID string
	Kind       JointRemovalScenarioKind

	RequestedPositionCount int

	RequestedActiveLiquidityShareBps int64

	RemovedPositionCount int

	RemovedLiquidity *big.Int

	RemovedActiveLiquidityShare decimal.Decimal

	CounterfactualActiveLiquidity *big.Int

	PositionKeys []string

	Skipped    bool
	SkipReason string

	ZeroForOne JointRemovalDirectionImpact
	OneForZero JointRemovalDirectionImpact

	SumIndividualTotalLSISBps decimal.Decimal

	TotalLSISBps decimal.Decimal

	TotalInteractionLSISBps decimal.Decimal

	TotalAmplificationRatio decimal.Decimal

	MaxDirectionalLSISBps decimal.Decimal
}

type JointRemovalDirectionImpact struct {
	BaseAUCBps           decimal.Decimal
	CounterfactualAUCBps decimal.Decimal
	DeltaAUCBps          decimal.Decimal
	LSISBps              decimal.Decimal

	SumIndividualLSISBps decimal.Decimal

	InteractionLSISBps decimal.Decimal

	AmplificationRatio decimal.Decimal

	DepthDeltas []DepthDelta
}

type jointRemovalScenarioSpec struct {
	ID   string
	Kind JointRemovalScenarioKind

	TopCount int

	TargetActiveLiquidityShareBps int64
}

func (s *LiquidityImpactService) AnalyzeJointRemovalScenarios(
	ctx context.Context,
	req JointRemovalRequest,
) (*JointRemovalReport, error) {
	if s == nil {
		return nil, fmt.Errorf(
			"joint removal impact: service is nil",
		)
	}

	if s.curveService == nil {
		return nil, fmt.Errorf(
			"joint removal impact: curve service is nil",
		)
	}

	if req.Pool == nil {
		return nil, fmt.Errorf(
			"joint removal impact: pool is nil",
		)
	}

	if req.Pool.Liquidity == nil ||
		req.Pool.Liquidity.Sign() <= 0 {
		return nil, fmt.Errorf(
			"joint removal impact: pool active liquidity must be positive",
		)
	}

	if req.BaseReport == nil ||
		req.BaseReport.ZeroForOneReport == nil ||
		req.BaseReport.OneForZeroReport == nil {
		return nil, fmt.Errorf(
			"joint removal impact: base bidirectional report is incomplete",
		)
	}

	if len(req.BaseReport.Positions) == 0 {
		return nil, fmt.Errorf(
			"joint removal impact: base report contains no positions",
		)
	}

	if len(req.ZeroForOneAmountsIn) == 0 {
		return nil, fmt.Errorf(
			"joint removal impact: zero_for_one amount grid is empty",
		)
	}

	if len(req.OneForZeroAmountsIn) == 0 {
		return nil, fmt.Errorf(
			"joint removal impact: one_for_zero amount grid is empty",
		)
	}

	if len(req.ThresholdsBps) == 0 {
		return nil, fmt.Errorf(
			"joint removal impact: thresholds are empty",
		)
	}

	if err := validateJointRemovalPositionOrder(
		req.BaseReport.Positions,
	); err != nil {
		return nil, err
	}

	specs, err :=
		buildJointRemovalScenarioSpecs(
			req.TopCounts,
			req.TargetActiveLiquidityShareBps,
		)
	if err != nil {
		return nil, err
	}

	report :=
		&JointRemovalReport{
			PoolAddress: req.Pool.PoolAddress,

			BlockNumber: req.Pool.BlockNumber,

			CurrentTick: req.Pool.CurrentTick,

			ThresholdsBps: append(
				[]decimal.Decimal(nil),
				req.ThresholdsBps...,
			),

			Scenarios: make(
				[]JointRemovalScenarioResult,
				0,
				len(specs),
			),
		}

	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if spec.Kind ==
			JointRemovalScenarioTopNByLSIS &&
			spec.TopCount >
				len(req.BaseReport.Positions) {
			report.Scenarios =
				append(
					report.Scenarios,
					jointRemovalUnavailableScenario(
						req,
						spec,
						fmt.Sprintf(
							"requested top %d positions but only %d are available",
							spec.TopCount,
							len(req.BaseReport.Positions),
						),
					),
				)

			continue
		}

		selected, err :=
			selectJointRemovalPositions(
				req.BaseReport.Positions,
				req.Pool.Liquidity,
				spec,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"joint removal impact: scenario %s: %w",
				spec.ID,
				err,
			)
		}

		result, err :=
			s.analyzeJointRemovalScenario(
				ctx,
				req,
				spec,
				selected,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"joint removal impact: scenario %s: %w",
				spec.ID,
				err,
			)
		}

		report.Scenarios =
			append(
				report.Scenarios,
				result,
			)
	}

	return report, nil
}

func jointRemovalUnavailableScenario(
	req JointRemovalRequest,
	spec jointRemovalScenarioSpec,
	reason string,
) JointRemovalScenarioResult {
	result :=
		JointRemovalScenarioResult{
			ScenarioID: spec.ID,

			Kind: spec.Kind,

			RequestedPositionCount: spec.TopCount,

			RequestedActiveLiquidityShareBps: spec.TargetActiveLiquidityShareBps,

			RemovedLiquidity: big.NewInt(0),

			CounterfactualActiveLiquidity: cloneBigInt(
				req.Pool.Liquidity,
			),

			Skipped: true,

			SkipReason: reason,
		}

	if req.BaseReport != nil {
		if req.BaseReport.ZeroForOneReport != nil {
			base :=
				req.BaseReport.
					ZeroForOneReport.
					BaseSummary.
					PriceImpactAUCBps

			result.ZeroForOne.BaseAUCBps =
				base

			result.ZeroForOne.CounterfactualAUCBps =
				base
		}

		if req.BaseReport.OneForZeroReport != nil {
			base :=
				req.BaseReport.
					OneForZeroReport.
					BaseSummary.
					PriceImpactAUCBps

			result.OneForZero.BaseAUCBps =
				base

			result.OneForZero.CounterfactualAUCBps =
				base
		}
	}

	return result
}

func (s *LiquidityImpactService) analyzeJointRemovalScenario(
	ctx context.Context,
	req JointRemovalRequest,
	spec jointRemovalScenarioSpec,
	selected []BidirectionalPositionImpact,
) (JointRemovalScenarioResult, error) {
	counterfactualPool :=
		req.Pool

	removedLiquidity :=
		big.NewInt(0)

	positionKeys := make(
		[]string,
		0,
		len(selected),
	)

	sumIndividualZeroForOne :=
		decimal.Zero

	sumIndividualOneForZero :=
		decimal.Zero

	for _, impact := range selected {
		if impact.Position.Liquidity == nil ||
			impact.Position.Liquidity.Sign() <= 0 {
			return JointRemovalScenarioResult{}, fmt.Errorf(
				"position %s has invalid liquidity",
				impact.PositionKey,
			)
		}

		nextPool, err :=
			uniswapv3.RemoveLiquidity(
				counterfactualPool,
				impact.Position,
			)
		if err != nil {
			return JointRemovalScenarioResult{}, fmt.Errorf(
				"remove position %s: %w",
				impact.PositionKey,
				err,
			)
		}

		counterfactualPool =
			nextPool

		removedLiquidity.Add(
			removedLiquidity,
			impact.Position.Liquidity,
		)

		positionKey :=
			impact.PositionKey

		if positionKey == "" {
			positionKey =
				liquidityPositionKey(
					impact.Position,
				)
		}

		positionKeys =
			append(
				positionKeys,
				positionKey,
			)

		sumIndividualZeroForOne =
			sumIndividualZeroForOne.Add(
				impact.ZeroForOneLSISBps,
			)

		sumIndividualOneForZero =
			sumIndividualOneForZero.Add(
				impact.OneForZeroLSISBps,
			)
	}

	result :=
		JointRemovalScenarioResult{
			ScenarioID: spec.ID,
			Kind:       spec.Kind,

			RequestedPositionCount: spec.TopCount,

			RequestedActiveLiquidityShareBps: spec.TargetActiveLiquidityShareBps,

			RemovedPositionCount: len(selected),

			RemovedLiquidity: new(big.Int).Set(
				removedLiquidity,
			),

			RemovedActiveLiquidityShare: activeLiquidityShare(
				removedLiquidity,
				req.Pool.Liquidity,
			),

			CounterfactualActiveLiquidity: new(big.Int).Set(
				counterfactualPool.Liquidity,
			),

			PositionKeys: positionKeys,
		}

	if counterfactualPool.Liquidity.Sign() <= 0 {
		result.Skipped = true

		result.SkipReason =
			"joint removal leaves zero active liquidity"

		return result, nil
	}

	zeroForOne, err :=
		s.analyzeJointRemovalDirection(
			ctx,
			counterfactualPool,
			req.ZeroForOneAmountsIn,
			req.ThresholdsBps,
			true,
			req.BaseReport.
				ZeroForOneReport.
				BaseSummary,
			sumIndividualZeroForOne,
		)
	if err != nil {
		return JointRemovalScenarioResult{}, fmt.Errorf(
			"zero_for_one: %w",
			err,
		)
	}

	oneForZero, err :=
		s.analyzeJointRemovalDirection(
			ctx,
			counterfactualPool,
			req.OneForZeroAmountsIn,
			req.ThresholdsBps,
			false,
			req.BaseReport.
				OneForZeroReport.
				BaseSummary,
			sumIndividualOneForZero,
		)
	if err != nil {
		return JointRemovalScenarioResult{}, fmt.Errorf(
			"one_for_zero: %w",
			err,
		)
	}

	result.ZeroForOne =
		zeroForOne

	result.OneForZero =
		oneForZero

	result.SumIndividualTotalLSISBps =
		sumIndividualZeroForOne.Add(
			sumIndividualOneForZero,
		)

	result.TotalLSISBps =
		zeroForOne.LSISBps.Add(
			oneForZero.LSISBps,
		)

	result.TotalInteractionLSISBps =
		result.TotalLSISBps.Sub(
			result.SumIndividualTotalLSISBps,
		)

	result.TotalAmplificationRatio =
		jointRemovalAmplificationRatio(
			result.TotalLSISBps,
			result.SumIndividualTotalLSISBps,
		)

	result.MaxDirectionalLSISBps =
		maxDecimal(
			zeroForOne.LSISBps,
			oneForZero.LSISBps,
		)

	return result, nil
}

func (s *LiquidityImpactService) analyzeJointRemovalDirection(
	ctx context.Context,
	counterfactualPool *domain.ReconstructedPool,
	amountsIn []*big.Int,
	thresholdsBps []decimal.Decimal,
	zeroForOne bool,
	baseSummary PriceImpactCurveSummary,
	sumIndividualLSISBps decimal.Decimal,
) (JointRemovalDirectionImpact, error) {
	curve, err :=
		s.curveService.Build(
			ctx,
			PriceImpactCurveRequest{
				Pool: counterfactualPool,

				AmountsIn: amountsIn,

				ZeroForOne: zeroForOne,
			},
		)
	if err != nil {
		return JointRemovalDirectionImpact{}, fmt.Errorf(
			"build counterfactual curve: %w",
			err,
		)
	}

	summary, err :=
		s.curveService.Summarize(
			curve,
			thresholdsBps,
		)
	if err != nil {
		return JointRemovalDirectionImpact{}, fmt.Errorf(
			"summarize counterfactual curve: %w",
			err,
		)
	}

	deltaAUC :=
		summary.PriceImpactAUCBps.Sub(
			baseSummary.PriceImpactAUCBps,
		)

	lsis :=
		positiveOnly(
			deltaAUC,
		)

	return JointRemovalDirectionImpact{
		BaseAUCBps: baseSummary.PriceImpactAUCBps,

		CounterfactualAUCBps: summary.PriceImpactAUCBps,

		DeltaAUCBps: deltaAUC,

		LSISBps: lsis,

		SumIndividualLSISBps: sumIndividualLSISBps,

		InteractionLSISBps: lsis.Sub(
			sumIndividualLSISBps,
		),

		AmplificationRatio: jointRemovalAmplificationRatio(
			lsis,
			sumIndividualLSISBps,
		),

		DepthDeltas: compareDepths(
			baseSummary.ThresholdDepths,
			summary.ThresholdDepths,
		),
	}, nil
}

func buildJointRemovalScenarioSpecs(
	topCounts []int,
	targetSharesBps []int64,
) ([]jointRemovalScenarioSpec, error) {
	specs := make(
		[]jointRemovalScenarioSpec,
		0,
		len(topCounts)+
			len(targetSharesBps),
	)

	seen :=
		make(
			map[string]struct{},
			len(topCounts)+
				len(targetSharesBps),
		)

	for _, count := range topCounts {
		if count <= 0 {
			return nil, fmt.Errorf(
				"joint removal impact: top count %d must be positive",
				count,
			)
		}

		id :=
			fmt.Sprintf(
				"top_%d_by_lsis",
				count,
			)

		if _, exists := seen[id]; exists {
			continue
		}

		seen[id] =
			struct{}{}

		specs =
			append(
				specs,
				jointRemovalScenarioSpec{
					ID: id,

					Kind: JointRemovalScenarioTopNByLSIS,

					TopCount: count,
				},
			)
	}

	for _, targetBps := range targetSharesBps {
		if targetBps <= 0 ||
			targetBps >=
				positionCoverageDenominator {
			return nil, fmt.Errorf(
				"joint removal impact: target active-liquidity share %d bps must be inside [1,%d)",
				targetBps,
				positionCoverageDenominator,
			)
		}

		id :=
			fmt.Sprintf(
				"target_%d_bps_active_liquidity_by_lsis",
				targetBps,
			)

		if _, exists := seen[id]; exists {
			continue
		}

		seen[id] =
			struct{}{}

		specs =
			append(
				specs,
				jointRemovalScenarioSpec{
					ID: id,

					Kind: JointRemovalScenarioTargetActiveLiquidityShareByLSIS,

					TargetActiveLiquidityShareBps: targetBps,
				},
			)
	}

	if len(specs) == 0 {
		return nil, fmt.Errorf(
			"joint removal impact: no scenarios were configured",
		)
	}

	return specs, nil
}

func selectJointRemovalPositions(
	positions []BidirectionalPositionImpact,
	activeLiquidity *big.Int,
	spec jointRemovalScenarioSpec,
) ([]BidirectionalPositionImpact, error) {
	switch spec.Kind {
	case JointRemovalScenarioTopNByLSIS:
		if spec.TopCount >
			len(positions) {
			return nil, fmt.Errorf(
				"top count %d exceeds available positions %d",
				spec.TopCount,
				len(positions),
			)
		}

		return append(
			[]BidirectionalPositionImpact(nil),
			positions[:spec.TopCount]...,
		), nil

	case JointRemovalScenarioTargetActiveLiquidityShareByLSIS:
		if activeLiquidity == nil ||
			activeLiquidity.Sign() <= 0 {
			return nil, fmt.Errorf(
				"active liquidity must be positive",
			)
		}

		requiredLiquidity :=
			jointRemovalRequiredLiquidity(
				activeLiquidity,
				spec.TargetActiveLiquidityShareBps,
			)

		selected := make(
			[]BidirectionalPositionImpact,
			0,
		)

		selectedLiquidity :=
			big.NewInt(0)

		for _, impact := range positions {
			if impact.Position.Liquidity == nil ||
				impact.Position.Liquidity.Sign() <= 0 {
				return nil, fmt.Errorf(
					"position %s has invalid liquidity",
					impact.PositionKey,
				)
			}

			selected =
				append(
					selected,
					impact,
				)

			selectedLiquidity.Add(
				selectedLiquidity,
				impact.Position.Liquidity,
			)

			if selectedLiquidity.Cmp(
				requiredLiquidity,
			) >= 0 {
				break
			}
		}

		if selectedLiquidity.Cmp(
			requiredLiquidity,
		) < 0 {
			return nil, fmt.Errorf(
				"selected positions provide liquidity %s below required %s for target %d bps",
				selectedLiquidity,
				requiredLiquidity,
				spec.TargetActiveLiquidityShareBps,
			)
		}

		return selected, nil

	default:
		return nil, fmt.Errorf(
			"unsupported joint removal scenario kind %q",
			spec.Kind,
		)
	}
}

func jointRemovalRequiredLiquidity(
	activeLiquidity *big.Int,
	targetBps int64,
) *big.Int {
	required :=
		new(big.Int).Mul(
			new(big.Int).Set(
				activeLiquidity,
			),
			big.NewInt(
				targetBps,
			),
		)

	required.Add(
		required,
		big.NewInt(
			positionCoverageDenominator-1,
		),
	)

	required.Quo(
		required,
		big.NewInt(
			positionCoverageDenominator,
		),
	)

	return required
}

func validateJointRemovalPositionOrder(
	positions []BidirectionalPositionImpact,
) error {
	for index, impact := range positions {
		if impact.Position.Liquidity == nil ||
			impact.Position.Liquidity.Sign() <= 0 {
			return fmt.Errorf(
				"joint removal impact: position %d has invalid liquidity",
				index,
			)
		}

		if index == 0 {
			continue
		}

		if positions[index-1].
			TotalLSISBps.
			LessThan(
				impact.TotalLSISBps,
			) {
			return fmt.Errorf(
				"joint removal impact: positions are not ordered by descending total LSIS at index %d",
				index,
			)
		}
	}

	return nil
}

func jointRemovalAmplificationRatio(
	joint decimal.Decimal,
	sumIndividual decimal.Decimal,
) decimal.Decimal {
	if sumIndividual.IsZero() {
		return decimal.Zero
	}

	return joint.Div(
		sumIndividual,
	)
}
