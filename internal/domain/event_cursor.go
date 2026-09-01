package domain

import "fmt"

// EventCursor identifies the deterministic position of an event inside the
// Ethereum event stream.
//
// Ethereum log_index is block-global, therefore block number and log index are
// sufficient for ordering the Mint, Burn and Swap events relevant to this
// study.
type EventCursor struct {
	BlockNumber uint64
	LogIndex    int
}

func (c EventCursor) Validate() error {
	if c.BlockNumber == 0 {
		return fmt.Errorf(
			"event cursor block number must be greater than zero",
		)
	}

	if c.LogIndex < 0 {
		return fmt.Errorf(
			"event cursor log index must not be negative: %d",
			c.LogIndex,
		)
	}

	return nil
}

// Compare returns:
//
//	-1 when c is before other
//	 0 when c equals other
//	+1 when c is after other
func (c EventCursor) Compare(
	other EventCursor,
) int {
	switch {
	case c.BlockNumber < other.BlockNumber:
		return -1

	case c.BlockNumber > other.BlockNumber:
		return 1

	case c.LogIndex < other.LogIndex:
		return -1

	case c.LogIndex > other.LogIndex:
		return 1

	default:
		return 0
	}
}

func (c EventCursor) Before(
	other EventCursor,
) bool {
	return c.Compare(other) < 0
}

func (c EventCursor) After(
	other EventCursor,
) bool {
	return c.Compare(other) > 0
}

func (c EventCursor) Equal(
	other EventCursor,
) bool {
	return c.Compare(other) == 0
}

func (c EventCursor) String() string {
	return fmt.Sprintf(
		"%d:%d",
		c.BlockNumber,
		c.LogIndex,
	)
}

func (a LPAction) Cursor() EventCursor {
	return EventCursor{
		BlockNumber: a.BlockNumber,
		LogIndex:    a.LogIndex,
	}
}

func (s SwapEvent) Cursor() EventCursor {
	return EventCursor{
		BlockNumber: s.BlockNumber,
		LogIndex:    s.LogIndex,
	}
}
