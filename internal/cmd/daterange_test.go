package cmd

import (
	"testing"
	"time"
)

func TestRangePresets(t *testing.T) {
	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")

	cases := []struct {
		preset string
		start  string
		end    string
	}{
		{"today", "today", "today"},
		{"yesterday", "yesterday", "yesterday"},
		// Windows end yesterday and are inclusive, so "last 7 days" spans
		// yesterday-6 .. yesterday.
		{"last-7d", today.AddDate(0, 0, -7).Format("2006-01-02"), yesterday},
		{"last-28d", today.AddDate(0, 0, -28).Format("2006-01-02"), yesterday},
		{"last-90d", today.AddDate(0, 0, -90).Format("2006-01-02"), yesterday},
	}
	for _, c := range cases {
		t.Run(c.preset, func(t *testing.T) {
			rf := rangeFlags{Range: c.preset}
			start, end, err := rf.resolve("")
			if err != nil {
				t.Fatalf("resolve(%q): %v", c.preset, err)
			}
			if start != c.start || end != c.end {
				t.Errorf("resolve(%q) = (%q, %q), want (%q, %q)", c.preset, start, end, c.start, c.end)
			}
		})
	}
}

func TestRangeInclusiveWindowLength(t *testing.T) {
	rf := rangeFlags{Range: "last-7d"}
	start, end, err := rf.resolve("")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := time.Parse("2006-01-02", start)
	e, _ := time.Parse("2006-01-02", end)
	if days := int(e.Sub(s).Hours()/24) + 1; days != 7 {
		t.Errorf("last-7d spans %d days, want 7 (inclusive)", days)
	}
}

func TestRangeDefaultAndExplicitDates(t *testing.T) {
	// An empty --range falls back to the configured default.
	rf := rangeFlags{}
	start, end, err := rf.resolve("yesterday")
	if err != nil || start != "yesterday" || end != "yesterday" {
		t.Errorf("resolve with default = (%q, %q, %v)", start, end, err)
	}

	rf = rangeFlags{Start: "2026-01-01", End: "2026-01-31"}
	start, end, err = rf.resolve("last-28d")
	if err != nil || start != "2026-01-01" || end != "2026-01-31" {
		t.Errorf("explicit dates = (%q, %q, %v)", start, end, err)
	}

	// The API's own relative forms pass through untouched.
	rf = rangeFlags{Start: "30daysAgo", End: "yesterday"}
	start, end, err = rf.resolve("")
	if err != nil || start != "30daysAgo" || end != "yesterday" {
		t.Errorf("relative dates = (%q, %q, %v)", start, end, err)
	}
}

func TestRangeRejectsBadInput(t *testing.T) {
	cases := []rangeFlags{
		{Range: "last-7d", Start: "2026-01-01", End: "2026-01-31"}, // mutually exclusive
		{Start: "2026-01-01"}, // half a window
		{End: "2026-01-31"},
		{Start: "01/01/2026", End: "2026-01-31"}, // wrong date format
		{Range: "last-decade"},
	}
	for _, rf := range cases {
		if _, _, err := rf.resolve(""); err == nil {
			t.Errorf("resolve(%+v): expected an error, got nil", rf)
		}
	}
}

func TestCompareRange(t *testing.T) {
	// The previous period is the same length, ending the day before the start.
	start, end, err := compareRange("2026-01-08", "2026-01-14", "previous-period")
	if err != nil {
		t.Fatal(err)
	}
	if start != "2026-01-01" || end != "2026-01-07" {
		t.Errorf("previous-period = (%q, %q), want (2026-01-01, 2026-01-07)", start, end)
	}

	start, end, err = compareRange("2026-03-01", "2026-03-31", "previous-year")
	if err != nil {
		t.Fatal(err)
	}
	if start != "2025-03-01" || end != "2025-03-31" {
		t.Errorf("previous-year = (%q, %q), want (2025-03-01, 2025-03-31)", start, end)
	}

	if _, _, err := compareRange("2026-01-01", "2026-01-31", "previous-decade"); err == nil {
		t.Error("an unknown --compare mode should be rejected")
	}
}

func TestDateRangesAddsComparison(t *testing.T) {
	rf := rangeFlags{Compare: "previous-period"}
	ranges, err := rf.dateRanges("2026-01-08", "2026-01-14")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 {
		t.Fatalf("got %d date ranges, want 2", len(ranges))
	}
	if ranges[0].Name != "current" || ranges[1].Name != "comparison" {
		t.Errorf("names = (%q, %q)", ranges[0].Name, ranges[1].Name)
	}

	rf = rangeFlags{}
	ranges, err = rf.dateRanges("2026-01-08", "2026-01-14")
	if err != nil || len(ranges) != 1 {
		t.Errorf("without --compare, got %d ranges (err %v), want 1", len(ranges), err)
	}
}

func TestAbsoluteDate(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"today", today},
		{"yesterday", today.AddDate(0, 0, -1)},
		{"7daysAgo", today.AddDate(0, 0, -7)},
	}
	for _, c := range cases {
		got, err := absoluteDate(c.in)
		if err != nil {
			t.Fatalf("absoluteDate(%q): %v", c.in, err)
		}
		if !got.Equal(c.want) {
			t.Errorf("absoluteDate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
