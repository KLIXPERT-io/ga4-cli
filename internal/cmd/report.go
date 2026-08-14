package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KLIXPERT-io/ga4-cli/internal/cache"
	"github.com/KLIXPERT-io/ga4-cli/internal/client"
	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	"github.com/KLIXPERT-io/ga4-cli/internal/output"
	"github.com/KLIXPERT-io/ga4-cli/internal/quota"
	"github.com/spf13/cobra"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

// maxRowsPerRequest is the API's hard cap on rows returned by a single call.
const maxRowsPerRequest = 250000

func newReportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "report",
		Short: "Run Data API reports (core, realtime, pivot, and presets)",
	}
	c.AddCommand(newReportRunCmd(), newReportRealtimeCmd(), newReportPivotCmd())
	for _, p := range presets {
		c.AddCommand(newPresetCmd(p))
	}
	return c
}

// reportFlags carries everything `report run` and the presets share.
type reportFlags struct {
	Range         rangeFlags
	Dimensions    []string
	Metrics       []string
	Filters       []string
	FilterGroups  []string
	MetricFilters []string
	OrderBy       []string
	Limit         int64
	Offset        int64
	All           bool
	KeepEmptyRows bool
	CaseSensitive bool
	Currency      string
	Totals        bool
	ShowQuota     bool
}

func (rf *reportFlags) addCommon(cmd *cobra.Command) {
	addRangeFlags(cmd, &rf.Range)
	cmd.Flags().StringArrayVar(&rf.Filters, "filter", nil, "dimension filter <dim><op><value>, op in = != ~ !~ ~~ !~~ ^= $= @= (repeatable, ANDed)")
	cmd.Flags().StringArrayVar(&rf.FilterGroups, "filter-group", nil, "OR-of-AND group: comma-separated filters form one AND group; repeat the flag for OR (mutually exclusive with --filter)")
	cmd.Flags().StringArrayVar(&rf.MetricFilters, "metric-filter", nil, "metric filter <metric><op><number>, op in = != > >= < <=, or metric=lo..hi (repeatable, ANDed)")
	cmd.Flags().StringArrayVar(&rf.OrderBy, "order-by", nil, "sort by a selected field: name, -name, name:asc, name:desc (repeatable)")
	cmd.Flags().Int64Var(&rf.Limit, "limit", 20, "row limit (max 250000; page size when --all)")
	cmd.Flags().Int64Var(&rf.Offset, "offset", 0, "row offset for manual pagination")
	cmd.Flags().BoolVar(&rf.All, "all", false, "auto-paginate until every matching row is fetched (uses --limit as page size)")
	cmd.Flags().BoolVar(&rf.KeepEmptyRows, "keep-empty-rows", false, "include rows whose metrics are all zero")
	cmd.Flags().BoolVar(&rf.CaseSensitive, "case-sensitive", false, "make string filters case sensitive")
	cmd.Flags().StringVar(&rf.Currency, "currency", "", "ISO 4217 currency for revenue metrics (default: the property's own)")
	cmd.Flags().BoolVar(&rf.Totals, "totals", false, "include a totals row across all matching rows")
	cmd.Flags().BoolVar(&rf.ShowQuota, "show-quota", false, "include the property's live token quota in the response")
}

