package cmd

import (
	"strings"
	"testing"

	"github.com/KLIXPERT-io/ga4-cli/internal/config"
)

func TestNormalizeProperty(t *testing.T) {
	for _, in := range []string{"123456789", "properties/123456789", " properties/123456789 ", "properties/123456789/"} {
		got, err := normalizeProperty(in)
		if err != nil {
			t.Fatalf("normalizeProperty(%q): %v", in, err)
		}
		if got != "properties/123456789" {
			t.Errorf("normalizeProperty(%q) = %q", in, got)
		}
	}
}

func TestNormalizePropertyRejectsNonPropertyIDs(t *testing.T) {
	cases := []struct {
		in         string
		wantInHint string
	}{
		// The two mix-ups worth naming explicitly: a measurement ID belongs to
		// a data stream, and UA properties are not GA4 at all.
		{"G-ABC1234567", "measurement ID"},
		{"g-abc1234567", "measurement ID"},
		{"UA-12345-1", "Universal Analytics"},
		{"my-property", "not a valid property ID"},
		{"", "no property given"},
	}
	for _, c := range cases {
		_, err := normalizeProperty(c.in)
		if err == nil {
			t.Errorf("normalizeProperty(%q): expected an error", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.wantInHint) {
			t.Errorf("normalizeProperty(%q) error = %q, want it to mention %q", c.in, err.Error(), c.wantInHint)
		}
	}
}

func TestResolvePropertyFallsBackToConfig(t *testing.T) {
	s := &State{Cfg: &config.Config{Defaults: config.Defaults{Property: "properties/987654321"}}}
	got, err := s.resolveProperty(nil)
	if err != nil {
		t.Fatalf("resolveProperty: %v", err)
	}
	if got != "properties/987654321" {
		t.Errorf("resolveProperty(nil) = %q, want the configured default", got)
	}
	// An explicit argument wins over the default.
	got, err = s.resolveProperty([]string{"111"})
	if err != nil || got != "properties/111" {
		t.Errorf("resolveProperty([111]) = (%q, %v)", got, err)
	}
}

func TestNormalizeAccount(t *testing.T) {
	for _, in := range []string{"12345678", "accounts/12345678"} {
		got, err := normalizeAccount(in)
		if err != nil || got != "accounts/12345678" {
			t.Errorf("normalizeAccount(%q) = (%q, %v)", in, got, err)
		}
	}
	for _, in := range []string{"", "abc", "accounts/"} {
		if _, err := normalizeAccount(in); err == nil {
			t.Errorf("normalizeAccount(%q): expected an error", in)
		}
	}
}

func TestParseCSVAll(t *testing.T) {
	// Both the comma form and the repeated-flag form must reach the same list.
	got := parseCSVAll([]string{"date, pagePath", "", "country"})
	want := []string{"date", "pagePath", "country"}
	if len(got) != len(want) {
		t.Fatalf("parseCSVAll = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseCSVAll = %v, want %v", got, want)
		}
	}
	if parseCSVAll(nil) != nil {
		t.Error("parseCSVAll(nil) should be nil")
	}
}
