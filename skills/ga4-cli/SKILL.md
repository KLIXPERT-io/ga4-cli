---
name: ga4-cli
description: Query Google Analytics 4 from the command line with the `ga4` CLI — reports, realtime, pivots, property discovery, and the GA4 dimension/metric schema. Use whenever the task involves GA4 traffic, users, sessions, pageviews, events, conversions, channels, or any Google Analytics data pull. Triggers include "how much traffic", "top pages", "where does traffic come from", "GA4", "Google Analytics", "sessions last month", "compare traffic year over year".
---

# ga4 — Google Analytics 4 CLI

`ga4` wraps the Google Analytics Data API v1beta and Admin API v1beta. It is **read-only**.

## Before anything else

```bash
ga4 auth status          # confirms credentials work; exit 2 means they do not
ga4 properties list      # numeric property IDs — this is what every command needs
```

Property IDs are **numeric** (`properties/123456789`). A `G-XXXXXXXXXX` is a *measurement ID*, not a property ID; `ga4 properties streams <property>` maps between them. Set `ga4 config set defaults.property 123456789` once and the ID can be omitted from every later command.

## Output contract

Default output is JSON with a fixed envelope:

```json
{ "data": { "rows": [ ... ], "row_count": 28, ... }, "meta": { "cached": false, "api_calls": 1, "row_count": 28 } }
```

Rows are already flattened: **one object per row, keyed by field name**, with numeric metrics as real JSON numbers. Do not zip parallel arrays — that work is done.

- `meta.cached` / `meta.ttl_remaining_sec` — served from local cache. Add `--refresh` to force a fresh call.
- `meta.partial: true` — GA4 thresholded or sampled the result; say so when reporting the numbers.
- `--output csv` for anything a human will open in a spreadsheet; `--output table` for terminal display.

Errors are a single JSON line on **stderr**, with an exit code:

| exit | meaning | what to do |
|---|---|---|
| 2 | auth | `ga4 auth status`; the property probably has not been shared with the caller |
| 3 | quota / rate limit | `ga4 quota`; back off, the buckets refill hourly |
| 4 | not found | wrong property ID — `ga4 properties list` |
| 5 | invalid args | read `hint`; often a bad field name (`ga4 metadata --search`) or incompatible pair (`ga4 compat`) |
| 6 | network | retry |

## Start with a preset

Presets cover the common questions and need no field knowledge. All of them take `--range`, `--compare`, `--limit`, `--filter`, and `--output`.

```bash
ga4 report overview        # users, sessions, views, engagement, duration
ga4 report timeseries      # daily series, chronological — good with --output csv
ga4 report pages           # top pages by views
ga4 report landing-pages   # top entry pages by sessions
ga4 report sources         # source/medium
ga4 report channels        # default channel grouping
ga4 report events          # top events
ga4 report countries
ga4 report devices
```

## Full control

```bash
ga4 report run --dimensions <csv> --metrics <csv> [flags]
```

`--metrics` is required. Field names are GA4 API names (`activeUsers`, `sessions`, `screenPageViews`, `eventCount`, `totalRevenue`, `pagePath`, `sessionSource`, `sessionDefaultChannelGroup`, `deviceCategory`, `country`, `date`).

**Look names up rather than guessing:**

```bash
ga4 metadata --search revenue          # matches API name, UI name, description
ga4 metadata --kind dimensions
ga4 compat --dimensions <d> --metrics <m>   # do these work together?
```

Not every dimension pairs with every metric — they must share a scope. On an `incompatible_fields` error, run `ga4 compat` before retrying.

### Date ranges

`--range today|yesterday|last-7d|last-14d|last-28d|last-30d|last-90d|last-180d|last-12m|this-month|last-month|ytd`
(presets end **yesterday** and are inclusive, so repeated runs are stable), or `--start`/`--end` with `YYYY-MM-DD`, `NdaysAgo`, `today`, `yesterday`.

`--compare previous-period|previous-year` adds a second window in the same call. The response then carries an extra `dateRange` column with values `current` and `comparison` — compare the two, do not run two commands.

### Filters

`--filter <dimension><op><value>`, repeatable, ANDed:

`=` exact · `!=` not exact · `~` contains · `!~` not contains · `~~` regex (RE2) · `!~~` not regex · `^=` begins with · `$=` ends with · `@=` in comma list

The separator is the *earliest* operator, so `=`/`~` inside a regex or query string stay in the value. Quote anything with shell metacharacters. `--case-sensitive` to match exactly.

`--metric-filter <metric><op><number>` with `= != > >= < <=`, or `metric=lo..hi`. Applies after aggregation (SQL `HAVING`).

`--filter-group 'a=1,b=2' --filter-group 'c=3'` is OR-of-AND. Mutually exclusive with `--filter`.

### Sorting and paging

`--order-by name` (metrics default to descending, dimensions to ascending), `-name`, `name:asc`, `name:desc`. The field must be selected by `--dimensions`/`--metrics`. Without `--order-by`, results come back top-N by the first metric.

`--limit N` (default 20, max 250000) · `--offset N` · `--all` to auto-paginate every matching row.

## Worked examples

```bash
# Traffic last month vs the same month a year earlier
ga4 report overview --range last-month --compare previous-year

# Top 50 blog pages by views, only ones with real traffic
ga4 report run --dimensions pagePath --metrics screenPageViews,activeUsers \
  --filter 'pagePath~~^/blog/' --metric-filter 'screenPageViews>100' \
  --order-by -screenPageViews --limit 50

# Daily organic sessions as CSV for a chart
ga4 report run --dimensions date --metrics sessions \
  --filter 'sessionDefaultChannelGroup=Organic Search' \
  --range last-90d --order-by date:asc --output csv

# Channel performance split by device
ga4 report pivot --dimensions sessionDefaultChannelGroup,deviceCategory --metrics sessions \
  --pivot sessionDefaultChannelGroup:10 --pivot deviceCategory:3

# Everything, for offline analysis
ga4 report run --dimensions pagePath,country --metrics sessions --all --output csv > data.csv

# Who is on the site right now
ga4 report realtime --dimensions unifiedScreenName --limit 20
```

## Rules of thumb

- Reach for a preset first; drop to `report run` only when the preset cannot express the question.
- Use `--compare` instead of issuing two commands for two periods.
- Prefer `--output csv` when handing data to a human or another tool; keep JSON when reading it yourself.
- Check `meta.partial` before quoting exact figures.
- `--all` can return a lot of rows; pair it with `--output csv` and a file, not with reading it into context.
- Reads are cached (15m default, 1m realtime, 24h metadata). Add `--refresh` when freshness matters.
- Never suggest write operations — this CLI only holds a read-only scope.