func newReportRunCmd() *cobra.Command {
	rf := &reportFlags{}
	c := &cobra.Command{
		Use:   "run [property]",
		Short: "Run a core report with any dimensions, metrics, filters, and date range",
		Long: `Runs properties.runReport against the Data API v1beta.

The property defaults to defaults.property from config when omitted. Field
names are the API names (activeUsers, sessionSource, pagePath, …) — look them
up with ` + "`ga4 metadata`" + ` and check a combination with ` + "`ga4 compat`" + `.

Examples:
  # top 50 pages by views over the last 28 days
  ga4 report run 123456789 --dimensions pagePath --metrics screenPageViews,activeUsers --limit 50

  # daily time series as CSV
  ga4 report run 123456789 --dimensions date --metrics sessions --range last-90d --output csv

  # organic search sessions only, compared to the previous year
  ga4 report run 123456789 --dimensions sessionDefaultChannelGroup --metrics sessions \
    --filter sessionDefaultChannelGroup=Organic\ Search --compare previous-year

  # pages under /blog/ with more than 100 views, biggest first
  ga4 report run 123456789 --dimensions pagePath --metrics screenPageViews \
    --filter 'pagePath~~^/blog/' --metric-filter 'screenPageViews>100' --order-by -screenPageViews

  # OR-of-AND: (mobile AND Germany) OR (desktop)
  ga4 report run 123456789 --dimensions deviceCategory,country --metrics sessions \
    --filter-group 'deviceCategory=mobile,country=Germany' --filter-group 'deviceCategory=desktop'

  # every row, streamed to CSV
  ga4 report run 123456789 --dimensions pagePath --metrics screenPageViews --all --output csv > pages.csv`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			if len(rf.Metrics) == 0 {
				return errs.New(errs.CodeInvalidArgs, "--metrics is required").
					WithHint("Start with --metrics activeUsers, or list what the property supports with `ga4 metadata`.")
			}
			return runCoreReport(ctx, cmd, s, property, rf)
		},
	}
	rf.addCommon(c)
	c.Flags().StringArrayVar(&rf.Dimensions, "dimensions", nil, "comma list of dimension API names, e.g. date,pagePath,country (repeatable)")
	c.Flags().StringArrayVar(&rf.Metrics, "metrics", nil, "comma list of metric API names, e.g. activeUsers,sessions (repeatable, required)")
	return c
}

// runCoreReport builds, executes, caches, and renders a runReport call.
func runCoreReport(ctx context.Context, cmd *cobra.Command, s *State, property string, rf *reportFlags) error {
	dims := parseCSVAll(rf.Dimensions)
	metrics := parseCSVAll(rf.Metrics)

	start, end, err := rf.Range.resolve(s.Cfg.Defaults.Range)
	if err != nil {
		return err
	}
	dateRanges, err := rf.Range.dateRanges(start, end)
	if err != nil {
		return err
	}
	dimFilter, err := buildDimensionFilter(rf.Filters, rf.FilterGroups, rf.CaseSensitive)
	if err != nil {
		return err
	}
	metricFilter, err := buildMetricFilter(rf.MetricFilters)
	if err != nil {
		return err
	}
	// With more than one date range the API prepends a `dateRange` dimension to
	// every row, and it is orderable like any other selected field.
	orderable := dims
	if len(dateRanges) > 1 {
		orderable = append(append([]string{}, dims...), "dateRange")
	}
	orderBys, err := buildOrderBys(rf.OrderBy, orderable, metrics)
	if err != nil {
		return err
	}
	if len(orderBys) == 0 && len(metrics) > 0 && len(dims) > 0 {
		// Without an explicit sort the API returns rows in an unspecified
		// order; "top N by the first metric" is the near-universal intent.
		orderBys = []*analyticsdata.OrderBy{{
			Metric:          &analyticsdata.MetricOrderBy{MetricName: metrics[0]},
			Desc:            true,
			ForceSendFields: []string{"Desc"},
		}}
	}
	limit, err := clampLimit(rf.Limit)
	if err != nil {
		return err
	}

	req := &analyticsdata.RunReportRequest{
		DateRanges:          dateRanges,
		Dimensions:          toDimensions(dims),
		Metrics:             toMetrics(metrics),
		DimensionFilter:     dimFilter,
		MetricFilter:        metricFilter,
		OrderBys:            orderBys,
		Limit:               limit,
		Offset:              rf.Offset,
		KeepEmptyRows:       rf.KeepEmptyRows,
		CurrencyCode:        firstNonEmpty(rf.Currency, s.Cfg.Defaults.Currency),
		ReturnPropertyQuota: true,
	}
	if rf.Totals {
		req.MetricAggregations = []string{"TOTAL"}
	}

	c, identity, err := s.buildClient(ctx)
	if err != nil {
		return err
	}

	key := cache.Key("report.run", reportCacheArgs(rf, start, end, dims, metrics, limit), property, identity)
	data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
		payload, err := executeReport(ctx, s, c, property, req, rf.All)
		if err != nil {
			return nil, err
		}
		return json.Marshal(payload)
	})
	if err != nil {
		return err
	}
	return emitReport(cmd, data, meta, rf.ShowQuota)
}

