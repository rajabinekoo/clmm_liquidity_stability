package domain

import (
	"fmt"
	"math/big"
	"strings"
)

const (
	minUniswapV3Tick = -887272
	maxUniswapV3Tick = 887272
)

type SwapEvent struct {
	ID          string
	TxHash      string
	PoolAddress string

	BlockNumber uint64
	LogIndex    int
	Timestamp   uint64

	// The signs follow the Uniswap v3 pool perspective:
	//
	//   positive = token entered the pool
	//   negative = token left the pool
	Amount0Raw *big.Int
	Amount1Raw *big.Int

	// These fields represent the pool state immediately after the Swap event.
	SqrtPriceX96After *big.Int
	TickAfter         int
}

// Validate verifies that the event can represent a normal Uniswap v3 Swap.
//
// A valid Swap must have exactly one positive token delta and one negative
// token delta. Zero values and equal-sign values are rejected instead of being
// silently classified as an unknown direction.
func (s SwapEvent) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf(
			"swap event id is required",
		)
	}

	if strings.TrimSpace(s.TxHash) == "" {
		return fmt.Errorf(
			"swap transaction hash is required",
		)
	}

	if strings.TrimSpace(s.PoolAddress) == "" {
		return fmt.Errorf(
			"swap pool address is required",
		)
	}

	if s.BlockNumber == 0 {
		return fmt.Errorf(
			"swap block number must be greater than zero",
		)
	}

	if s.LogIndex < 0 {
		return fmt.Errorf(
			"swap log index must not be negative: %d",
			s.LogIndex,
		)
	}

	if s.Timestamp == 0 {
		return fmt.Errorf(
			"swap timestamp must be greater than zero",
		)
	}

	if s.Amount0Raw == nil {
		return fmt.Errorf(
			"swap amount0 is nil",
		)
	}

	if s.Amount1Raw == nil {
		return fmt.Errorf(
			"swap amount1 is nil",
		)
	}

	if s.Amount0Raw.Sign() == 0 {
		return fmt.Errorf(
			"swap amount0 must not be zero",
		)
	}

	if s.Amount1Raw.Sign() == 0 {
		return fmt.Errorf(
			"swap amount1 must not be zero",
		)
	}

	if !s.IsZeroForOne() &&
		!s.IsOneForZero() {
		return fmt.Errorf(
			"swap token deltas must have opposite signs: amount0=%s amount1=%s",
			s.Amount0Raw,
			s.Amount1Raw,
		)
	}

	if s.SqrtPriceX96After == nil {
		return fmt.Errorf(
			"swap sqrt price after event is nil",
		)
	}

	if s.SqrtPriceX96After.Sign() <= 0 {
		return fmt.Errorf(
			"swap sqrt price after event must be positive: %s",
			s.SqrtPriceX96After,
		)
	}

	// The Uniswap v3 Swap event stores sqrtPriceX96 as uint160.
	if s.SqrtPriceX96After.BitLen() > 160 {
		return fmt.Errorf(
			"swap sqrt price after event exceeds uint160: %s",
			s.SqrtPriceX96After,
		)
	}

	if s.TickAfter < minUniswapV3Tick ||
		s.TickAfter > maxUniswapV3Tick {
		return fmt.Errorf(
			"swap tick after event %d is outside [%d,%d]",
			s.TickAfter,
			minUniswapV3Tick,
			maxUniswapV3Tick,
		)
	}

	return nil
}

func (s SwapEvent) IsZeroForOne() bool {
	return s.Amount0Raw != nil &&
		s.Amount1Raw != nil &&
		s.Amount0Raw.Sign() > 0 &&
		s.Amount1Raw.Sign() < 0
}

func (s SwapEvent) IsOneForZero() bool {
	return s.Amount0Raw != nil &&
		s.Amount1Raw != nil &&
		s.Amount1Raw.Sign() > 0 &&
		s.Amount0Raw.Sign() < 0
}

// AmountInRaw returns the gross token input represented by the Swap event.
//
// For Uniswap v3 this positive pool delta includes the fee paid to the pool,
// so it is the correct gross input for an exact-input simulator with per-step
// fee accounting.
func (s SwapEvent) AmountInRaw() *big.Int {
	switch {
	case s.IsZeroForOne():
		return new(big.Int).Set(
			s.Amount0Raw,
		)

	case s.IsOneForZero():
		return new(big.Int).Set(
			s.Amount1Raw,
		)

	default:
		return big.NewInt(0)
	}
}

func (s SwapEvent) AmountOutRaw() *big.Int {
	switch {
	case s.IsZeroForOne():
		return absBigInt(
			s.Amount1Raw,
		)

	case s.IsOneForZero():
		return absBigInt(
			s.Amount0Raw,
		)

	default:
		return big.NewInt(0)
	}
}

func absBigInt(
	value *big.Int,
) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}

	return new(big.Int).Abs(
		new(big.Int).Set(value),
	)
}

// ValidateForSimulation checks whether the Swap contains an unambiguous
// exact-input direction with strictly positive input and output amounts.
//
// Validate permits a single zero token delta for structural ingestion, but
// such an event cannot be replayed as an exact-input simulation sample.
func (s SwapEvent) ValidateForSimulation() error {
	if err := s.Validate(); err != nil {
		return err
	}

	if !s.IsZeroForOne() &&
		!s.IsOneForZero() {
		return fmt.Errorf(
			"swap does not have opposite non-zero token deltas: amount0=%s amount1=%s",
			s.Amount0Raw,
			s.Amount1Raw,
		)
	}

	amountIn := s.AmountInRaw()
	if amountIn.Sign() <= 0 {
		return fmt.Errorf(
			"swap input amount must be positive: %s",
			amountIn,
		)
	}

	amountOut := s.AmountOutRaw()
	if amountOut.Sign() <= 0 {
		return fmt.Errorf(
			"swap output amount must be positive: %s",
			amountOut,
		)
	}

	return nil
}
