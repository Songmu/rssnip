// This file covers date parsing and period-based filtering of items.
package rssnip

import (
	"testing"
	"time"
)

func TestWithinPeriod(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01")
	until := mustParseTimeBound(t, "2024-02-01")
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
		if got := withinPeriod(item, since, until, false); got != tt.want {
			t.Errorf("withinPeriod(%q) = %v, want %v", tt.date, got, tt.want)
		}
	}
}

func TestParseTimeBoundUsesLocationForDateOnly(t *testing.T) {
	t.Parallel()
	location := time.FixedZone("UTC+09", 9*60*60)
	got := mustParseTimeBoundInLocation(t, "2024-01-01", location)
	want := time.Date(2024, time.January, 1, 0, 0, 0, 0, location)
	if !got.Equal(want) || got.Location() != location {
		t.Errorf("parseTimeBound() = %v in %v, want %v in %v",
			got, got.Location(), want, location)
	}
}

func TestParseTimeBoundKeepsRFC3339Offset(t *testing.T) {
	t.Parallel()
	location := time.FixedZone("UTC+09", 9*60*60)
	got := mustParseTimeBoundInLocation(t, "2024-01-01T12:00:00-05:00", location)
	_, offset := got.Zone()
	if offset != -5*60*60 {
		t.Errorf("offset = %d, want %d", offset, -5*60*60)
	}
}

func TestParseTimeBoundRejectsTimezoneLessDatetime(t *testing.T) {
	t.Parallel()
	if _, err := parseTimeBound("2024-01-01T12:00:00", time.UTC); err == nil {
		t.Fatal("timezone-less datetime should be rejected")
	}
}

func TestWithinPeriodAllowsEmptyEqualBounds(t *testing.T) {
	t.Parallel()
	bound := mustParseTimeBound(t, "2024-01-01T00:00:00Z")
	item := Item{DatePublished: "2024-01-01T00:00:00Z"}
	if withinPeriod(item, bound, bound, false) {
		t.Error("an equal since and until must form an empty period")
	}
}

func TestWithinPeriodFallsBackToModifiedDate(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01")
	item := Item{
		DatePublished: "not-rfc3339",
		DateModified:  "2024-01-15T12:00:00Z",
	}
	if !withinPeriod(item, since, nil, false) {
		t.Error("parseable modification date should be used when publication date is invalid")
	}
}

func TestWithinPeriodPrefersPublishedDateByDefault(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01")
	until := mustParseTimeBound(t, "2024-02-01")
	item := Item{
		DatePublished: "2023-12-01T00:00:00Z",
		DateModified:  "2024-01-15T12:00:00Z",
	}
	if withinPeriod(item, since, until, false) {
		t.Error("item should be excluded because its publication date is out of range")
	}
	if !withinPeriod(item, since, until, true) {
		t.Error("item should be included when the modification date is preferred")
	}
}

func TestWithinPeriodPreferUpdatedFallsBackToPublishedDate(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-01")
	until := mustParseTimeBound(t, "2024-02-01")
	item := Item{DatePublished: "2024-01-15T12:00:00Z"}
	if !withinPeriod(item, since, until, true) {
		t.Error("publication date should be used when no modification date is available")
	}
	invalid := Item{
		DatePublished: "2024-01-15T12:00:00Z",
		DateModified:  "not-rfc3339",
	}
	if !withinPeriod(invalid, since, until, true) {
		t.Error("publication date should be used when the modification date is invalid")
	}
}

func TestFractionalSecondDates(t *testing.T) {
	t.Parallel()
	const value = "2024-01-15T12:00:00.123456789Z"
	if got := normalizeDate(value); got != value {
		t.Errorf("normalizeDate(%q) = %q", value, got)
	}
	since := mustParseTimeBound(t, "2024-01-15T12:00:00.123Z")
	if !withinPeriod(Item{DatePublished: value}, since, nil, false) {
		t.Error("fractional-second item should be included")
	}
}

func TestDateOrderCandidateRequiresFiveComparableItems(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-10")
	var candidate dateOrderCandidate
	for _, value := range []string{
		"2024-01-12T00:00:00Z",
		"2024-01-11T00:00:00Z",
		"2024-01-10T00:00:00Z",
		"2024-01-09T00:00:00Z",
	} {
		itemTime, ok := parseItemDate(value)
		candidate.observe(itemTime, ok)
	}
	if candidate.exhausted(*since) {
		t.Error("four comparable items must not enable early termination")
	}
	itemTime, ok := parseItemDate("2024-01-08T00:00:00Z")
	candidate.observe(itemTime, ok)
	if !candidate.exhausted(*since) {
		t.Error("five non-increasing comparable items before since should enable early termination")
	}
}

func TestDateOrderCandidateRejectsLaterIncrease(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-10")
	var candidate dateOrderCandidate
	for _, value := range []string{
		"2024-01-14T00:00:00Z",
		"2024-01-13T00:00:00Z",
		"2024-01-12T00:00:00Z",
		"2024-01-11T00:00:00Z",
		"2024-01-09T00:00:00Z",
		"2024-01-11T00:00:00Z",
		"2024-01-07T00:00:00Z",
	} {
		itemTime, ok := parseItemDate(value)
		candidate.observe(itemTime, ok)
	}
	if candidate.exhausted(*since) {
		t.Error("an ordering candidate must remain rejected after a later increase")
	}
}

