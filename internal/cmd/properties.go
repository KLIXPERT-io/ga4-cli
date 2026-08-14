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
	analyticsadmin "google.golang.org/api/analyticsadmin/v1beta"
)

func newPropertiesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "properties",
		Aliases: []string{"props"},
		Short:   "GA4 properties and their data streams",
	}
	c.AddCommand(newPropertiesListCmd(), newPropertiesGetCmd(), newPropertiesStreamsCmd())
	return c
}

// propertyRow is the flattened view used for CSV/table output and, in the
// account-summaries path, as the JSON payload itself — the nested
// account → properties shape the API returns is awkward to grep, and the
// account is more useful repeated on every row.
type propertyRow struct {
	Property     string `json:"property"`
	DisplayName  string `json:"display_name"`
	Account      string `json:"account"`
	AccountName  string `json:"account_name,omitempty"`
	PropertyType string `json:"property_type,omitempty"`
}

func newPropertiesListCmd() *cobra.Command {
	var (
		account     string
		filter      string
		showDeleted bool
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List every GA4 property the caller can access",
		Long: `Without --account this uses accountSummaries, which returns every account and
its properties in one pass — the fastest way to find a property ID.

With --account it queries the account directly, which additionally supports
--show-deleted and the Admin API's own --filter expressions.

Examples:
  ga4 properties list
  ga4 properties list --output csv
  ga4 properties list --account 12345678
  ga4 properties list --account 12345678 --show-deleted`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}

			parent := ""
			if account != "" {
				a, err := normalizeAccount(account)
				if err != nil {
					return err
				}
				parent = a
			}
			keyArgs := []string{"account=" + parent, "filter=" + filter, boolArg("deleted", showDeleted)}
			key := cache.Key("properties.list", keyArgs, parent, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
				if parent == "" {
					return listFromAccountSummaries(ctx, s, c)
				}
				return listFromAccount(ctx, s, c, parent, filter, showDeleted)
			})
			if err != nil {
				return err
			}
			var props []propertyRow
			if err := json.Unmarshal(data, &props); err != nil {
				return errs.New(errs.CodeGeneric, err.Error())
			}
			cols := []string{"property", "display_name", "account", "account_name", "property_type"}
			rows := make([]output.Row, 0, len(props))
			for _, p := range props {
				rows = append(rows, output.Row{
					"property": p.Property, "display_name": p.DisplayName,
					"account": p.Account, "account_name": p.AccountName,
					"property_type": p.PropertyType,
				})
			}
			return emit(cmd, props, meta, cols, rows)
		},
	}
	c.Flags().StringVar(&account, "account", "", "restrict to one account ID (e.g. 12345678)")
	c.Flags().StringVar(&filter, "filter", "", "Admin API filter expression, e.g. 'parent:accounts/123' (requires --account)")
	c.Flags().BoolVar(&showDeleted, "show-deleted", false, "include soft-deleted properties (requires --account)")
	return c
}

func listFromAccountSummaries(ctx context.Context, s *State, c *client.Client) (json.RawMessage, error) {
	var out []propertyRow
	err := c.Admin.AccountSummaries.List().PageSize(200).Pages(ctx,
		func(resp *analyticsadmin.GoogleAnalyticsAdminV1betaListAccountSummariesResponse) error {
			_ = s.Quota.Bump(quota.CategoryAdmin, 1)
			for _, sum := range resp.AccountSummaries {
				for _, p := range sum.PropertySummaries {
					out = append(out, propertyRow{
						Property:     p.Property,
						DisplayName:  p.DisplayName,
						Account:      sum.Account,
						AccountName:  sum.DisplayName,
						PropertyType: p.PropertyType,
					})
				}
			}
			return nil
		})
	if err != nil {
		return nil, client.Translate(err)
	}
	return json.Marshal(out)
}

