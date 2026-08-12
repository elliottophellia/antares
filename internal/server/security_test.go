package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

type staticIPResolver map[string][]net.IP

func (r staticIPResolver) LookupIP(_ context.Context, network, host string) ([]net.IP, error) {
	ips, ok := r[network+" "+host]
	if !ok {
		return nil, &net.DNSError{Err: "not found", Name: host, IsNotFound: true}
	}
	out := make([]net.IP, len(ips))
	copy(out, ips)
	return out, nil
}

func synthesizeRFC6052(t *testing.T, prefix net.IP, bits int, v4 net.IP) net.IP {
	t.Helper()
	p := prefix.To16()
	v := v4.To4()
	if p == nil || v == nil {
		t.Fatalf("invalid synthesis input: prefix=%v v4=%v", prefix, v4)
	}
	out := make(net.IP, net.IPv6len)
	if bits == 96 {
		copy(out[:12], p[:12])
		copy(out[12:], v)
		return out
	}
	compact := make([]byte, 15)
	prefixBytes := bits / 8
	copy(compact[:prefixBytes], p[:prefixBytes])
	copy(compact[prefixBytes:prefixBytes+net.IPv4len], v)
	copy(out[:8], compact[:8])
	out[8] = 0
	copy(out[9:], compact[8:])
	return out
}

func TestQueryTokenAllowlist(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "/api/logs/stream", want: true},
		{path: "/api/chat/attach", want: true},
		{path: "/api/subagent/sub_123/attach", want: true},
		{path: "/api/files/raw", want: true},
		{path: "/api/status", want: false},
		{path: "/api/config/raw", want: false},
		{path: "/api/config", want: false},
		{path: "/api/subagent/sub_123/attach/extra", want: false},
	} {
		if got := queryTokenAllowed(tc.path); got != tc.want {
			t.Errorf("queryTokenAllowed(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestWithAuthRejectsQueryTokenOnOrdinaryAPI(t *testing.T) {
	cfg := config.Default()
	cfg.Server.AuthToken = "synthetic-token"
	s := &Server{cfg: cfg}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := s.withAuth(next)

	ordinary := httptest.NewRequest(http.MethodGet, "/api/config/raw?token=synthetic-token", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, ordinary)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary query-token request returned %d, want 401", rr.Code)
	}

	stream := httptest.NewRequest(http.MethodGet, "/api/logs/stream?token=synthetic-token", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, stream)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("allowlisted stream query-token request returned %d, want 204", rr.Code)
	}
}

func TestRequestIsLoopback(t *testing.T) {
	for _, tc := range []struct {
		remote string
		want   bool
	}{
		{remote: "127.0.0.1:1234", want: true},
		{remote: "[::1]:1234", want: true},
		{remote: "192.0.2.10:1234", want: false},
		{remote: "203.0.113.8:1234", want: false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		if got := requestIsLoopback(r); got != tc.want {
			t.Errorf("requestIsLoopback(%q) = %v, want %v", tc.remote, got, tc.want)
		}
	}
}

func TestExtractRFC6052IPv4SupportsEveryPrefixLength(t *testing.T) {
	prefix := net.ParseIP("fd00:aa:bb:2090::")
	want := net.ParseIP("54.158.233.194")
	for _, bits := range []int{32, 40, 48, 56, 64, 96} {
		t.Run(fmt.Sprintf("/%d", bits), func(t *testing.T) {
			synth := synthesizeRFC6052(t, prefix, bits, want)
			got, ok := extractRFC6052IPv4(synth, bits)
			if !ok || !got.Equal(want) {
				t.Fatalf("extractRFC6052IPv4(%s, %d) = %v, %v; want %s, true",
					synth, bits, got, ok, want)
			}
		})
	}
}

func TestExtractRFC6052IPv4RejectsInvalidFormat(t *testing.T) {
	ip := synthesizeRFC6052(t, net.ParseIP("fd00:aa:bb:2090::"), 64, net.ParseIP("54.158.233.194"))
	ip[8] = 1
	for _, tc := range []struct {
		ip   net.IP
		bits int
	}{
		{ip: ip, bits: 64},
		{ip: net.ParseIP("54.158.233.194"), bits: 96},
		{ip: net.ParseIP("2001:db8::1"), bits: 72},
	} {
		if _, ok := extractRFC6052IPv4(tc.ip, tc.bits); ok {
			t.Fatalf("extractRFC6052IPv4(%s, %d) accepted invalid format", tc.ip, tc.bits)
		}
	}
}

func TestDiscoverNAT64PrefixesUsesIPv4OnlyARPA(t *testing.T) {
	prefix := net.ParseIP("fd00:aa:bb:2090::")
	resolver := staticIPResolver{
		"ip6 ipv4only.arpa": {
			synthesizeRFC6052(t, prefix, 96, net.ParseIP("192.0.0.170")),
			synthesizeRFC6052(t, prefix, 96, net.ParseIP("192.0.0.171")),
		},
	}
	prefixes, err := discoverNAT64Prefixes(context.Background(), resolver)
	if err != nil {
		t.Fatalf("discoverNAT64Prefixes: %v", err)
	}
	if len(prefixes) != 1 || prefixes[0].bits != 96 {
		t.Fatalf("prefixes = %+v, want one /96 prefix", prefixes)
	}
	if !prefixMatches(prefix, prefixes[0].network, 96) {
		t.Fatalf("prefix network = %s, want %s/96", prefixes[0].network, prefix)
	}
}

func TestValidateProviderBaseURLBlocksPrivateDestinations(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{
		"http://127.0.0.1:8080/v1",
		"http://10.0.0.8/v1",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]:8080/v1",
	} {
		if err := validateProviderBaseURL(ctx, raw, false); err == nil {
			t.Errorf("validateProviderBaseURL(%q) accepted a private destination", raw)
		}
	}
	if err := validateProviderBaseURL(ctx, "http://127.0.0.1:11434/v1", true); err != nil {
		t.Fatalf("local provider was rejected: %v", err)
	}
	if err := validateProviderBaseURL(ctx, "http://10.0.0.8/v1", true); err == nil {
		t.Fatal("local provider accepted a non-loopback private destination")
	}
	if err := validateProviderBaseURL(ctx, "ftp://8.8.8.8/v1", false); err == nil {
		t.Fatal("non-HTTP provider URL was accepted")
	}
	if err := validateProviderBaseURL(ctx, "https://user:pass@8.8.8.8/v1", false); err == nil {
		t.Fatal("provider URL containing userinfo was accepted")
	}
	if err := validateProviderBaseURL(ctx, "https://8.8.8.8/v1", false); err != nil {
		t.Fatalf("public provider URL was rejected: %v", err)
	}
}

