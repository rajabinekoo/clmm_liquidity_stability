package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/providers"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/database"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/services"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/utils"
	"github.com/rajabinekoo/clmm-liquidity-stability/migrations"

	"github.com/shopspring/decimal"
)

func main() {
	if err := run(); err != nil {
		slog.Error(
			"analyzer exited with error",
			"error", err,
		)

		os.Exit(1)
	}
}

func run() error {
	config, err := utils.LoadConfig()
	if err != nil {
		return err
	}

	provider := providers.New(
		config.TheGraphAPIKey,
		config.TheGraphTimeout,
	)

	curveAmounts, err := config.AmountGridToken0Raw()
	if err != nil {
		return err
	}

	if len(curveAmounts) < 2 {
		return fmt.Errorf("amount grid must contain at least two values")
	}

	slog.Info(
		"amount grid loaded",
		"token0", config.Token0Symbol,
		"amounts", len(curveAmounts),
	)

	outputDir := config.OutputDir()

	poolConfigPath := filepath.Join(
		outputDir,
		"pool_config.json",
	)

	if err := utils.WritePoolConfigJSON(
		poolConfigPath,
		config,
	); err != nil {
		return err
	}

	slog.Info(
		"pool config exported",
		"path", poolConfigPath,
	)

	thresholdsBps := []decimal.Decimal{
		decimal.NewFromInt(10),
		decimal.NewFromInt(50),
		decimal.NewFromInt(100),
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	db, err := database.OpenPostgresDB(
		config.PostgresURL,
	)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := database.MigrationFS(
		db,
		migrations.FS,
		".",
	); err != nil {
		return fmt.Errorf(
			"run migrations: %w",
			err,
		)
	}

	poolStateRepository :=
		repositories.NewPoolStateRepository(db)

	poolReconstructor :=
		services.NewPoolReconstructor(
			poolStateRepository,
		)

	pool, err := poolReconstructor.ReconstructLatest(
		ctx,
		config.PoolAddress,
	)
	if err != nil {
		return fmt.Errorf(
			"reconstruct pool: %w",
			err,
		)
	}

	slog.Info(
		"pool reconstructed",
		"pool_address", pool.PoolAddress,
		"block_number", pool.BlockNumber,
		"current_tick", pool.CurrentTick,
		"sqrt_price_x96", pool.SqrtPriceX96.String(),
		"liquidity", pool.Liquidity.String(),
		"initialized_ticks", len(pool.InitializedTicks),
	)

	simulator, err := uniswapv3.NewSimulator(config.PoolFee)
	if err != nil {
		return err
	}

	amountIn := new(big.Int).Set(curveAmounts[0])

	swap1, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:   amountIn,
			ZeroForOne: true,
		},
	)
	if err != nil {
		return err
	}

	slog.Info(
		"swap simulated",
		"amount_in", swap1.AmountIn.String(),
		"amount_in_less_fee", swap1.AmountInLessFee.String(),
		"amount_out", swap1.AmountOut.String(),
		"fee_amount", swap1.FeeAmount.String(),
		"tick_before", swap1.TickBefore,
		"tick_after_approx", swap1.TickAfterApprox,
		"crossed_ticks", swap1.CrossedTicks,
		"spot_price_before", swap1.SpotPriceBefore.String(),
		"spot_price_after", swap1.SpotPriceAfter.String(),
		"execution_price", swap1.ExecutionPrice.String(),
		"price_impact_bps", swap1.PriceImpactBps.String(),
	)

	amountIn = new(big.Int).Set(curveAmounts[len(curveAmounts)-1])

	swap2, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:   amountIn,
			ZeroForOne: true,
		},
	)
	if err != nil {
		return err
	}

	slog.Info(
		"swap simulated",
		"amount_in", swap2.AmountIn.String(),
		"amount_in_less_fee", swap2.AmountInLessFee.String(),
		"amount_out", swap2.AmountOut.String(),
		"fee_amount", swap2.FeeAmount.String(),
		"tick_before", swap2.TickBefore,
		"tick_after_approx", swap2.TickAfterApprox,
		"crossed_ticks", swap2.CrossedTicks,
		"spot_price_before", swap2.SpotPriceBefore.String(),
		"spot_price_after", swap2.SpotPriceAfter.String(),
		"execution_price", swap2.ExecutionPrice.String(),
		"price_impact_bps", swap2.PriceImpactBps.String(),
	)

	curveService := services.NewPriceImpactCurveService(
		simulator,
	)

	curve, err := curveService.Build(
		ctx,
		services.PriceImpactCurveRequest{
			Pool:       pool,
			AmountsIn:  curveAmounts,
			ZeroForOne: true,
		},
	)
	if err != nil {
		return err
	}

	for _, point := range curve.Points {
		slog.Info(
			"price impact curve point",
			"zero_for_one", curve.ZeroForOne,
			"amount_in", point.AmountIn.String(),
			"amount_out", point.AmountOut.String(),
			"tick_after_approx", point.TickAfterApprox,
			"crossed_ticks", point.CrossedTicks,
			"execution_price", point.ExecutionPrice.String(),
			"spot_price_after", point.SpotPriceAfter.String(),
			"price_impact_bps", point.PriceImpactBps.String(),
		)
	}

	summary, err := curveService.Summarize(
		curve,
		thresholdsBps,
	)
	if err != nil {
		return err
	}

	slog.Info(
		"price impact curve summary",
		"zero_for_one", curve.ZeroForOne,
		"max_amount_in", summary.MaxAmountIn.String(),
		"price_impact_auc_bps", summary.PriceImpactAUCBps.String(),
	)

	for _, depth := range summary.ThresholdDepths {
		slog.Info(
			"depth at threshold",
			"zero_for_one", curve.ZeroForOne,
			"threshold_bps", depth.ThresholdBps.String(),
			"depth_amount_raw", depth.DepthAmount.String(),
			"breached", depth.Breached,
		)
	}

	impactService := services.NewLiquidityImpactService(
		poolStateRepository,
		curveService,
	)

	impactReport, err := impactService.AnalyzeActivePositions(
		ctx,
		services.LiquidityImpactRequest{
			Pool:                pool,
			AmountsIn:           curveAmounts,
			ZeroForOne:          true,
			PositionLimit:       config.PositionLimit,
			ThresholdsBps:       thresholdsBps,
			PositionCoverageBps: config.PositionCoverageBps,
		},
	)
	if err != nil {
		return err
	}

	slog.Info(
		"liquidity impact report",
		"zero_for_one", impactReport.ZeroForOne,
		"base_auc_bps", impactReport.BaseSummary.PriceImpactAUCBps.String(),
		"positions", len(impactReport.Positions),
	)

	for rank, impact := range impactReport.Positions {
		slog.Info(
			"position liquidity impact",
			"rank", rank+1,
			"owner", impact.Position.Owner,
			"tick_lower", impact.Position.TickLower,
			"tick_upper", impact.Position.TickUpper,
			"position_liquidity", impact.Position.Liquidity.String(),
			"base_active_liquidity", impact.BaseActiveLiquidity.String(),
			"counterfactual_active_liquidity", impact.CounterfactualActiveLiquidity.String(),
			"base_auc_bps", impact.BaseAUCBps.String(),
			"counterfactual_auc_bps", impact.CounterfactualAUCBps.String(),
			"delta_auc_bps", impact.DeltaAUCBps.String(),
			"lsis_bps", impact.LSISBps.String(),
		)

		for _, depth := range impact.DepthDeltas {
			slog.Info(
				"position depth impact",
				"rank", rank+1,
				"threshold_bps", depth.ThresholdBps.String(),
				"base_depth_raw", depth.BaseDepthAmount.String(),
				"counterfactual_depth_raw", depth.CounterfactualDepthAmount.String(),
				"delta_depth_raw", depth.DeltaDepthAmount.String(),
				"base_breached", depth.BaseBreached,
				"counterfactual_breached", depth.CounterfactualBreached,
			)
		}
	}

	token1Amounts, err := token1EquivalentAmounts(
		curveAmounts,
		pool.SqrtPriceX96,
	)
	if err != nil {
		return err
	}

	bidirectionalReport, err := impactService.AnalyzeBidirectionalActivePositions(
		ctx,
		services.BidirectionalLiquidityImpactRequest{
			Pool:                pool,
			ZeroForOneAmountsIn: curveAmounts,
			OneForZeroAmountsIn: token1Amounts,
			PositionLimit:       config.PositionLimit,
			ThresholdsBps:       thresholdsBps,
			PositionCoverageBps: config.PositionCoverageBps,
		},
	)
	if err != nil {
		return err
	}

	slog.Info(
		"bidirectional liquidity impact report",
		"zero_for_one_base_auc_bps",
		bidirectionalReport.ZeroForOneReport.BaseSummary.PriceImpactAUCBps.String(),
		"one_for_zero_base_auc_bps",
		bidirectionalReport.OneForZeroReport.BaseSummary.PriceImpactAUCBps.String(),
		"positions",
		len(bidirectionalReport.Positions),
	)

	for rank, impact := range bidirectionalReport.Positions {
		slog.Info(
			"bidirectional position liquidity impact",
			"rank", rank+1,
			"position_key", impact.PositionKey,
			"tick_lower", impact.Position.TickLower,
			"tick_upper", impact.Position.TickUpper,
			"position_liquidity", impact.Position.Liquidity.String(),

			"active_liquidity_share", impact.ActiveLiquidityShare.String(),
			"range_width", impact.RangeWidth,
			"distance_to_lower_tick", impact.DistanceToLowerTick,
			"distance_to_upper_tick", impact.DistanceToUpperTick,
			"distance_to_nearest_edge", impact.DistanceToNearestEdge,
			"liquidity_density", impact.LiquidityDensity.String(),

			"zero_for_one_lsis_bps", impact.ZeroForOneLSISBps.String(),
			"one_for_zero_lsis_bps", impact.OneForZeroLSISBps.String(),
			"total_lsis_bps", impact.TotalLSISBps.String(),
			"max_directional_lsis_bps", impact.MaxDirectionalLSISBps.String(),
		)
	}

	csvPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"bidirectional_lsis_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteBidirectionalImpactCSV(
		csvPath,
		pool,
		bidirectionalReport,
	); err != nil {
		return err
	}

	slog.Info(
		"bidirectional impact csv exported",
		"path", csvPath,
	)

	correlations, err := services.BuildBidirectionalImpactCorrelations(
		bidirectionalReport,
	)
	if err != nil {
		return err
	}

	jointRemovalTopCounts, err :=
		config.JointRemovalTopCountValues()
	if err != nil {
		return err
	}

	jointRemovalShareTargets, err :=
		config.
			JointRemovalActiveLiquidityShareBpsValues()
	if err != nil {
		return err
	}

	jointRemovalReport, err :=
		impactService.AnalyzeJointRemovalScenarios(
			ctx,
			services.JointRemovalRequest{
				Pool: pool,

				BaseReport: bidirectionalReport,

				ZeroForOneAmountsIn: curveAmounts,

				OneForZeroAmountsIn: token1Amounts,

				ThresholdsBps: thresholdsBps,

				TopCounts: jointRemovalTopCounts,

				TargetActiveLiquidityShareBps: jointRemovalShareTargets,
			},
		)
	if err != nil {
		return fmt.Errorf(
			"analyze joint removal scenarios: %w",
			err,
		)
	}

	jointRemovalPath :=
		filepath.Join(
			outputDir,
			fmt.Sprintf(
				"joint_removal_lsis_%s_%d.csv",
				pool.PoolAddress,
				pool.BlockNumber,
			),
		)

	if err :=
		services.WriteJointRemovalImpactCSV(
			jointRemovalPath,
			pool,
			jointRemovalReport,
		); err != nil {
		return err
	}

	for _, scenario := range jointRemovalReport.Scenarios {
		slog.Info(
			"joint removal impact",

			"scenario_id",
			scenario.ScenarioID,

			"scenario_kind",
			scenario.Kind,

			"removed_positions",
			scenario.RemovedPositionCount,

			"removed_active_liquidity_share",
			scenario.
				RemovedActiveLiquidityShare.
				String(),

			"joint_total_lsis_bps",
			scenario.
				TotalLSISBps.
				String(),

			"sum_individual_total_lsis_bps",
			scenario.
				SumIndividualTotalLSISBps.
				String(),

			"interaction_lsis_bps",
			scenario.
				TotalInteractionLSISBps.
				String(),

			"amplification_ratio",
			scenario.
				TotalAmplificationRatio.
				String(),

			"skipped",
			scenario.Skipped,
		)
	}

	slog.Info(
		"joint removal impact CSV exported",
		"path",
		jointRemovalPath,
	)

	correlationPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"bidirectional_lsis_correlations_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteImpactCorrelationCSV(
		correlationPath,
		correlations,
	); err != nil {
		return err
	}

	slog.Info(
		"bidirectional impact correlations exported",
		"path", correlationPath,
	)

	for _, correlation := range correlations {
		slog.Info(
			"impact correlation",
			"metric", correlation.Metric,
			"target", correlation.Target,
			"count", correlation.Count,
			"pearson", correlation.Pearson,
			"spearman", correlation.Spearman,
		)
	}

	batchService := services.NewSnapshotBatchAnalysisService(
		provider,
		poolStateRepository,
		impactService,
	)

	batchResults, err := batchService.Analyze(
		ctx,
		services.SnapshotBatchRequest{
			PoolAddress: pool.PoolAddress,

			LatestBlock:    pool.BlockNumber,
			LookbackBlocks: config.LookbackBlocks,
			StepBlocks:     config.StepBlocks,
			MaxSnapshots:   config.MaxSnapshots,

			ZeroForOneAmountsIn: curveAmounts,
			PositionLimit:       config.PositionLimit,
			ThresholdsBps:       thresholdsBps,
			PositionCoverageBps: config.PositionCoverageBps,

			JointRemovalTopCounts: append(
				[]int(nil),
				jointRemovalTopCounts...,
			),

			JointRemovalActiveLiquidityShareBps: append(
				[]int64(nil),
				jointRemovalShareTargets...,
			),
		},
	)
	if err != nil {
		return err
	}

	batchObservationCount := 0

	for _, result := range batchResults {
		if result.Report == nil {
			continue
		}

		batchObservationCount += len(result.Report.Positions)
	}

	batchSummaryPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"snapshot_batch_summary_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteSnapshotBatchSummaryCSV(
		batchSummaryPath,
		batchResults,
	); err != nil {
		return err
	}

	slog.Info(
		"snapshot batch summary exported",
		"path", batchSummaryPath,
	)

	batchPositionsPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"snapshot_batch_positions_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteSnapshotBatchPositionsCSV(
		batchPositionsPath,
		batchResults,
	); err != nil {
		return err
	}

	slog.Info(
		"snapshot batch positions exported",
		"path", batchPositionsPath,
	)

	batchRangesPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"snapshot_batch_ranges_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteSnapshotBatchRangeAggregateCSV(
		batchRangesPath,
		batchResults,
	); err != nil {
		return err
	}

	slog.Info(
		"snapshot batch ranges exported",
		"path", batchRangesPath,
	)

	batchJointRemovalPath :=
		filepath.Join(
			outputDir,
			fmt.Sprintf(
				"snapshot_batch_joint_removal_%s_%d.csv",
				pool.PoolAddress,
				pool.BlockNumber,
			),
		)

	if err :=
		services.WriteSnapshotBatchJointRemovalCSV(
			batchJointRemovalPath,
			batchResults,
		); err != nil {
		return fmt.Errorf(
			"write snapshot batch joint-removal CSV: %w",
			err,
		)
	}

	batchJointRemovalRows := 0

	for _, result := range batchResults {
		if result.JointRemoval == nil {
			continue
		}

		batchJointRemovalRows +=
			len(
				result.
					JointRemoval.
					Scenarios,
			)
	}

	slog.Info(
		"snapshot batch joint-removal exported",

		"path",
		batchJointRemovalPath,

		"snapshots",
		len(batchResults),

		"rows",
		batchJointRemovalRows,
	)

	batchCorrelations, err := services.BuildSnapshotBatchPositionCorrelations(
		batchResults,
	)
	if err != nil {
		return err
	}

	batchCorrelationsPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"snapshot_batch_position_correlations_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteImpactCorrelationCSV(
		batchCorrelationsPath,
		batchCorrelations,
	); err != nil {
		return err
	}

	slog.Info(
		"snapshot batch position correlations exported",
		"path", batchCorrelationsPath,
		"observations", batchObservationCount,
	)

	for _, correlation := range batchCorrelations {
		slog.Info(
			"snapshot batch position correlation",
			"metric", correlation.Metric,
			"target", correlation.Target,
			"count", correlation.Count,
			"pearson", correlation.Pearson,
			"spearman", correlation.Spearman,
		)
	}

	batchDiagnostics, err := services.BuildSnapshotBatchDiagnostics(
		batchResults,
	)
	if err != nil {
		return err
	}

	batchDiagnosticsPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"snapshot_batch_diagnostics_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteSnapshotBatchDiagnosticsCSV(
		batchDiagnosticsPath,
		batchDiagnostics,
	); err != nil {
		return err
	}

	slog.Info(
		"snapshot batch diagnostics exported",
		"path", batchDiagnosticsPath,
		"snapshots", len(batchDiagnostics),
	)

	for _, diagnostic := range batchDiagnostics {
		slog.Info(
			"snapshot batch diagnostic",
			"snapshot_index", diagnostic.SnapshotIndex,
			"block_number", diagnostic.BlockNumber,
			"top1_lsis_share", diagnostic.Top1LSISShare.String(),
			"top3_lsis_share", diagnostic.Top3LSISShare.String(),
			"total_lsis_hhi_topk", diagnostic.TotalLSISHHITopK.String(),
			"effective_lsis_positions_topk", diagnostic.EffectiveLSISPositionsTopK.String(),
			"total_lsis_gini_topk", diagnostic.TotalLSISGiniTopK.String(),
		)
	}

	empiricalSummary, err := services.BuildSnapshotBatchEmpiricalSummary(
		batchDiagnostics,
	)
	if err != nil {
		return err
	}

	empiricalSummaryPath := filepath.Join(
		outputDir,
		fmt.Sprintf(
			"empirical_summary_%s_%d.csv",
			pool.PoolAddress,
			pool.BlockNumber,
		),
	)

	if err := services.WriteEmpiricalSummaryCSV(
		empiricalSummaryPath,
		empiricalSummary,
		len(batchDiagnostics),
		batchObservationCount,
	); err != nil {
		return err
	}

	slog.Info(
		"empirical summary exported",
		"path", empiricalSummaryPath,
	)

	validationService := services.NewSwapValidationService(
		provider,
		poolStateRepository,
		simulator,
	)

	// The local reconstruction snapshot and LP-action checkpoint are complete
	// through pool.BlockNumber. Therefore a Swap in this block can safely use
	// block-1 as its starting snapshot.
	validationToBlock := pool.BlockNumber

	if validationToBlock <= 1 {
		return fmt.Errorf(
			"swap validation requires a pool block greater than one: %d",
			validationToBlock,
		)
	}

	validationFromBlock := blockLookback(
		validationToBlock,
		config.LookbackBlocks,
	)

	validationReport, err :=
		validationService.ValidateCleanFirstSwapsPerBlock(
			ctx,
			services.SwapValidationRequest{
				PoolAddress: pool.PoolAddress,

				FromBlock: validationFromBlock,

				ToBlock: validationToBlock,

				PageSize: config.PageSize,

				MaxSamples: config.ValidationSamples,

				BlockWindowSize: config.ValidationBlockWindowSize,
			},
		)
	if err != nil {
		// Mechanical parity is now an integrity gate. Infrastructure,
		// reconstruction or simulation errors must not be silently converted
		// into a successful analyzer run.
		return fmt.Errorf(
			"run clean swap parity validation: %w",
			err,
		)
	}

	for _, skipped := range validationReport.Skipped {
		slog.Info(
			"swap validation candidate skipped",
			"swap_id", skipped.SwapID,
			"tx_hash", skipped.TxHash,
			"block_number", skipped.BlockNumber,
			"log_index", skipped.LogIndex,
			"reason", string(skipped.Reason),
			"detail", skipped.Detail,
		)
	}

	for _, result := range validationReport.Results {
		accountedInput :=
			new(big.Int).Add(
				new(big.Int).Set(
					result.SimAmountInLessFeeRaw,
				),
				result.SimFeeAmountRaw,
			)

		slog.Info(
			"swap validation result",
			"swap_id", result.SwapID,
			"tx_hash", result.TxHash,
			"block_number", result.BlockNumber,
			"log_index", result.LogIndex,
			"snapshot_block", result.SnapshotBlock,
			"zero_for_one", result.ZeroForOne,

			"amount_in_raw",
			result.AmountInRaw.String(),

			"sim_amount_in_less_fee_raw",
			result.SimAmountInLessFeeRaw.String(),

			"sim_fee_amount_raw",
			result.SimFeeAmountRaw.String(),

			"sim_accounted_input_raw",
			accountedInput.String(),

			"fee_accounting_exact",
			accountedInput.Cmp(
				result.AmountInRaw,
			) == 0,

			"actual_amount_out_raw",
			result.ActualAmountOutRaw.String(),

			"sim_amount_out_raw",
			result.SimAmountOutRaw.String(),

			"amount_out_abs_diff_raw",
			result.AmountOutAbsDiffRaw.String(),

			"amount_out_diff_bps",
			result.AmountOutDiffBps.String(),

			"amount_out_exact",
			result.AmountOutExact,

			"actual_sqrt_price_x96_after",
			result.ActualSqrtPriceX96After.String(),

			"sim_sqrt_price_x96_after",
			result.SimSqrtPriceX96After.String(),

			"sqrt_price_abs_diff_raw",
			result.SqrtPriceAbsDiffRaw.String(),

			"sqrt_price_diff_bps",
			result.SqrtPriceDiffBps.String(),

			"sqrt_price_exact",
			result.SqrtPriceExact,

			"actual_tick_after",
			result.ActualTickAfter,

			"sim_tick_after",
			result.SimTickAfter,

			"tick_delta",
			result.TickDelta,

			"tick_exact",
			result.TickExact,

			"sim_swap_steps",
			result.SimSwapSteps,

			"sim_crossed_ticks",
			result.SimCrossedTicks,

			"exact_match",
			result.ExactMatch,
		)
	}

	validationSummary, err :=
		services.BuildSwapValidationSummary(
			validationReport,
		)
	if err != nil {
		return fmt.Errorf(
			"build swap validation summary: %w",
			err,
		)
	}

	validationBaseName := fmt.Sprintf(
		"swap_validation_%s_%d_%d",
		validationReport.PoolAddress,
		validationReport.FromBlock,
		validationReport.ToBlock,
	)

	validationResultsPath := filepath.Join(
		outputDir,
		validationBaseName+"_results.csv",
	)

	validationSkipsPath := filepath.Join(
		outputDir,
		validationBaseName+"_skips.csv",
	)

	validationSummaryPath := filepath.Join(
		outputDir,
		validationBaseName+"_summary.csv",
	)

	if err := services.WriteSwapValidationCSV(
		validationResultsPath,
		validationReport.Results,
	); err != nil {
		return fmt.Errorf(
			"write swap validation results: %w",
			err,
		)
	}

	if err := services.WriteSwapValidationSkipsCSV(
		validationSkipsPath,
		validationReport.Skipped,
	); err != nil {
		return fmt.Errorf(
			"write swap validation skips: %w",
			err,
		)
	}

	if err := services.WriteSwapValidationSummaryCSV(
		validationSummaryPath,
		validationSummary,
	); err != nil {
		return fmt.Errorf(
			"write swap validation summary: %w",
			err,
		)
	}

	if validationSummary.CleanSamples == 0 {
		slog.Warn(
			"swap validation found no clean samples",
			"pool_address",
			validationSummary.PoolAddress,

			"candidate_blocks",
			validationSummary.CandidateBlocks,

			"skipped_samples",
			validationSummary.SkippedSamples,

			"incomplete_lp_index_skips",
			validationSummary.IncompleteLPIndexSkips,

			"prior_liquidity_action_skips",
			validationSummary.PriorLiquidityActionSkips,
		)
	}

	slog.Info(
		"swap validation completed",
		"pool_address",
		validationSummary.PoolAddress,

		"from_block",
		validationSummary.FromBlock,

		"to_block",
		validationSummary.ToBlock,

		"graph_indexed_through",
		validationSummary.GraphIndexedThrough,

		"scanned_windows",
		validationSummary.ScannedWindows,

		"candidate_blocks",
		validationSummary.CandidateBlocks,

		"clean_samples",
		validationSummary.CleanSamples,

		"skipped_samples",
		validationSummary.SkippedSamples,

		"clean_sample_percent",
		validationSummary.CleanSamplePercent.String(),

		"exact_matches",
		validationSummary.ExactMatches,

		"exact_match_percent",
		validationSummary.ExactMatchPercent.String(),

		"amount_out_exact_percent",
		validationSummary.AmountOutExactPercent.String(),

		"sqrt_price_exact_percent",
		validationSummary.SqrtPriceExactPercent.String(),

		"tick_exact_percent",
		validationSummary.TickExactPercent.String(),

		"mean_amount_out_diff_bps",
		validationSummary.MeanAmountOutDiffBps.String(),

		"max_amount_out_diff_bps",
		validationSummary.MaxAmountOutDiffBps.String(),

		"mean_sqrt_price_diff_bps",
		validationSummary.MeanSqrtPriceDiffBps.String(),

		"max_sqrt_price_diff_bps",
		validationSummary.MaxSqrtPriceDiffBps.String(),

		"total_swap_steps",
		validationSummary.TotalSwapSteps,

		"total_crossed_ticks",
		validationSummary.TotalCrossedTicks,

		"results_path",
		validationResultsPath,

		"skips_path",
		validationSkipsPath,

		"summary_path",
		validationSummaryPath,
	)

	if err := runBurnEventStudy(
		ctx,
		provider,
		poolStateRepository,
		curveService,
		pool,
		curveAmounts,
		token1Amounts,
		thresholdsBps,
		burnEventStudyConfig{
			OutputDir: outputDir,

			LookbackBlocks: config.LookbackBlocks,

			PageSize: config.BurnPageSize,

			SwapPageSize: config.BurnSwapPageSize,

			MaxCandidates: config.BurnMaxCandidates,

			MaxSamples: config.BurnMaxSamples,

			SamplingBins: config.BurnSamplingBins,

			SamplingSeed: config.BurnSamplingSeed,

			MinimumSpacingBlocks: config.BurnMinimumSpacingBlocks,

			RequireMaxSamples: config.BurnRequireMaxSamples,

			Horizons: defaultBurnOutcomeHorizons(),
		},
	); err != nil {
		return fmt.Errorf(
			"run burn event study: %w",
			err,
		)
	}

	return nil
}

func token1EquivalentAmounts(
	token0Amounts []*big.Int,
	sqrtPriceX96 *big.Int,
) ([]*big.Int, error) {
	result := make([]*big.Int, 0, len(token0Amounts))

	for _, amount0 := range token0Amounts {
		amount1, err := uniswapv3.QuoteToken1ForToken0Raw(
			sqrtPriceX96,
			amount0,
		)
		if err != nil {
			return nil, err
		}

		result = append(result, amount1)
	}

	return result, nil
}

func blockLookback(
	currentBlock uint64,
	lookback uint64,
) uint64 {
	if currentBlock <= lookback {
		return 1
	}

	return currentBlock - lookback
}
