package uniswapv3

import (
	"fmt"
	"math/big"
)

const (
	// MinTick and MaxTick are the exact bounds supported by Uniswap v3.
	MinTick = -887272
	MaxTick = 887272
)

var (
	one = big.NewInt(1)

	two32 = new(big.Int).Lsh(
		new(big.Int).Set(one),
		32,
	)

	two128 = new(big.Int).Lsh(
		new(big.Int).Set(one),
		128,
	)

	maxUint256 = new(big.Int).Sub(
		new(big.Int).Lsh(
			new(big.Int).Set(one),
			256,
		),
		one,
	)

	sqrtRatioRoundingMask = new(big.Int).Sub(
		new(big.Int).Set(two32),
		one,
	)

	// MinSqrtRatio is SqrtRatioAtTick(MinTick).
	MinSqrtRatio = mustDecimalBigInt(
		"4295128739",
	)

	// MaxSqrtRatio is SqrtRatioAtTick(MaxTick).
	//
	// TickAtSqrtRatio follows the Uniswap v3 convention and accepts:
	//
	//	MinSqrtRatio <= sqrtPriceX96 < MaxSqrtRatio
	MaxSqrtRatio = mustDecimalBigInt(
		"1461446703485210103287273052203988822378723970342",
	)
)

type tickRatioConstant struct {
	mask  int
	value *big.Int
}

var tickRatioConstants = []tickRatioConstant{
	{
		mask:  0x2,
		value: mustHexBigInt("fff97272373d413259a46990580e213a"),
	},
	{
		mask:  0x4,
		value: mustHexBigInt("fff2e50f5f656932ef12357cf3c7fdcc"),
	},
	{
		mask:  0x8,
		value: mustHexBigInt("ffe5caca7e10e4e61c3624eaa0941cd0"),
	},
	{
		mask:  0x10,
		value: mustHexBigInt("ffcb9843d60f6159c9db58835c926644"),
	},
	{
		mask:  0x20,
		value: mustHexBigInt("ff973b41fa98c081472e6896dfb254c0"),
	},
	{
		mask:  0x40,
		value: mustHexBigInt("ff2ea16466c96a3843ec78b326b52861"),
	},
	{
		mask:  0x80,
		value: mustHexBigInt("fe5dee046a99a2a811c461f1969c3053"),
	},
	{
		mask:  0x100,
		value: mustHexBigInt("fcbe86c7900a88aedcffc83b479aa3a4"),
	},
	{
		mask:  0x200,
		value: mustHexBigInt("f987a7253ac413176f2b074cf7815e54"),
	},
	{
		mask:  0x400,
		value: mustHexBigInt("f3392b0822b70005940c7a398e4b70f3"),
	},
	{
		mask:  0x800,
		value: mustHexBigInt("e7159475a2c29b7443b29c7fa6e889d9"),
	},
	{
		mask:  0x1000,
		value: mustHexBigInt("d097f3bdfd2022b8845ad8f792aa5825"),
	},
	{
		mask:  0x2000,
		value: mustHexBigInt("a9f746462d870fdf8a65dc1f90e061e5"),
	},
	{
		mask:  0x4000,
		value: mustHexBigInt("70d869a156d2a1b890bb3df62baf32f7"),
	},
	{
		mask:  0x8000,
		value: mustHexBigInt("31be135f97d08fd981231505542fcfa6"),
	},
	{
		mask:  0x10000,
		value: mustHexBigInt("9aa508b5b7a84e1c677de54f3e99bc9"),
	},
	{
		mask:  0x20000,
		value: mustHexBigInt("5d6af8dedb81196699c329225ee604"),
	},
	{
		mask:  0x40000,
		value: mustHexBigInt("2216e584f5fa1ea926041bedfe98"),
	},
	{
		mask:  0x80000,
		value: mustHexBigInt("48a170391f7dc42444e8fa2"),
	},
}

