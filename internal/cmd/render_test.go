package cmd

import (
	"encoding/json"
	"testing"

	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

func TestFlattenRows(t *testing.T) {
	dims := []string{"date", "country"}
	mets := []metricHeader{
		{Name: "sessions", Type: "TYPE_INTEGER"},
		{Name: "engagementRate", Type: "TYPE_FLOAT"},
	}
	rows := []*analyticsdata.Row{
		{
			DimensionValues: []*analyticsdata.DimensionValue{{Value: "20260401"}, {Value: "Germany"}},
			MetricValues:    []*analyticsdata.MetricValue{{Value: "1234"}, {Value: "0.6125"}},
		},
	}
	got := flattenRows(dims, mets, rows)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	row := got[0]
	if row["date"] != "20260401" || row["country"] != "Germany" {
		t.Errorf("dimensions = %v", row)
	}
	// Metrics must come back as real numbers, not the API's strings, so
	// consumers can do arithmetic without re-parsing.
	if v, ok := row["sessions"].(int64); !ok || v != 1234 {
		t.Errorf("sessions = %#v, want int64(1234)", row["sessions"])
	}
	if v, ok := row["engagementRate"].(float64); !ok || v != 0.6125 {
		t.Errorf("engagementRate = %#v, want float64(0.6125)", row["engagementRate"])
	}
}

func TestFlattenRowsToleratesShortRows(t *testing.T) {
	// A row shorter than its headers must not panic; the missing keys are
	// simply absent.
	got := flattenRows([]string{"a", "b"}, []metricHeader{{Name: "m", Type: "TYPE_INTEGER"}},
		[]*analyticsdata.Row{{DimensionValues: []*analyticsdata.DimensionValue{{Value: "x"}}}})
	if len(got) != 1 || got[0]["a"] != "x" {
		t.Fatalf("got %v", got)
	}
	if _, present := got[0]["b"]; present {
		t.Error("a missing dimension value should leave the key absent")
	}
}

func TestTypedMetric(t *testing.T) {
	cases := []struct {
		raw, typ string
		want     any
	}{
		{"42", "TYPE_INTEGER", int64(42)},
		{"1.5", "TYPE_FLOAT", 1.5},
		{"90.25", "TYPE_SECONDS", 90.25},
		{"12.99", "TYPE_CURRENCY", 12.99},
		// Unknown types and unparseable values stay strings rather than risking
		// a lossy conversion.
		{"abc", "TYPE_INTEGER", "abc"},
		{"whatever", "TYPE_UNKNOWN_TO_US", "whatever"},
	}
	for _, c := range cases {
		if got := typedMetric(c.raw, c.typ); got != c.want {
			t.Errorf("typedMetric(%q, %q) = %#v, want %#v", c.raw, c.typ, got, c.want)
		}
	}
}

func TestReportPayloadColumns(t *testing.T) {
	p := &reportPayload{
		DimensionHeaders: []string{"date", "country"},
		MetricHeaders:    []metricHeader{{Name: "sessions"}, {Name: "activeUsers"}},
	}
	want := []string{"date", "country", "sessions", "activeUsers"}
	got := p.columns()
	if len(got) != len(want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("columns = %v, want %v", got, want)
		}
	}
}

func TestReportPayloadRoundTrip(t *testing.T) {
	// The payload is what goes into the cache, so it has to survive a JSON
	// round trip with its typed metrics intact.
	in := &reportPayload{
		Property:         "properties/123",
		DimensionHeaders: []string{"date"},
		MetricHeaders:    []metricHeader{{Name: "sessions", Type: "TYPE_INTEGER"}},
		Rows:             flattenRows([]string{"date"}, []metricHeader{{Name: "sessions", Type: "TYPE_INTEGER"}}, []*analyticsdata.Row{{DimensionValues: []*analyticsdata.DimensionValue{{Value: "20260401"}}, MetricValues: []*analyticsdata.MetricValue{{Value: "7"}}}}),
		RowCount:         1,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out reportPayload
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Property != in.Property || out.RowCount != 1 || len(out.Rows) != 1 {
		t.Fatalf("round trip lost data: %+v", out)
	}
	if got := out.Rows[0]["sessions"]; got != float64(7) {
		// JSON has one number type, so an int64 comes back as float64 — which
		// still renders as "7" through fmtCell.
		t.Errorf("sessions after round trip = %#v", got)
	}
}

func TestToQuota(t *testing.T) {
	if toQuota(nil) != nil {
		t.Error("a nil PropertyQuota should map to nil")
	}
	got := toQuota(&analyticsdata.PropertyQuota{
		TokensPerDay:  &analyticsdata.QuotaStatus{Consumed: 10, Remaining: 190},
		TokensPerHour: nil,
	})
	if got.TokensPerDay == nil || got.TokensPerDay.Consumed != 10 || got.TokensPerDay.Remaining != 190 {
		t.Errorf("tokens per day = %+v", got.TokensPerDay)
	}
	if got.TokensPerHour != nil {
		t.Error("an absent bucket should stay nil rather than becoming a zero value")
	}
}
