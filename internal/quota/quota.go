// Package quota tracks Data API usage.
//
// The GA4 Data API meters on *tokens*, not requests: a report's cost depends on
// its dimensions, date range, and cardinality, so a client cannot compute it.
// The API will however report the live state back to us when a request sets
// returnPropertyQuota, and that answer is authoritative — it already accounts
// for Standard vs Analytics 360 limits. So this package does two things:
//
//   - counts requests locally per day, per category, as a cheap activity log;
//   - persists the last quota state the API reported for each property, and
//     warns when the remaining token budget runs low.
package quota

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Documented Data API limits for a Standard property, kept for reference in
// `ga4 quota` output. Analytics 360 properties get 10x these; the numbers the
// API reports per request always win over these constants.
const (
	StandardTokensPerDay  = 200000
	StandardTokensPerHour = 40000
	StandardConcurrent    = 10
)

// Categories of API call tracked locally.
const (
	CategoryCore     = "core"     // runReport, runPivotReport, batchRunReports
	CategoryRealtime = "realtime" // runRealtimeReport
	CategoryAdmin    = "admin"    // Admin API + metadata + compatibility
)

// warnThresholds are the remaining-budget percentages worth interrupting for,
// loosest first.
var warnThresholds = []int{25, 10, 5}

// Status mirrors the API's QuotaStatus for one bucket.
type Status struct {
	Consumed  int64 `json:"consumed"`
	Remaining int64 `json:"remaining"`
}

// Property is the last quota state the API reported for one property.
type Property struct {
	ObservedAt                            time.Time `json:"observed_at"`
	TokensPerDay                          *Status   `json:"tokens_per_day,omitempty"`
	TokensPerHour                         *Status   `json:"tokens_per_hour,omitempty"`
	TokensPerProjectPerHour               *Status   `json:"tokens_per_project_per_hour,omitempty"`
	ConcurrentRequests                    *Status   `json:"concurrent_requests,omitempty"`
	ServerErrorsPerProjectPerHour         *Status   `json:"server_errors_per_project_per_hour,omitempty"`
	PotentiallyThresholdedRequestsPerHour *Status   `json:"potentially_thresholded_requests_per_hour,omitempty"`
}

type Counters struct {
	Date       string               `json:"date"` // YYYY-MM-DD, America/Los_Angeles
	Core       int                  `json:"core"`
	Realtime   int                  `json:"realtime"`
	Admin      int                  `json:"admin"`
	Properties map[string]*Property `json:"properties,omitempty"`
	// WarnedBelow records the lowest remaining-token percentage already warned
	// about per property, so one low-budget day does not spam every call.
	WarnedBelow map[string]int `json:"warned_below,omitempty"`
}

type Store struct {
	Path   string
	mu     sync.Mutex
	WarnFn func(msg string)
}

var laLoc *time.Location

func init() {
	// GA4 quota buckets reset on Google's own daily boundary, which follows
	// Pacific time like the rest of the Analytics platform.
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.FixedZone("PST", -8*3600)
	}
	laLoc = loc
}

func today() string { return time.Now().In(laLoc).Format("2006-01-02") }

func New(path string) *Store { return &Store{Path: path} }

// withLock opens the file (creating if missing), applies an exclusive flock,
// reads current state, runs fn which may mutate Counters, writes, unlocks.
func (s *Store) withLock(fn func(c *Counters) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer unlockFile(f)

	var c Counters
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &c); err != nil {
			// corrupt — reset rather than fail the user's command
			c = Counters{}
		}
	}
	if c.Date != today() {
		c = Counters{Date: today()}
	}
	if err := fn(&c); err != nil {
		return err
	}
	out, err := json.MarshalIndent(&c, "", "  ")
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err = f.Write(out)
	return err
}

// Load returns a snapshot (read-only, still rolls the date if stale).
func (s *Store) Load() (*Counters, error) {
	var out Counters
	err := s.withLock(func(c *Counters) error { out = *c; return nil })
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Bump increments the request counter for a category.
func (s *Store) Bump(category string, n int) error {
	return s.withLock(func(c *Counters) error {
		switch category {
		case CategoryCore:
			c.Core += n
		case CategoryRealtime:
			c.Realtime += n
		case CategoryAdmin:
			c.Admin += n
		default:
			return fmt.Errorf("unknown quota category: %s", category)
		}
		return nil
	})
}

// Record stores the quota state the API just reported and warns when the
// daily or hourly token budget drops below 25%, 10%, and 5% remaining.
func (s *Store) Record(property string, pq *Property) error {
	if pq == nil {
		return nil
	}
	var warnings []string
	err := s.withLock(func(c *Counters) error {
		if c.Properties == nil {
			c.Properties = map[string]*Property{}
		}
		pq.ObservedAt = time.Now().UTC()
		c.Properties[property] = pq
		if c.WarnedBelow == nil {
			c.WarnedBelow = map[string]int{}
		}
		pct, ok := lowestRemainingPct(pq)
		if !ok {
			return nil
		}
		// Warn on the tightest threshold the budget has fallen below, and only
		// once per threshold — so a long day at 20% stays quiet, but dropping
		// from 20% to 5% speaks up again.
		crossed := 0
		for _, threshold := range warnThresholds {
			if pct <= threshold {
				crossed = threshold
			}
		}
		if crossed == 0 {
			return nil
		}
		if prev, seen := c.WarnedBelow[property]; seen && prev <= crossed {
			return nil
		}
		c.WarnedBelow[property] = crossed
		warnings = append(warnings, fmt.Sprintf(
			"%s: %d%% of the Data API token budget remaining", property, pct))
		return nil
	})
	if s.WarnFn != nil {
		for _, w := range warnings {
			s.WarnFn(w)
		}
	}
	return err
}

// lowestRemainingPct returns the tightest of the token buckets as a percentage
// of its total, which is what actually gates the next request.
func lowestRemainingPct(pq *Property) (int, bool) {
	lowest := 101
	found := false
	for _, st := range []*Status{pq.TokensPerDay, pq.TokensPerHour, pq.TokensPerProjectPerHour} {
		if st == nil {
			continue
		}
		total := st.Consumed + st.Remaining
		if total <= 0 {
			continue
		}
		pct := int(st.Remaining * 100 / total)
		if pct < lowest {
			lowest = pct
			found = true
		}
	}
	return lowest, found
}

// Sentinel errors resolved by the caller to structured errs.E values.
var (
	ErrQuotaExceeded = errors.New("quota_exceeded")
	ErrRateLimited   = errors.New("rate_limited")
)
