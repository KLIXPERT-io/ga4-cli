package cmd

import (
	"strings"

	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
)

// normalizeProperty accepts the several shapes a user (or an LLM agent) is
// likely to reach for and returns the canonical "properties/<numeric-id>"
// resource name the APIs require.
//
// The common failure is handing over a G-XXXXXXXXXX measurement ID or a UA-
// property, neither of which the Data API accepts, so both get a specific
// error rather than a 404 from Google.
func normalizeProperty(in string) (string, error) {
	p := strings.TrimSpace(in)
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "", errs.New(errs.CodeInvalidArgs, "no property given").
			WithHint("Pass the numeric property ID, or set a default with `ga4 config set defaults.property properties/123456789`.")
	}
	upper := strings.ToUpper(p)
	switch {
	case strings.HasPrefix(upper, "G-"):
		return "", errs.Newf(errs.CodeInvalidArgs, "%q is a measurement ID, not a property ID", p).
			WithHint("Find the numeric property ID in GA4 Admin → Property settings, or run `ga4 properties list`.")
	case strings.HasPrefix(upper, "UA-"):
		return "", errs.Newf(errs.CodeInvalidArgs, "%q is a Universal Analytics property", p).
			WithHint("This CLI speaks the GA4 APIs only. Universal Analytics properties stopped collecting data in 2023.")
	}
	p = strings.TrimPrefix(p, "properties/")
	if !isNumeric(p) {
		return "", errs.Newf(errs.CodeInvalidArgs, "%q is not a valid property ID", in).
			WithHint("Property IDs are numeric, e.g. 123456789 or properties/123456789. Run `ga4 properties list` to see yours.")
	}
	return "properties/" + p, nil
}

// resolveProperty takes the positional argument (which may be absent) and
// falls back to defaults.property from config.
func (s *State) resolveProperty(args []string) (string, error) {
	raw := ""
	if len(args) > 0 {
		raw = args[0]
	}
	if raw == "" && s.Cfg != nil {
		raw = s.Cfg.Defaults.Property
	}
	return normalizeProperty(raw)
}

// normalizeAccount canonicalizes an account ID to "accounts/<numeric-id>".
func normalizeAccount(in string) (string, error) {
	a := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(in), "/"))
	a = strings.TrimPrefix(a, "accounts/")
	if a == "" || !isNumeric(a) {
		return "", errs.Newf(errs.CodeInvalidArgs, "%q is not a valid account ID", in).
			WithHint("Account IDs are numeric, e.g. 12345678. Run `ga4 accounts list` to see yours.")
	}
	return "accounts/" + a, nil
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseCSVAll flattens repeatable comma-friendly flags, so both
// `--metrics a,b` and `--metrics a --metrics b` work.
func parseCSVAll(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, parseCSV(v)...)
	}
	return out
}

func contains(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}
