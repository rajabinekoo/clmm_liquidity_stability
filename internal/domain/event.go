package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type LPActionType int16

const (
	LPActionMint LPActionType = 1
	LPActionBurn LPActionType = 2
)

type LPAction struct {
	ID       string
	SourceID string

	PoolAddress string
	Action      LPActionType

	TxHash      string
	BlockNumber uint64
	LogIndex    int
	Timestamp   time.Time

	Owner  *string
	Sender *string
	Origin *string

	TickLower int
	TickUpper int

	LiquidityDelta decimal.Decimal

	Amount0   decimal.Decimal
	Amount1   decimal.Decimal
	AmountUSD decimal.Decimal
}
