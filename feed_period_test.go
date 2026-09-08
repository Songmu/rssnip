// This file covers date parsing and period-based filtering of items.
package rssnip

import "testing"

func TestWithinPeriod(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01", false)
	until := mustParseTimeBound(t, "2024-01-31", true)
	tests := []struct {
		date string
		want bool
	}{
		{"2023-12-31T23:59:59Z", false},
		{"2024-01-01T00:00:00Z", true},
		{"2024-01-31T23:59:59Z", true},
		{"2024-02-01T00:00:00Z", false},
		{"", false},
	}
	for _, tt := range tests {
		item := Item{DatePublished: tt.date}
		if got := withinPeriod(item, since, until); got != tt.want {
			t.Errorf("withinPeriod(%q) = %v, want %v", tt.date, got, tt.want)
		}
	}
}

func TestWithinPeriodFallsBackToModifiedDate(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01", false)
	item := Item{
		DatePublished: "not-rfc3339",
		DateModified:  "2024-01-15T12:00:00Z",
	}
	if !withinPeriod(item, since, nil) {
		t.Error("parseable modification date should be used when publication date is invalid")
	}
}

func TestWithinPeriodAcceptsEitherPublishedOrModifiedDate(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01", false)
	until := mustParseTimeBound(t, "2024-01-31", true)
	item := Item{
		DatePublished: "2023-12-01T00:00:00Z",
		DateModified:  "2024-01-15T12:00:00Z",
	}
	if !withinPeriod(item, since, until) {
		t.Error("item should be included when its modification date is in range")
	}
}

func TestFractionalSecondDates(t *testing.T) {
	t.Parallel()
	const value = "2024-01-15T12:00:00.123456789Z"
	if got := normalizeDate(value); got != value {
		t.Errorf("normalizeDate(%q) = %q", value, got)
	}
	since := mustParseTimeBound(t, "2024-01-15T12:00:00.123Z", false)
	if !withinPeriod(Item{DatePublished: value}, since, nil) {
		t.Error("fractional-second item should be included")
	}
}
