package uniswapv3

import (
	"math/big"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestApplyLiquidityChangeActiveMint(
	t *testing.T,
) {
	t.Parallel()

	pool :=
		liquidityChangeTestPool()

	result, err :=
		ApplyLiquidityChange(
			pool,
			domain.LiquidityChange{
				ID: "mint",

				BlockNumber: 101,

				LogIndex: 1,

				TickLower: -50,

				TickUpper: 50,

				LiquidityDelta: big.NewInt(500),
			},
		)
	if err != nil {
		t.Fatalf(
			"ApplyLiquidityChange() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		result.Liquidity,
		big.NewInt(1_500),
	)

	assertBigIntEqual(
		t,
		result.Ticks[-50].LiquidityGross,
		big.NewInt(500),
	)

	assertBigIntEqual(
		t,
		result.Ticks[-50].LiquidityNet,
		big.NewInt(500),
	)

	assertBigIntEqual(
		t,
		result.Ticks[50].LiquidityGross,
		big.NewInt(500),
	)

	assertBigIntEqual(
		t,
		result.Ticks[50].LiquidityNet,
		big.NewInt(-500),
	)

	// Input pool must remain untouched.
	assertBigIntEqual(
		t,
		pool.Liquidity,
		big.NewInt(1_000),
	)

	if _, exists :=
		pool.Ticks[-50]; exists {
		t.Fatal(
			"input pool was mutated",
		)
	}
}

func TestApplyLiquidityChangeInactiveMintDoesNotChangeActiveLiquidity(
	t *testing.T,
) {
	t.Parallel()

	pool :=
		liquidityChangeTestPool()

	result, err :=
		ApplyLiquidityChange(
			pool,
			domain.LiquidityChange{
				ID: "mint",

				BlockNumber: 101,

				LogIndex: 1,

				TickLower: 100,

				TickUpper: 200,

				LiquidityDelta: big.NewInt(500),
			},
		)
	if err != nil {
		t.Fatalf(
			"ApplyLiquidityChange() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		result.Liquidity,
		big.NewInt(1_000),
	)
}

func TestApplyLiquidityChangeBurnRemovesInitializedTicks(
	t *testing.T,
) {
	t.Parallel()

	pool :=
		liquidityChangeTestPool()

	result, err :=
		ApplyLiquidityChange(
			pool,
			domain.LiquidityChange{
				ID: "burn",

				BlockNumber: 101,

				LogIndex: 1,

				TickLower: -100,

				TickUpper: 100,

				LiquidityDelta: big.NewInt(-1_000),
			},
		)
	if err != nil {
		t.Fatalf(
			"ApplyLiquidityChange() error = %v",
			err,
		)
	}

	assertBigIntEqual(
		t,
		result.Liquidity,
		big.NewInt(0),
	)

	if len(result.Ticks) != 0 {
		t.Fatalf(
			"len(Ticks) = %d, want 0",
			len(result.Ticks),
		)
	}

	if len(result.InitializedTicks) != 0 {
		t.Fatalf(
			"len(InitializedTicks) = %d, want 0",
			len(result.InitializedTicks),
		)
	}
}

func TestApplyLiquidityChangeRejectsOverBurn(
	t *testing.T,
) {
	t.Parallel()

	pool :=
		liquidityChangeTestPool()

	_, err :=
		ApplyLiquidityChange(
			pool,
			domain.LiquidityChange{
				ID: "burn",

				BlockNumber: 101,

				LogIndex: 1,

				TickLower: -100,

				TickUpper: 100,

				LiquidityDelta: big.NewInt(-1_001),
			},
		)

	if err == nil {
		t.Fatal(
			"ApplyLiquidityChange() expected over-burn error",
		)
	}
}

func liquidityChangeTestPool() *domain.ReconstructedPool {
	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	return &domain.ReconstructedPool{
		PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

		BlockNumber: 100,

		SqrtPriceX96: q96,

		CurrentTick: 0,

		Liquidity: big.NewInt(1_000),

		Ticks: map[int]*domain.TickState{
			-100: {
				Index: -100,

				LiquidityGross: big.NewInt(1_000),

				LiquidityNet: big.NewInt(1_000),
			},

			100: {
				Index: 100,

				LiquidityGross: big.NewInt(1_000),

				LiquidityNet: big.NewInt(-1_000),
			},
		},

		InitializedTicks: []int{
			-100,
			100,
		},
	}
}
