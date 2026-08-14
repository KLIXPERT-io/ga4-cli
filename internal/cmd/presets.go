package cmd

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// A preset is a `report run` with the dimensions and metrics already chosen —
// the handful of questions people actually open GA4 to answer. Every flag that
// `report run` takes still applies, so a preset is a starting point rather
// than a dead end: --filter, --range, --limit, and --compare all work, and
// --dimensions/--metrics append to the preset's own.
type preset struct {
	name       string
	short      string
	long       string
	dimensions []string
	metrics    []string
	orderBy    []string
	limit      int64
}

var presets = []preset{
	{
		name:  "overview",
		short: "Headline metrics for the property (no dimensions)",
		long: `Totals for the window: users, sessions, views, engagement, and duration.

Examples:
  ga4 report overview 123456789
  ga4 report overview 123456789 --range last-7d
  ga4 report overview 123456789 --compare previous-year`,
		metrics: []string{"activeUsers", "newUsers", "sessions", "screenPageViews",
			"engagementRate", "averageSessionDuration", "bounceRate"},
		limit: 10,
	},
	{
		name:       "timeseries",
		short:      "Daily users, sessions, and views",
		long:       "A day-by-day series, ordered chronologically — pipe it straight into a chart with --output csv.",
		dimensions: []string{"date"},
		metrics:    []string{"activeUsers", "sessions", "screenPageViews"},
		orderBy:    []string{"date:asc"},
		limit:      400,
	},
	{
		name:       "pages",
		short:      "Top pages by views",
		dimensions: []string{"pagePath", "pageTitle"},
		metrics:    []string{"screenPageViews", "activeUsers", "averageSessionDuration"},
		limit:      20,
	},
	{
		name:       "landing-pages",
		short:      "Top landing pages by sessions",
		dimensions: []string{"landingPage"},
		metrics:    []string{"sessions", "activeUsers", "bounceRate"},
		limit:      20,
	},
	{
		name:       "sources",
		short:      "Top traffic sources and mediums",
		dimensions: []string{"sessionSource", "sessionMedium"},
		metrics:    []string{"sessions", "activeUsers", "engagementRate"},
		limit:      20,
	},
	{
		name:       "channels",
		short:      "Sessions by default channel group",
		dimensions: []string{"sessionDefaultChannelGroup"},
		metrics:    []string{"sessions", "activeUsers", "engagementRate"},
		limit:      20,
	},
	{
		name:       "events",
		short:      "Top events by count",
		dimensions: []string{"eventName"},
		metrics:    []string{"eventCount", "activeUsers"},
		limit:      20,
	},
	{
		name:       "countries",
		short:      "Users by country",
		dimensions: []string{"country"},
		metrics:    []string{"activeUsers", "sessions", "engagementRate"},
		limit:      20,
	},
	{
		name:       "devices",
		short:      "Users by device category",
		dimensions: []string{"deviceCategory"},
		metrics:    []string{"activeUsers", "sessions", "engagementRate"},
		limit:      10,
	},
}

func newPresetCmd(p preset) *cobra.Command {
	rf := &reportFlags{}
	long := p.long
	if long == "" {
		long = p.short + "."
	}
	c := &cobra.Command{
		Use:   p.name + " [property]",
		Short: p.short,
		Long: long + "\n\nDimensions: " + joinOrNone(p.dimensions) +
			"\nMetrics: " + joinOrNone(p.metrics) +
			"\n\nAdd --dimensions/--metrics to extend the selection, or use `ga4 report run` for full control.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := getState(cmd)
			property, err := s.resolveProperty(args)
			if err != nil {
				return err
			}
			// Preset fields come first so the default ordering (top N by the
			// first metric) stays the preset's own.
			rf.Dimensions = append(append([]string{}, p.dimensions...), rf.Dimensions...)
			rf.Metrics = append(append([]string{}, p.metrics...), rf.Metrics...)
			if len(rf.OrderBy) == 0 {
				rf.OrderBy = p.orderBy
			}
			return runCoreReport(cmd.Context(), cmd, s, property, rf)
		},
	}
	rf.addCommon(c)
	c.Flags().StringArrayVar(&rf.Dimensions, "dimensions", nil, "additional dimension API names (repeatable)")
	c.Flags().StringArrayVar(&rf.Metrics, "metrics", nil, "additional metric API names (repeatable)")
	c.Flags().Lookup("limit").DefValue = strconv.FormatInt(p.limit, 10)
	rf.Limit = p.limit
	return c
}

func joinOrNone(ss []string) string {
	if len(ss) == 0 {
		return "(none)"
	}
	return strings.Join(ss, ", ")
}
