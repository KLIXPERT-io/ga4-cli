package cmd

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/KLIXPERT-io/ga4-cli/internal/cache"
	"github.com/KLIXPERT-io/ga4-cli/internal/client"
	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	"github.com/KLIXPERT-io/ga4-cli/internal/output"
	"github.com/KLIXPERT-io/ga4-cli/internal/quota"
	"github.com/spf13/cobra"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

// fieldRow is one dimension or metric in the property's schema.
type fieldRow struct {
	Kind        string `json:"kind"` // "dimension" or "metric"
	APIName     string `json:"api_name"`
	UIName      string `json:"ui_name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Type        string `json:"type,omitempty"`
	Custom      bool   `json:"custom_definition,omitempty"`
}

func newMetadataCmd() *cobra.Command {
	var (
		kind       string
		search     string
		customOnly bool
	)
	c := &cobra.Command{
		Use:   "metadata [property]",
		Short: "List the dimensions and metrics a property supports",
		Long: `Returns every dimension and metric available for the property, including its
custom definitions and any event-scoped fields — this is where the exact API
names for --dimensions and --metrics come from.

Examples:
  ga4 metadata 123456789
  ga4 metadata 123456789 --search revenue
  ga4 metadata 123456789 --kind metrics --output csv
  ga4 metadata 123456789 --custom-only
  # the universal schema, without naming a property:
  ga4 metadata 0`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			// Property 0 is the API's own alias for the universal schema, so it
			// has to survive normalization.
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			switch kind {
			case "all", "dimensions", "metrics":
			default:
				return errs.New(errs.CodeInvalidArgs, "--kind must be one of: all, dimensions, metrics")
			}

			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			key := cache.Key("metadata", nil, property, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.MetadataTTL(), func(ctx context.Context) (json.RawMessage, error) {
				_ = s.Quota.Bump(quota.CategoryAdmin, 1)
				resp, err := c.Data.Properties.GetMetadata(property + "/metadata").Context(ctx).Do()
				if err != nil {
					return nil, client.Translate(err)
				}
				return json.Marshal(collectFields(resp))
			})
			if err != nil {
				return err
			}
			var fields []fieldRow
			if err := json.Unmarshal(data, &fields); err != nil {
				return errs.New(errs.CodeGeneric, err.Error())
			}
			fields = filterFields(fields, kind, search, customOnly)

			cols := []string{"kind", "api_name", "ui_name", "type", "category", "custom_definition", "description"}
			rows := make([]output.Row, 0, len(fields))
			for _, f := range fields {
				rows = append(rows, output.Row{
					"kind": f.Kind, "api_name": f.APIName, "ui_name": f.UIName,
					"type": f.Type, "category": f.Category,
					"custom_definition": f.Custom, "description": f.Description,
				})
			}
			// A metadata read is one API call regardless of how much of it the
			// filters keep, but the count of what survived is the useful number.
			meta.RowCount = int64(len(fields))
			return emit(cmd, fields, meta, cols, rows)
		},
	}
	c.Flags().StringVar(&kind, "kind", "all", "restrict to: all|dimensions|metrics")
	c.Flags().StringVar(&search, "search", "", "case-insensitive substring match on API name, UI name, or description")
	c.Flags().BoolVar(&customOnly, "custom-only", false, "only custom dimensions and metrics defined on this property")
	return c
}

func collectFields(m *analyticsdata.Metadata) []fieldRow {
	out := make([]fieldRow, 0, len(m.Dimensions)+len(m.Metrics))
	for _, d := range m.Dimensions {
		out = append(out, fieldRow{
			Kind: "dimension", APIName: d.ApiName, UIName: d.UiName,
			Description: d.Description, Category: d.Category, Custom: d.CustomDefinition,
		})
	}
	for _, mt := range m.Metrics {
		out = append(out, fieldRow{
			Kind: "metric", APIName: mt.ApiName, UIName: mt.UiName,
			Description: mt.Description, Category: mt.Category,
			Type: strings.TrimPrefix(mt.Type, "TYPE_"), Custom: mt.CustomDefinition,
		})
	}
	return out
}

func filterFields(fields []fieldRow, kind, search string, customOnly bool) []fieldRow {
	needle := strings.ToLower(strings.TrimSpace(search))
	out := make([]fieldRow, 0, len(fields))
	for _, f := range fields {
		if kind == "dimensions" && f.Kind != "dimension" {
			continue
		}
		if kind == "metrics" && f.Kind != "metric" {
			continue
		}
		if customOnly && !f.Custom {
			continue
		}
		if needle != "" && !matchesField(f, needle) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func matchesField(f fieldRow, needle string) bool {
	for _, hay := range []string{f.APIName, f.UIName, f.Description} {
		if strings.Contains(strings.ToLower(hay), needle) {
			return true
		}
	}
	return false
}

func newCompatCmd() *cobra.Command {
	var (
		dimensions []string
		metrics    []string
		filters    []string
		onlyIncomp bool
	)
	c := &cobra.Command{
		Use:   "compat [property]",
		Short: "Check whether a set of dimensions and metrics can be queried together",
		Long: `Not every GA4 dimension pairs with every metric — the two have to share a
scope. This runs properties.checkCompatibility, which reports per field whether
it can join the set, so an incompatible combination can be diagnosed without
burning a report request on a 400.

Examples:
  ga4 compat 123456789 --dimensions pagePath --metrics sessions
  ga4 compat 123456789 --dimensions sessionSource,eventName --metrics sessions,eventCount
  ga4 compat 123456789 --dimensions city --metrics totalRevenue --incompatible-only`,
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
			if len(dims) == 0 && len(mets) == 0 {
				return errs.New(errs.CodeInvalidArgs, "give at least one --dimensions or --metrics value")
			}
			dimFilter, err := buildDimensionFilter(filters, nil, false)
			if err != nil {
				return err
			}
			req := &analyticsdata.CheckCompatibilityRequest{
				Dimensions:      toDimensions(dims),
				Metrics:         toMetrics(mets),
				DimensionFilter: dimFilter,
			}
			if onlyIncomp {
				req.CompatibilityFilter = "INCOMPATIBLE"
			}

			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			keyArgs := []string{"dims=" + strings.Join(dims, ","), "metrics=" + strings.Join(mets, ","),
				"filters=" + strings.Join(filters, "|"), boolArg("incompatible_only", onlyIncomp)}
			key := cache.Key("compat", keyArgs, property, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.MetadataTTL(), func(ctx context.Context) (json.RawMessage, error) {
				_ = s.Quota.Bump(quota.CategoryAdmin, 1)
				resp, err := c.Data.Properties.CheckCompatibility(property, req).Context(ctx).Do()
				if err != nil {
					return nil, client.Translate(err)
				}
				return json.Marshal(collectCompatibility(resp))
			})
			if err != nil {
				return err
			}
			var results []compatRow
			if err := json.Unmarshal(data, &results); err != nil {
				return errs.New(errs.CodeGeneric, err.Error())
			}
			cols := []string{"kind", "api_name", "compatibility", "ui_name"}
			rows := make([]output.Row, 0, len(results))
			for _, r := range results {
				rows = append(rows, output.Row{
					"kind": r.Kind, "api_name": r.APIName,
					"compatibility": r.Compatibility, "ui_name": r.UIName,
				})
			}
			return emit(cmd, results, meta, cols, rows)
		},
	}
	c.Flags().StringArrayVar(&dimensions, "dimensions", nil, "comma list of dimension API names to test (repeatable)")
	c.Flags().StringArrayVar(&metrics, "metrics", nil, "comma list of metric API names to test (repeatable)")
	c.Flags().StringArrayVar(&filters, "filter", nil, "dimension filter to include in the compatibility check (repeatable)")
	c.Flags().BoolVar(&onlyIncomp, "incompatible-only", false, "report only the fields that do not fit")
	return c
}

type compatRow struct {
	Kind          string `json:"kind"`
	APIName       string `json:"api_name"`
	UIName        string `json:"ui_name,omitempty"`
	Compatibility string `json:"compatibility"`
}

func collectCompatibility(resp *analyticsdata.CheckCompatibilityResponse) []compatRow {
	out := make([]compatRow, 0, len(resp.DimensionCompatibilities)+len(resp.MetricCompatibilities))
	for _, d := range resp.DimensionCompatibilities {
		r := compatRow{Kind: "dimension", Compatibility: d.Compatibility}
		if d.DimensionMetadata != nil {
			r.APIName, r.UIName = d.DimensionMetadata.ApiName, d.DimensionMetadata.UiName
		}
		out = append(out, r)
	}
	for _, m := range resp.MetricCompatibilities {
		r := compatRow{Kind: "metric", Compatibility: m.Compatibility}
		if m.MetricMetadata != nil {
			r.APIName, r.UIName = m.MetricMetadata.ApiName, m.MetricMetadata.UiName
		}
		out = append(out, r)
	}
	return out
}