// SqrtRatioAtTick calculates:
//
//	sqrt(1.0001^tick) * 2^96
//
// using the same integer constants and rounding behavior as Uniswap v3
// TickMath.getSqrtRatioAtTick.
//
// The returned value is always a new big.Int and may safely be modified by the
// caller.
func SqrtRatioAtTick(
	tick int,
) (*big.Int, error) {
	if tick < MinTick || tick > MaxTick {
		return nil, fmt.Errorf(
			"tick %d is outside supported range [%d,%d]",
			tick,
			MinTick,
			MaxTick,
		)
	}

	absTick := tick
	if absTick < 0 {
		absTick = -absTick
	}

	ratio := new(big.Int)

	if absTick&0x1 != 0 {
		ratio.Set(
			mustHexBigInt(
				"fffcb933bd6fad37aa2d162d1a594001",
			),
		)
	} else {
		ratio.Set(two128)
	}

	for _, constant := range tickRatioConstants {
		if absTick&constant.mask == 0 {
			continue
		}

		ratio.Mul(
			ratio,
			constant.value,
		)

		ratio.Rsh(
			ratio,
			128,
		)
	}

	if tick > 0 {
		ratio.Div(
			maxUint256,
			ratio,
		)
	}

	result := new(big.Int).Rsh(
		new(big.Int).Set(ratio),
		32,
	)

	// Solidity rounds the Q128.128 value upward when converting it to Q64.96
	// whenever discarded lower bits are non-zero.
	remainder := new(big.Int).And(
		new(big.Int).Set(ratio),
		sqrtRatioRoundingMask,
	)

	if remainder.Sign() != 0 {
		result.Add(
			result,
			one,
		)
	}

	return result, nil
}

// TickAtSqrtRatio returns the greatest tick whose square-root ratio does not
// exceed sqrtPriceX96:
//
//	SqrtRatioAtTick(tick) <= sqrtPriceX96
//
// The implementation deliberately avoids float64 and logarithms. A bounded
// integer binary search is deterministic, exact and sufficiently fast for the
// offline research pipeline.
func TickAtSqrtRatio(
	sqrtPriceX96 *big.Int,
) (int, error) {
	if sqrtPriceX96 == nil {
		return 0, fmt.Errorf(
			"sqrt price is nil",
		)
	}

	if sqrtPriceX96.Cmp(MinSqrtRatio) < 0 {
		return 0, fmt.Errorf(
			"sqrt price %s is below minimum %s",
			sqrtPriceX96,
			MinSqrtRatio,
		)
	}

	// Uniswap v3 uses an exclusive upper bound for getTickAtSqrtRatio.
	if sqrtPriceX96.Cmp(MaxSqrtRatio) >= 0 {
		return 0, fmt.Errorf(
			"sqrt price %s must be lower than maximum %s",
			sqrtPriceX96,
			MaxSqrtRatio,
		)
	}

	low := MinTick
	high := MaxTick

	for low <= high {
		middle := low + (high-low)/2

		middleRatio, err :=
			SqrtRatioAtTick(middle)
		if err != nil {
			return 0, fmt.Errorf(
				"calculate sqrt ratio for tick %d: %w",
				middle,
				err,
			)
		}

		comparison :=
			middleRatio.Cmp(sqrtPriceX96)

		switch {
		case comparison == 0:
			return middle, nil

		case comparison < 0:
			low = middle + 1

		default:
			high = middle - 1
		}
	}

	// At loop termination high is the greatest tick with ratio <= input.
	if high < MinTick || high > MaxTick {
		return 0, fmt.Errorf(
			"failed to resolve tick for sqrt price %s",
			sqrtPriceX96,
		)
	}

	return high, nil
}

func mustHexBigInt(
	value string,
) *big.Int {
	number, ok := new(big.Int).SetString(
		value,
		16,
	)
	if !ok {
		panic(
			"invalid hexadecimal integer constant: " +
				value,
		)
	}

	return number
}

func mustDecimalBigInt(
	value string,
) *big.Int {
	number, ok := new(big.Int).SetString(
		value,
		10,
	)
	if !ok {
		panic(
			"invalid decimal integer constant: " +
				value,
		)
	}

	return number
}
