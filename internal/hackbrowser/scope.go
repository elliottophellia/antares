// Network scope — which hostnames the crawler captures requests for.
//
// Distinct from the planner-side --exclude (semantic task filter). Scope is
// purely about which hosts the Network domain reports to the capture
// pipeline: a host outside scope is invisible to it, even if the page makes
// a request to it.
//
// Resolution at startup:
//   1. If the caller passes Scope explicitly, use it verbatim.
//   2. Otherwise derive from the target URL via the public suffix list:
//      "*.${eTLD+1}". This catches api.example.com, static.example.com,
//      and friends without forcing the user to enumerate them.

package hackbrowser

import (
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// NormalizeScope strips scheme/path/port and lowercases the input. Wildcard
// "*.foo.com" prefixes are preserved. Empty input → empty output.
func NormalizeScope(input string) string {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	// Cut at the first separator that ends the host.
	for _, r := range "/?#:" {
		if i := strings.IndexRune(s, r); i >= 0 {
			s = s[:i]
		}
	}
	s = strings.Trim(s, ".")
	return s
}

// DeriveScope produces "*.${eTLD+1}" from a target URL. Examples:
//
//	https://test.com          → *.test.com
//	https://app.test.com      → *.test.com
//	https://x.example.com.tr  → *.example.com.tr (PSL handles ccTLDs)
//	https://10.0.0.1          → *.10.0.0.1       (raw IP — PSL cannot resolve)
//
// The fallback is the bare hostname so the agent still works against test
// setups that have no real eTLD+1.
func DeriveScope(targetURL string) string {
	host := hostOf(targetURL)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "*.") {
		return host
	}
	suffix, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil || suffix == "" {
		return "*." + host
	}
	return "*." + suffix
}

// ScopeMatcher returns true when a host matches ANY of the patterns (OR).
// A pattern may be a wildcard ("*.foo.com") or a bare host ("foo.com") —
// both match foo.com itself plus any subdomain. Empty pattern list →
// matcher that rejects everything (safe default).
type ScopeMatcher func(host string) bool

// MakeMatcher builds a ScopeMatcher from one or more patterns. Inputs are
// normalized; wildcard prefixes stripped; the matcher is a tight loop over
// a slice of bases so it can be called per request without allocating.
func MakeMatcher(scopes []string) ScopeMatcher {
	bases := make([]string, 0, len(scopes))
	for _, s := range scopes {
		n := NormalizeScope(s)
		if n == "" {
			continue
		}
		bases = append(bases, strings.TrimPrefix(n, "*."))
	}
	if len(bases) == 0 {
		return func(string) bool { return false }
	}
	return func(host string) bool {
		h := strings.ToLower(strings.Trim(host, "."))
		for _, base := range bases {
			if h == base || strings.HasSuffix(h, "."+base) {
				return true
			}
		}
		return false
	}
}

// hostOf extracts the lowercased hostname from a URL. Returns "" for inputs
// that are not URLs.
func hostOf(targetURL string) string {
	raw := strings.TrimSpace(targetURL)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
