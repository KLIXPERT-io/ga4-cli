package cmd

import (
	"testing"

	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

func TestParseDimensionFilter(t *testing.T) {
	cases := []struct {
		in        string
		field     string
		matchType string
		value     string
		negated   bool
	}{
		{in: "country=Germany", field: "country", matchType: "EXACT", value: "Germany"},
		{in: "country!=Germany", field: "country", matchType: "EXACT", value: "Germany", negated: true},
		{in: "pagePath~/blog/", field: "pagePath", matchType: "CONTAINS", value: "/blog/"},
		{in: "pagePath!~/tag/", field: "pagePath", matchType: "CONTAINS", value: "/tag/", negated: true},
		{in: "pagePath~~^/(blog|guides)/", field: "pagePath", matchType: "FULL_REGEXP", value: "^/(blog|guides)/"},
		{in: "pagePath!~~^/tag/", field: "pagePath", matchType: "FULL_REGEXP", value: "^/tag/", negated: true},
		{in: "pagePath^=/blog", field: "pagePath", matchType: "BEGINS_WITH", value: "/blog"},
		{in: "pagePath$=.html", field: "pagePath", matchType: "ENDS_WITH", value: ".html"},
		// The separator is the earliest operator, so operator characters inside
		// the value stay part of the value.
		{in: "pagePath=/a?b=c", field: "pagePath", matchType: "EXACT", value: "/a?b=c"},
		{in: "eventName~a~~b", field: "eventName", matchType: "CONTAINS", value: "a~~b"},
		{in: "pagePath~~^/a=b", field: "pagePath", matchType: "FULL_REGEXP", value: "^/a=b"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseDimensionFilter(c.in, false)
			if err != nil {
				t.Fatalf("parseDimensionFilter(%q): %v", c.in, err)
			}
			if c.negated {
				if got.NotExpression == nil {
					t.Fatalf("expected a NOT wrapper for %q", c.in)
				}
				got = got.NotExpression
			} else if got.NotExpression != nil {
				t.Fatalf("unexpected NOT wrapper for %q", c.in)
			}
			f := got.Filter
			if f == nil || f.StringFilter == nil {
				t.Fatalf("expected a string filter for %q", c.in)
			}
			if f.FieldName != c.field || f.StringFilter.MatchType != c.matchType || f.StringFilter.Value != c.value {
				t.Errorf("parseDimensionFilter(%q) = (%q, %q, %q), want (%q, %q, %q)",
					c.in, f.FieldName, f.StringFilter.MatchType, f.StringFilter.Value,
					c.field, c.matchType, c.value)
			}
		})
	}
}

func TestParseDimensionFilterInList(t *testing.T) {
	got, err := parseDimensionFilter("country@=Germany,Austria,Switzerland", true)
	if err != nil {
		t.Fatalf("parseDimensionFilter: %v", err)
	}
	il := got.Filter.InListFilter
	if il == nil {
		t.Fatal("expected an in-list filter")
	}
	if len(il.Values) != 3 || il.Values[0] != "Germany" || il.Values[2] != "Switzerland" {
		t.Errorf("values = %v", il.Values)
	}
	if !il.CaseSensitive {
		t.Error("case sensitivity should be carried through to the in-list filter")
	}
}

func TestParseDimensionFilterRejectsMalformed(t *testing.T) {
	for _, in := range []string{"country", "=Germany", "~blog", "", "country=", "country@="} {
		if _, err := parseDimensionFilter(in, false); err == nil {
			t.Errorf("parseDimensionFilter(%q): expected an error, got nil", in)
		}
	}
}

func TestParseMetricFilter(t *testing.T) {
	cases := []struct {
		in    string
		field string
		op    string
		i64   int64
		f64   float64
	}{
		{in: "sessions>100", field: "sessions", op: "GREATER_THAN", i64: 100},
		{in: "sessions>=100", field: "sessions", op: "GREATER_THAN_OR_EQUAL", i64: 100},
		{in: "sessions<10", field: "sessions", op: "LESS_THAN", i64: 10},
		{in: "sessions<=10", field: "sessions", op: "LESS_THAN_OR_EQUAL", i64: 10},
		{in: "sessions=5", field: "sessions", op: "EQUAL", i64: 5},
		{in: "bounceRate<=0.4", field: "bounceRate", op: "LESS_THAN_OR_EQUAL", f64: 0.4},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseMetricFilter(c.in)
			if err != nil {
				t.Fatalf("parseMetricFilter(%q): %v", c.in, err)
			}
			nf := got.Filter.NumericFilter
			if nf == nil {
				t.Fatalf("expected a numeric filter for %q", c.in)
			}
			if got.Filter.FieldName != c.field || nf.Operation != c.op {
				t.Errorf("= (%q, %q), want (%q, %q)", got.Filter.FieldName, nf.Operation, c.field, c.op)
			}
			if nf.Value.Int64Value != c.i64 || nf.Value.DoubleValue != c.f64 {
				t.Errorf("value = (int %d, float %v), want (int %d, float %v)",
					nf.Value.Int64Value, nf.Value.DoubleValue, c.i64, c.f64)
			}
		})
	}
}

