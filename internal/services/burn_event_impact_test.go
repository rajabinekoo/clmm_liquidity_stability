package services

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

type fakeBurnEventImpactRepository struct {
	rangeLiquidity *big.Int

	calls int
}

func (f *fakeBurnEventImpactRepository) LoadRangeLiquidityBeforeCursor(
	_ context.Context,
	_ string,
	_ int,
	_ int,
	_ domain.EventCursor,
) (*big.Int, error) {
	f.calls++

	return new(big.Int).Set(
		f.rangeLiquidity,
	), nil
}

func TestBurnEventImpactAnalyzeActiveBurn(
	t *testing.T,
) {
	t.Parallel()

	const (
		poolLiquidity int64 = 1_000_000_000_000

		removedLiquidity int64 = 100_000_000_000
	)

	preBurn :=
		burnImpactPreBurnFixture(
			poolLiquidity,
			removedLiquidity,
		)

	repository :=
		&fakeBurnEventImpactRepository{
			rangeLiquidity: big.NewInt(
				poolLiquidity,
			),
		}

	simulator, err :=
		uniswapv3.NewSimulator(
			500,
		)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	service :=
		NewBurnEventImpactService(
			repository,
			NewPriceImpactCurveService(
				simulator,
			),
		)

	result, err :=
		service.Analyze(
			context.Background(),
			BurnEventImpactRequest{
				PreBurn: preBurn,

				ZeroForOneAmountsIn: burnImpactAmountGrid(),

				OneForZeroAmountsIn: burnImpactAmountGrid(),

				ThresholdsBps: []decimal.Decimal{
					decimal.NewFromInt(1),
					decimal.NewFromInt(10),
					decimal.NewFromInt(50),
				},
			},
		)
	if err != nil {
		t.Fatalf(
			"Analyze() error = %v",
			err,
		)
	}

	if repository.calls != 1 {
		t.Fatalf(
			"repository calls = %d, want 1",
			repository.calls,
		)
	}

	if !result.BurnRangeActive {
		t.Fatal(
			"BurnRangeActive = false, want true",
		)
	}

	if result.RangeLocation !=
		BurnRangeActive {
		t.Fatalf(
			"RangeLocation = %q, want %q",
			result.RangeLocation,
			BurnRangeActive,
		)
	}

	if result.RangeWidth != 200 {
		t.Fatalf(
			"RangeWidth = %d, want 200",
			result.RangeWidth,
		)
	}

	if result.DistanceToLowerTick != 100 ||
		result.DistanceToUpperTick != 100 ||
		result.DistanceToNearestBoundary != 100 {
		t.Fatalf(
			"unexpected distances: lower=%d upper=%d nearest=%d",
			result.DistanceToLowerTick,
			result.DistanceToUpperTick,
			result.DistanceToNearestBoundary,
		)
	}

	assertBurnImpactBigIntEqual(
		t,
		result.RangeLiquidityBeforeBurn,
		big.NewInt(
			poolLiquidity,
		),
	)

	assertBurnImpactBigIntEqual(
		t,
		result.RangeLiquidityAfterBurn,
		big.NewInt(
			poolLiquidity-
				removedLiquidity,
		),
	)

	assertBurnImpactBigIntEqual(
		t,
		result.ActiveLiquidityBeforeBurn,
		big.NewInt(
			poolLiquidity,
		),
	)

	assertBurnImpactBigIntEqual(
		t,
		result.ActiveLiquidityAfterBurn,
		big.NewInt(
			poolLiquidity-
				removedLiquidity,
		),
	)

	assertBurnImpactBigIntEqual(
		t,
		result.ActiveLiquidityRemoved,
		big.NewInt(
			removedLiquidity,
		),
	)

	expectedTenPercent :=
		decimal.RequireFromString(
			"0.1",
		)

	assertBurnImpactDecimalEqual(
		t,
		result.RemovalFraction,
		expectedTenPercent,
	)

	assertBurnImpactDecimalEqual(
		t,
		result.RangeActiveLiquidityShare,
		decimal.NewFromInt(1),
	)

	assertBurnImpactDecimalEqual(
		t,
		result.ActiveRemovalShare,
		expectedTenPercent,
	)

	if !result.TotalLSISBps.
		GreaterThan(
			decimal.Zero,
		) {
		t.Fatalf(
			"TotalLSISBps = %s, want positive",
			result.TotalLSISBps,
		)
	}

	if !result.MaxDirectionalLSISBps.
		GreaterThan(
			decimal.Zero,
		) {
		t.Fatalf(
			"MaxDirectionalLSISBps = %s, want positive",
			result.MaxDirectionalLSISBps,
		)
	}

	// Applying the event must never mutate the exact pre-burn pool.
	assertBurnImpactBigIntEqual(
		t,
		preBurn.Pool.Liquidity,
		big.NewInt(
			poolLiquidity,
		),
	)
}

