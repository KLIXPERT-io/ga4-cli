# ga4 — Google Analytics 4 CLI

[![Latest release](https://img.shields.io/github/v/release/KLIXPERT-io/ga4-cli?sort=semver)](https://github.com/KLIXPERT-io/ga4-cli/releases/latest)

A fast, LLM-friendly Go CLI over the **Google Analytics Data API v1beta** and **Admin API v1beta** — the GA4 counterpart to [`gsc-cli`](https://github.com/KLIXPERT-io/gsc-cli), with the same flags, output envelope, and error contract.

- Single static binary, no runtime, no daemon.
- JSON default output, CSV/table supported, TTY auto-detected.
- Rows come back **flattened and typed** — one object per row, real JSON numbers.
- Structured errors with machine-readable codes + exit codes.
- Local disk cache with TTLs, and Data API token-quota tracking straight from the API.
- Service accounts *or* OAuth, auto-detected from the credentials file.
- OS keychain token storage with a 0600 file fallback.

## Install

macOS / Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.ps1 | iex
```

Or `go install github.com/KLIXPERT-io/ga4-cli/cmd/ga4@latest`. See [INSTALL.md](./INSTALL.md) for manual downloads, version pinning, and checksum verification.

After install, `ga4` keeps itself up to date in the background — see [INSTALL.md](./INSTALL.md#auto-update) for details and how to opt out.

## Use with local LLM agents (Claude, Gemini, …)

`ga4` ships an agent skill that teaches LLM coding agents how to drive the CLI (commands, flags, the JSON envelope, exit codes, quota awareness). Install it into any tool that supports the [`skills`](https://github.com/anthropics/skills) format:

```bash
npx skills add https://github.com/KLIXPERT-io/ga4-cli/skills --skill ga4-cli
```

## Setup

### 1. Google credentials

`ga4` authenticates in one of two ways, auto-detected from the credentials file you point it at:

| | **Service account** | **OAuth client** |
|---|---|---|
| Best for | CI, cron, servers, shared agents | interactive use on your own machine |
| Login | none — the key signs its own tokens | `ga4 auth login` opens a browser |
| Access granted via | adding the SA as a property user | your own Google account's GA4 access |

Both request a single scope: `https://www.googleapis.com/auth/analytics.readonly`. The CLI never writes to Google Analytics.

#### 1a. Service account (recommended, headless)

1. **Enable both APIs** in a Google Cloud project:
   - [Google Analytics Data API](https://console.cloud.google.com/apis/library/analyticsdata.googleapis.com) → **Enable**
   - [Google Analytics Admin API](https://console.cloud.google.com/apis/library/analyticsadmin.googleapis.com) → **Enable**
2. **Create the service account and a JSON key:**

   ```bash
   gcloud iam service-accounts create ga4-reader \
     --display-name "GA4 Reader" --project YOUR_PROJECT
   gcloud iam service-accounts keys create ~/secrets/ga4-sa.json \
     --iam-account ga4-reader@YOUR_PROJECT.iam.gserviceaccount.com
   ```

   (Or **Credentials → Create Credentials → Service account** in the Cloud Console, then **Keys → Add key → JSON**.)
3. **Grant it access to the property.** In [Google Analytics](https://analytics.google.com/) → **Admin → Property access management → +** , add the service account's `client_email` with the **Viewer** role. This is the step people forget — without it every call returns `auth_denied`.
4. Point `ga4` at the key:

   ```bash
   ga4 config set auth.service_account_path ~/secrets/ga4-sa.json
   ga4 auth status   # verifies the key mints a token
   ```

   Or per-invocation / in CI, with no config file at all:

   ```bash
   ga4 properties list --service-account ~/secrets/ga4-sa.json
   export GA4_SERVICE_ACCOUNT=~/secrets/ga4-sa.json   # or GOOGLE_APPLICATION_CREDENTIALS
   ga4 properties list
   ```

**Google Workspace domain-wide delegation.** If you cannot add the service account as a property user, let it impersonate a Workspace user that already has access. Grant the SA's client ID the `analytics.readonly` scope in the Admin console (**Security → API controls → Domain-wide delegation**), then:

```bash
ga4 properties list --service-account ~/secrets/ga4-sa.json --subject user@yourdomain.com
# or persist it: ga4 config set auth.subject user@yourdomain.com
```

#### 1b. OAuth client (interactive)

1. Enable both APIs as in step 1a.
2. **Configure the OAuth consent screen** ([APIs & Services → OAuth consent screen](https://console.cloud.google.com/apis/credentials/consent)) and add the scope `https://www.googleapis.com/auth/analytics.readonly`.
3. **Create credentials:** [APIs & Services → Credentials](https://console.cloud.google.com/apis/credentials) → **Create Credentials → OAuth client ID → Desktop app**, and download `client_secrets.json`.
4. Log in:

   ```bash
   ga4 config set auth.credentials_path ~/secrets/client_secrets.json
   ga4 auth login
   ```

   The flow starts a loopback server on `127.0.0.1:<random-port>`, opens your browser, and stores tokens in the OS keychain (with a file fallback).

#### Credential precedence

Highest to lowest — within each layer, a service account source wins, so `GA4_SERVICE_ACCOUNT` in CI does not disturb a local OAuth setup:

1. `--service-account`, then `--credentials`
2. `GA4_SERVICE_ACCOUNT`, then `GA4_CREDENTIALS`, then `GOOGLE_APPLICATION_CREDENTIALS`
3. `auth.service_account_path`, then `auth.credentials_path` in config

`--credentials` accepts either flavor — the kind is detected from the file's contents.

### 2. Find your property ID

GA4 property IDs are **numeric** (`properties/123456789`). The `G-XXXXXXXXXX` you tag a site with is a *measurement ID* and belongs to a data stream — `ga4` rejects it with a pointer to the right command.

```bash
ga4 properties list                        # every property you can see
ga4 properties streams 123456789           # maps measurement IDs to the property
ga4 config set defaults.property 123456789 # then the ID can be omitted everywhere
```

## Quick tour

```bash
# Presets — the questions people actually open GA4 to answer
ga4 report overview 123456789
ga4 report timeseries 123456789 --range last-90d --output csv
ga4 report pages 123456789 --limit 50
ga4 report channels 123456789 --compare previous-year
ga4 report sources 123456789
ga4 report events 123456789
ga4 report countries 123456789
ga4 report devices 123456789
ga4 report landing-pages 123456789

# Full control
ga4 report run 123456789 --dimensions date --metrics sessions,activeUsers --range last-28d

# Filter to a section of the site, keep only pages with real traffic
ga4 report run 123456789 --dimensions pagePath --metrics screenPageViews \
  --filter 'pagePath~~^/blog/' --metric-filter 'screenPageViews>100' \
  --order-by -screenPageViews

# OR-of-AND filter groups: (mobile AND Germany) OR (desktop)
ga4 report run 123456789 --dimensions deviceCategory,country --metrics sessions \
  --filter-group 'deviceCategory=mobile,country=Germany' --filter-group 'deviceCategory=desktop'

# Every row, streamed to CSV
ga4 report run 123456789 --dimensions pagePath --metrics screenPageViews --all --output csv > pages.csv

# Cross-tabulate
ga4 report pivot 123456789 --dimensions sessionDefaultChannelGroup,deviceCategory \
  --metrics sessions --pivot sessionDefaultChannelGroup:10 --pivot deviceCategory:3

# Who's on the site right now
ga4 report realtime 123456789 --dimensions unifiedScreenName

# Discovery: what fields exist, and do they work together?
ga4 metadata 123456789 --search revenue
ga4 compat 123456789 --dimensions sessionSource --metrics eventCount

# Quota
ga4 quota
```

### Filters

`--filter` takes `<dimension><op><value>`, repeatable and ANDed:

| op | meaning | example |
|---|---|---|
| `=` | exact | `country=Germany` |
| `!=` | not exact | `country!=Germany` |
| `~` | contains | `pagePath~/blog/` |
| `!~` | does not contain | `pagePath!~/tag/` |
| `~~` | matches regex (RE2) | `pagePath~~^/(blog\|guides)/` |
| `!~~` | does not match regex | `pagePath!~~^/tag/` |
| `^=` | begins with | `pagePath^=/blog` |
| `$=` | ends with | `pagePath$=.html` |
| `@=` | in list | `country@=Germany,Austria` |

The separator is the *earliest* operator in the string, so `=` and `~` inside a regex or a query string stay part of the value. Add `--case-sensitive` to match exactly.

`--metric-filter` takes `<metric><op><number>` with `= != > >= < <=`, plus `metric=lo..hi` for an inclusive range. Metric filters apply after aggregation, like SQL `HAVING`.

`--filter-group` expresses OR-of-AND: each flag is one AND group, repeated flags are ORed together. It is mutually exclusive with `--filter`.

## Output

Every JSON response has the envelope:

```json
{
  "data": {
    "property": "properties/123456789",
    "date_ranges": [{ "name": "current", "start": "2026-07-18", "end": "2026-08-14" }],
    "dimension_headers": ["date"],
    "metric_headers": [{ "name": "sessions", "type": "TYPE_INTEGER" }],
    "rows": [{ "date": "20260814", "sessions": 1234 }],
    "row_count": 28
  },
  "meta": {
    "cached": true,
    "cached_at": "2026-08-14T14:30:00Z",
    "ttl_remaining_sec": 543,
    "api_calls": 0,
    "row_count": 28
  }
}
```

Rows are flattened and typed: one object per row keyed by field name, with integer and float metrics as real JSON numbers rather than the API's strings. `meta.partial` is set when GA4 reports that the result was [thresholded](https://support.google.com/analytics/answer/9383630) or sampled — the answer is to a slightly different question than the one asked.

Errors are always JSON on stderr, even in CSV mode:

```json
{"error":{"code":"auth_denied","message":"...","hint":"Grant the caller access to the property: ...","retriable":false}}
```

Exit codes: `0` ok · `1` generic · `2` auth · `3` quota/rate · `4` not-found · `5` validation · `6` network.

## Quota

The Data API meters on **tokens**, not requests, and a report's cost depends on what it asks for — so a client cannot compute it. Every report `ga4` runs sets `returnPropertyQuota`, and the newest answer per property is stored and shown by `ga4 quota`, alongside local request counters. A warning goes to stderr the first time the remaining budget falls below 25%, 10%, and 5%.

Standard properties get 200,000 core tokens/day and 40,000/hour; Analytics 360 gets more. The per-property numbers reported by the API already account for that.

## Config

`~/.config/ga4/config.toml` (`ga4 config path` prints the exact location):

```toml
auto_update = true

[auth]
service_account_path = "~/secrets/ga4-sa.json"  # wins over credentials_path
# credentials_path = "~/secrets/client_secrets.json"
# subject = "user@yourdomain.com"               # Workspace delegation (service account only)

[defaults]
property = "properties/123456789"
output = "json"
range = "last-28d"
# currency = "EUR"

[cache]
# dir = "~/custom/cache"   # default: ~/.config/ga4/cache
default_ttl = "15m"
ttl_realtime = "1m"
ttl_metadata = "24h"

[logging]
verbose = false
format = "text"
```

Manage it with `ga4 config get|set|path|list`.

## Data directory

`ga4` stores persistent data under `~/.config/ga4/`:

- `cache/` — cached API responses
- `quota.json` — request counters and the last reported token budget
- `token.json` — OAuth token, only when no OS keychain is available (mode 0600)
- `update-state.json` — auto-update state

## Flags shared across commands

- `--output json|csv|table` (default: `json`, or `table` on TTY)
- `--no-cache` — bypass cache reads and writes
- `--refresh` — bypass read but write fresh result
- `--cache-ttl <duration>` — override TTL for this call
- `--credentials <path>` — OAuth client or service account key (auto-detected)
- `--service-account <path>` — service account key, no browser login
- `--subject <email>` — Workspace user to impersonate (service account only)
- `-v, --verbose` / `-q, --quiet`
- `--log-format text|json`

## Command surface

| command | what it does |
|---|---|
| `auth login\|status\|logout` | authorize, inspect, or clear credentials |
| `accounts list\|get` | Analytics accounts |
| `properties list\|get\|streams` | GA4 properties, settings, and data streams |
| `report run` | any dimensions/metrics/filters/date range |
| `report realtime` | the last ~30 minutes |
| `report pivot` | cross-tabulation |
| `report overview\|timeseries\|pages\|landing-pages\|sources\|channels\|events\|countries\|devices` | presets |
| `metadata` | every dimension and metric the property supports |
| `compat` | whether a set of fields can be queried together |
| `quota` | usage and the live token budget |
| `config get\|set\|path\|list` | configuration |
| `update status\|check\|apply` | self-update |

## Non-goals

- No writes to Google Analytics — this is a read-only client, by scope.
- No web UI, no daemon mode.
- No non-GA4 sources (Search Console lives in [`gsc-cli`](https://github.com/KLIXPERT-io/gsc-cli)).
- No visualization — charts are the caller's job (pipe CSV into your plotting tool of choice).

## License

MIT