func TestParseMetricFilterNegationAndRange(t *testing.T) {
	neg, err := parseMetricFilter("sessions!=5")
	if err != nil {
		t.Fatalf("parseMetricFilter: %v", err)
	}
	if neg.NotExpression == nil || neg.NotExpression.Filter.NumericFilter.Operation != "EQUAL" {
		t.Errorf("!= should be a NOT around EQUAL, got %+v", neg)
	}

	between, err := parseMetricFilter("sessions=10..100")
	if err != nil {
		t.Fatalf("parseMetricFilter: %v", err)
	}
	bf := between.Filter.BetweenFilter
	if bf == nil {
		t.Fatal("expected a between filter")
	}
	if bf.FromValue.Int64Value != 10 || bf.ToValue.Int64Value != 100 {
		t.Errorf("between = %d..%d, want 10..100", bf.FromValue.Int64Value, bf.ToValue.Int64Value)
	}
}

func TestParseMetricFilterRejectsMalformed(t *testing.T) {
	for _, in := range []string{"sessions", ">100", "sessions>abc", "sessions>", ""} {
		if _, err := parseMetricFilter(in); err == nil {
			t.Errorf("parseMetricFilter(%q): expected an error, got nil", in)
		}
	}
}

func TestBuildDimensionFilterGroups(t *testing.T) {
	// Two groups become an OR of two ANDs.
	fe, err := buildDimensionFilter(nil, []string{"deviceCategory=mobile,country=Germany", "deviceCategory=desktop"}, false)
	if err != nil {
		t.Fatalf("buildDimensionFilter: %v", err)
	}
	if fe.OrGroup == nil || len(fe.OrGroup.Expressions) != 2 {
		t.Fatalf("expected an OR of 2 groups, got %+v", fe)
	}
	if fe.OrGroup.Expressions[0].AndGroup == nil || len(fe.OrGroup.Expressions[0].AndGroup.Expressions) != 2 {
		t.Error("first group should be an AND of 2 filters")
	}
	// A single-filter group collapses rather than nesting a one-element AND.
	if fe.OrGroup.Expressions[1].Filter == nil {
		t.Error("a one-filter group should collapse to a bare filter")
	}

	// A single --filter collapses too.
	single, err := buildDimensionFilter([]string{"country=Germany"}, nil, false)
	if err != nil {
		t.Fatalf("buildDimensionFilter: %v", err)
	}
	if single.Filter == nil {
		t.Errorf("a single filter should not be wrapped in an AND group, got %+v", single)
	}

	if _, err := buildDimensionFilter([]string{"a=b"}, []string{"c=d"}, false); err == nil {
		t.Error("--filter and --filter-group together should be rejected")
	}
	if got, _ := buildDimensionFilter(nil, nil, false); got != nil {
		t.Errorf("no filters should yield nil, got %+v", got)
	}
}

func TestBuildOrderBys(t *testing.T) {
	dims := []string{"date", "pagePath"}
	metrics := []string{"sessions", "activeUsers"}

	cases := []struct {
		in       string
		isMetric bool
		name     string
		desc     bool
	}{
		// A bare metric means "top N by it", so it defaults to descending.
		{in: "sessions", isMetric: true, name: "sessions", desc: true},
		{in: "-sessions", isMetric: true, name: "sessions", desc: true},
		{in: "sessions:asc", isMetric: true, name: "sessions", desc: false},
		{in: "sessions:desc", isMetric: true, name: "sessions", desc: true},
		// A bare dimension sorts ascending, which is what a date series wants.
		{in: "date", isMetric: false, name: "date", desc: false},
		{in: "date:desc", isMetric: false, name: "date", desc: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := buildOrderBys([]string{c.in}, dims, metrics)
			if err != nil {
				t.Fatalf("buildOrderBys(%q): %v", c.in, err)
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 order-by, got %d", len(got))
			}
			ob := got[0]
			if c.isMetric {
				if ob.Metric == nil || ob.Metric.MetricName != c.name {
					t.Fatalf("expected metric order-by %q, got %+v", c.name, ob)
				}
			} else {
				if ob.Dimension == nil || ob.Dimension.DimensionName != c.name {
					t.Fatalf("expected dimension order-by %q, got %+v", c.name, ob)
				}
			}
			if ob.Desc != c.desc {
				t.Errorf("desc = %v, want %v", ob.Desc, c.desc)
			}
			// Desc=false is the zero value, so it must not be force-sent, while
			// Desc=true must be, or the API silently sorts the other way.
			if c.desc && !contains(ob.ForceSendFields, "Desc") {
				t.Error("descending order-bys must force-send Desc")
			}
		})
	}
}

func TestBuildOrderBysRejectsUnselectedField(t *testing.T) {
	if _, err := buildOrderBys([]string{"totalRevenue"}, []string{"date"}, []string{"sessions"}); err == nil {
		t.Error("ordering by an unselected field should be rejected")
	}
	if _, err := buildOrderBys([]string{"sessions:sideways"}, nil, []string{"sessions"}); err == nil {
		t.Error("an unknown sort direction should be rejected")
	}
}

func TestAndOrGroupCollapse(t *testing.T) {
	one := []*analyticsdata.FilterExpression{{Filter: &analyticsdata.Filter{FieldName: "a"}}}
	if got := andGroup(one); got.AndGroup != nil {
		t.Error("andGroup of one should collapse")
	}
	if got := orGroup(one); got.OrGroup != nil {
		t.Error("orGroup of one should collapse")
	}
	if got := andGroup(nil); got != nil {
		t.Error("andGroup of none should be nil")
	}
}
