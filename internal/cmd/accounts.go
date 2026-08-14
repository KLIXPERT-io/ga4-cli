package cmd

import (
	"context"
	"encoding/json"

	"github.com/KLIXPERT-io/ga4-cli/internal/cache"
	"github.com/KLIXPERT-io/ga4-cli/internal/client"
	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	"github.com/KLIXPERT-io/ga4-cli/internal/output"
	"github.com/KLIXPERT-io/ga4-cli/internal/quota"
	"github.com/spf13/cobra"
	analyticsadmin "google.golang.org/api/analyticsadmin/v1beta"
)

func newAccountsCmd() *cobra.Command {
	c := &cobra.Command{Use: "accounts", Short: "Google Analytics accounts"}
	c.AddCommand(newAccountsListCmd(), newAccountsGetCmd())
	return c
}

func newAccountsListCmd() *cobra.Command {
	var showDeleted bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List the Analytics accounts the caller can see",
		Long: `Lists every Analytics account visible to the authenticated identity.

Examples:
  ga4 accounts list
  ga4 accounts list --output csv
  ga4 accounts list --show-deleted`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			key := cache.Key("accounts.list", []string{boolArg("deleted", showDeleted)}, "", identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
				var all []*analyticsadmin.GoogleAnalyticsAdminV1betaAccount
				call := c.Admin.Accounts.List().PageSize(200).ShowDeleted(showDeleted)
				err := call.Pages(ctx, func(resp *analyticsadmin.GoogleAnalyticsAdminV1betaListAccountsResponse) error {
					_ = s.Quota.Bump(quota.CategoryAdmin, 1)
					all = append(all, resp.Accounts...)
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
			var accounts []*analyticsadmin.GoogleAnalyticsAdminV1betaAccount
			if err := json.Unmarshal(data, &accounts); err != nil {
				return errs.New(errs.CodeGeneric, err.Error())
			}
			cols := []string{"account", "display_name", "region_code", "create_time"}
			rows := make([]output.Row, 0, len(accounts))
			for _, a := range accounts {
				rows = append(rows, output.Row{
					"account":      a.Name,
					"display_name": a.DisplayName,
					"region_code":  a.RegionCode,
					"create_time":  a.CreateTime,
				})
			}
			return emit(cmd, accounts, meta, cols, rows)
		},
	}
	c.Flags().BoolVar(&showDeleted, "show-deleted", false, "include soft-deleted accounts")
	return c
}

func newAccountsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <account>",
		Short: "Show one account's details",
		Long: `Examples:
  ga4 accounts get 12345678
  ga4 accounts get accounts/12345678`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s := getState(cmd)
			account, err := normalizeAccount(args[0])
			if err != nil {
				return err
			}
			c, identity, err := s.buildClient(ctx)
			if err != nil {
				return err
			}
			key := cache.Key("accounts.get", []string{account}, account, identity)
			data, meta, err := cachedOrCall(ctx, s, key, s.Cfg.TTL(), func(ctx context.Context) (json.RawMessage, error) {
				return fetchAdmin(ctx, s, func(ctx context.Context) (any, error) {
					return c.Admin.Accounts.Get(account).Context(ctx).Do()
				})
			})
			if err != nil {
				return err
			}
			var a analyticsadmin.GoogleAnalyticsAdminV1betaAccount
			_ = json.Unmarshal(data, &a)
			return emit(cmd, a, meta, nil, nil)
		},
	}
}

// fetchAdmin runs an Admin API call, counts it, and marshals the result.
func fetchAdmin(ctx context.Context, s *State, call func(context.Context) (any, error)) (json.RawMessage, error) {
	_ = s.Quota.Bump(quota.CategoryAdmin, 1)
	v, err := call(ctx)
	if err != nil {
		return nil, client.Translate(err)
	}
	return json.Marshal(v)
}

func boolArg(name string, v bool) string {
	if v {
		return name + "=true"
	}
	return name + "=false"
}
