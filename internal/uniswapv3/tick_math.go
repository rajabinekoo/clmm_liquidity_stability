package uniswapv3

import (
	"fmt"
	"math/big"
)

const (
	MinTick = -887272
	MaxTick = 887272
)

var (
	two32       = new(big.Int).Lsh(big.NewInt(1), 32)
	two128      = new(big.Int).Lsh(big.NewInt(1), 128)
	maxUint256  = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	roundingMod = new(big.Int).Sub(two32, big.NewInt(1))
)

type tickRatioConstant struct {
	mask  int
	value *big.Int
}

var tickRatioConstants = []tickRatioConstant{
	{0x2, mustHex("fff97272373d413259a46990580e213a")},
	{0x4, mustHex("fff2e50f5f656932ef12357cf3c7fdcc")},
	{0x8, mustHex("ffe5caca7e10e4e61c3624eaa0941cd0")},
	{0x10, mustHex("ffcb9843d60f6159c9db58835c926644")},
	{0x20, mustHex("ff973b41fa98c081472e6896dfb254c0")},
	{0x40, mustHex("ff2ea16466c96a3843ec78b326b52861")},
	{0x80, mustHex("fe5dee046a99a2a811c461f1969c3053")},
	{0x100, mustHex("fcbe86c7900a88aedcffc83b479aa3a4")},
	{0x200, mustHex("f987a7253ac413176f2b074cf7815e54")},
	{0x400, mustHex("f3392b0822b70005940c7a398e4b70f3")},
	{0x800, mustHex("e7159475a2c29b7443b29c7fa6e889d9")},
	{0x1000, mustHex("d097f3bdfd2022b8845ad8f792aa5825")},
	{0x2000, mustHex("a9f746462d870fdf8a65dc1f90e061e5")},
	{0x4000, mustHex("70d869a156d2a1b890bb3df62baf32f7")},
	{0x8000, mustHex("31be135f97d08fd981231505542fcfa6")},
	{0x10000, mustHex("9aa508b5b7a84e1c677de54f3e99bc9")},
	{0x20000, mustHex("5d6af8dedb81196699c329225ee604")},
	{0x40000, mustHex("2216e584f5fa1ea926041bedfe98")},
	{0x80000, mustHex("48a170391f7dc42444e8fa2")},
}

func SqrtRatioAtTick(tick int) (*big.Int, error) {
	if tick < MinTick || tick > MaxTick {
		return nil, fmt.Errorf("tick %d is outside [%d,%d]", tick, MinTick, MaxTick)
	}

	absTick := tick
	if absTick < 0 {
		absTick = -absTick
	}

	ratio := new(big.Int)
	if absTick&0x1 != 0 {
		ratio.Set(mustHex("fffcb933bd6fad37aa2d162d1a594001"))
	} else {
		ratio.Set(two128)
	}

	for _, constant := range tickRatioConstants {
		if absTick&constant.mask != 0 {
			ratio.Mul(ratio, constant.value)
			ratio.Rsh(ratio, 128)
		}
	}

	if tick > 0 {
		ratio.Div(maxUint256, ratio)
	}

	result := new(big.Int).Rsh(ratio, 32)

	// Round up if ratio % 2^32 != 0.
	if new(big.Int).And(ratio, roundingMod).Sign() != 0 {
		result.Add(result, big.NewInt(1))
	}

	return result, nil
}

func mustHex(value string) *big.Int {
	n, ok := new(big.Int).SetString(value, 16)
	if !ok {
		panic("invalid hex constant: " + value)
	}
	return n
}
