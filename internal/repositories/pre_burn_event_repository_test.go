package repositories

import (
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestNormalizePreBurnEventQuery(
	t *testing.T,
) {
	t.Parallel()

	address, cursor, err :=
		normalizePreBurnEventQuery(
			" 0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA ",
			domain.EventCursor{
				BlockNumber: 100,

				LogIndex: 12,
			},
		)
	if err != nil {
		t.Fatalf(
			"normalizePreBurnEventQuery() error = %v",
			err,
		)
	}

	expectedAddress :=
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if address != expectedAddress {
		t.Fatalf(
			"address = %q, want %q",
			address,
			expectedAddress,
		)
	}

	if cursor.BlockNumber != 100 ||
		cursor.LogIndex != 12 {
		t.Fatalf(
			"cursor = %s",
			cursor,
		)
	}
}

func TestNormalizePreBurnEventQueryRejectsInvalidInput(
	t *testing.T,
) {
	t.Parallel()

	if _, _, err :=
		normalizePreBurnEventQuery(
			"",
			domain.EventCursor{
				BlockNumber: 100,

				LogIndex: 1,
			},
		); err == nil {
		t.Fatal(
			"expected empty-address error",
		)
	}

	if _, _, err :=
		normalizePreBurnEventQuery(
			"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			domain.EventCursor{},
		); err == nil {
		t.Fatal(
			"expected invalid-cursor error",
		)
	}
}
