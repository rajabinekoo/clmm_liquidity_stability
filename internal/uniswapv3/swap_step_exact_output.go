package uniswapv3

import (
	"fmt"
	"math/big"
)

type exactOutputSwapStep struct {
	SqrtPriceNextX96 *big.Int

	AmountIn  *big.Int
	AmountOut *big.Int
	FeeAmount *big.Int
}

func computeSwapStepExactOutput(
	sqrtPriceCurrentX96 *big.Int,
	sqrtPriceTargetX96 *big.Int,
	liquidity *big.Int,
	amountRemainingOut *big.Int,
	feePips int64,
) (exactOutputSwapStep, error) {
	if err := validatePositiveUint160(
		"current sqrt price",
		sqrtPriceCurrentX96,
	); err != nil {
		return exactOutputSwapStep{}, err
	}

	if err := validatePositiveUint160(
		"target sqrt price",
		sqrtPriceTargetX96,
	); err != nil {
		return exactOutputSwapStep{}, err
	}

	if err := validatePositiveUint128(
		"liquidity",
		liquidity,
	); err != nil {
		return exactOutputSwapStep{}, err
	}

	if err := validateUint256(
		"amount remaining out",
		amountRemainingOut,
	); err != nil {
		return exactOutputSwapStep{}, err
	}

	if amountRemainingOut.Sign() == 0 {
		return exactOutputSwapStep{}, fmt.Errorf(
			"amount remaining out must be greater than zero",
		)
	}

	if feePips < 0 ||
		feePips >= FeeDenominator {
		return exactOutputSwapStep{}, fmt.Errorf(
			"invalid fee pips %d: expected value inside [0,%d)",
			feePips,
			FeeDenominator,
		)
	}

	zeroForOne :=
		sqrtPriceCurrentX96.Cmp(
			sqrtPriceTargetX96,
		) >= 0

	amountOutToTarget, err :=
		outputAvailableToReachTarget(
			sqrtPriceCurrentX96,
			sqrtPriceTargetX96,
			liquidity,
			zeroForOne,
		)
	if err != nil {
		return exactOutputSwapStep{}, err
	}

	var sqrtPriceNextX96 *big.Int

	if amountRemainingOut.Cmp(
		amountOutToTarget,
	) >= 0 {
		sqrtPriceNextX96 =
			new(big.Int).Set(
				sqrtPriceTargetX96,
			)
	} else {
		sqrtPriceNextX96, err =
			getNextSqrtPriceFromOutput(
				sqrtPriceCurrentX96,
				liquidity,
				amountRemainingOut,
				zeroForOne,
			)
		if err != nil {
			return exactOutputSwapStep{}, fmt.Errorf(
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
		return exactOutputSwapStep{}, err
	}

	reachedTarget :=
		sqrtPriceNextX96.Cmp(
			sqrtPriceTargetX96,
		) == 0

	amountIn, amountOut, err :=
		exactOutputSwapStepAmounts(
			sqrtPriceCurrentX96,
			sqrtPriceNextX96,
			liquidity,
			zeroForOne,
			reachedTarget,
			amountOutToTarget,
		)
	if err != nil {
		return exactOutputSwapStep{}, err
	}

	if amountOut.Cmp(
		amountRemainingOut,
	) > 0 {
		amountOut =
			new(big.Int).Set(
				amountRemainingOut,
			)
	}

	feeAmount, err :=
		mulDivRoundingUp(
			amountIn,
			big.NewInt(feePips),
			big.NewInt(
				FeeDenominator-feePips,
			),
		)
	if err != nil {
		return exactOutputSwapStep{}, fmt.Errorf(
			"calculate exact-output swap fee: %w",
			err,
		)
	}

	return exactOutputSwapStep{
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

func outputAvailableToReachTarget(
	sqrtPriceCurrentX96 *big.Int,
	sqrtPriceTargetX96 *big.Int,
	liquidity *big.Int,
	zeroForOne bool,
) (*big.Int, error) {
	if zeroForOne {
		amount, err := getAmount1Delta(
			sqrtPriceTargetX96,
			sqrtPriceCurrentX96,
			liquidity,
			false,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"calculate token1 output available to target: %w",
				err,
			)
		}

		return amount, nil
	}

	amount, err := getAmount0Delta(
		sqrtPriceCurrentX96,
		sqrtPriceTargetX96,
		liquidity,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"calculate token0 output available to target: %w",
			err,
		)
	}

	return amount, nil
}

func exactOutputSwapStepAmounts(
	sqrtPriceCurrentX96 *big.Int,
	sqrtPriceNextX96 *big.Int,
	liquidity *big.Int,
	zeroForOne bool,
	reachedTarget bool,
	amountOutToTarget *big.Int,
) (
	amountIn *big.Int,
	amountOut *big.Int,
	err error,
) {
	if zeroForOne {
		amountIn, err = getAmount0Delta(
			sqrtPriceNextX96,
			sqrtPriceCurrentX96,
			liquidity,
			true,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"calculate token0 input: %w",
				err,
			)
		}

		if reachedTarget {
			amountOut =
				new(big.Int).Set(
					amountOutToTarget,
				)
		} else {
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
		}

		return amountIn, amountOut, nil
	}

	amountIn, err = getAmount1Delta(
		sqrtPriceCurrentX96,
		sqrtPriceNextX96,
		liquidity,
		true,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"calculate token1 input: %w",
			err,
		)
	}

	if reachedTarget {
		amountOut =
			new(big.Int).Set(
				amountOutToTarget,
			)
	} else {
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
	}

	return amountIn, amountOut, nil
}
