package services

import (
	"encoding/csv"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestWriteSnapshotBatchJointRemovalCSV(
	t *testing.T,
) {
	t.Parallel()

	thresholds :=
		[]decimal.Decimal{
			decimal.NewFromInt(10),
		}

	results :=
		[]SnapshotBatchDetailedResult{
			snapshotJointRemovalFixture(
				1,
				100,
				thresholds,
			),

			snapshotJointRemovalFixture(
				2,
				200,
				thresholds,
			),
		}

	path :=
		filepath.Join(
			t.TempDir(),
			"snapshot_joint_removal.csv",
		)

	if err :=
		WriteSnapshotBatchJointRemovalCSV(
			path,
			results,
		); err != nil {
		t.Fatalf(
			"WriteSnapshotBatchJointRemovalCSV() error = %v",
			err,
		)
	}

	file, err :=
		os.Open(
			path,
		)
	if err != nil {
		t.Fatalf(
			"open CSV: %v",
			err,
		)
	}
	defer file.Close()

	records, err :=
		csv.NewReader(
			file,
		).ReadAll()
	if err != nil {
		t.Fatalf(
			"read CSV: %v",
			err,
		)
	}

	// One header plus two scenarios for each of two snapshots.
	if len(records) != 5 {
		t.Fatalf(
			"record count = %d, want 5",
			len(records),
		)
	}

	if records[0][0] !=
		"snapshot_index" {
		t.Fatalf(
			"first header = %q, want snapshot_index",
			records[0][0],
		)
	}

	if records[1][0] != "1" ||
		records[3][0] != "2" {
		t.Fatalf(
			"snapshot indexes = %q and %q, want 1 and 2",
			records[1][0],
			records[3][0],
		)
	}

	if records[1][1] !=
		"top_1_by_lsis" {
		t.Fatalf(
			"first scenario ID = %q, want top_1_by_lsis",
			records[1][1],
		)
	}
}

func snapshotJointRemovalFixture(
	snapshotIndex int,
	blockNumber uint64,
	thresholds []decimal.Decimal,
) SnapshotBatchDetailedResult {
	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	pool :=
		&domain.ReconstructedPool{
			PoolAddress: poolAddress,

			BlockNumber: blockNumber,

			SqrtPriceX96: big.NewInt(1),

			CurrentTick: 100,

			Liquidity: big.NewInt(1_000),

			Ticks: map[int]*domain.TickState{},
		}

	scenarios :=
		[]JointRemovalScenarioResult{
			{
				ScenarioID: "top_1_by_lsis",

				Kind: JointRemovalScenarioTopNByLSIS,

				RequestedPositionCount: 1,

				RemovedPositionCount: 1,

				RemovedLiquidity: big.NewInt(100),

				RemovedActiveLiquidityShare: decimal.RequireFromString(
					"0.1",
				),

				CounterfactualActiveLiquidity: big.NewInt(900),

				PositionKeys: []string{
					"position-1",
				},
			},

			{
				ScenarioID: "top_3_by_lsis",

				Kind: JointRemovalScenarioTopNByLSIS,

				RequestedPositionCount: 3,

				RemovedPositionCount: 3,

				RemovedLiquidity: big.NewInt(300),

				RemovedActiveLiquidityShare: decimal.RequireFromString(
					"0.3",
				),

				CounterfactualActiveLiquidity: big.NewInt(700),

				PositionKeys: []string{
					"position-1",
					"position-2",
					"position-3",
				},
			},
		}

	return SnapshotBatchDetailedResult{
		Summary: SnapshotBatchResult{
			SnapshotIndex: snapshotIndex,

			PoolAddress: poolAddress,

			BlockNumber: blockNumber,

			CurrentTick: 100,
		},

		Pool: pool,

		JointRemoval: &JointRemovalReport{
			PoolAddress: poolAddress,

			BlockNumber: blockNumber,

			CurrentTick: 100,

			ThresholdsBps: append(
				[]decimal.Decimal(nil),
				thresholds...,
			),

			Scenarios: scenarios,
		},
	}
}