// executeReport runs one report, following pagination when --all is set.
func executeReport(ctx context.Context, s *State, c *client.Client, property string, req *analyticsdata.RunReportRequest, all bool) (*reportPayload, error) {
	payload := &reportPayload{Property: property}
	for _, dr := range req.DateRanges {
		payload.DateRanges = append(payload.DateRanges, dateRangeOut{Name: dr.Name, Start: dr.StartDate, End: dr.EndDate})
	}

	pageSize := req.Limit
	for {
		_ = s.Quota.Bump(quota.CategoryCore, 1)
		resp, err := c.Data.Properties.RunReport(property, req).Context(ctx).Do()
		if err != nil {
			return nil, client.Translate(err)
		}
		if q := toQuota(resp.PropertyQuota); q != nil {
			_ = s.Quota.Record(property, q)
			payload.PropertyQuota = q
		}
		if payload.DimensionHeaders == nil {
			payload.DimensionHeaders = dimensionNames(resp.DimensionHeaders)
			payload.MetricHeaders = metricHeaders(resp.MetricHeaders)
			payload.RowCount = resp.RowCount
			if md := resp.Metadata; md != nil {
				payload.SubjectToThresh = md.SubjectToThresholding
				payload.CurrencyCode = md.CurrencyCode
				payload.TimeZone = md.TimeZone
				payload.SamplingApplied = len(md.SamplingMetadatas) > 0
			}
			payload.Totals = flattenRows(payload.DimensionHeaders, payload.MetricHeaders, resp.Totals)
		}
		payload.Rows = append(payload.Rows, flattenRows(payload.DimensionHeaders, payload.MetricHeaders, resp.Rows)...)

		if !all || len(resp.Rows) == 0 || int64(len(payload.Rows)) >= resp.RowCount {
			break
		}
		req.Offset += pageSize
		req.ForceSendFields = appendUnique(req.ForceSendFields, "Offset")
	}
	return payload, nil
}

