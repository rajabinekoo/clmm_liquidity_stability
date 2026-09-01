package services

import (
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"
)

func WriteSnapshotBatchJointRemovalCSV(
	path string,
	results []SnapshotBatchDetailedResult,
) error {
	if len(results) == 0 {
		return fmt.Errorf(
			"write snapshot batch joint-removal CSV: results are empty",
		)
	}

	var (
		referenceThresholds  []decimal.Decimal
		referenceScenarioIDs []string
	)

	rows := make(
		[][]string,
		0,
	)

	for resultIndex, result := range results {
		if result.Summary.SnapshotIndex <= 0 {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d has invalid snapshot index %d",
				resultIndex,
				result.Summary.SnapshotIndex,
			)
		}

		if result.Pool == nil {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d has nil pool",
				resultIndex,
			)
		}

		if result.JointRemoval == nil {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d has nil joint-removal report",
				resultIndex,
			)
		}

		if normalizeAddress(
			result.JointRemoval.PoolAddress,
		) != normalizeAddress(
			result.Pool.PoolAddress,
		) {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d pool mismatch: report=%s pool=%s",
				resultIndex,
				result.JointRemoval.PoolAddress,
				result.Pool.PoolAddress,
			)
		}

		if result.JointRemoval.BlockNumber !=
			result.Pool.BlockNumber {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d block mismatch: report=%d pool=%d",
				resultIndex,
				result.JointRemoval.BlockNumber,
				result.Pool.BlockNumber,
			)
		}

		if result.JointRemoval.CurrentTick !=
			result.Pool.CurrentTick {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d tick mismatch: report=%d pool=%d",
				resultIndex,
				result.JointRemoval.CurrentTick,
				result.Pool.CurrentTick,
			)
		}

		if len(
			result.JointRemoval.ThresholdsBps,
		) == 0 {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d has no thresholds",
				resultIndex,
			)
		}

		scenarioIDs, err :=
			snapshotJointRemovalScenarioIDs(
				result.JointRemoval,
			)
		if err != nil {
			return fmt.Errorf(
				"write snapshot batch joint-removal CSV: result %d: %w",
				resultIndex,
				err,
			)
		}

		if resultIndex == 0 {
			referenceThresholds =
				append(
					[]decimal.Decimal(nil),
					result.
						JointRemoval.
						ThresholdsBps...,
				)

			referenceScenarioIDs =
				append(
					[]string(nil),
					scenarioIDs...,
				)
		} else {
			if !jointRemovalThresholdSequenceEqual(
				referenceThresholds,
				result.
					JointRemoval.
					ThresholdsBps,
			) {
				return fmt.Errorf(
					"write snapshot batch joint-removal CSV: result %d uses different thresholds",
					resultIndex,
				)
			}

			if !jointRemovalStringSequenceEqual(
				referenceScenarioIDs,
				scenarioIDs,
			) {
				return fmt.Errorf(
					"write snapshot batch joint-removal CSV: result %d uses different scenario IDs or ordering",
					resultIndex,
				)
			}
		}

		for _, scenario := range result.
			JointRemoval.
			Scenarios {
			baseRow :=
				jointRemovalImpactCSVRow(
					result.Pool,
					scenario,
					referenceThresholds,
				)

			row := make(
				[]string,
				0,
				len(baseRow)+1,
			)

			row =
				append(
					row,
					strconv.Itoa(
						result.
							Summary.
							SnapshotIndex,
					),
				)

			row =
				append(
					row,
					baseRow...,
				)

			rows =
				append(
					rows,
					row,
				)
		}
	}

	baseHeader :=
		jointRemovalImpactCSVHeader(
			referenceThresholds,
		)

	header := make(
		[]string,
		0,
		len(baseHeader)+1,
	)

	header =
		append(
			header,
			"snapshot_index",
		)

	header =
		append(
			header,
			baseHeader...,
		)

	if err :=
		writeCSVAtomically(
			path,
			header,
			rows,
		); err != nil {
		return fmt.Errorf(
			"write snapshot batch joint-removal CSV: %w",
			err,
		)
	}

	return nil
}

func snapshotJointRemovalScenarioIDs(
	report *JointRemovalReport,
) ([]string, error) {
	if report == nil {
		return nil, fmt.Errorf(
			"joint-removal report is nil",
		)
	}

	if len(report.Scenarios) == 0 {
		return nil, fmt.Errorf(
			"joint-removal report contains no scenarios",
		)
	}

	seen := make(
		map[string]struct{},
		len(report.Scenarios),
	)

	ids := make(
		[]string,
		0,
		len(report.Scenarios),
	)

	for index, scenario := range report.Scenarios {
		if scenario.ScenarioID == "" {
			return nil, fmt.Errorf(
				"scenario %d has empty ID",
				index,
			)
		}

		if _, exists :=
			seen[scenario.ScenarioID]; exists {
			return nil, fmt.Errorf(
				"duplicate scenario ID %q",
				scenario.ScenarioID,
			)
		}

		seen[scenario.ScenarioID] = struct{}{}

		ids =
			append(
				ids,
				scenario.ScenarioID,
			)
	}

	return ids, nil
}

func jointRemovalThresholdSequenceEqual(
	left []decimal.Decimal,
	right []decimal.Decimal,
) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if !left[index].Equal(
			right[index],
		) {
			return false
		}
	}

	return true
}

func jointRemovalStringSequenceEqual(
	left []string,
	right []string,
) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}
