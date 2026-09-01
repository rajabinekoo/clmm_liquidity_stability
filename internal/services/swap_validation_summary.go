package services

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type SwapValidationSummary struct {
	PoolAddress         string
	FromBlock           uint64
	ToBlock             uint64
	GraphIndexedThrough uint64

	ScannedWindows  int
	CandidateBlocks int
	CleanSamples    int
	SkippedSamples  int

	InvalidSwapSkips          int
	IncompleteLPIndexSkips    int
	PriorLiquidityActionSkips int

	ExactMatches          int
	AmountOutExactMatches int
	SqrtPriceExactMatches int
	TickExactMatches      int

	CleanSamplePercent    decimal.Decimal
	ExactMatchPercent     decimal.Decimal
	AmountOutExactPercent decimal.Decimal
	SqrtPriceExactPercent decimal.Decimal
	TickExactPercent      decimal.Decimal

	MeanAmountOutDiffBps decimal.Decimal
	MaxAmountOutDiffBps  decimal.Decimal
	MeanSqrtPriceDiffBps decimal.Decimal
	MaxSqrtPriceDiffBps  decimal.Decimal

	TotalSwapSteps    int
	TotalCrossedTicks int
}

func BuildSwapValidationSummary(
	report SwapValidationReport,
) (SwapValidationSummary, error) {
	if strings.TrimSpace(report.PoolAddress) == "" {
		return SwapValidationSummary{}, fmt.Errorf(
			"build swap validation summary: pool address is required",
		)
	}

	if report.FromBlock == 0 ||
		report.ToBlock == 0 ||
		report.FromBlock > report.ToBlock {
		return SwapValidationSummary{}, fmt.Errorf(
			"build swap validation summary: invalid block range [%d,%d]",
			report.FromBlock,
			report.ToBlock,
		)
	}

	if report.GraphIndexedThrough < report.ToBlock {
		return SwapValidationSummary{}, fmt.Errorf(
			"build swap validation summary: graph indexed through %d before report to-block %d",
			report.GraphIndexedThrough,
			report.ToBlock,
		)
	}

	if report.ScannedWindows < 0 ||
		report.CandidateBlocks < 0 {
		return SwapValidationSummary{}, fmt.Errorf(
			"build swap validation summary: negative scan counters",
		)
	}

	processedCandidates :=
		len(report.Results) +
			len(report.Skipped)

	if report.CandidateBlocks != processedCandidates {
		return SwapValidationSummary{}, fmt.Errorf(
			"build swap validation summary: candidate blocks=%d but results+skips=%d",
			report.CandidateBlocks,
			processedCandidates,
		)
	}

	summary := SwapValidationSummary{
		PoolAddress: report.PoolAddress,

		FromBlock: report.FromBlock,

		ToBlock: report.ToBlock,

		GraphIndexedThrough: report.GraphIndexedThrough,

		ScannedWindows: report.ScannedWindows,

		CandidateBlocks: report.CandidateBlocks,

		CleanSamples: len(report.Results),

		SkippedSamples: len(report.Skipped),
	}

	amountOutDiffSum :=
		decimal.Zero

	sqrtPriceDiffSum :=
		decimal.Zero

	for index, result := range report.Results {
		if err :=
			validateSwapValidationResultConsistency(
				result,
			); err != nil {
			return SwapValidationSummary{}, fmt.Errorf(
				"build swap validation summary: result %d: %w",
				index,
				err,
			)
		}

		if result.BlockNumber <
			report.FromBlock ||
			result.BlockNumber >
				report.ToBlock {
			return SwapValidationSummary{}, fmt.Errorf(
				"build swap validation summary: result %d block %d outside [%d,%d]",
				index,
				result.BlockNumber,
				report.FromBlock,
				report.ToBlock,
			)
		}

		if result.ExactMatch {
			summary.ExactMatches++
		}

		if result.AmountOutExact {
			summary.AmountOutExactMatches++
		}

		if result.SqrtPriceExact {
			summary.SqrtPriceExactMatches++
		}

		if result.TickExact {
			summary.TickExactMatches++
		}

		amountOutDiffSum =
			amountOutDiffSum.Add(
				result.AmountOutDiffBps,
			)

		sqrtPriceDiffSum =
			sqrtPriceDiffSum.Add(
				result.SqrtPriceDiffBps,
			)

		if result.AmountOutDiffBps.
			GreaterThan(
				summary.MaxAmountOutDiffBps,
			) {
			summary.MaxAmountOutDiffBps =
				result.AmountOutDiffBps
		}

		if result.SqrtPriceDiffBps.
			GreaterThan(
				summary.MaxSqrtPriceDiffBps,
			) {
			summary.MaxSqrtPriceDiffBps =
				result.SqrtPriceDiffBps
		}

		summary.TotalSwapSteps +=
			result.SimSwapSteps

		summary.TotalCrossedTicks +=
			result.SimCrossedTicks
	}

	for index, skipped := range report.Skipped {
		if skipped.BlockNumber <
			report.FromBlock ||
			skipped.BlockNumber >
				report.ToBlock {
			return SwapValidationSummary{}, fmt.Errorf(
				"build swap validation summary: skip %d block %d outside [%d,%d]",
				index,
				skipped.BlockNumber,
				report.FromBlock,
				report.ToBlock,
			)
		}

		switch skipped.Reason {
		case SwapValidationSkipInvalidSwap:
			summary.InvalidSwapSkips++

		case SwapValidationSkipIncompleteLPIndex:
			summary.IncompleteLPIndexSkips++

		case SwapValidationSkipPriorLiquidityAction:
			summary.PriorLiquidityActionSkips++

		default:
			return SwapValidationSummary{}, fmt.Errorf(
				"build swap validation summary: skip %d has unknown reason %q",
				index,
				skipped.Reason,
			)
		}
	}

	summary.CleanSamplePercent =
		validationPercent(
			summary.CleanSamples,
			summary.CandidateBlocks,
		)

	summary.ExactMatchPercent =
		validationPercent(
			summary.ExactMatches,
			summary.CleanSamples,
		)

	summary.AmountOutExactPercent =
		validationPercent(
			summary.AmountOutExactMatches,
			summary.CleanSamples,
		)

	summary.SqrtPriceExactPercent =
		validationPercent(
			summary.SqrtPriceExactMatches,
			summary.CleanSamples,
		)

	summary.TickExactPercent =
		validationPercent(
			summary.TickExactMatches,
			summary.CleanSamples,
		)

	if summary.CleanSamples > 0 {
		denominator :=
			decimal.NewFromInt(
				int64(
					summary.CleanSamples,
				),
			)

		summary.MeanAmountOutDiffBps =
			amountOutDiffSum.Div(
				denominator,
			)

		summary.MeanSqrtPriceDiffBps =
			sqrtPriceDiffSum.Div(
				denominator,
			)
	}

	return summary, nil
}

func validationPercent(
	part int,
	total int,
) decimal.Decimal {
	if part <= 0 ||
		total <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromInt(
		int64(part),
	).
		Mul(
			decimal.NewFromInt(100),
		).
		Div(
			decimal.NewFromInt(
				int64(total),
			),
		)
}
