package quota

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "quota.json"))
}

func TestBumpPersistsPerCategory(t *testing.T) {
	s := newTestStore(t)
	if err := s.Bump(CategoryCore, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Bump(CategoryRealtime, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Bump(CategoryAdmin, 3); err != nil {
		t.Fatal(err)
	}
	c, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Core != 2 || c.Realtime != 1 || c.Admin != 3 {
		t.Errorf("counters = core %d, realtime %d, admin %d", c.Core, c.Realtime, c.Admin)
	}
	if err := s.Bump("nonsense", 1); err == nil {
		t.Error("an unknown category should be rejected")
	}
}

func TestRecordStoresLatestPerProperty(t *testing.T) {
	s := newTestStore(t)
	if err := s.Record("properties/1", &Property{TokensPerDay: &Status{Consumed: 10, Remaining: 190}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("properties/1", &Property{TokensPerDay: &Status{Consumed: 20, Remaining: 180}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("properties/2", &Property{TokensPerDay: &Status{Consumed: 1, Remaining: 199}}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Properties) != 2 {
		t.Fatalf("tracked %d properties, want 2", len(c.Properties))
	}
	// The newest reading wins; quota state is a snapshot, not a running total.
	if got := c.Properties["properties/1"].TokensPerDay.Consumed; got != 20 {
		t.Errorf("consumed = %d, want the most recent reading (20)", got)
	}
	if c.Properties["properties/1"].ObservedAt.IsZero() {
		t.Error("Record should stamp ObservedAt")
	}
	if err := s.Record("properties/1", nil); err != nil {
		t.Errorf("a nil quota should be a no-op, got %v", err)
	}
}

func TestRecordWarnsOncePerThreshold(t *testing.T) {
	s := newTestStore(t)
	var warnings []string
	s.WarnFn = func(msg string) { warnings = append(warnings, msg) }

	// Comfortable budget: silence.
	mustRecord(t, s, "properties/1", 50, 150) // 75% remaining
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings while the budget is healthy: %v", warnings)
	}

	// Crossing 25% warns once, and staying there does not warn again.
	mustRecord(t, s, "properties/1", 160, 40) // 20% remaining
	if len(warnings) != 1 {
		t.Fatalf("crossing 25%% should warn once, got %v", warnings)
	}
	mustRecord(t, s, "properties/1", 165, 35)
	if len(warnings) != 1 {
		t.Fatalf("staying inside the same band should not re-warn, got %v", warnings)
	}

	// Dropping into the next band warns again.
	mustRecord(t, s, "properties/1", 190, 10) // 5% remaining
	if len(warnings) != 2 {
		t.Fatalf("crossing a tighter threshold should warn again, got %v", warnings)
	}
}

func TestLowestRemainingPctPicksTightestBucket(t *testing.T) {
	pq := &Property{
		TokensPerDay:       &Status{Consumed: 10, Remaining: 90}, // 90%
		TokensPerHour:      &Status{Consumed: 95, Remaining: 5},  // 5%  <- the binding one
		ConcurrentRequests: &Status{Consumed: 0, Remaining: 10},
	}
	pct, ok := lowestRemainingPct(pq)
	if !ok || pct != 5 {
		t.Errorf("lowestRemainingPct = (%d, %v), want (5, true)", pct, ok)
	}
	// Nothing to go on: no percentage, and therefore no warning.
	if _, ok := lowestRemainingPct(&Property{}); ok {
		t.Error("an empty quota should not yield a percentage")
	}
	if _, ok := lowestRemainingPct(&Property{TokensPerDay: &Status{}}); ok {
		t.Error("a zero-total bucket should not yield a percentage")
	}
}

func mustRecord(t *testing.T, s *Store, property string, consumed, remaining int64) {
	t.Helper()
	if err := s.Record(property, &Property{TokensPerDay: &Status{Consumed: consumed, Remaining: remaining}}); err != nil {
		t.Fatal(err)
	}
}
