package uniswapv3

import (
	"fmt"
	"math/big"
)

// exactInputSwapStep is the result of processing one exact-input swap step
// inside a single liquidity range.
//
// AmountIn excludes the fee. Therefore the caller must subtract:
//
//	AmountIn + FeeAmount
//
// from the remaining user input after every step.
type exactInputSwapStep struct {
	SqrtPriceNextX96 *big.Int

	AmountIn  *big.Int
	AmountOut *big.Int
	FeeAmount *big.Int
}

// computeSwapStepExactInput reproduces the exact-input branch of Uniswap v3
// SwapMath.computeSwapStep.
//
// The swap direction is inferred from the relationship between the current and
// target prices:
//
//	current >= target  => token0 in, token1 out
//	current < target   => token1 in, token0 out
//
// amountRemaining includes both usable input and the fee.
//
// The returned amount values never alias the input big.Int values.
func computeSwapStepExactInput(
	sqrtPriceCurrentX96 *big.Int,
	sqrtPriceTargetX96 *big.Int,
	liquidity *big.Int,
	amountRemaining *big.Int,
	feePips int64,
) (exactInputSwapStep, error) {
	if err := validatePositiveUint160(
		"current sqrt price",
		sqrtPriceCurrentX96,
	); err != nil {
		return exactInputSwapStep{}, err
	}

	if err := validatePositiveUint160(
		"target sqrt price",
		sqrtPriceTargetX96,
	); err != nil {
		return exactInputSwapStep{}, err
	}

	if err := validatePositiveUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return exactInputSwapStep{}, err
	}

	if err := validateUint256(
		"amount remaining",
		amountRemaining,
	); err != nil {
		return exactInputSwapStep{}, err
	}

	if amountRemaining.Sign() == 0 {
		return exactInputSwapStep{}, fmt.Errorf(
			"amount remaining must be greater than zero",
		)
	}

	if feePips < 0 ||
		feePips >= FeeDenominator {
		return exactInputSwapStep{}, fmt.Errorf(
			"invalid fee pips %d: expected value inside [0,%d)",
			feePips,
			FeeDenominator,
		)
	}

	zeroForOne :=
		sqrtPriceCurrentX96.Cmp(
			sqrtPriceTargetX96,
		) >= 0

	feeDenominator :=
		big.NewInt(FeeDenominator)

	feeComplement :=
		big.NewInt(
			FeeDenominator - feePips,
		)

	// This is the maximum part of amountRemaining that may be exchanged after
	// reserving the pool fee:
	//
	// floor(
	//     amountRemaining * (1_000_000 - feePips)
	//     -----------------------------------------
	//                  1_000_000
	// )
	amountRemainingLessFee, err :=
		mulDivFloor(
			amountRemaining,
			feeComplement,
			feeDenominator,
		)
	if err != nil {
		return exactInputSwapStep{}, fmt.Errorf(
			"calculate amount remaining after fee: %w",
			err,
		)
	}

	amountInToTarget, err :=
		amountRequiredToReachTarget(
			sqrtPriceCurrentX96,
			sqrtPriceTargetX96,
			liquidity,
			zeroForOne,
		)
	if err != nil {
		return exactInputSwapStep{}, err
	}

	var sqrtPriceNextX96 *big.Int

	if amountRemainingLessFee.Cmp(
		amountInToTarget,
	) >= 0 {
		// The available input is sufficient to reach the end of this range.
		sqrtPriceNextX96 =
			new(big.Int).Set(
				sqrtPriceTargetX96,
			)
	} else {
		// The available input is exhausted before the target price.
		sqrtPriceNextX96, err =
			getNextSqrtPriceFromInput(
				sqrtPriceCurrentX96,
				liquidity,
				amountRemainingLessFee,
				zeroForOne,
			)
		if err != nil {
			return exactInputSwapStep{}, fmt.Errorf(
				"calculate partial-step next sqrt price: %w",
				err,
			)
		}
	}

	if err := validateSwapStepPriceMovement(
		sqrtPriceCurrentX96,
		sqrtPriceTargetX96,
		sqrtPriceNextX96,
		zeroForOne,
	); err != nil {
		return exactInputSwapStep{}, err
	}

	reachedTarget :=
		sqrtPriceNextX96.Cmp(
			sqrtPriceTargetX96,
		) == 0

	amountIn, amountOut, err :=
		swapStepAmounts(
			sqrtPriceCurrentX96,
			sqrtPriceNextX96,
			liquidity,
			zeroForOne,
			reachedTarget,
			amountInToTarget,
		)
	if err != nil {
		return exactInputSwapStep{}, err
	}

	feeAmount, err :=
		swapStepFeeAmount(
			amountRemaining,
			amountIn,
			feePips,
			reachedTarget,
		)
	if err != nil {
		return exactInputSwapStep{}, err
	}

	consumedInput := new(big.Int).Add(
		new(big.Int).Set(amountIn),
		feeAmount,
	)

	if consumedInput.Cmp(
		amountRemaining,
	) > 0 {
		return exactInputSwapStep{}, fmt.Errorf(
			"swap step consumes %s but only %s input remains",
			consumedInput,
			amountRemaining,
		)
	}

	if !reachedTarget &&
		consumedInput.Cmp(amountRemaining) != 0 {
		return exactInputSwapStep{}, fmt.Errorf(
			"partial swap step consumed %s but must consume all remaining input %s",
			consumedInput,
			amountRemaining,
		)
	}

	return exactInputSwapStep{
		SqrtPriceNextX96: new(big.Int).Set(
			sqrtPriceNextX96,
		),

		AmountIn: new(big.Int).Set(
			amountIn,
		),

		AmountOut: new(big.Int).Set(
			amountOut,
		),

		FeeAmount: new(big.Int).Set(
			feeAmount,
		),
	}, nil
}

