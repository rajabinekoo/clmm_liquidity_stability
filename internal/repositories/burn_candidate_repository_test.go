package repositories

import (
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestNormalizeBurnCandidatePageRequest(
	t *testing.T,
) {
	t.Parallel()

	after :=
		domain.EventCursor{
			BlockNumber: 150,

			LogIndex: 12,
		}

	req, err :=
		normalizeBurnCandidatePageRequest(
			BurnCandidatePageRequest{
				PoolAddress: " 0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA ",

				FromBlock: 100,

				ToBlock: 200,

				After: &after,

				Limit: 100,
			},
		)
	if err != nil {
		t.Fatalf(
			"normalizeBurnCandidatePageRequest() error = %v",
			err,
		)
	}

	expectedAddress :=
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if req.PoolAddress !=
		expectedAddress {
		t.Fatalf(
			"PoolAddress = %q, want %q",
			req.PoolAddress,
			expectedAddress,
		)
	}

	if req.After == &after {
		t.Fatal(
			"normalized request aliases original cursor pointer",
		)
	}

	if !req.After.Equal(after) {
		t.Fatalf(
			"After = %s, want %s",
			req.After,
			after,
		)
	}
}

func TestNormalizeBurnCandidatePageRequestRejectsInvalidValues(
	t *testing.T,
) {
	t.Parallel()

	valid :=
		BurnCandidatePageRequest{
			PoolAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",

			FromBlock: 100,

			ToBlock: 200,

			Limit: 100,
		}

	testCases := []struct {
		name   string
		mutate func(
			*BurnCandidatePageRequest,
		)
	}{
		{
			name: "empty pool address",

			mutate: func(
				req *BurnCandidatePageRequest,
			) {
				req.PoolAddress = ""
			},
		},
		{
			name: "zero from block",

			mutate: func(
				req *BurnCandidatePageRequest,
			) {
				req.FromBlock = 0
			},
		},
		{
			name: "reversed range",

			mutate: func(
				req *BurnCandidatePageRequest,
			) {
				req.FromBlock = 201
			},
		},
		{
			name: "zero limit",

			mutate: func(
				req *BurnCandidatePageRequest,
			) {
				req.Limit = 0
			},
		},
		{
			name: "limit too large",

			mutate: func(
				req *BurnCandidatePageRequest,
			) {
				req.Limit =
					maxBurnCandidatePageSize + 1
			},
		},
		{
			name: "after cursor outside range",

			mutate: func(
				req *BurnCandidatePageRequest,
			) {
				req.After =
					&domain.EventCursor{
						BlockNumber: 99,

						LogIndex: 1,
					}
			},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				req := valid

				testCase.mutate(
					&req,
				)

				if _, err :=
					normalizeBurnCandidatePageRequest(
						req,
					); err == nil {
					t.Fatal(
						"normalizeBurnCandidatePageRequest() expected error",
					)
				}
			},
		)
	}
}
