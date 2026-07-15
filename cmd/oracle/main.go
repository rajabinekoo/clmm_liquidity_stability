package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"oracle/internal/providers"
	"oracle/internal/uniswapv3"
	"os"
	"os/signal"
	"syscall"

	"oracle/internal/database"
	"oracle/internal/repositories"
	"oracle/internal/services"
	"oracle/internal/utils"
	"oracle/migrations"

	"github.com/shopspring/decimal"
)

func main() {
	if err := run(); err != nil {
		slog.Error(
			"oracle exited with error",
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

	simulator, err := uniswapv3.NewSimulator(500) // USDC/WETH 0.05% fee tier
	if err != nil {
		return err
	}

	amountIn := new(big.Int).Mul(
		big.NewInt(1_000),
		big.NewInt(1_000_000), // 1000 USDC with 6 decimals
	)

	//swap, err := simulator.SimulateExactInputNoCross(
	//	pool,
	//	uniswapv3.ExactInputRequest{
	//		AmountIn:   amountIn,
	//		ZeroForOne: true, // token0 -> token1, USDC -> WETH
	//	},
	//)

	swap1, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:   amountIn,
			ZeroForOne: true, // token0 -> token1, USDC -> WETH
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

	amountIn = new(big.Int).Mul(
		big.NewInt(1_000_000),
		big.NewInt(1_000_000), // 1,000,000 USDC
	)

	swap2, err := simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:   amountIn,
			ZeroForOne: true, // token0 -> token1, USDC -> WETH
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

	simulator2, err := uniswapv3.NewSimulator(500) // 0.05%
	if err != nil {
		return err
	}

	curveService := services.NewPriceImpactCurveService(simulator2)

	curve, err := curveService.Build(
		ctx,
		services.PriceImpactCurveRequest{
			Pool: pool,
			AmountsIn: []*big.Int{
				usdcAmount(1_000),
				usdcAmount(10_000),
				usdcAmount(50_000),
				usdcAmount(100_000),
				usdcAmount(500_000),
				usdcAmount(1_000_000),
				usdcAmount(2_000_000),
				usdcAmount(5_000_000),
			},
			ZeroForOne: true, // USDC -> WETH
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
		[]decimal.Decimal{
			decimal.NewFromInt(10),  // 10 bps
			decimal.NewFromInt(50),  // 50 bps
			decimal.NewFromInt(100), // 100 bps
		},
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

	curveAmounts := []*big.Int{
		usdcAmount(1_000),
		usdcAmount(10_000),
		usdcAmount(50_000),
		usdcAmount(100_000),
		usdcAmount(500_000),
		usdcAmount(1_000_000),
		usdcAmount(2_000_000),
		usdcAmount(5_000_000),
	}

	//positions, err := poolStateRepository.LoadActivePositionsAt(
	//	ctx,
	//	pool.PoolAddress,
	//	pool.BlockNumber,
	//	pool.CurrentTick,
	//	5,
	//)
	//if err != nil {
	//	return err
	//}

	//for _, position := range positions {
	//	counterfactualPool, err := uniswapv3.RemoveLiquidity(
	//		pool,
	//		position,
	//	)
	//	if err != nil {
	//		return err
	//	}
	//
	//	counterfactualCurve, err := curveService.Build(
	//		ctx,
	//		services.PriceImpactCurveRequest{
	//			Pool:       counterfactualPool,
	//			AmountsIn:  curveAmounts,
	//			ZeroForOne: true,
	//		},
	//	)
	//	if err != nil {
	//		return err
	//	}
	//
	//	counterfactualSummary, err := curveService.Summarize(
	//		counterfactualCurve,
	//		[]decimal.Decimal{
	//			decimal.NewFromInt(10),
	//			decimal.NewFromInt(50),
	//			decimal.NewFromInt(100),
	//		},
	//	)
	//	if err != nil {
	//		return err
	//	}
	//
	//	deltaAUC := counterfactualSummary.PriceImpactAUCBps.
	//		Sub(summary.PriceImpactAUCBps)
	//
	//	slog.Info(
	//		"counterfactual liquidity removal",
	//		"owner", position.Owner,
	//		"tick_lower", position.TickLower,
	//		"tick_upper", position.TickUpper,
	//		"position_liquidity", position.Liquidity.String(),
	//		"base_auc_bps", summary.PriceImpactAUCBps.String(),
	//		"counterfactual_auc_bps", counterfactualSummary.PriceImpactAUCBps.String(),
	//		"delta_auc_bps", deltaAUC.String(),
	//		"base_active_liquidity", pool.Liquidity.String(),
	//		"counterfactual_active_liquidity", counterfactualPool.Liquidity.String(),
	//	)
	//}

	impactService := services.NewLiquidityImpactService(
		poolStateRepository,
		curveService,
	)

	impactReport, err := impactService.AnalyzeActivePositions(
		ctx,
		services.LiquidityImpactRequest{
			Pool:          pool,
			AmountsIn:     curveAmounts,
			ZeroForOne:    true,
			PositionLimit: 10,
			ThresholdsBps: []decimal.Decimal{
				decimal.NewFromInt(10),
				decimal.NewFromInt(50),
				decimal.NewFromInt(100),
			},
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

	wethEquivalentAmounts, err := token1EquivalentAmounts(
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
			OneForZeroAmountsIn: wethEquivalentAmounts,
			PositionLimit:       10,
			ThresholdsBps: []decimal.Decimal{
				decimal.NewFromInt(10),
				decimal.NewFromInt(50),
				decimal.NewFromInt(100),
			},
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

	csvPath := fmt.Sprintf(
		"outputs/bidirectional_lsis_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
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

	correlationPath := fmt.Sprintf(
		"outputs/bidirectional_lsis_correlations_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
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
			LookbackBlocks: 50_000,
			StepBlocks:     5_000,
			MaxSnapshots:   10,

			ZeroForOneAmountsIn: curveAmounts,
			PositionLimit:       10,
			ThresholdsBps: []decimal.Decimal{
				decimal.NewFromInt(10),
				decimal.NewFromInt(50),
				decimal.NewFromInt(100),
			},
		},
	)
	if err != nil {
		return err
	}

	batchSummaryPath := fmt.Sprintf(
		"outputs/snapshot_batch_summary_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
	)

	if err := services.WriteSnapshotBatchSummaryCSV(
		batchSummaryPath,
		batchResults,
	); err != nil {
		return err
	}

	batchPositionsPath := fmt.Sprintf(
		"outputs/snapshot_batch_positions_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
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

	batchRangesPath := fmt.Sprintf(
		"outputs/snapshot_batch_ranges_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
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

	batchCorrelations, err := services.BuildSnapshotBatchPositionCorrelations(
		batchResults,
	)
	if err != nil {
		return err
	}

	batchCorrelationsPath := fmt.Sprintf(
		"outputs/snapshot_batch_position_correlations_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
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
		"observations", len(batchResults)*10,
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

	batchDiagnosticsPath := fmt.Sprintf(
		"outputs/snapshot_batch_diagnostics_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
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

	empiricalSummaryPath := fmt.Sprintf(
		"outputs/empirical_summary_%s_%d.csv",
		pool.PoolAddress,
		pool.BlockNumber,
	)

	if err := services.WriteEmpiricalSummaryCSV(
		empiricalSummaryPath,
		empiricalSummary,
		len(batchDiagnostics),
		len(batchResults)*10,
	); err != nil {
		return err
	}

	slog.Info(
		"empirical summary exported",
		"path", empiricalSummaryPath,
	)

	//validationService := services.NewSwapValidationService(
	//	provider,
	//	poolStateRepository,
	//	simulator,
	//)
	//
	//validationResults, err := validationService.ValidateFirstSwapsPerBlock(
	//	ctx,
	//	services.SwapValidationRequest{
	//		PoolAddress:     pool.PoolAddress,
	//		FromBlock:       blockLookback(pool.BlockNumber, 200),
	//		ToBlock:         pool.BlockNumber - 1,
	//		PageSize:        20,
	//		MaxSamples:      5,
	//		BlockWindowSize: 8,
	//	},
	//)
	//if err != nil {
	//	slog.Warn(
	//		"swap validation skipped",
	//		"reason", err,
	//	)
	//} else {
	//	for _, result := range validationResults {
	//		slog.Info(
	//			"swap validation result",
	//			"swap_id", result.SwapID,
	//			"block_number", result.BlockNumber,
	//			"log_index", result.LogIndex,
	//			"zero_for_one", result.ZeroForOne,
	//			"amount_in_raw", result.AmountInRaw.String(),
	//			"actual_amount_out_raw", result.ActualAmountOutRaw.String(),
	//			"sim_amount_out_raw", result.SimAmountOutRaw.String(),
	//			"amount_out_abs_diff_raw", result.AmountOutAbsDiffRaw.String(),
	//			"amount_out_diff_bps", result.AmountOutDiffBps.String(),
	//			"actual_tick_after", result.ActualTickAfter,
	//			"sim_tick_after", result.SimTickAfter,
	//			"tick_delta", result.TickDelta,
	//		)
	//
	//		validationPath := fmt.Sprintf(
	//			"outputs/swap_validation_%s_%d_%d.csv",
	//			pool.PoolAddress,
	//			validationResults[0].BlockNumber,
	//			validationResults[len(validationResults)-1].BlockNumber,
	//		)
	//
	//		if err := services.WriteSwapValidationCSV(
	//			validationPath,
	//			validationResults,
	//		); err != nil {
	//			return err
	//		}
	//
	//		slog.Info(
	//			"swap validation csv exported",
	//			"path", validationPath,
	//		)
	//	}
	//}

	return nil
}

func usdcAmount(units int64) *big.Int {
	return new(big.Int).Mul(
		big.NewInt(units),
		big.NewInt(1_000_000), // USDC decimals = 6
	)
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
