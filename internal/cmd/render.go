package cmd

import (
	"strconv"

	"github.com/KLIXPERT-io/ga4-cli/internal/output"
	"github.com/KLIXPERT-io/ga4-cli/internal/quota"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

// reportPayload is the normalized shape every report command caches and emits.
//
// The Data API answers with parallel arrays — headers in one list, per-row
// dimensionValues and metricValues in others — which is compact on the wire but
// awkward for anything downstream. Flattening each row into a single object
// keyed by field name makes the JSON self-describing, gives CSV/table rendering
// its columns for free, and means an agent never has to zip two arrays to read
// a number.
type reportPayload struct {
	Property         string          `json:"property"`
	DateRanges       []dateRangeOut  `json:"date_ranges,omitempty"`
	DimensionHeaders []string        `json:"dimension_headers"`
	MetricHeaders    []metricHeader  `json:"metric_headers"`
	Rows             []output.Row    `json:"rows"`
	Totals           []output.Row    `json:"totals,omitempty"`
	RowCount         int64           `json:"row_count"`
	SubjectToThresh  bool            `json:"subject_to_thresholding,omitempty"`
	SamplingApplied  bool            `json:"sampling_applied,omitempty"`
	CurrencyCode     string          `json:"currency_code,omitempty"`
	TimeZone         string          `json:"time_zone,omitempty"`
	PropertyQuota    *quota.Property `json:"property_quota,omitempty"`
}

type dateRangeOut struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type metricHeader struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// columns returns the CSV/table column order: dimensions first, then metrics,
// matching the order the API returned them in.
func (p *reportPayload) columns() []string {
	cols := make([]string, 0, len(p.DimensionHeaders)+len(p.MetricHeaders))
	cols = append(cols, p.DimensionHeaders...)
	for _, m := range p.MetricHeaders {
		cols = append(cols, m.Name)
	}
	return cols
}

// flattenRows zips the API's parallel arrays into one object per row.
func flattenRows(dimHeaders []string, metHeaders []metricHeader, rows []*analyticsdata.Row) []output.Row {
	out := make([]output.Row, 0, len(rows))
	for _, r := range rows {
		row := make(output.Row, len(dimHeaders)+len(metHeaders))
		for i, h := range dimHeaders {
			if i < len(r.DimensionValues) {
				row[h] = r.DimensionValues[i].Value
			}
		}
		for i, h := range metHeaders {
			if i < len(r.MetricValues) {
				row[h.Name] = typedMetric(r.MetricValues[i].Value, h.Type)
			}
		}
		out = append(out, row)
	}
	return out
}

// typedMetric converts the API's stringly-typed metric value into a real JSON
// number where the declared metric type says it is one, so consumers can do
// arithmetic without parsing. Anything unrecognized stays a string rather than
// risking a lossy conversion.
func typedMetric(raw, metricType string) any {
	switch metricType {
	case "TYPE_INTEGER":
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return i
		}
	case "TYPE_FLOAT", "TYPE_SECONDS", "TYPE_MILLISECONDS", "TYPE_MINUTES", "TYPE_HOURS",
		"TYPE_STANDARD", "TYPE_CURRENCY", "TYPE_FEET", "TYPE_MILES", "TYPE_METERS", "TYPE_KILOMETERS":
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f
		}
	}
	return raw
}

func dimensionNames(hs []*analyticsdata.DimensionHeader) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func metricHeaders(hs []*analyticsdata.MetricHeader) []metricHeader {
	out := make([]metricHeader, 0, len(hs))
	for _, h := range hs {
		out = append(out, metricHeader{Name: h.Name, Type: h.Type})
	}
	return out
}

// toQuota converts the API's PropertyQuota into the storable form.
func toQuota(pq *analyticsdata.PropertyQuota) *quota.Property {
	if pq == nil {
		return nil
	}
	st := func(s *analyticsdata.QuotaStatus) *quota.Status {
		if s == nil {
			return nil
		}
		return &quota.Status{Consumed: s.Consumed, Remaining: s.Remaining}
	}
	return &quota.Property{
		TokensPerDay:                          st(pq.TokensPerDay),
		TokensPerHour:                         st(pq.TokensPerHour),
		TokensPerProjectPerHour:               st(pq.TokensPerProjectPerHour),
		ConcurrentRequests:                    st(pq.ConcurrentRequests),
		ServerErrorsPerProjectPerHour:         st(pq.ServerErrorsPerProjectPerHour),
		PotentiallyThresholdedRequestsPerHour: st(pq.PotentiallyThresholdedRequestsPerHour),
	}
}
