package domain

import "math/big"

type SwapEvent struct {
	ID          string
	TxHash      string
	PoolAddress string

	BlockNumber uint64
	LogIndex    int
	Timestamp   uint64

	Amount0Raw *big.Int
	Amount1Raw *big.Int

	SqrtPriceX96After *big.Int
	TickAfter         int
}

func (s SwapEvent) IsZeroForOne() bool {
	return s.Amount0Raw.Sign() > 0 && s.Amount1Raw.Sign() < 0
}

func (s SwapEvent) IsOneForZero() bool {
	return s.Amount1Raw.Sign() > 0 && s.Amount0Raw.Sign() < 0
}

func (s SwapEvent) AmountInRaw() *big.Int {
	if s.IsZeroForOne() {
		return new(big.Int).Set(s.Amount0Raw)
	}

	if s.IsOneForZero() {
		return new(big.Int).Set(s.Amount1Raw)
	}

	return big.NewInt(0)
}

func (s SwapEvent) AmountOutRaw() *big.Int {
	if s.IsZeroForOne() {
		return absBigInt(s.Amount1Raw)
	}

	if s.IsOneForZero() {
		return absBigInt(s.Amount0Raw)
	}

	return big.NewInt(0)
}

func absBigInt(value *big.Int) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}

	if value.Sign() < 0 {
		return new(big.Int).Neg(value)
	}

	return new(big.Int).Set(value)
}
