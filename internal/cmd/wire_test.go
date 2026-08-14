package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

// The generated client drops zero-valued fields unless they are force-sent, and
// several of the values this CLI builds are legitimately zero or false. These
// tests assert on the actual JSON that goes over the wire, because a silently
// dropped field would change the meaning of a request without failing anything.

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestOrderByDescReachesTheWire(t *testing.T) {
	obs, err := buildOrderBys([]string{"-sessions"}, nil, []string{"sessions"})
	if err != nil {
		t.Fatal(err)
	}
	got := marshal(t, obs[0])
	if !strings.Contains(got, `"desc":true`) {
		t.Errorf("descending order-by marshalled as %s, want it to carry desc:true", got)
	}
	if !strings.Contains(got, `"metricName":"sessions"`) {
		t.Errorf("order-by lost its metric name: %s", got)
	}

	// Ascending is the API default, so it may legitimately be omitted.
	asc, err := buildOrderBys([]string{"sessions:asc"}, nil, []string{"sessions"})
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, asc[0]); strings.Contains(got, `"desc":true`) {
		t.Errorf("ascending order-by marshalled as %s", got)
	}
}

func TestNumericFilterValuesReachTheWire(t *testing.T) {
	// int64 values are serialized as strings by the API convention.
	fe, err := parseMetricFilter("sessions>100")
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, fe); !strings.Contains(got, `"int64Value":"100"`) {
		t.Errorf("integer metric filter marshalled as %s", got)
	}

	fe, err = parseMetricFilter("bounceRate<=0.4")
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, fe); !strings.Contains(got, `"doubleValue":0.4`) {
		t.Errorf("float metric filter marshalled as %s", got)
	}

	// Zero is a meaningful bound and must survive omitempty.
	fe, err = parseMetricFilter("sessions>0")
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, fe); !strings.Contains(got, `"int64Value":"0"`) {
		t.Errorf("a zero bound was dropped: %s", got)
	}
}

func TestDimensionFilterShape(t *testing.T) {
	fe, err := buildDimensionFilter([]string{"pagePath~~^/blog/", "country=Germany"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	got := marshal(t, fe)
	for _, want := range []string{`"andGroup"`, `"matchType":"FULL_REGEXP"`, `"value":"^/blog/"`, `"fieldName":"country"`} {
		if !strings.Contains(got, want) {
			t.Errorf("filter JSON %s is missing %s", got, want)
		}
	}

	neg, err := parseDimensionFilter("country!=Germany", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, neg); !strings.Contains(got, `"notExpression"`) {
		t.Errorf("!= should marshal to a notExpression, got %s", got)
	}
}

func TestRealtimeMinuteRangeSendsZeroEnd(t *testing.T) {
	// endMinutesAgo:0 means "up to now"; dropping it would widen the window.
	mr := &analyticsdata.MinuteRange{
		Name: "recent", StartMinutesAgo: 5, EndMinutesAgo: 0,
		ForceSendFields: []string{"EndMinutesAgo"},
	}
	if got := marshal(t, mr); !strings.Contains(got, `"endMinutesAgo":0`) {
		t.Errorf("minute range marshalled as %s, want endMinutesAgo:0", got)
	}
}

func TestRunReportRequestShape(t *testing.T) {
	rf := &reportFlags{
		Range:   rangeFlags{Start: "2026-01-01", End: "2026-01-31"},
		Metrics: []string{"sessions"},
	}
	dateRanges, err := rf.Range.dateRanges("2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatal(err)
	}
	req := &analyticsdata.RunReportRequest{
		DateRanges:          dateRanges,
		Dimensions:          toDimensions([]string{"date"}),
		Metrics:             toMetrics([]string{"sessions"}),
		Limit:               20,
		ReturnPropertyQuota: true,
	}
	got := marshal(t, req)
	for _, want := range []string{
		`"startDate":"2026-01-01"`,
		`"endDate":"2026-01-31"`,
		`{"name":"date"}`,
		`{"name":"sessions"}`,
		`"limit":"20"`, // int64 fields go over the wire as strings
		`"returnPropertyQuota":true`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("request JSON %s is missing %s", got, want)
		}
	}
}

func TestPivotLimitIsHonored(t *testing.T) {
	pivots, err := parsePivots([]string{"country:5"}, []string{"country"}, []string{"sessions"})
	if err != nil {
		t.Fatal(err)
	}
	if pivots[0].Limit != 5 {
		t.Errorf("pivot limit = %d, want 5", pivots[0].Limit)
	}
	// A pivot may only name dimensions the report actually selects.
	if _, err := parsePivots([]string{"city:5"}, []string{"country"}, []string{"sessions"}); err == nil {
		t.Error("pivoting on an unselected dimension should be rejected")
	}
	if _, err := parsePivots([]string{"country:abc"}, []string{"country"}, nil); err == nil {
		t.Error("a non-numeric pivot limit should be rejected")
	}
}