func listFromAccount(ctx context.Context, s *State, c *client.Client, parent, filter string, showDeleted bool) (json.RawMessage, error) {
	// The Admin API requires a parent: filter; an explicit --filter wins so a
	// caller can express something more specific.
	expr := filter
	if expr == "" {
		expr = "parent:" + parent
	}
	var out []propertyRow
	call := c.Admin.Properties.List().Filter(expr).PageSize(200).ShowDeleted(showDeleted)
	err := call.Pages(ctx, func(resp *analyticsadmin.GoogleAnalyticsAdminV1betaListPropertiesResponse) error {
		_ = s.Quota.Bump(quota.CategoryAdmin, 1)
		for _, p := range resp.Properties {
			out = append(out, propertyRow{
				Property:     p.Name,
				DisplayName:  p.DisplayName,
				Account:      p.Parent,
				PropertyType: p.PropertyType,
			})
		}
		return nil
	})
	if err != nil {
		return nil, client.Translate(err)
	}
	return json.Marshal(out)
}

func newPropertiesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [property]",
		Short: "Show a property's settings (time zone, currency, industry, service level)",
		Long: `Returns the full property record. The time zone and currency matter for
reporting: report dates are bucketed in the property's time zone, and revenue
metrics are denominated in its currency unless --currency overrides them.

Examples:
  ga4 properties get 123456789
  ga4 properties get properties/123456789 --output json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			key := cache.Key("properties.get", []string{property}, property, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
				return fetchAdmin(ctx, s, func(ctx context.Context) (any, error) {
					return c.Admin.Properties.Get(property).Context(ctx).Do()
				})
			})
			if err != nil {
				return err
			}
			var p analyticsadmin.GoogleAnalyticsAdminV1betaProperty
			_ = json.Unmarshal(data, &p)
			return emit(cmd, p, meta, nil, nil)
		},
	}
}

func newPropertiesStreamsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "streams [property]",
		Short: "List a property's data streams (web, iOS, Android)",
		Long: `Data streams are where measurement IDs live: the G-XXXXXXXXXX a site tags
with belongs to a web stream under some numeric property. This is the command
that maps one to the other.

Examples:
  ga4 properties streams 123456789
  ga4 properties streams 123456789 --output csv`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			key := cache.Key("properties.streams", []string{property}, property, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
				var all []*analyticsadmin.GoogleAnalyticsAdminV1betaDataStream
				err := c.Admin.Properties.DataStreams.List(property).PageSize(200).Pages(ctx,
					func(resp *analyticsadmin.GoogleAnalyticsAdminV1betaListDataStreamsResponse) error {
						_ = s.Quota.Bump(quota.CategoryAdmin, 1)
						all = append(all, resp.DataStreams...)
						return nil
					})
				if err != nil {
					return nil, client.Translate(err)
				}
				return json.Marshal(all)
			})
			if err != nil {
				return err
			}
			var streams []*analyticsadmin.GoogleAnalyticsAdminV1betaDataStream
			if err := json.Unmarshal(data, &streams); err != nil {
				return errs.New(errs.CodeGeneric, err.Error())
			}
			cols := []string{"stream", "display_name", "type", "measurement_id", "uri"}
			rows := make([]output.Row, 0, len(streams))
			for _, st := range streams {
				measurementID, uri := "", ""
				switch {
				case st.WebStreamData != nil:
					measurementID, uri = st.WebStreamData.MeasurementId, st.WebStreamData.DefaultUri
				case st.AndroidAppStreamData != nil:
					measurementID, uri = st.AndroidAppStreamData.FirebaseAppId, st.AndroidAppStreamData.PackageName
				case st.IosAppStreamData != nil:
					measurementID, uri = st.IosAppStreamData.FirebaseAppId, st.IosAppStreamData.BundleId
				}
				rows = append(rows, output.Row{
					"stream":         st.Name,
					"display_name":   st.DisplayName,
					"type":           strings.TrimSuffix(st.Type, "_DATA_STREAM"),
					"measurement_id": measurementID,
					"uri":            uri,
				})
			}
			return emit(cmd, streams, meta, cols, rows)
		},
	}
}
