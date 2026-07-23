package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const (
	maxBurnCandidatePageSize = 1_000

	maxPostgresBigInt = uint64(1<<63 - 1)
)

type BurnCandidatePageRequest struct {
	PoolAddress string

	FromBlock uint64
	ToBlock   uint64

	After *domain.EventCursor

	Limit int
}

type BurnCandidatePage struct {
	PoolAddress string

	FromBlock uint64
	ToBlock   uint64

	IndexedThrough uint64

	Candidates []domain.BurnCandidate

	// NextCursor is non-nil only when another page exists.
	NextCursor *domain.EventCursor
}

// LoadBurnCandidatesPage loads actual Pool Burn events in deterministic
// blockchain order.
//
// The local LP-action checkpoint must cover the complete requested interval.
// Returning candidates from a partially indexed interval is prohibited.
func (r *PoolStateRepository) LoadBurnCandidatesPage(
	ctx context.Context,
	req BurnCandidatePageRequest,
) (BurnCandidatePage, error) {
	if r == nil ||
		r.db == nil {
		return BurnCandidatePage{}, fmt.Errorf(
			"load burn candidates: repository database is nil",
		)
	}

	req, err :=
		normalizeBurnCandidatePageRequest(
			req,
		)
	if err != nil {
		return BurnCandidatePage{}, err
	}

	tx, err := r.db.BeginTx(
		ctx,
		&sql.TxOptions{
			ReadOnly:  true,
			Isolation: sql.LevelRepeatableRead,
		},
	)
	if err != nil {
		return BurnCandidatePage{}, fmt.Errorf(
			"load burn candidates: begin transaction: %w",
			err,
		)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	indexedThrough, err :=
		loadBurnCandidateCheckpoint(
			ctx,
			tx,
			req.PoolAddress,
		)
	if err != nil {
		return BurnCandidatePage{}, err
	}

	if indexedThrough <
		req.ToBlock {
		return BurnCandidatePage{}, fmt.Errorf(
			"load burn candidates: LP-action checkpoint %d is before requested to-block %d",
			indexedThrough,
			req.ToBlock,
		)
	}

	candidates, err :=
		queryBurnCandidates(
			ctx,
			tx,
			req,
		)
	if err != nil {
		return BurnCandidatePage{}, err
	}

	var nextCursor *domain.EventCursor

	if len(candidates) >
		req.Limit {
		candidates =
			candidates[:req.Limit]

		lastCursor :=
			candidates[len(candidates)-1].Cursor

		nextCursor =
			&domain.EventCursor{
				BlockNumber: lastCursor.BlockNumber,

				LogIndex: lastCursor.LogIndex,
			}
	}

	if err := tx.Commit(); err != nil {
		return BurnCandidatePage{}, fmt.Errorf(
			"load burn candidates: commit transaction: %w",
			err,
		)
	}

	committed = true

	return BurnCandidatePage{
		PoolAddress: req.PoolAddress,

		FromBlock: req.FromBlock,

		ToBlock: req.ToBlock,

		IndexedThrough: indexedThrough,

		Candidates: candidates,

		NextCursor: nextCursor,
	}, nil
}

func normalizeBurnCandidatePageRequest(
	req BurnCandidatePageRequest,
) (BurnCandidatePageRequest, error) {
	req.PoolAddress =
		strings.ToLower(
			strings.TrimSpace(
				req.PoolAddress,
			),
		)

	if req.PoolAddress == "" {
		return BurnCandidatePageRequest{}, fmt.Errorf(
			"load burn candidates: pool address is required",
		)
	}

	if req.FromBlock == 0 ||
		req.ToBlock == 0 ||
		req.FromBlock >
			req.ToBlock {
		return BurnCandidatePageRequest{}, fmt.Errorf(
			"load burn candidates: invalid block range [%d,%d]",
			req.FromBlock,
			req.ToBlock,
		)
	}

	if req.FromBlock >
		maxPostgresBigInt ||
		req.ToBlock >
			maxPostgresBigInt {
		return BurnCandidatePageRequest{}, fmt.Errorf(
			"load burn candidates: block range exceeds PostgreSQL BIGINT",
		)
	}

	if req.Limit <= 0 ||
		req.Limit >
			maxBurnCandidatePageSize {
		return BurnCandidatePageRequest{}, fmt.Errorf(
			"load burn candidates: limit %d must be inside [1,%d]",
			req.Limit,
			maxBurnCandidatePageSize,
		)
	}

	if req.After != nil {
		if err :=
			req.After.Validate(); err != nil {
			return BurnCandidatePageRequest{}, fmt.Errorf(
				"load burn candidates: invalid after cursor: %w",
				err,
			)
		}

		if req.After.BlockNumber <
			req.FromBlock ||
			req.After.BlockNumber >
				req.ToBlock {
			return BurnCandidatePageRequest{}, fmt.Errorf(
				"load burn candidates: after cursor %s is outside block range [%d,%d]",
				req.After,
				req.FromBlock,
				req.ToBlock,
			)
		}

		cursorCopy :=
			*req.After

		req.After =
			&cursorCopy
	}

	return req, nil
}

func loadBurnCandidateCheckpoint(
	ctx context.Context,
	tx *sql.Tx,
	poolAddress string,
) (uint64, error) {
	var indexedThrough int64

	err := tx.QueryRowContext(
		ctx,
		`
			SELECT last_completed_block
			FROM indexer_checkpoints
			WHERE pool_address = $1
		`,
		poolAddress,
	).Scan(
		&indexedThrough,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"load burn candidates: read checkpoint for pool %s: %w",
			poolAddress,
			err,
		)
	}

	if indexedThrough < 0 {
		return 0, fmt.Errorf(
			"load burn candidates: negative checkpoint %d for pool %s",
			indexedThrough,
			poolAddress,
		)
	}

	return uint64(
		indexedThrough,
	), nil
}

