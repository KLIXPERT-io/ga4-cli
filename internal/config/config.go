// Package config loads ~/.config/ga4/config.toml and merges it with the
// flag/env/default layers.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Auth struct {
	CredentialsPath string `toml:"credentials_path"`
	// ServiceAccountPath points at a service account key file. It takes
	// precedence over CredentialsPath, so an OAuth setup can stay configured
	// while a machine runs on a service account.
	ServiceAccountPath string `toml:"service_account_path"`
	// Subject enables domain-wide delegation: the service account impersonates
	// this Workspace user. Service account credentials only.
	Subject string `toml:"subject"`
}

type Defaults struct {
	Property string `toml:"property"`
	Output   string `toml:"output"`
	Range    string `toml:"range"`
	Currency string `toml:"currency"`
}

type Cache struct {
	Dir         string `toml:"dir"`
	DefaultTTL  string `toml:"default_ttl"`
	TTLRealtime string `toml:"ttl_realtime"`
	TTLMetadata string `toml:"ttl_metadata"`
}

type Logging struct {
	Verbose bool   `toml:"verbose"`
	Format  string `toml:"format"`
}

type Config struct {
	Auth       Auth     `toml:"auth"`
	Defaults   Defaults `toml:"defaults"`
	Cache      Cache    `toml:"cache"`
	Logging    Logging  `toml:"logging"`
	AutoUpdate bool     `toml:"auto_update"`
	// Path the config was loaded from (empty if defaults).
	path string
}

// Default returns built-in defaults.
func Default() *Config {
	return &Config{
		Defaults:   Defaults{Output: "json", Range: "last-28d"},
		Cache:      Cache{DefaultTTL: "15m", TTLRealtime: "1m", TTLMetadata: "24h"},
		Logging:    Logging{Format: "text"},
		AutoUpdate: true,
	}
}

// AutoUpdateEnabled is the single source of truth for whether the background
// auto-updater (and `ga4 update` apply paths) may run. It returns false when
// GA4_NO_UPDATE is set to a non-empty value other than "0"/"false"
// (case-insensitive), or when c.AutoUpdate is false. A nil *Config is treated
// as defaults (AutoUpdate=true).
func AutoUpdateEnabled(c *Config) bool {
	if v, ok := os.LookupEnv("GA4_NO_UPDATE"); ok && v != "" {
		switch strings.ToLower(v) {
		case "0", "false":
			// explicit off-of-off: treat as not set (do not disable)
		default:
			return false
		}
	}
	if c != nil && !c.AutoUpdate {
		return false
	}
	return true
}

// DataDir returns the base directory for all ga4 persistent data (cache,
// quota, token fallback, update state). Uses os.UserConfigDir(), i.e.
// ~/.config/ga4 on Linux, ~/Library/Application Support/ga4 on macOS.
func DataDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "ga4"), nil
}

// Path returns the location of config.toml.
func Path() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

// Load reads the config file; a missing file yields defaults.
func Load() (*Config, error) {
	c := Default()
	p, err := Path()
	if err != nil {
		return c, err
	}
	c.path = p
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if _, err := toml.Decode(string(b), c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	c.path = p
	return c, nil
}

// Save writes config back to disk (mkdir -p).
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

func (c *Config) LoadedPath() string { return c.path }

// TTL returns the cache default TTL parsed as a duration (15m fallback).
func (c *Config) TTL() time.Duration { return parseTTL(c.Cache.DefaultTTL, 15*time.Minute) }

// RealtimeTTL returns the realtime-report cache TTL (default 1m). Realtime data
// churns by the minute, so anything longer would serve stale numbers.
func (c *Config) RealtimeTTL() time.Duration { return parseTTL(c.Cache.TTLRealtime, time.Minute) }

// MetadataTTL returns the dimension/metric metadata cache TTL (default 24h).
func (c *Config) MetadataTTL() time.Duration { return parseTTL(c.Cache.TTLMetadata, 24*time.Hour) }

func parseTTL(v string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// Get returns the value at a dotted key (e.g. "auth.credentials_path").
func (c *Config) Get(key string) (string, bool) {
	switch key {
	case "auth.credentials_path":
		return c.Auth.CredentialsPath, true
	case "auth.service_account_path":
		return c.Auth.ServiceAccountPath, true
	case "auth.subject":
		return c.Auth.Subject, true
	case "defaults.property":
		return c.Defaults.Property, true
	case "defaults.output":
		return c.Defaults.Output, true
	case "defaults.range":
		return c.Defaults.Range, true
	case "defaults.currency":
		return c.Defaults.Currency, true
	case "cache.dir":
		return c.Cache.Dir, true
	case "cache.default_ttl":
		return c.Cache.DefaultTTL, true
	case "cache.ttl_realtime":
		return c.Cache.TTLRealtime, true
	case "cache.ttl_metadata":
		return c.Cache.TTLMetadata, true
	case "logging.verbose":
		return fmt.Sprintf("%v", c.Logging.Verbose), true
	case "logging.format":
		return c.Logging.Format, true
	case "auto_update":
		return fmt.Sprintf("%v", c.AutoUpdate), true
	}
	return "", false
}

// Set updates a dotted key and saves.
func (c *Config) Set(key, value string) error {
	switch key {
	case "auth.credentials_path":
		c.Auth.CredentialsPath = value
	case "auth.service_account_path":
		c.Auth.ServiceAccountPath = value
	case "auth.subject":
		c.Auth.Subject = value
	case "defaults.property":
		c.Defaults.Property = value
	case "defaults.output":
		if value != "" && value != "json" && value != "csv" && value != "table" {
			return fmt.Errorf("defaults.output must be json, csv, or table")
		}
		c.Defaults.Output = value
	case "defaults.range":
		c.Defaults.Range = value
	case "defaults.currency":
		c.Defaults.Currency = value
	case "cache.dir":
		c.Cache.Dir = value
	case "cache.default_ttl":
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("invalid duration: %q", value)
		}
		c.Cache.DefaultTTL = value
	case "cache.ttl_realtime":
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("invalid duration: %q", value)
		}
		c.Cache.TTLRealtime = value
	case "cache.ttl_metadata":
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("invalid duration: %q", value)
		}
		c.Cache.TTLMetadata = value
	case "logging.verbose":
		c.Logging.Verbose = value == "true" || value == "1"
	case "logging.format":
		if value != "text" && value != "json" {
			return fmt.Errorf("logging.format must be text or json")
		}
		c.Logging.Format = value
	case "auto_update":
		c.AutoUpdate = value == "true" || value == "1"
	default:
		return fmt.Errorf("unknown key: %s", key)
	}
	return c.Save()
}

// Keys returns the list of known keys (stable order).
func Keys() []string {
	return []string{
		"auth.credentials_path",
		"auth.service_account_path",
		"auth.subject",
		"defaults.property",
		"defaults.output",
		"defaults.range",
		"defaults.currency",
		"cache.dir",
		"cache.default_ttl",
		"cache.ttl_realtime",
		"cache.ttl_metadata",
		"logging.verbose",
		"logging.format",
		"auto_update",
	}
}

// ExpandHome expands a leading ~ in paths.
func ExpandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
