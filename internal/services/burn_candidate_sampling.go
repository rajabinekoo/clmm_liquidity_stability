package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/repositories"
)

type burnCandidateDiscovery struct {
	Candidates []domain.BurnCandidate

	Pages          int
	IndexedThrough uint64
}

type burnCandidateSamplingDiagnostics struct {
	BinCount         int
	NonEmptyBinCount int
}

type scoredBurnCandidate struct {
	Candidate domain.BurnCandidate
	Score     [sha256.Size]byte
}

func (s *BurnSampleCollector) discoverBurnCandidates(
	ctx context.Context,
	req BurnSampleCollectionRequest,
) (burnCandidateDiscovery, error) {
	result := burnCandidateDiscovery{
		Candidates: make(
			[]domain.BurnCandidate,
			0,
			req.MaxCandidates,
		),
	}

	var (
		after         *domain.EventCursor
		previousEvent *domain.EventCursor
	)

	for {
		if err := ctx.Err(); err != nil {
			return burnCandidateDiscovery{}, err
		}

		remainingCandidates :=
			req.MaxCandidates -
				len(result.Candidates)

		if remainingCandidates <= 0 {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: reached max candidates %d before completing block range [%d,%d]; increase BURN_MAX_CANDIDATES",
				req.MaxCandidates,
				req.FromBlock,
				req.ToBlock,
			)
		}

		pageLimit := req.PageSize

		if remainingCandidates < pageLimit {
			pageLimit = remainingCandidates
		}

		page, err :=
			s.candidateRepository.LoadBurnCandidatesPage(
				ctx,
				repositories.BurnCandidatePageRequest{
					PoolAddress: req.PoolAddress,

					FromBlock: req.FromBlock,
					ToBlock:   req.ToBlock,

					After: after,

					Limit: pageLimit,
				},
			)
		if err != nil {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: load page: %w",
				err,
			)
		}

		if err := validateBurnCandidatePage(
			page,
			req,
		); err != nil {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: invalid page: %w",
				err,
			)
		}

		if err := validateBurnCandidatePageOrdering(
			page.Candidates,
			req,
			previousEvent,
		); err != nil {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: invalid ordering on page %d: %w",
				result.Pages+1,
				err,
			)
		}

		result.Pages++

		if result.Pages == 1 {
			result.IndexedThrough =
				page.IndexedThrough
		} else if page.IndexedThrough !=
			result.IndexedThrough {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: checkpoint changed during discovery: previous=%d current=%d",
				result.IndexedThrough,
				page.IndexedThrough,
			)
		}

		if len(page.Candidates) == 0 {
			if page.NextCursor != nil {
				return burnCandidateDiscovery{}, fmt.Errorf(
					"discover burn candidates: empty page has next cursor %s",
					page.NextCursor,
				)
			}

			break
		}

		for _, candidate := range page.Candidates {
			result.Candidates = append(
				result.Candidates,
				cloneBurnCandidate(candidate),
			)

			cursorCopy := candidate.Cursor

			previousEvent =
				&cursorCopy
		}

		if page.NextCursor == nil {
			break
		}

		lastCandidateCursor :=
			page.Candidates[len(page.Candidates)-1].Cursor

		if !page.NextCursor.Equal(
			lastCandidateCursor,
		) {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: next cursor %s does not match page tail %s",
				page.NextCursor,
				lastCandidateCursor,
			)
		}

		if len(result.Candidates) >=
			req.MaxCandidates {
			return burnCandidateDiscovery{}, fmt.Errorf(
				"discover burn candidates: reached max candidates %d while another page exists; increase BURN_MAX_CANDIDATES",
				req.MaxCandidates,
			)
		}

		nextCursorCopy :=
			*page.NextCursor

		after =
			&nextCursorCopy
	}

	return result, nil
}

