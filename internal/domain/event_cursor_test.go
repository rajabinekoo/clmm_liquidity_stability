package domain

import "testing"

func TestEventCursorCompare(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name     string
		left     EventCursor
		right    EventCursor
		expected int
	}{
		{
			name: "earlier block",

			left: EventCursor{
				BlockNumber: 100,
				LogIndex:    20,
			},

			right: EventCursor{
				BlockNumber: 101,
				LogIndex:    1,
			},

			expected: -1,
		},
		{
			name: "later block",

			left: EventCursor{
				BlockNumber: 101,
				LogIndex:    1,
			},

			right: EventCursor{
				BlockNumber: 100,
				LogIndex:    20,
			},

			expected: 1,
		},
		{
			name: "earlier log in same block",

			left: EventCursor{
				BlockNumber: 100,
				LogIndex:    4,
			},

			right: EventCursor{
				BlockNumber: 100,
				LogIndex:    5,
			},

			expected: -1,
		},
		{
			name: "later log in same block",

			left: EventCursor{
				BlockNumber: 100,
				LogIndex:    6,
			},

			right: EventCursor{
				BlockNumber: 100,
				LogIndex:    5,
			},

			expected: 1,
		},
		{
			name: "equal",

			left: EventCursor{
				BlockNumber: 100,
				LogIndex:    5,
			},

			right: EventCursor{
				BlockNumber: 100,
				LogIndex:    5,
			},

			expected: 0,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				actual := testCase.left.Compare(
					testCase.right,
				)

				if actual != testCase.expected {
					t.Fatalf(
						"Compare() = %d, want %d",
						actual,
						testCase.expected,
					)
				}
			},
		)
	}
}

func TestEventCursorRelations(
	t *testing.T,
) {
	t.Parallel()

	earlier := EventCursor{
		BlockNumber: 100,
		LogIndex:    4,
	}

	later := EventCursor{
		BlockNumber: 100,
		LogIndex:    5,
	}

	equal := EventCursor{
		BlockNumber: 100,
		LogIndex:    4,
	}

	if !earlier.Before(later) {
		t.Fatal(
			"Before() = false, want true",
		)
	}

	if !later.After(earlier) {
		t.Fatal(
			"After() = false, want true",
		)
	}

	if !earlier.Equal(equal) {
		t.Fatal(
			"Equal() = false, want true",
		)
	}

	if earlier.Equal(later) {
		t.Fatal(
			"Equal() = true for different cursors",
		)
	}
}

func TestEventCursorValidate(
	t *testing.T,
) {
	t.Parallel()

	testCases := []struct {
		name        string
		cursor      EventCursor
		expectError bool
	}{
		{
			name: "valid",

			cursor: EventCursor{
				BlockNumber: 1,
				LogIndex:    0,
			},
		},
		{
			name: "zero block",

			cursor: EventCursor{
				BlockNumber: 0,
				LogIndex:    1,
			},

			expectError: true,
		},
		{
			name: "negative log index",

			cursor: EventCursor{
				BlockNumber: 1,
				LogIndex:    -1,
			},

			expectError: true,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Parallel()

				err := testCase.cursor.Validate()

				if testCase.expectError &&
					err == nil {
					t.Fatal(
						"Validate() expected error",
					)
				}

				if !testCase.expectError &&
					err != nil {
					t.Fatalf(
						"Validate() error = %v",
						err,
					)
				}
			},
		)
	}
}

func TestEventCursorString(
	t *testing.T,
) {
	t.Parallel()

	cursor := EventCursor{
		BlockNumber: 12_345,
		LogIndex:    67,
	}

	if actual := cursor.String(); actual != "12345:67" {
		t.Fatalf(
			"String() = %q, want %q",
			actual,
			"12345:67",
		)
	}
}

func TestDomainEventsExposeCursor(
	t *testing.T,
) {
	t.Parallel()

	action := LPAction{
		BlockNumber: 100,
		LogIndex:    12,
	}

	swap := SwapEvent{
		BlockNumber: 101,
		LogIndex:    14,
	}

	expectedActionCursor := EventCursor{
		BlockNumber: 100,
		LogIndex:    12,
	}

	expectedSwapCursor := EventCursor{
		BlockNumber: 101,
		LogIndex:    14,
	}

	if !action.Cursor().Equal(
		expectedActionCursor,
	) {
		t.Fatalf(
			"LPAction.Cursor() = %s, want %s",
			action.Cursor(),
			expectedActionCursor,
		)
	}

	if !swap.Cursor().Equal(
		expectedSwapCursor,
	) {
		t.Fatalf(
			"SwapEvent.Cursor() = %s, want %s",
			swap.Cursor(),
			expectedSwapCursor,
		)
	}
}
