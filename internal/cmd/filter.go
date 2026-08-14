package cmd

import (
	"strconv"
	"strings"

	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

// Dimension filter operators, longest token first so that "!~~" wins over
// "!~" and "~~" over "~".
var dimensionOps = []struct {
	token     string
	matchType string
	negate    bool
	inList    bool
}{
	{token: "!~~", matchType: "FULL_REGEXP", negate: true},
	{token: "~~", matchType: "FULL_REGEXP"},
	{token: "!~", matchType: "CONTAINS", negate: true},
	{token: "!=", matchType: "EXACT", negate: true},
	{token: "^=", matchType: "BEGINS_WITH"},
	{token: "$=", matchType: "ENDS_WITH"},
	{token: "@=", inList: true},
	{token: "~", matchType: "CONTAINS"},
	{token: "=", matchType: "EXACT"},
}

// parseDimensionFilter turns one `<dimension><op><value>` expression into a
// FilterExpression.
//
// The separator is the *earliest* operator in the string so that operator
// characters inside the value — a regex, a URL with '=' in a query string —
// stay part of the value. On a tie at the same index the longest token wins.
func parseDimensionFilter(expr string, caseSensitive bool) (*analyticsdata.FilterExpression, error) {
	field, op, value, ok := splitOnOperator(expr, len(dimensionOps), func(i int) string { return dimensionOps[i].token })
	if !ok {
		return nil, errs.Newf(errs.CodeInvalidArgs,
			"filter must be <dimension><op><value> with op in =, !=, ~, !~, ~~, !~~, ^=, $=, @=: %q", expr).
			WithHint("Example: --filter country=Germany --filter 'pagePath~~^/blog/'")
	}
	spec := dimensionOps[op]

	f := &analyticsdata.Filter{FieldName: field}
	if spec.inList {
		values := parseCSV(value)
		if len(values) == 0 {
			return nil, errs.Newf(errs.CodeInvalidArgs, "filter %q: @= needs a comma-separated value list", expr)
		}
		f.InListFilter = &analyticsdata.InListFilter{Values: values, CaseSensitive: caseSensitive}
	} else {
		if value == "" {
			return nil, errs.Newf(errs.CodeInvalidArgs, "filter %q: empty value", expr)
		}
		f.StringFilter = &analyticsdata.StringFilter{
			MatchType:     spec.matchType,
			Value:         value,
			CaseSensitive: caseSensitive,
		}
	}

	fe := &analyticsdata.FilterExpression{Filter: f}
	if spec.negate {
		return &analyticsdata.FilterExpression{NotExpression: fe}, nil
	}
	return fe, nil
}

// Metric filter operators, again longest-first.
var metricOps = []struct {
	token     string
	operation string
	negate    bool
}{
	{token: ">=", operation: "GREATER_THAN_OR_EQUAL"},
	{token: "<=", operation: "LESS_THAN_OR_EQUAL"},
	{token: "!=", operation: "EQUAL", negate: true},
	{token: ">", operation: "GREATER_THAN"},
	{token: "<", operation: "LESS_THAN"},
	{token: "=", operation: "EQUAL"},
}

// parseMetricFilter turns one `<metric><op><number>` expression into a
// FilterExpression. `metric=10..100` expresses an inclusive between-filter.
func parseMetricFilter(expr string) (*analyticsdata.FilterExpression, error) {
	field, op, value, ok := splitOnOperator(expr, len(metricOps), func(i int) string { return metricOps[i].token })
	if !ok {
		return nil, errs.Newf(errs.CodeInvalidArgs,
			"metric filter must be <metric><op><number> with op in =, !=, >, >=, <, <=: %q", expr).
			WithHint("Examples: --metric-filter 'sessions>100' --metric-filter 'bounceRate<=0.4' --metric-filter 'sessions=10..100'")
	}
	spec := metricOps[op]

	f := &analyticsdata.Filter{FieldName: field}
	if lo, hi, isRange := strings.Cut(value, ".."); isRange && spec.operation == "EQUAL" && !spec.negate {
		from, err := numericValue(expr, lo)
		if err != nil {
			return nil, err
		}
		to, err := numericValue(expr, hi)
		if err != nil {
			return nil, err
		}
		f.BetweenFilter = &analyticsdata.BetweenFilter{FromValue: from, ToValue: to}
	} else {
		nv, err := numericValue(expr, value)
		if err != nil {
			return nil, err
		}
		f.NumericFilter = &analyticsdata.NumericFilter{Operation: spec.operation, Value: nv}
	}

	fe := &analyticsdata.FilterExpression{Filter: f}
	if spec.negate {
		return &analyticsdata.FilterExpression{NotExpression: fe}, nil
	}
	return fe, nil
}

func numericValue(expr, raw string) (*analyticsdata.NumericValue, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errs.Newf(errs.CodeInvalidArgs, "metric filter %q: empty value", expr)
	}
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return &analyticsdata.NumericValue{Int64Value: i, ForceSendFields: []string{"Int64Value"}}, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, errs.Newf(errs.CodeInvalidArgs, "metric filter %q: %q is not a number", expr, raw)
	}
	return &analyticsdata.NumericValue{DoubleValue: f, ForceSendFields: []string{"DoubleValue"}}, nil
}