func newReportRealtimeCmd() *cobra.Command {
	var (
		dimensions []string
		metrics    []string
		filters    []string
		orderBy    []string
		limit      int64
		minutesAgo int64
		showQuota  bool
		caseSens   bool
	)
	c := &cobra.Command{
		Use:   "realtime [property]",
		Short: "Report on activity in the last 30 minutes",
		Long: `Runs properties.runRealtimeReport, which covers roughly the last 30 minutes
of events. Realtime supports a smaller field set than core reports — activeUsers,
screenPageViews, eventCount, unifiedScreenName, country, deviceCategory, and
similar. Results are cached for one minute by default.

Examples:
  ga4 report realtime 123456789
  ga4 report realtime 123456789 --dimensions unifiedScreenName --metrics activeUsers --limit 20
  ga4 report realtime 123456789 --dimensions country --minutes-ago 5`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			dims := parseCSVAll(dimensions)
			mets := parseCSVAll(metrics)
			if len(mets) == 0 {
				mets = []string{"activeUsers"}
			}
			dimFilter, err := buildDimensionFilter(filters, nil, caseSens)
			if err != nil {
				return err
			}
			orderBys, err := buildOrderBys(orderBy, dims, mets)
			if err != nil {
				return err
			}
			if len(orderBys) == 0 && len(dims) > 0 {
				orderBys = []*analyticsdata.OrderBy{{
					Metric:          &analyticsdata.MetricOrderBy{MetricName: mets[0]},
					Desc:            true,
					ForceSendFields: []string{"Desc"},
				}}
			}
			limit, err := clampLimit(limit)
			if err != nil {
				return err
			}
			req := &analyticsdata.RunRealtimeReportRequest{
				Dimensions:          toDimensions(dims),
				Metrics:             toMetrics(mets),
				DimensionFilter:     dimFilter,
				OrderBys:            orderBys,
				Limit:               limit,
				ReturnPropertyQuota: true,
			}
			if minutesAgo > 0 {
				if minutesAgo > 29 {
					return errs.New(errs.CodeInvalidArgs, "--minutes-ago must be between 1 and 29").
						WithHint("Realtime data only covers the last 30 minutes.")
				}
				req.MinuteRanges = []*analyticsdata.MinuteRange{{
					Name:            "recent",
					StartMinutesAgo: minutesAgo,
					EndMinutesAgo:   0,
					ForceSendFields: []string{"EndMinutesAgo"},
				}}
			}

			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			keyArgs := []string{strings.Join(dims, ","), strings.Join(mets, ","), strings.Join(filters, "|"),
				strings.Join(orderBy, "|"), fmt.Sprintf("limit=%d", limit), fmt.Sprintf("minutes=%d", minutesAgo)}
			key := cache.Key("report.realtime", keyArgs, property, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.RealtimeTTL(), func(ctx context.Context) (json.RawMessage, error) {
				_ = s.Quota.Bump(quota.CategoryRealtime, 1)
				resp, err := c.Data.Properties.RunRealtimeReport(property, req).Context(ctx).Do()
				if err != nil {
					return nil, client.Translate(err)
				}
				payload := &reportPayload{
					Property:         property,
					DimensionHeaders: dimensionNames(resp.DimensionHeaders),
					MetricHeaders:    metricHeaders(resp.MetricHeaders),
					RowCount:         resp.RowCount,
				}
				payload.Rows = flattenRows(payload.DimensionHeaders, payload.MetricHeaders, resp.Rows)
				payload.Totals = flattenRows(payload.DimensionHeaders, payload.MetricHeaders, resp.Totals)
				if q := toQuota(resp.PropertyQuota); q != nil {
					_ = s.Quota.Record(property, q)
					payload.PropertyQuota = q
				}
				return json.Marshal(payload)
			})
			if err != nil {
				return err
			}
			return emitReport(cmd, data, meta, showQuota)
		},
	}
	c.Flags().StringArrayVar(&dimensions, "dimensions", nil, "comma list of realtime dimension API names (repeatable)")
	c.Flags().StringArrayVar(&metrics, "metrics", nil, "comma list of realtime metric API names (default activeUsers)")
	c.Flags().StringArrayVar(&filters, "filter", nil, "dimension filter <dim><op><value> (repeatable, ANDed)")
	c.Flags().StringArrayVar(&orderBy, "order-by", nil, "sort by a selected field: name, -name, name:asc, name:desc (repeatable)")
	c.Flags().Int64Var(&limit, "limit", 20, "row limit")
	c.Flags().Int64Var(&minutesAgo, "minutes-ago", 0, "restrict to the last N minutes (1-29; default: the full ~30 minute window)")
	c.Flags().BoolVar(&caseSens, "case-sensitive", false, "make string filters case sensitive")
	c.Flags().BoolVar(&showQuota, "show-quota", false, "include the property's live token quota in the response")
	return c
}

