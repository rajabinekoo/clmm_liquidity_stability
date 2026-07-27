package services

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type SwapReplayMode string

const (
	SwapReplayModeExactInput SwapReplayMode = "exact_input"

	SwapReplayModeExactOutput SwapReplayMode = "exact_output"

	SwapReplayModeBoth SwapReplayMode = "exact_input_and_exact_output"
)

type BurnSwapReplayAudit struct {
	SwapID string
	TxHash string

	Cursor domain.EventCursor

	ZeroForOne bool
	Mode       SwapReplayMode

	AmountInRaw  *big.Int
	AmountOutRaw *big.Int

	SqrtPriceX96After *big.Int

	SimulatedSqrtPriceX96After *big.Int
	SqrtPriceAbsDiffRaw        *big.Int
	SqrtPriceExact             bool
	SqrtPriceWithinTolerance   bool

	TickAfter int

	SwapSteps    int
	CrossedTicks int
}

func (a BurnSwapReplayAudit) Validate() error {
	if strings.TrimSpace(a.SwapID) == "" {
		return fmt.Errorf(
			"burn swap replay audit: swap id is required",
		)
	}

	if strings.TrimSpace(a.TxHash) == "" {
		return fmt.Errorf(
			"burn swap replay audit: transaction hash is required",
		)
	}

	if err := a.Cursor.Validate(); err != nil {
		return fmt.Errorf(
			"burn swap replay audit: invalid cursor: %w",
			err,
		)
	}

	switch a.Mode {
	case SwapReplayModeExactInput,
		SwapReplayModeExactOutput,
		SwapReplayModeBoth:

	default:
		return fmt.Errorf(
			"burn swap replay audit: unsupported mode %q",
			a.Mode,
		)
	}

	if a.AmountInRaw == nil ||
		a.AmountInRaw.Sign() <= 0 {
		return fmt.Errorf(
			"burn swap replay audit: amount in must be positive",
		)
	}

	if a.AmountOutRaw == nil ||
		a.AmountOutRaw.Sign() <= 0 {
		return fmt.Errorf(
			"burn swap replay audit: amount out must be positive",
		)
	}

	if a.SqrtPriceX96After == nil ||
		a.SqrtPriceX96After.Sign() <= 0 {
		return fmt.Errorf(
			"burn swap replay audit: sqrt price after must be positive",
		)
	}

	if a.SimulatedSqrtPriceX96After == nil ||
		a.SimulatedSqrtPriceX96After.Sign() <= 0 {
		return fmt.Errorf(
			"burn swap replay audit: simulated sqrt price after must be positive",
		)
	}

	expectedSqrtDifference :=
		new(big.Int).Sub(
			a.SimulatedSqrtPriceX96After,
			a.SqrtPriceX96After,
		)

	expectedSqrtDifference.Abs(
		expectedSqrtDifference,
	)

	if a.SqrtPriceAbsDiffRaw == nil ||
		a.SqrtPriceAbsDiffRaw.Sign() < 0 ||
		a.SqrtPriceAbsDiffRaw.Cmp(
			expectedSqrtDifference,
		) != 0 {
		return fmt.Errorf(
			"burn swap replay audit: sqrt price absolute difference=%v, expected=%s",
			a.SqrtPriceAbsDiffRaw,
			expectedSqrtDifference,
		)
	}

	expectedSqrtExact :=
		expectedSqrtDifference.Sign() == 0

	if a.SqrtPriceExact !=
		expectedSqrtExact {
		return fmt.Errorf(
			"burn swap replay audit: sqrt price exact=%t, expected=%t",
			a.SqrtPriceExact,
			expectedSqrtExact,
		)
	}

	if a.SqrtPriceExact &&
		!a.SqrtPriceWithinTolerance {
		return fmt.Errorf(
			"burn swap replay audit: exact sqrt price must be within tolerance",
		)
	}

	if a.SwapSteps <= 0 {
		return fmt.Errorf(
			"burn swap replay audit: swap steps must be positive",
		)
	}

	if a.CrossedTicks < 0 ||
		a.CrossedTicks > a.SwapSteps {
		return fmt.Errorf(
			"burn swap replay audit: crossed ticks=%d is invalid for swap steps=%d",
			a.CrossedTicks,
			a.SwapSteps,
		)
	}

	return nil
}

func cloneBurnSwapReplayAudits(
	values []BurnSwapReplayAudit,
) []BurnSwapReplayAudit {
	if values == nil {
		return nil
	}

	result := make(
		[]BurnSwapReplayAudit,
		len(values),
	)

	for index, value := range values {
		result[index] =
			BurnSwapReplayAudit{
				SwapID: value.SwapID,
				TxHash: value.TxHash,

				Cursor: value.Cursor,

				ZeroForOne: value.ZeroForOne,
				Mode:       value.Mode,

				AmountInRaw: cloneBigInt(
					value.AmountInRaw,
				),

				AmountOutRaw: cloneBigInt(
					value.AmountOutRaw,
				),

				SqrtPriceX96After: cloneBigInt(
					value.SqrtPriceX96After,
				),

				SimulatedSqrtPriceX96After: cloneBigInt(
					value.SimulatedSqrtPriceX96After,
				),

				SqrtPriceAbsDiffRaw: cloneBigInt(
					value.SqrtPriceAbsDiffRaw,
				),

				SqrtPriceExact: value.SqrtPriceExact,

				SqrtPriceWithinTolerance: value.SqrtPriceWithinTolerance,

				TickAfter: value.TickAfter,

				SwapSteps: value.SwapSteps,

				CrossedTicks: value.CrossedTicks,
			}
	}

	return result
}
