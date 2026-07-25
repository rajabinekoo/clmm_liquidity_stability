package services

import (
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

type burnSampleSpacingConflict struct {
	EventKey string

	BlockNumber uint64
	Distance    uint64
}

func findBurnSampleSpacingConflict(
	candidate domain.BurnCandidate,
	samples []BurnEventSample,
	minimumSpacingBlocks uint64,
) *burnSampleSpacingConflict {
	if minimumSpacingBlocks == 0 {
		return nil
	}

	var closest *burnSampleSpacingConflict

	for _, sample := range samples {
		distance := burnBlockDistance(
			candidate.Cursor.BlockNumber,
			sample.Burn.Cursor.BlockNumber,
		)

		if distance >= minimumSpacingBlocks {
			continue
		}

		conflict :=
			burnSampleSpacingConflict{
				EventKey: sample.Burn.EventKey(),

				BlockNumber: sample.
					Burn.
					Cursor.
					BlockNumber,

				Distance: distance,
			}

		if closest == nil ||
			conflict.Distance < closest.Distance {
			closest = &conflict
		}
	}

	return closest
}

func burnBlockDistance(
	left uint64,
	right uint64,
) uint64 {
	if left >= right {
		return left - right
	}

	return right - left
}