func TestRequireSetupAccessIsLoopbackOnlyWithoutBearer(t *testing.T) {
	cfg := config.Default()
	s := &Server{cfg: cfg}
	remote := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	remote.RemoteAddr = "192.0.2.10:1234"
	rr := httptest.NewRecorder()
	if !s.requireSetupAccess(rr, remote) {
		t.Fatal("remote setup request was allowed without a bearer token")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remote setup returned %d, want 403", rr.Code)
	}

	local := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	rr = httptest.NewRecorder()
	if s.requireSetupAccess(rr, local) {
		t.Fatalf("loopback setup request was rejected: %s", rr.Body.String())
	}
}

func TestWildcardCORSDoesNotEnableCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Server.CORSOrigins = []string{"*"}
	s := &Server{cfg: cfg}
	h := s.withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.Header.Set("Origin", "https://attacker.invalid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow-origin = %q, want wildcard", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("wildcard CORS enabled credentials: %q", got)
	}
}

func TestMCPRegistrationRequiresProtection(t *testing.T) {
	s := &Server{cfg: config.Default()}
	r := httptest.NewRequest(http.MethodPost, "/api/mcp/servers", nil)
	rr := httptest.NewRecorder()
	s.handleAddMCPServer(rr, r)
	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("unprotected MCP registration returned %d, want 428", rr.Code)
	}

	// A configured bearer token is accepted by the sensitive-action gate.
	s.cfg.Server.AuthToken = "synthetic-token"
	r = httptest.NewRequest(http.MethodPost, "/api/mcp/servers", nil)
	r.Header.Set("Authorization", "Bearer synthetic-token")
	rr = httptest.NewRecorder()
	// The request will fail during JSON decoding after passing the gate.
	s.handleAddMCPServer(rr, r)
	if rr.Code == http.StatusPreconditionRequired {
		t.Fatal("valid bearer token did not pass MCP protection gate")
	}
}

func TestDashboardGateAcceptsAllowlistedQueryCapability(t *testing.T) {
	cfg := config.Default()
	cfg.Server.AuthToken = "synthetic-token"
	s := &Server{cfg: cfg}
	r := httptest.NewRequest(http.MethodGet, "/api/files/raw?token=synthetic-token", nil)
	rr := httptest.NewRecorder()
	if s.requireDashboardPassword(rr, r) {
		t.Fatalf("allowlisted media capability was rejected: %s", rr.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/config/raw?token=synthetic-token", nil)
	rr = httptest.NewRecorder()
	if !s.requireDashboardPassword(rr, r) || rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("ordinary query token bypassed dashboard gate: code=%d body=%s", rr.Code, rr.Body.String())
	}
}