func queryBurnCandidates(
	ctx context.Context,
	tx *sql.Tx,
	req BurnCandidatePageRequest,
) ([]domain.BurnCandidate, error) {
	query := `
		SELECT
			id,
			tx_hash,
			block_number,
			log_index,
			timestamp,
			tick_lower,
			tick_upper,
			(-liquidity_delta)::text
		FROM lp_actions
		WHERE pool_address = $1
		  AND action = $2
		  AND block_number >= $3
		  AND block_number <= $4
		  AND liquidity_delta < 0
	`

	args := []any{
		req.PoolAddress,
		int16(
			domain.LPActionBurn,
		),
		int64(
			req.FromBlock,
		),
		int64(
			req.ToBlock,
		),
	}

	if req.After != nil {
		query += `
		  AND (
				block_number > $5
				OR (
					block_number = $5
					AND log_index > $6
				)
		  )
		`

		args = append(
			args,
			int64(
				req.After.BlockNumber,
			),
			req.After.LogIndex,
		)
	}

	query += `
		ORDER BY
			block_number ASC,
			log_index ASC
	`

	limitParameter :=
		len(args) + 1

	query += fmt.Sprintf(
		" LIMIT $%d",
		limitParameter,
	)

	args = append(
		args,
		req.Limit+1,
	)

	rows, err := tx.QueryContext(
		ctx,
		query,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load burn candidates: query: %w",
			err,
		)
	}
	defer rows.Close()

	candidates := make(
		[]domain.BurnCandidate,
		0,
		req.Limit+1,
	)

	for rows.Next() {
		var (
			candidate domain.BurnCandidate

			blockNumber int64

			removedLiquidity string
		)

		if err := rows.Scan(
			&candidate.ID,
			&candidate.TxHash,
			&blockNumber,
			&candidate.Cursor.LogIndex,
			&candidate.Timestamp,
			&candidate.TickLower,
			&candidate.TickUpper,
			&removedLiquidity,
		); err != nil {
			return nil, fmt.Errorf(
				"load burn candidates: scan: %w",
				err,
			)
		}

		if blockNumber <= 0 {
			return nil, fmt.Errorf(
				"load burn candidates: candidate %s has invalid block number %d",
				candidate.ID,
				blockNumber,
			)
		}

		candidate.PoolAddress =
			req.PoolAddress

		candidate.TxHash =
			strings.ToLower(
				strings.TrimSpace(
					candidate.TxHash,
				),
			)

		candidate.Cursor.BlockNumber =
			uint64(blockNumber)

		liquidity, ok :=
			new(big.Int).SetString(
				removedLiquidity,
				10,
			)
		if !ok {
			return nil, fmt.Errorf(
				"load burn candidates: candidate %s has invalid removed liquidity %q",
				candidate.ID,
				removedLiquidity,
			)
		}

		candidate.LiquidityRemoved =
			liquidity

		if err := candidate.Validate(); err != nil {
			return nil, fmt.Errorf(
				"load burn candidates: candidate %s: %w",
				candidate.ID,
				err,
			)
		}

		candidates = append(
			candidates,
			candidate,
		)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"load burn candidates: iterate: %w",
			err,
		)
	}

	return candidates, nil
}