func newReportPivotCmd() *cobra.Command {
	rf := &reportFlags{}
	var pivots []string
	c := &cobra.Command{
		Use:   "pivot [property]",
		Short: "Run a pivot report (cross-tabulate one dimension against another)",
		Long: `Runs properties.runPivotReport. Each --pivot takes the dimensions that form
one axis and, optionally, how many of its values to keep:

  --pivot <dim>[,<dim>...][:<limit>]

Every dimension named in a pivot must also appear in --dimensions.

Examples:
  # channels down the side, device across the top
  ga4 report pivot 123456789 --dimensions sessionDefaultChannelGroup,deviceCategory \
    --metrics sessions --pivot sessionDefaultChannelGroup:10 --pivot deviceCategory:3

  # month over month by country
  ga4 report pivot 123456789 --dimensions month,country --metrics activeUsers \
    --pivot month:12 --pivot country:5 --range last-12m`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			dims := parseCSVAll(rf.Dimensions)
			metrics := parseCSVAll(rf.Metrics)
			if len(metrics) == 0 {
				return errs.New(errs.CodeInvalidArgs, "--metrics is required")
			}
			if len(pivots) == 0 {
				return errs.New(errs.CodeInvalidArgs, "at least one --pivot is required").
					WithHint("Example: --pivot country:10")
			}
			start, end, err := rf.Range.resolve(s.Cfg.Defaults.Range)
			if err != nil {
				return err
			}
			dateRanges, err := rf.Range.dateRanges(start, end)
			if err != nil {
				return err
			}
			dimFilter, err := buildDimensionFilter(rf.Filters, rf.FilterGroups, rf.CaseSensitive)
			if err != nil {
				return err
			}
			metricFilter, err := buildMetricFilter(rf.MetricFilters)
			if err != nil {
				return err
			}
			parsed, err := parsePivots(pivots, dims, metrics)
			if err != nil {
				return err
			}
			req := &analyticsdata.RunPivotReportRequest{
				DateRanges:          dateRanges,
				Dimensions:          toDimensions(dims),
				Metrics:             toMetrics(metrics),
				DimensionFilter:     dimFilter,
				MetricFilter:        metricFilter,
				Pivots:              parsed,
				CurrencyCode:        firstNonEmpty(rf.Currency, s.Cfg.Defaults.Currency),
				KeepEmptyRows:       rf.KeepEmptyRows,
				ReturnPropertyQuota: true,
			}

			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			keyArgs := append(reportCacheArgs(rf, start, end, dims, metrics, 0), "pivots="+strings.Join(pivots, "|"))
			key := cache.Key("report.pivot", keyArgs, property, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
				_ = s.Quota.Bump(quota.CategoryCore, 1)
				resp, err := c.Data.Properties.RunPivotReport(property, req).Context(ctx).Do()
				if err != nil {
					return nil, client.Translate(err)
				}
				payload := &reportPayload{
					Property:         property,
					DimensionHeaders: dimensionNames(resp.DimensionHeaders),
					MetricHeaders:    metricHeaders(resp.MetricHeaders),
				}
				for _, dr := range dateRanges {
					payload.DateRanges = append(payload.DateRanges, dateRangeOut{Name: dr.Name, Start: dr.StartDate, End: dr.EndDate})
				}
				payload.Rows = flattenRows(payload.DimensionHeaders, payload.MetricHeaders, resp.Rows)
				payload.Totals = flattenRows(payload.DimensionHeaders, payload.MetricHeaders, resp.Aggregates)
				payload.RowCount = int64(len(payload.Rows))
				if md := resp.Metadata; md != nil {
					payload.SubjectToThresh = md.SubjectToThresholding
					payload.CurrencyCode = md.CurrencyCode
					payload.TimeZone = md.TimeZone
				}
				if q := toQuota(resp.PropertyQuota); q != nil {
					_ = s.Quota.Record(property, q)
					payload.PropertyQuota = q
				}
				return json.Marshal(payload)
			})
			if err != nil {
				return err
			}
			return emitReport(cmd, data, meta, rf.ShowQuota)
		},
	}
	rf.addCommon(c)
	c.Flags().StringArrayVar(&rf.Dimensions, "dimensions", nil, "comma list of dimension API names (repeatable)")
	c.Flags().StringArrayVar(&rf.Metrics, "metrics", nil, "comma list of metric API names (repeatable, required)")
	c.Flags().StringArrayVar(&pivots, "pivot", nil, "pivot axis: <dim>[,<dim>][:<limit>] (repeatable, at least one required)")
	// Pivot pagination works per axis via the pivot's own limit/offset.
	_ = c.Flags().MarkHidden("all")
	_ = c.Flags().MarkHidden("limit")
	_ = c.Flags().MarkHidden("offset")
	return c
}