func TestPaginationDateOrderNarrowsCandidates(t *testing.T) {
	t.Parallel()
	order := newPaginationDateOrder()
	for _, item := range []Item{
		{
			DatePublished: "2024-01-12T00:00:00Z",
			DateModified:  "2024-01-15T00:00:00Z",
		},
		{
			DatePublished: "2024-01-11T00:00:00Z",
			DateModified:  "2024-01-14T00:00:00Z",
		},
		{
			DatePublished: "2024-01-10T00:00:00Z",
			DateModified:  "2024-01-13T00:00:00Z",
		},
		{
			DatePublished: "2024-01-11T12:00:00Z",
			DateModified:  "2024-01-12T00:00:00Z",
		},
	} {
		order.observe(item)
	}
	if !order.published.rejected {
		t.Error("published ordering should be rejected by the later increase")
	}
	if order.modified.rejected {
		t.Error("modified ordering should remain viable")
	}
}

func TestPaginationDateOrderIgnoresMissingDates(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-10")
	order := newPaginationDateOrder()
	order.observe(Item{DatePublished: "2024-01-12T00:00:00Z"})
	order.observe(Item{})
	order.observe(Item{DatePublished: "2024-01-11T00:00:00Z"})
	order.observe(Item{DatePublished: "2024-01-10T00:00:00Z"})
	order.observe(Item{DatePublished: "2024-01-09T00:00:00Z"})
	if order.exhausted(*since, false) {
		t.Error("an item without a date must not count toward the five-item threshold")
	}
	order.observe(Item{DatePublished: "2024-01-08T00:00:00Z"})
	if !order.exhausted(*since, false) {
		t.Error("five comparable items across missing dates should enable termination")
	}
}

func TestPaginationDateOrderUsesModeSpecificCandidate(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-03-01")
	order := newPaginationDateOrder()
	for _, item := range []Item{
		{
			DatePublished: "2024-01-01T00:00:00Z",
			DateModified:  "2024-03-03T00:00:00Z",
		},
		{
			DatePublished: "2024-02-20T00:00:00Z",
			DateModified:  "2024-03-02T00:00:00Z",
		},
		{
			DatePublished: "2024-01-15T00:00:00Z",
			DateModified:  "2024-02-28T00:00:00Z",
		},
		{
			DatePublished: "2024-01-14T00:00:00Z",
			DateModified:  "2024-02-27T00:00:00Z",
		},
		{
			DatePublished: "2024-01-13T00:00:00Z",
			DateModified:  "2024-02-26T00:00:00Z",
		},
	} {
		order.observe(item)
	}
	if !order.published.rejected || order.modified.rejected {
		t.Fatalf("candidate states = published rejected %t, modified rejected %t",
			order.published.rejected, order.modified.rejected)
	}
	if !order.exhausted(*since, false) {
		t.Error("modified ordering should bound ordinary published filtering")
	}
	if !order.exhausted(*since, true) {
		t.Error("modified ordering should terminate updated filtering")
	}
}

func TestPaginationDateOrderRejectsInvalidModifiedBound(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-03-01")
	order := newPaginationDateOrder()
	for _, item := range []Item{
		{
			DatePublished: "2024-02-01T00:00:00Z",
			DateModified:  "2024-03-03T00:00:00Z",
		},
		{
			DatePublished: "2024-02-20T00:00:00Z",
			DateModified:  "2024-03-02T00:00:00Z",
		},
		{
			DatePublished: "2024-02-10T00:00:00Z",
			DateModified:  "2024-02-28T00:00:00Z",
		},
		{
			DatePublished: "2024-02-15T00:00:00Z",
			DateModified:  "2024-02-14T00:00:00Z",
		},
		{
			DatePublished: "2024-02-13T00:00:00Z",
			DateModified:  "2024-02-12T00:00:00Z",
		},
	} {
		order.observe(item)
	}
	if order.modifiedBoundsPublished {
		t.Fatal("published after modified must invalidate the modified upper bound")
	}
	if order.exhausted(*since, false) {
		t.Error("ordinary filtering must not use an invalid modified upper bound")
	}
	if !order.exhausted(*since, true) {
		t.Error("updated filtering may still use valid modified ordering")
	}
}

func TestPaginationDateOrderKeepsSinceInclusive(t *testing.T) {
	t.Parallel()
	since := mustParseTimeBound(t, "2024-01-10")
	order := newPaginationDateOrder()
	for _, value := range []string{
		"2024-01-14T00:00:00Z",
		"2024-01-13T00:00:00Z",
		"2024-01-12T00:00:00Z",
		"2024-01-11T00:00:00Z",
		"2024-01-10T00:00:00Z",
	} {
		order.observe(Item{DatePublished: value})
	}
	if order.exhausted(*since, false) {
		t.Error("an item equal to since must not trigger termination")
	}
	order.observe(Item{DatePublished: "2024-01-09T23:59:59Z"})
	if !order.exhausted(*since, false) {
		t.Error("a strictly older tail should trigger termination")
	}
}