func amountRequiredToReachTarget(
	sqrtPriceCurrentX96 *big.Int,
	sqrtPriceTargetX96 *big.Int,
	liquidity *big.Int,
	zeroForOne bool,
) (*big.Int, error) {
	if zeroForOne {
		amount, err := getAmount0Delta(
			sqrtPriceTargetX96,
			sqrtPriceCurrentX96,
			liquidity,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"calculate token0 input required to reach target: %w",
				err,
			)
		}

		return amount, nil
	}

	amount, err := getAmount1Delta(
		sqrtPriceCurrentX96,
		sqrtPriceTargetX96,
		liquidity,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate token1 input required to reach target: %w",
			err,
		)
	}

	return amount, nil
}

func swapStepAmounts(
	sqrtPriceCurrentX96 *big.Int,
	sqrtPriceNextX96 *big.Int,
	liquidity *big.Int,
	zeroForOne bool,
	reachedTarget bool,
	amountInToTarget *big.Int,
) (
	amountIn *big.Int,
	amountOut *big.Int,
	err error,
) {
	if zeroForOne {
		if reachedTarget {
			amountIn =
				new(big.Int).Set(
					amountInToTarget,
				)
		} else {
			amountIn, err = getAmount0Delta(
				sqrtPriceNextX96,
				sqrtPriceCurrentX96,
				liquidity,
				true,
			)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"calculate partial token0 input: %w",
					err,
				)
			}
		}

		amountOut, err = getAmount1Delta(
			sqrtPriceNextX96,
			sqrtPriceCurrentX96,
			liquidity,
			false,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"calculate token1 output: %w",
				err,
			)
		}

		return amountIn, amountOut, nil
	}

	if reachedTarget {
		amountIn =
			new(big.Int).Set(
				amountInToTarget,
			)
	} else {
		amountIn, err = getAmount1Delta(
			sqrtPriceCurrentX96,
			sqrtPriceNextX96,
			liquidity,
			true,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"calculate partial token1 input: %w",
				err,
			)
		}
	}

	amountOut, err = getAmount0Delta(
		sqrtPriceCurrentX96,
		sqrtPriceNextX96,
		liquidity,
		false,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"calculate token0 output: %w",
			err,
		)
	}

	return amountIn, amountOut, nil
}

func swapStepFeeAmount(
	amountRemaining *big.Int,
	amountIn *big.Int,
	feePips int64,
	reachedTarget bool,
) (*big.Int, error) {
	if !reachedTarget {
		if amountIn.Cmp(amountRemaining) > 0 {
			return nil, fmt.Errorf(
				"partial swap input %s exceeds remaining amount %s",
				amountIn,
				amountRemaining,
			)
		}

		// When the target is not reached, all user input is consumed. The
		// difference between the total remaining input and usable input is
		// therefore the exact fee for this step.
		return new(big.Int).Sub(
			new(big.Int).Set(
				amountRemaining,
			),
			amountIn,
		), nil
	}

	if feePips == 0 ||
		amountIn.Sign() == 0 {
		return big.NewInt(0), nil
	}

	feeAmount, err := mulDivRoundingUp(
		amountIn,
		big.NewInt(feePips),
		big.NewInt(
			FeeDenominator-feePips,
		),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate target-reaching swap fee: %w",
			err,
		)
	}

	return feeAmount, nil
}

func validateSwapStepPriceMovement(
	current *big.Int,
	target *big.Int,
	next *big.Int,
	zeroForOne bool,
) error {
	if next == nil {
		return fmt.Errorf(
			"swap step produced a nil next sqrt price",
		)
	}

	if zeroForOne {
		if next.Cmp(current) > 0 {
			return fmt.Errorf(
				"zero-for-one next price %s exceeds current price %s",
				next,
				current,
			)
		}

		if next.Cmp(target) < 0 {
			return fmt.Errorf(
				"zero-for-one next price %s crossed below target %s",
				next,
				target,
			)
		}

		return nil
	}

	if next.Cmp(current) < 0 {
		return fmt.Errorf(
			"one-for-zero next price %s is below current price %s",
			next,
			current,
		)
	}

	if next.Cmp(target) > 0 {
		return fmt.Errorf(
			"one-for-zero next price %s crossed above target %s",
			next,
			target,
		)
	}

	return nil
}