func buildBurnCandidateSamplingOrder(
	candidates []domain.BurnCandidate,
	fromBlock uint64,
	toBlock uint64,
	binCount int,
	seed uint64,
) (
	[]domain.BurnCandidate,
	burnCandidateSamplingDiagnostics,
	error,
) {
	if len(candidates) == 0 {
		return []domain.BurnCandidate{},
			burnCandidateSamplingDiagnostics{
				BinCount: binCount,
			},
			nil
	}

	// Zero preserves the former chronological behavior.
	// This is useful for backward-compatible tests.
	if binCount == 0 {
		ordered := make(
			[]domain.BurnCandidate,
			len(candidates),
		)

		for index, candidate := range candidates {
			ordered[index] =
				cloneBurnCandidate(
					candidate,
				)
		}

		return ordered,
			burnCandidateSamplingDiagnostics{},
			nil
	}

	if binCount < 0 {
		return nil,
			burnCandidateSamplingDiagnostics{},
			fmt.Errorf(
				"build burn candidate sampling order: bin count %d must not be negative",
				binCount,
			)
	}

	if fromBlock == 0 ||
		toBlock == 0 ||
		fromBlock > toBlock {
		return nil,
			burnCandidateSamplingDiagnostics{},
			fmt.Errorf(
				"build burn candidate sampling order: invalid block range [%d,%d]",
				fromBlock,
				toBlock,
			)
	}

	bins := make(
		[][]scoredBurnCandidate,
		binCount,
	)

	for index, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return nil,
				burnCandidateSamplingDiagnostics{},
				fmt.Errorf(
					"build burn candidate sampling order: candidate %d: %w",
					index,
					err,
				)
		}

		binIndex, err :=
			burnSamplingBinIndex(
				candidate.Cursor.BlockNumber,
				fromBlock,
				toBlock,
				binCount,
			)
		if err != nil {
			return nil,
				burnCandidateSamplingDiagnostics{},
				fmt.Errorf(
					"build burn candidate sampling order: candidate %s: %w",
					candidate.EventKey(),
					err,
				)
		}

		bins[binIndex] = append(
			bins[binIndex],
			scoredBurnCandidate{
				Candidate: cloneBurnCandidate(
					candidate,
				),

				Score: burnCandidateSamplingScore(
					candidate,
					seed,
				),
			},
		)
	}

	nonEmptyBins := 0

	for binIndex := range bins {
		if len(bins[binIndex]) == 0 {
			continue
		}

		nonEmptyBins++

		sort.Slice(
			bins[binIndex],
			func(
				left int,
				right int,
			) bool {
				comparison :=
					bytes.Compare(
						bins[binIndex][left].
							Score[:],
						bins[binIndex][right].
							Score[:],
					)

				if comparison != 0 {
					return comparison < 0
				}

				return bins[binIndex][left].
					Candidate.
					Cursor.
					Before(
						bins[binIndex][right].
							Candidate.
							Cursor,
					)
			},
		)
	}

	ordered := make(
		[]domain.BurnCandidate,
		0,
		len(candidates),
	)

	// Round-robin selection:
	//
	// round 0 -> one candidate from every non-empty bin
	// round 1 -> second candidate from every non-empty bin
	// ...
	for round := 0; ; round++ {
		added := false

		for binIndex := range bins {
			if round >=
				len(bins[binIndex]) {
				continue
			}

			ordered = append(
				ordered,
				cloneBurnCandidate(
					bins[binIndex][round].
						Candidate,
				),
			)

			added = true
		}

		if !added {
			break
		}
	}

	if len(ordered) != len(candidates) {
		return nil,
			burnCandidateSamplingDiagnostics{},
			fmt.Errorf(
				"build burn candidate sampling order: ordered candidates=%d, input candidates=%d",
				len(ordered),
				len(candidates),
			)
	}

	return ordered,
		burnCandidateSamplingDiagnostics{
			BinCount: binCount,

			NonEmptyBinCount: nonEmptyBins,
		},
		nil
}

func burnSamplingBinIndex(
	blockNumber uint64,
	fromBlock uint64,
	toBlock uint64,
	binCount int,
) (int, error) {
	if binCount <= 0 {
		return 0, fmt.Errorf(
			"sampling bin count %d must be positive",
			binCount,
		)
	}

	if blockNumber < fromBlock ||
		blockNumber > toBlock {
		return 0, fmt.Errorf(
			"block %d is outside sampling range [%d,%d]",
			blockNumber,
			fromBlock,
			toBlock,
		)
	}

	// big.Int prevents overflow if a very large block interval
	// is multiplied by the number of bins.
	span := new(big.Int).SetUint64(
		toBlock - fromBlock,
	)

	span.Add(
		span,
		big.NewInt(1),
	)

	scaledOffset :=
		new(big.Int).SetUint64(
			blockNumber - fromBlock,
		)

	scaledOffset.Mul(
		scaledOffset,
		big.NewInt(
			int64(binCount),
		),
	)

	scaledOffset.Quo(
		scaledOffset,
		span,
	)

	binIndex :=
		int(
			scaledOffset.Int64(),
		)

	if binIndex >= binCount {
		binIndex =
			binCount - 1
	}

	return binIndex, nil
}

func burnCandidateSamplingScore(
	candidate domain.BurnCandidate,
	seed uint64,
) [sha256.Size]byte {
	var seedBytes [8]byte

	binary.BigEndian.PutUint64(
		seedBytes[:],
		seed,
	)

	hashInput := make(
		[]byte,
		0,
		len(seedBytes)+
			len(candidate.EventKey()),
	)

	hashInput = append(
		hashInput,
		seedBytes[:]...,
	)

	hashInput = append(
		hashInput,
		candidate.EventKey()...,
	)

	return sha256.Sum256(
		hashInput,
	)
}

func sortBurnCollectionObservations(
	report *BurnSampleCollectionReport,
) {
	if report == nil {
		return
	}

	sort.Slice(
		report.Samples,
		func(
			left int,
			right int,
		) bool {
			return report.
				Samples[left].
				Burn.
				Cursor.
				Before(
					report.
						Samples[right].
						Burn.
						Cursor,
				)
		},
	)

	sort.Slice(
		report.Skipped,
		func(
			left int,
			right int,
		) bool {
			return report.
				Skipped[left].
				Burn.
				Cursor.
				Before(
					report.
						Skipped[right].
						Burn.
						Cursor,
				)
		},
	)
}
