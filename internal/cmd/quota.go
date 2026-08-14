package cmd

import (
	"sort"

	"github.com/KLIXPERT-io/ga4-cli/internal/output"
	"github.com/KLIXPERT-io/ga4-cli/internal/quota"
	"github.com/spf13/cobra"
)

func newQuotaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "Show today's API usage and the last token budget the API reported",
		Long: `The Data API meters on tokens, not requests, and the cost of a report depends
on what it asks for — so the only trustworthy numbers are the ones the API
itself returns. Every report this CLI runs asks for them, and the newest answer
per property is shown here alongside local request counts.

Request counters live at <config-dir>/ga4/quota.json and reset at midnight
America/Los_Angeles.

Examples:
  ga4 quota
  ga4 quota --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := getState(cmd)
			c, err := s.Quota.Load()
			if err != nil {
				return err
			}
			data := map[string]any{
				"date": c.Date,
				"requests_today": map[string]any{
					"core":     c.Core,
					"realtime": c.Realtime,
					"admin":    c.Admin,
				},
				"properties": c.Properties,
				"documented_standard_limits": map[string]any{
					"tokens_per_day":      quota.StandardTokensPerDay,
					"tokens_per_hour":     quota.StandardTokensPerHour,
					"concurrent_requests": quota.StandardConcurrent,
					"note":                "Analytics 360 properties get higher limits; the per-property numbers above come from the API and already account for that.",
				},
			}

			cols := []string{"scope", "bucket", "consumed", "remaining"}
			rows := []output.Row{
				{"scope": "local", "bucket": "core_requests_today", "consumed": c.Core, "remaining": "—"},
				{"scope": "local", "bucket": "realtime_requests_today", "consumed": c.Realtime, "remaining": "—"},
				{"scope": "local", "bucket": "admin_requests_today", "consumed": c.Admin, "remaining": "—"},
			}
			for _, name := range sortedKeys(c.Properties) {
				pq := c.Properties[name]
				for _, b := range []struct {
					label string
					st    *quota.Status
				}{
					{"tokens_per_day", pq.TokensPerDay},
					{"tokens_per_hour", pq.TokensPerHour},
					{"tokens_per_project_per_hour", pq.TokensPerProjectPerHour},
					{"concurrent_requests", pq.ConcurrentRequests},
				} {
					if b.st == nil {
						continue
					}
					rows = append(rows, output.Row{
						"scope": name, "bucket": b.label,
						"consumed": b.st.Consumed, "remaining": b.st.Remaining,
					})
				}
			}
			return emit(cmd, data, output.Meta{APICalls: 0}, cols, rows)
		},
	}
}

func sortedKeys(m map[string]*quota.Property) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
