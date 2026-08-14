package cmd

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	"github.com/spf13/cobra"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

type rangeFlags struct {
	Range   string
	Start   string
	End     string
	Compare string
}

func addRangeFlags(cmd *cobra.Command, rf *rangeFlags) {
	cmd.Flags().StringVar(&rf.Range, "range", "", "preset range: today|yesterday|last-7d|last-14d|last-28d|last-30d|last-90d|last-180d|last-12m|this-month|last-month|ytd")
	cmd.Flags().StringVar(&rf.Start, "start", "", "start date YYYY-MM-DD, NdaysAgo, today, or yesterday (mutually exclusive with --range)")
	cmd.Flags().StringVar(&rf.End, "end", "", "end date YYYY-MM-DD, NdaysAgo, today, or yesterday (mutually exclusive with --range)")
	cmd.Flags().StringVar(&rf.Compare, "compare", "", "add a comparison window: previous-period|previous-year")
}

// relativeDate matches the relative forms the Data API accepts verbatim, so a
// user's "7daysAgo" is passed through untouched instead of being re-derived
// here against a possibly different clock.
var relativeDate = regexp.MustCompile(`^(today|yesterday|[0-9]{1,4}daysAgo)$`)

// resolve returns (start, end) as strings the Data API accepts.
func (rf *rangeFlags) resolve(defaultRange string) (string, string, error) {
	today := time.Now().UTC()
	if rf.Start != "" || rf.End != "" {
		if rf.Range != "" {
			return "", "", errs.New(errs.CodeInvalidDateRange, "--range and --start/--end are mutually exclusive")
		}
		if rf.Start == "" || rf.End == "" {
			return "", "", errs.New(errs.CodeInvalidDateRange, "--start and --end must both be provided")
		}
		start, err := validateDate("--start", rf.Start)
		if err != nil {
			return "", "", err
		}
		end, err := validateDate("--end", rf.End)
		if err != nil {
			return "", "", err
		}
		return start, end, nil
	}

	r := firstNonEmpty(rf.Range, defaultRange, "last-28d")
	// GA4 finalizes "today" only partially; the presets end yesterday so that
	// repeated runs of the same window return stable numbers. Explicit
	// --start/--end still let a caller ask for today.
	yesterday := today.AddDate(0, 0, -1)
	fmtDate := func(t time.Time) string { return t.Format("2006-01-02") }

	switch r {
	case "today":
		return "today", "today", nil
	case "yesterday":
		return "yesterday", "yesterday", nil
	case "this-month":
		first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		return fmtDate(first), "today", nil
	case "last-month":
		firstThis := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		firstPrev := firstThis.AddDate(0, -1, 0)
		return fmtDate(firstPrev), fmtDate(firstThis.AddDate(0, 0, -1)), nil
	case "ytd":
		jan1 := time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		return fmtDate(jan1), "today", nil
	}

	var days int
	switch r {
	case "last-7d":
		days = 7
	case "last-14d":
		days = 14
	case "last-28d":
		days = 28
	case "last-30d":
		days = 30
	case "last-90d":
		days = 90
	case "last-180d":
		days = 180
	case "last-12m":
		days = 365
	default:
		return "", "", errs.New(errs.CodeInvalidDateRange, "unknown --range: "+r).
			WithHint("Valid presets: today, yesterday, last-7d, last-14d, last-28d, last-30d, last-90d, last-180d, last-12m, this-month, last-month, ytd.")
	}
	// A "last 7 days" window is the 7 days ending yesterday, inclusive.
	start := yesterday.AddDate(0, 0, -(days - 1))
	return fmtDate(start), fmtDate(yesterday), nil
}

func validateDate(flag, v string) (string, error) {
	v = strings.TrimSpace(v)
	if relativeDate.MatchString(v) {
		return v, nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", errs.Newf(errs.CodeInvalidDateRange, "invalid %s: %q", flag, v).
			WithHint("Use YYYY-MM-DD, NdaysAgo, today, or yesterday.")
	}
	return v, nil
}

// dateRanges builds the request's DateRange list, appending the comparison
// window when one was asked for. The Data API adds a `dateRange` dimension
// column to the response whenever more than one range is present, which is how
// the caller tells the two apart.
func (rf *rangeFlags) dateRanges(start, end string) ([]*analyticsdata.DateRange, error) {
	ranges := []*analyticsdata.DateRange{{StartDate: start, EndDate: end, Name: "current"}}
	if rf.Compare == "" {
		return ranges, nil
	}
	cs, ce, err := compareRange(start, end, rf.Compare)
	if err != nil {
		return nil, err
	}
	return append(ranges, &analyticsdata.DateRange{StartDate: cs, EndDate: ce, Name: "comparison"}), nil
}

// compareRange returns the start/end of the comparison window. Relative dates
// are resolved against today first, since shifting "7daysAgo" back a period
// only means anything once it is an actual date.
func compareRange(start, end, mode string) (string, string, error) {
	s, err1 := absoluteDate(start)
	e, err2 := absoluteDate(end)
	if err1 != nil || err2 != nil {
		return "", "", errs.New(errs.CodeInvalidDateRange, "--compare needs a resolvable date range")
	}
	switch mode {
	case "previous-period":
		// The window immediately preceding, of the same length.
		d := e.Sub(s)
		ps := s.Add(-d - 24*time.Hour)
		pe := s.AddDate(0, 0, -1)
		return ps.Format("2006-01-02"), pe.Format("2006-01-02"), nil
	case "previous-year":
		return s.AddDate(-1, 0, 0).Format("2006-01-02"), e.AddDate(-1, 0, 0).Format("2006-01-02"), nil
	}
	return "", "", errs.New(errs.CodeInvalidArgs, "unknown --compare: "+mode).
		WithHint("Valid modes: previous-period, previous-year.")
}

// absoluteDate turns any date form the API accepts into a time.Time.
func absoluteDate(v string) (time.Time, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	switch {
	case v == "today":
		return today, nil
	case v == "yesterday":
		return today.AddDate(0, 0, -1), nil
	case strings.HasSuffix(v, "daysAgo"):
		n, err := strconv.Atoi(strings.TrimSuffix(v, "daysAgo"))
		if err != nil {
			return time.Time{}, err
		}
		return today.AddDate(0, 0, -n), nil
	}
	return time.Parse("2006-01-02", v)
}