// splitOnOperator finds the earliest operator token in expr, breaking ties by
// token length. It returns the field name, the index of the matched operator,
// and the remaining value.
func splitOnOperator(expr string, n int, tokenAt func(int) string) (field string, op int, value string, ok bool) {
	best := -1
	bestOp := -1
	for i := 0; i < n; i++ {
		tok := tokenAt(i)
		idx := strings.Index(expr, tok)
		if idx <= 0 { // a leading operator means there is no field name
			continue
		}
		if best == -1 || idx < best || (idx == best && len(tok) > len(tokenAt(bestOp))) {
			best, bestOp = idx, i
		}
	}
	if best <= 0 {
		return "", 0, "", false
	}
	tok := tokenAt(bestOp)
	return expr[:best], bestOp, expr[best+len(tok):], true
}

// andGroup combines expressions with AND, collapsing the single-expression
// case so simple requests stay simple on the wire.
func andGroup(exprs []*analyticsdata.FilterExpression) *analyticsdata.FilterExpression {
	switch len(exprs) {
	case 0:
		return nil
	case 1:
		return exprs[0]
	default:
		return &analyticsdata.FilterExpression{
			AndGroup: &analyticsdata.FilterExpressionList{Expressions: exprs},
		}
	}
}

func orGroup(exprs []*analyticsdata.FilterExpression) *analyticsdata.FilterExpression {
	switch len(exprs) {
	case 0:
		return nil
	case 1:
		return exprs[0]
	default:
		return &analyticsdata.FilterExpression{
			OrGroup: &analyticsdata.FilterExpressionList{Expressions: exprs},
		}
	}
}

// buildDimensionFilter assembles --filter (AND) and --filter-group (OR of AND)
// into one expression. The two flags are mutually exclusive: --filter-group is
// a strict superset, and mixing them makes the precedence ambiguous.
func buildDimensionFilter(filters, groups []string, caseSensitive bool) (*analyticsdata.FilterExpression, error) {
	if len(filters) > 0 && len(groups) > 0 {
		return nil, errs.New(errs.CodeInvalidArgs, "--filter and --filter-group are mutually exclusive").
			WithHint("--filter-group is a superset; express a single AND group there.")
	}
	if len(filters) > 0 {
		exprs := make([]*analyticsdata.FilterExpression, 0, len(filters))
		for _, f := range filters {
			fe, err := parseDimensionFilter(f, caseSensitive)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, fe)
		}
		return andGroup(exprs), nil
	}
	if len(groups) == 0 {
		return nil, nil
	}
	ors := make([]*analyticsdata.FilterExpression, 0, len(groups))
	for i, g := range groups {
		parts := strings.Split(g, ",")
		ands := make([]*analyticsdata.FilterExpression, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				return nil, errs.Newf(errs.CodeInvalidArgs, "invalid --filter-group[%d]: empty filter expression", i)
			}
			fe, err := parseDimensionFilter(p, caseSensitive)
			if err != nil {
				return nil, errs.Newf(errs.CodeInvalidArgs, "invalid --filter-group[%d]: %s", i, err.Error())
			}
			ands = append(ands, fe)
		}
		ors = append(ors, andGroup(ands))
	}
	return orGroup(ors), nil
}

// buildMetricFilter assembles repeated --metric-filter flags into an AND group.
func buildMetricFilter(filters []string) (*analyticsdata.FilterExpression, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	exprs := make([]*analyticsdata.FilterExpression, 0, len(filters))
	for _, f := range filters {
		fe, err := parseMetricFilter(f)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, fe)
	}
	return andGroup(exprs), nil
}

// buildOrderBys turns `--order-by` values into OrderBy specs. Accepted forms:
// "sessions" (descending for metrics, ascending for dimensions), "-sessions",
// "sessions:desc", "pagePath:asc". A name is classified as a metric or a
// dimension by looking at what the request already asks for, so a typo is
// caught here rather than as an opaque 400 from the API.
func buildOrderBys(specs []string, dims, metrics []string) ([]*analyticsdata.OrderBy, error) {
	out := make([]*analyticsdata.OrderBy, 0, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec)
		if name == "" {
			continue
		}
		desc := false
		explicit := false
		if strings.HasPrefix(name, "-") {
			name, desc, explicit = strings.TrimPrefix(name, "-"), true, true
		}
		if base, dir, found := strings.Cut(name, ":"); found {
			switch strings.ToLower(dir) {
			case "desc":
				desc, explicit = true, true
			case "asc":
				desc, explicit = false, true
			default:
				return nil, errs.Newf(errs.CodeInvalidArgs, "invalid --order-by direction %q in %q", dir, spec).
					WithHint("Use name:asc or name:desc.")
			}
			name = base
		}

		ob := &analyticsdata.OrderBy{}
		switch {
		case contains(metrics, name):
			ob.Metric = &analyticsdata.MetricOrderBy{MetricName: name}
			// "Top N by sessions" is what a bare metric almost always means.
			if !explicit {
				desc = true
			}
		case contains(dims, name):
			ob.Dimension = &analyticsdata.DimensionOrderBy{DimensionName: name}
		default:
			return nil, errs.Newf(errs.CodeInvalidArgs, "--order-by %q is not in --dimensions or --metrics", name).
				WithHint("Order by a field the report actually selects.")
		}
		ob.Desc = desc
		if desc {
			ob.ForceSendFields = append(ob.ForceSendFields, "Desc")
		}
		out = append(out, ob)
	}
	return out, nil
}
