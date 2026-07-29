package repositories

import (
	"math/big"
	"strings"
	"testing"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

func TestSwapIndexCoverageCoversRequiredRange(t *testing.T) {
	t.Parallel()

	coverage := SwapIndexCoverage{
		FirstIndexedBlock: 100,
		IndexedThrough:    1_000,
	}

	if !coverage.Covers(100, 1_000) {
		t.Fatal("coverage must include its exact boundaries")
	}

	if coverage.Covers(99, 1_000) {
		t.Fatal("coverage must reject an earlier required start")
	}

	if coverage.Covers(100, 1_001) {
		t.Fatal("coverage must reject a later required end")
	}
}

func TestValidateStoredSwapWindowRejectsDuplicateCursor(t *testing.T) {
	t.Parallel()

	const poolAddress = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	first := repositorySwapTestEvent(poolAddress, 120, 7, "swap-a")
	duplicate := repositorySwapTestEvent(poolAddress, 120, 7, "swap-b")

	err := validateStoredSwapWindow(
		poolAddress,
		100,
		200,
		[]domain.SwapEvent{first, duplicate},
	)
	if err == nil {
		t.Fatal("validateStoredSwapWindow() error = nil, want duplicate cursor rejection")
	}

	if !strings.Contains(err.Error(), "non-increasing cursor") {
		t.Fatalf("error = %q, want non-increasing cursor detail", err)
	}
}

func repositorySwapTestEvent(
	poolAddress string,
	blockNumber uint64,
	logIndex int,
	id string,
) domain.SwapEvent {
	return domain.SwapEvent{
		ID:                id,
		TxHash:            "0x1111111111111111111111111111111111111111111111111111111111111111",
		PoolAddress:       poolAddress,
		BlockNumber:       blockNumber,
		LogIndex:          logIndex,
		Timestamp:         1,
		Amount0Raw:        big.NewInt(100),
		Amount1Raw:        big.NewInt(-200),
		SqrtPriceX96After: big.NewInt(1_000),
		TickAfter:         100,
	}
}