func TestBurnEventImpactRejectsBurnLargerThanExactRangeAggregate(
	t *testing.T,
) {
	t.Parallel()

	preBurn :=
		burnImpactPreBurnFixture(
			1_000_000_000_000,
			100_000_000_000,
		)

	repository :=
		&fakeBurnEventImpactRepository{
			rangeLiquidity: big.NewInt(
				50_000_000_000,
			),
		}

	simulator, err :=
		uniswapv3.NewSimulator(
			500,
		)
	if err != nil {
		t.Fatalf(
			"NewSimulator() error = %v",
			err,
		)
	}

	service :=
		NewBurnEventImpactService(
			repository,
			NewPriceImpactCurveService(
				simulator,
			),
		)

	_, err =
		service.Analyze(
			context.Background(),
			BurnEventImpactRequest{
				PreBurn: preBurn,

				ZeroForOneAmountsIn: burnImpactAmountGrid(),

				OneForZeroAmountsIn: burnImpactAmountGrid(),

				ThresholdsBps: []decimal.Decimal{
					decimal.NewFromInt(10),
				},
			},
		)

	if err == nil {
		t.Fatal(
			"Analyze() expected range-liquidity error",
		)
	}
}

func TestBurnRangeLocationAtTick(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name            string
		currentTick     int
		expected        BurnRangeLocation
		expectedOutside int
	}{
		{
			name: "below",

			currentTick: -150,

			expected: BurnRangeBelowCurrentTick,

			expectedOutside: 50,
		},
		{
			name: "lower boundary active",

			currentTick: -100,

			expected: BurnRangeActive,

			expectedOutside: 0,
		},
		{
			name: "inside",

			currentTick: 0,

			expected: BurnRangeActive,

			expectedOutside: 0,
		},
		{
			name: "upper boundary inactive",

			currentTick: 100,

			expected: BurnRangeAboveCurrentTick,

			expectedOutside: 0,
		},
		{
			name: "above",

			currentTick: 150,

			expected: BurnRangeAboveCurrentTick,

			expectedOutside: 50,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				location, outside :=
					burnRangeLocationAtTick(
						testCase.currentTick,
						-100,
						100,
					)

				if location !=
					testCase.expected {
					t.Fatalf(
						"location = %q, want %q",
						location,
						testCase.expected,
					)
				}

				if outside !=
					testCase.expectedOutside {
					t.Fatalf(
						"outside = %d, want %d",
						outside,
						testCase.expectedOutside,
					)
				}
			},
		)
	}
}

func burnImpactPreBurnFixture(
	poolLiquidity int64,
	removedLiquidity int64,
) PreBurnStateResult {
	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	q96 :=
		new(big.Int).Lsh(
			big.NewInt(1),
			96,
		)

	liquidity :=
		big.NewInt(
			poolLiquidity,
		)

	burn :=
		domain.BurnCandidate{
			ID: "burn-event",

			PoolAddress: poolAddress,

			TxHash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",

			Cursor: domain.EventCursor{
				BlockNumber: 101,

				LogIndex: 20,
			},

			Timestamp: time.Unix(
				1_700_000_000,
				0,
			).UTC(),

			TickLower: -100,

			TickUpper: 100,

			LiquidityRemoved: big.NewInt(
				removedLiquidity,
			),
		}

	pool :=
		&domain.ReconstructedPool{
			PoolAddress: poolAddress,

			BlockNumber: 101,

			SqrtPriceX96: q96,

			CurrentTick: 0,

			Liquidity: new(big.Int).Set(
				liquidity,
			),

			Ticks: map[int]*domain.TickState{
				-100: {
					Index: -100,

					LiquidityGross: new(big.Int).Set(
						liquidity,
					),

					LiquidityNet: new(big.Int).Set(
						liquidity,
					),
				},

				100: {
					Index: 100,

					LiquidityGross: new(big.Int).Set(
						liquidity,
					),

					LiquidityNet: new(big.Int).Neg(
						new(big.Int).Set(
							liquidity,
						),
					),
				},
			},

			InitializedTicks: []int{
				-100,
				100,
			},
		}

	return PreBurnStateResult{
		Burn: burn,

		Pool: pool,

		SnapshotBlock: 100,

		BurnRangeActive: true,

		ActiveLiquidityBeforeBurn: new(big.Int).Set(
			liquidity,
		),
	}
}

func burnImpactAmountGrid() []*big.Int {
	return []*big.Int{
		big.NewInt(
			100_000_000,
		),
		big.NewInt(
			500_000_000,
		),
		big.NewInt(
			2_000_000_000,
		),
	}
}

func assertBurnImpactBigIntEqual(
	t *testing.T,
	actual *big.Int,
	expected *big.Int,
) {
	t.Helper()

	if actual == nil ||
		expected == nil ||
		actual.Cmp(expected) != 0 {
		t.Fatalf(
			"actual = %v, want %v",
			actual,
			expected,
		)
	}
}

func assertBurnImpactDecimalEqual(
	t *testing.T,
	actual decimal.Decimal,
	expected decimal.Decimal,
) {
	t.Helper()

	if !actual.Equal(expected) {
		t.Fatalf(
			"actual = %s, want %s",
			actual,
			expected,
		)
	}
}
