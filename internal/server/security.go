package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// queryTokenAllowed is deliberately narrow. EventSource and media elements
// cannot set an Authorization header, but ordinary API requests can and must
// never accept credentials in a URL.
func queryTokenAllowed(path string) bool {
	switch path {
	case "/api/chat/attach",
		"/api/intercept/breakpoints/stream",
		"/api/logs/stream",
		"/api/swarm/stream",
		"/api/board/stream",
		"/api/files/raw",
		"/api/social/image":
		return true
	default:
		return strings.HasPrefix(path, "/api/subagent/") && strings.HasSuffix(path, "/attach")
	}
}

// (The query-token allowlist includes the raw file endpoint for browser media
// elements. It is intentionally not used for JSON or mutating routes.)

func (s *Server) bearerAuthorized(r *http.Request) bool {
	cfg := s.config()
	if cfg == nil || cfg.Server.AuthDisabled {
		return false
	}
	token := strings.TrimSpace(cfg.Server.AuthToken)
	if token == "" {
		return false
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

func (s *Server) bearerAuthorizedOrQuery(r *http.Request) bool {
	if s.bearerAuthorized(r) {
		return true
	}
	cfg := s.config()
	if cfg == nil || cfg.Server.AuthDisabled || !queryTokenAllowed(r.URL.Path) {
		return false
	}
	token := strings.TrimSpace(cfg.Server.AuthToken)
	presented := strings.TrimSpace(r.URL.Query().Get("token"))
	return token != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

// requestIsLoopback does not trust X-Forwarded-For. A proxy may opt into
// handling that header separately, but bootstrap capabilities must default to
// the actual peer address.
func requestIsLoopback(r *http.Request) bool {
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return false
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else {
		host = strings.Trim(host, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireSetupAccess keeps the first-run mutating endpoints local unless the
// operator has already configured a bearer token. Setup status remains a
// read-only endpoint so the UI can explain how to bootstrap an instance.
func (s *Server) requireSetupAccess(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.config()
	if !NeedsSetup(cfg) {
		writeError(w, http.StatusConflict, errors.New("initial setup has already been completed"))
		return true
	}
	if requestIsLoopback(r) || s.bearerAuthorized(r) {
		return false
	}
	writeError(w, http.StatusForbidden, errors.New("initial setup is available only from loopback or with a configured bearer token"))
	return true
}

// validateProviderBaseURL validates the destination before the HTTP client is
// constructed. Local providers in the built-in catalogue are allowed to use a
// loopback endpoint; custom/provider URLs are not allowed to resolve into
// private, link-local, metadata, multicast, or otherwise non-public ranges.
func validateProviderBaseURL(ctx context.Context, raw string, allowLocal bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("provider base_url is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return errors.New("provider base_url must be an absolute HTTP(S) URL")
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return errors.New("provider base_url must use http or https")
	}
	if u.User != nil {
		return errors.New("provider base_url must not contain userinfo")
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	check := func(ip net.IP) error {
		if providerIPBlocked(ip) && !(allowLocal && ip.IsLoopback()) {
			return fmt.Errorf("provider base_url resolves to a non-public address (%s)", ip.String())
		}
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return check(ip)
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(lookupCtx, "ip", host)
	if err != nil {
		return fmt.Errorf("provider host cannot be resolved: %w", err)
	}
	if len(ips) == 0 {
		return errors.New("provider host has no address")
	}
	for _, ip := range ips {
		if err := check(ip); err != nil {
			return err
		}
	}
	return nil
}

func providerIPBlocked(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, cidr := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"2001:db8::/32",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

var rfc6052PrefixLengths = [...]int{32, 40, 48, 56, 64, 96}

func validRFC6052PrefixLength(bits int) bool {
	for _, candidate := range rfc6052PrefixLengths {
		if bits == candidate {
			return true
		}
	}
	return false
}

func extractRFC6052IPv4(ip net.IP, prefixBits int) (net.IP, bool) {
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil || !validRFC6052PrefixLength(prefixBits) {
		return nil, false
	}
	// RFC 6052 reserves bits 64-71 as the zero-valued "u" octet.
	if v6[8] != 0 {
		return nil, false
	}
	if prefixBits == 96 {
		return net.IPv4(v6[12], v6[13], v6[14], v6[15]), true
	}
	compact := make([]byte, 15)
	copy(compact[:8], v6[:8])
	copy(compact[8:], v6[9:])
	offset := prefixBits / 8
	return net.IPv4(
		compact[offset],
		compact[offset+1],
		compact[offset+2],
		compact[offset+3],
	), true
}

type providerIPResolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

type nat64Prefix struct {
	network net.IP
	bits    int
}

func prefixMatches(ip, network net.IP, bits int) bool {
	left, right := ip.To16(), network.To16()
	if left == nil || right == nil {
		return false
	}
	mask := net.CIDRMask(bits, 128)
	return left.Mask(mask).Equal(right.Mask(mask))
}

func isIPv4OnlyWKA(ip net.IP) bool {
	return ip.Equal(net.IPv4(192, 0, 0, 170)) || ip.Equal(net.IPv4(192, 0, 0, 171))
}

func discoverNAT64Prefixes(ctx context.Context, resolver providerIPResolver) ([]nat64Prefix, error) {
	ips, err := resolver.LookupIP(ctx, "ip6", "ipv4only.arpa")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []nat64Prefix
	for _, ip := range ips {
		for _, bits := range rfc6052PrefixLengths {
			embedded, ok := extractRFC6052IPv4(ip, bits)
			if !ok || !isIPv4OnlyWKA(embedded) {
				continue
			}
			mask := net.CIDRMask(bits, 128)
			network := append(net.IP(nil), ip.To16().Mask(mask)...)
			key := fmt.Sprintf("%d:%x", bits, []byte(network))
			if !seen[key] {
				seen[key] = true
				out = append(out, nat64Prefix{network: network, bits: bits})
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("DNS64 prefix discovery returned no RFC 6052 prefix")
	}
	return out, nil
}