// parsePivots turns `<dim>[,<dim>][:<limit>]` specs into Pivot objects.
func parsePivots(specs, dims, metrics []string) ([]*analyticsdata.Pivot, error) {
	out := make([]*analyticsdata.Pivot, 0, len(specs))
	for i, spec := range specs {
		fields, limitPart, hasLimit := strings.Cut(spec, ":")
		names := parseCSV(fields)
		if len(names) == 0 {
			return nil, errs.Newf(errs.CodeInvalidArgs, "invalid --pivot[%d]: no dimensions given", i)
		}
		for _, n := range names {
			if !contains(dims, n) {
				return nil, errs.Newf(errs.CodeInvalidArgs, "--pivot names %q, which is not in --dimensions", n).
					WithHint("Every pivoted dimension must also be selected by --dimensions.")
			}
		}
		p := &analyticsdata.Pivot{FieldNames: names, Limit: 10}
		if hasLimit {
			var n int64
			if _, err := fmt.Sscanf(limitPart, "%d", &n); err != nil || n <= 0 {
				return nil, errs.Newf(errs.CodeInvalidArgs, "invalid --pivot[%d] limit %q", i, limitPart)
			}
			p.Limit = n
		}
		if len(metrics) > 0 {
			p.OrderBys = []*analyticsdata.OrderBy{{
				Metric:          &analyticsdata.MetricOrderBy{MetricName: metrics[0]},
				Desc:            true,
				ForceSendFields: []string{"Desc"},
			}}
		}
		out = append(out, p)
	}
	return out, nil
}

// emitReport decodes the cached payload and renders it in the chosen format.
func emitReport(cmd *cobra.Command, data json.RawMessage, meta output.Meta, showQuota bool) error {
	var payload reportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return errs.New(errs.CodeGeneric, err.Error())
	}
	meta.RowCount = payload.RowCount
	// Thresholded and sampled reports are answers to a slightly different
	// question than the one that was asked, so flag them in the envelope.
	if payload.SubjectToThresh || payload.SamplingApplied {
		meta.Partial = true
	}
	if !showQuota {
		payload.PropertyQuota = nil
	}
	return emit(cmd, payload, meta, payload.columns(), payload.Rows)
}

func toDimensions(names []string) []*analyticsdata.Dimension {
	out := make([]*analyticsdata.Dimension, 0, len(names))
	for _, n := range names {
		out = append(out, &analyticsdata.Dimension{Name: n})
	}
	return out
}

func toMetrics(names []string) []*analyticsdata.Metric {
	out := make([]*analyticsdata.Metric, 0, len(names))
	for _, n := range names {
		out = append(out, &analyticsdata.Metric{Name: n})
	}
	return out
}

func clampLimit(limit int64) (int64, error) {
	if limit <= 0 {
		return 0, errs.New(errs.CodeInvalidArgs, "--limit must be positive")
	}
	if limit > maxRowsPerRequest {
		return maxRowsPerRequest, nil
	}
	return limit, nil
}

// reportCacheArgs captures every input that can change the result, so a cache
// hit can only ever come from an identical question.
func reportCacheArgs(rf *reportFlags, start, end string, dims, metrics []string, limit int64) []string {
	return []string{
		"start=" + start,
		"end=" + end,
		"compare=" + rf.Range.Compare,
		"dims=" + strings.Join(dims, ","),
		"metrics=" + strings.Join(metrics, ","),
		"filters=" + strings.Join(rf.Filters, "|"),
		"groups=" + strings.Join(rf.FilterGroups, "|"),
		"metricfilters=" + strings.Join(rf.MetricFilters, "|"),
		"orderby=" + strings.Join(rf.OrderBy, "|"),
		fmt.Sprintf("limit=%d", limit),
		fmt.Sprintf("offset=%d", rf.Offset),
		fmt.Sprintf("all=%v", rf.All),
		fmt.Sprintf("keepempty=%v", rf.KeepEmptyRows),
		fmt.Sprintf("case=%v", rf.CaseSensitive),
		fmt.Sprintf("totals=%v", rf.Totals),
		"currency=" + rf.Currency,
	}
}

func appendUnique(ss []string, v string) []string {
	if contains(ss, v) {
		return ss
	}
	return append(ss, v)
}
