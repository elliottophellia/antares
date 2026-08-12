# DNS64-Aware Provider Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow every public Antares provider to connect through standards-compliant DNS64/NAT64 without weakening provider URL SSRF protections.

**Architecture:** Add small RFC 6052 extraction and RFC 7050 prefix-discovery helpers behind an injectable `LookupIP` interface. Provider validation will continue to reject every blocked address unless a blocked IPv6 result matches a locally discovered NAT64 prefix, embeds a public IPv4 address, and that IPv4 exactly matches the provider hostname's public A result. A per-server resolver override will make the real credential handler testable without external DNS.

**Tech Stack:** Go standard library (`context`, `net`, `net/url`), existing `net/http/httptest` server tests, Bun/TypeScript verification, GitHub Actions.

## Global Constraints

- Support RFC 6052 prefix lengths `/32`, `/40`, `/48`, `/56`, `/64`, and `/96`.
- Keep literal private IPs, arbitrary ULAs, loopback, link-local, metadata, multicast, documentation, carrier-grade NAT, and reserved ranges blocked exactly as before.
- Preserve the existing exception that a built-in local provider may use loopback, but not another private-network address.
- Never accept a private AAAA record merely because the hostname also has a public A record.
- Accept a synthesized IPv6 address only when its embedded IPv4 is public and exactly matches a public IPv4 result for the same hostname.
- DNS64 discovery failures must fail closed with the original non-public-address error.
- Use no new dependencies and never log, return, or persist test credentials.
- Keep the existing two-second DNS validation deadline.

---

### Task 1: Add RFC 6052 extraction and RFC 7050 discovery primitives

**Files:**
- Modify: `internal/server/security.go:99-163`
- Modify: `internal/server/security_test.go:1-101`

**Interfaces:**
- Produces: `type providerIPResolver interface { LookupIP(context.Context, string, string) ([]net.IP, error) }`
- Produces: `type nat64Prefix struct { network net.IP; bits int }`
- Produces: `extractRFC6052IPv4(ip net.IP, prefixBits int) (net.IP, bool)`
- Produces: `discoverNAT64Prefixes(ctx context.Context, resolver providerIPResolver) ([]nat64Prefix, error)`
- Produces: test-only `staticIPResolver` and `synthesizeRFC6052`

- [ ] **Step 1: Add table-driven failing extraction tests**

Add these imports and test helpers to `internal/server/security_test.go`:

```go
import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
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
```

Then add:

```go
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
```

- [ ] **Step 2: Run the extraction tests and verify RED**

Run:

```bash
go test ./internal/server -run '^TestExtractRFC6052' -count=1
```

Expected: build failure because `extractRFC6052IPv4` does not exist.

- [ ] **Step 3: Implement RFC 6052 extraction**

Add to `internal/server/security.go`:

```go
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
```

- [ ] **Step 4: Run extraction tests and verify GREEN**

Run:

```bash
go test ./internal/server -run '^TestExtractRFC6052' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add a failing RFC 7050 discovery test**

Add:

```go
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
```

- [ ] **Step 6: Run discovery test and verify RED**

Run:

```bash
go test ./internal/server -run '^TestDiscoverNAT64Prefixes' -count=1
```

Expected: build failure because the resolver interface, `nat64Prefix`,
`prefixMatches`, and `discoverNAT64Prefixes` do not exist.

- [ ] **Step 7: Implement resolver abstraction and prefix discovery**

Add:

```go
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
```

- [ ] **Step 8: Run helper tests and the existing security tests**

Run:

```bash
go test ./internal/server -run '^(TestExtractRFC6052|TestDiscoverNAT64Prefixes|TestValidateProviderBaseURLBlocksPrivateDestinations)$' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the primitives**

```bash
git add internal/server/security.go internal/server/security_test.go
git commit -m "Add standards-based DNS64 prefix discovery"
```

---

### Task 2: Make provider URL validation DNS64-aware and exercise the Cursor handler

**Files:**
- Modify: `internal/server/security.go:99-163`
- Modify: `internal/server/security_test.go:74-101`
- Modify: `internal/server/server.go:45-80`
- Modify: `internal/server/handlers_setup.go:248-253,381-386,556-561`
- Modify: `internal/server/handlers_providers.go:277-288`
- Modify: `internal/server/cursor_provider_test.go:102-145`

**Interfaces:**
- Consumes: `providerIPResolver`, `nat64Prefix`, `discoverNAT64Prefixes`, `extractRFC6052IPv4`
- Produces: `validateProviderBaseURLWithResolver(context.Context, string, bool, providerIPResolver) error`
- Produces: `(*Server).validateProviderBaseURL(context.Context, string, bool) error`
- Produces: test-only `Server.providerResolver`

- [ ] **Step 1: Add DNS64 acceptance and SSRF rejection tests**

Add to `internal/server/security_test.go`:

```go
func dns64Resolver(t *testing.T, targetHost string, targetV4 net.IP) staticIPResolver {
	t.Helper()
	prefix := net.ParseIP("fd00:aa:bb:2090::")
	return staticIPResolver{
		"ip " + targetHost: {
			targetV4,
			synthesizeRFC6052(t, prefix, 96, targetV4),
		},
		"ip6 ipv4only.arpa": {
			synthesizeRFC6052(t, prefix, 96, net.ParseIP("192.0.0.170")),
			synthesizeRFC6052(t, prefix, 96, net.ParseIP("192.0.0.171")),
		},
	}
}

func TestValidateProviderBaseURLAllowsDiscoveredDNS64(t *testing.T) {
	resolver := dns64Resolver(t, "api.cursor.com", net.ParseIP("54.158.233.194"))
	err := validateProviderBaseURLWithResolver(
		context.Background(), "https://api.cursor.com", false, resolver)
	if err != nil {
		t.Fatalf("DNS64 provider rejected: %v", err)
	}
}

func TestValidateProviderBaseURLRejectsUnrelatedULAOnMixedDNS(t *testing.T) {
	resolver := dns64Resolver(t, "provider.example", net.ParseIP("54.158.233.194"))
	resolver["ip provider.example"] = append(
		resolver["ip provider.example"], net.ParseIP("fd00:dead:beef::1"))
	if err := validateProviderBaseURLWithResolver(
		context.Background(), "https://provider.example", false, resolver); err == nil {
		t.Fatal("mixed public DNS with an unrelated ULA was accepted")
	}
}

func TestValidateProviderBaseURLRejectsDNS64AddressForDifferentARecord(t *testing.T) {
	prefix := net.ParseIP("fd00:aa:bb:2090::")
	resolver := dns64Resolver(t, "provider.example", net.ParseIP("54.158.233.194"))
	resolver["ip provider.example"] = []net.IP{
		net.ParseIP("54.158.233.194"),
		synthesizeRFC6052(t, prefix, 96, net.ParseIP("54.225.153.71")),
	}
	if err := validateProviderBaseURLWithResolver(
		context.Background(), "https://provider.example", false, resolver); err == nil {
		t.Fatal("DNS64 address whose embedded IPv4 mismatched the A record was accepted")
	}
}

func TestValidateProviderBaseURLRejectsMissingOrMalformedDNS64Discovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		discovery []net.IP
	}{
		{name: "missing"},
		{name: "malformed", discovery: []net.IP{net.ParseIP("2001:4860::1")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := dns64Resolver(t, "provider.example", net.ParseIP("54.158.233.194"))
			if tc.discovery == nil {
				delete(resolver, "ip6 ipv4only.arpa")
			} else {
				resolver["ip6 ipv4only.arpa"] = tc.discovery
			}
			if err := validateProviderBaseURLWithResolver(
				context.Background(), "https://provider.example", false, resolver); err == nil {
				t.Fatal("provider passed without a valid discovered DNS64 prefix")
			}
		})
	}
}

func TestDNS64MatchRejectsEmbeddedPrivateIPv4(t *testing.T) {
	prefix := nat64Prefix{network: net.ParseIP("fd00:aa:bb:2090::"), bits: 96}
	ip := synthesizeRFC6052(t, prefix.network, prefix.bits, net.ParseIP("10.0.0.8"))
	publicV4 := map[string]struct{}{"10.0.0.8": {}}
	if dns64AddressMatches(ip, []nat64Prefix{prefix}, publicV4) {
		t.Fatal("DNS64 address embedding a private IPv4 was accepted")
	}
}
```

- [ ] **Step 2: Run DNS64 validator tests and verify RED**

Run:

```bash
go test ./internal/server -run '^(TestValidateProviderBaseURLAllowsDiscoveredDNS64|TestValidateProviderBaseURLRejects.*DNS|TestDNS64MatchRejectsEmbeddedPrivateIPv4)$' -count=1
```

Expected: build failure because `validateProviderBaseURLWithResolver` and
`dns64AddressMatches` do not exist.

- [ ] **Step 3: Implement fail-closed DNS64-aware validation**

Refactor `validateProviderBaseURL` so it is a wrapper:

```go
func validateProviderBaseURL(ctx context.Context, raw string, allowLocal bool) error {
	return validateProviderBaseURLWithResolver(ctx, raw, allowLocal, net.DefaultResolver)
}
```

Add:

```go
func providerIPError(ip net.IP) error {
	return fmt.Errorf("provider base_url resolves to a non-public address (%s)", ip.String())
}

func dns64AddressMatches(ip net.IP, prefixes []nat64Prefix, publicV4 map[string]struct{}) bool {
	for _, prefix := range prefixes {
		if !prefixMatches(ip, prefix.network, prefix.bits) {
			continue
		}
		embedded, ok := extractRFC6052IPv4(ip, prefix.bits)
		if !ok || providerIPBlocked(embedded) {
			continue
		}
		if _, ok := publicV4[embedded.String()]; ok {
			return true
		}
	}
	return false
}
```

Move the existing syntax checks into
`validateProviderBaseURLWithResolver`, then replace its hostname-resolution
loop with:

```go
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := resolver.LookupIP(lookupCtx, "ip", host)
	if err != nil {
		return fmt.Errorf("provider host cannot be resolved: %w", err)
	}
	if len(ips) == 0 {
		return errors.New("provider host has no address")
	}

	publicV4 := map[string]struct{}{}
	var blockedV6 []net.IP
	for _, ip := range ips {
		blocked := providerIPBlocked(ip) && !(allowLocal && ip.IsLoopback())
		if !blocked {
			if v4 := ip.To4(); v4 != nil && !providerIPBlocked(v4) {
				publicV4[v4.String()] = struct{}{}
			}
			continue
		}
		if ip.To4() != nil {
			return providerIPError(ip)
		}
		blockedV6 = append(blockedV6, append(net.IP(nil), ip...))
	}
	if len(blockedV6) == 0 {
		return nil
	}

	prefixes, err := discoverNAT64Prefixes(lookupCtx, resolver)
	if err != nil {
		return providerIPError(blockedV6[0])
	}
	for _, ip := range blockedV6 {
		if !dns64AddressMatches(ip, prefixes, publicV4) {
			return providerIPError(ip)
		}
	}
	return nil
```

Keep the literal-IP branch before any resolver call and route it through the
same existing private/loopback check.

- [ ] **Step 4: Run security tests and verify GREEN**

Run:

```bash
go test ./internal/server -run '^(TestExtractRFC6052|TestDiscoverNAT64Prefixes|TestValidateProviderBaseURL|TestDNS64Match)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add a failing handler-level Cursor DNS64 regression**

In `internal/server/server.go`, add a test-only resolver field beside
`cursorFactory`:

```go
	// providerResolver overrides provider hostname resolution in handler tests.
	// Production uses net.DefaultResolver.
	providerResolver providerIPResolver
```

Do not wire it yet. Add this tracking wrapper to
`internal/server/cursor_provider_test.go`:

```go
type trackingIPResolver struct {
	providerIPResolver
	calls int
}

func (r *trackingIPResolver) LookupIP(
	ctx context.Context, network, host string,
) ([]net.IP, error) {
	r.calls++
	return r.providerIPResolver.LookupIP(ctx, network, host)
}
```

In `TestConnectCursorPreservesActiveModel`, configure:

```go
	resolver := &trackingIPResolver{
		providerIPResolver: dns64Resolver(
			t, "api.cursor.com", net.ParseIP("54.158.233.194")),
	}
	s.providerResolver = resolver
```

Add `net` to the test imports, and change the request body to omit the
hermetic IP-literal override:

```go
	req := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key"}`))
```

After checking the response status, assert that the handler used the injected
resolver:

```go
	if resolver.calls == 0 {
		t.Fatal("Cursor connection bypassed the server provider resolver")
	}
```

- [ ] **Step 6: Run the handler test and verify RED**

Run:

```bash
go test ./internal/server -run '^TestConnectCursorPreservesActiveModel$' -count=1
```

Expected: FAIL on every network because the handler bypasses the injected
resolver; on a non-DNS64 network the request can otherwise succeed, but
`resolver.calls` remains zero.

- [ ] **Step 7: Route every provider handler through the server resolver**

Add to `internal/server/security.go`:

```go
func (s *Server) validateProviderBaseURL(ctx context.Context, raw string, allowLocal bool) error {
	resolver := s.providerResolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return validateProviderBaseURLWithResolver(ctx, raw, allowLocal, resolver)
}
```

Replace all four production calls:

```go
validateProviderBaseURL(r.Context(), baseURL, chosen.Local)
```

or:

```go
validateProviderBaseURL(r.Context(), baseURL, allowLocal)
```

with:

```go
s.validateProviderBaseURL(r.Context(), baseURL, chosen.Local)
```

or:

```go
s.validateProviderBaseURL(r.Context(), baseURL, allowLocal)
```

The affected files are `handlers_setup.go` (three calls) and
`handlers_providers.go` (one call).

- [ ] **Step 8: Run focused handler and security tests**

Run:

```bash
go test ./internal/server -run '^(TestConnectCursorPreservesActiveModel|TestExtractRFC6052|TestDiscoverNAT64Prefixes|TestValidateProviderBaseURL|TestDNS64Match)' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run touched-package race tests**

Run:

```bash
go test -race ./internal/server -run '^(TestConnectCursorPreservesActiveModel|TestValidateProviderBaseURL|TestDiscoverNAT64Prefixes)' -count=1
```

Expected: PASS with no race report.

- [ ] **Step 10: Commit the production wiring**

```bash
git add internal/server/security.go internal/server/security_test.go \
  internal/server/server.go internal/server/handlers_setup.go \
  internal/server/handlers_providers.go internal/server/cursor_provider_test.go
git commit -m "Fix provider connections on DNS64 networks"
```

---

### Task 3: Verify, deploy locally, and update the existing pull request

**Files:**
- Verify only: all Go and web packages
- Generated and restore after build: `internal/server/dist/.gitkeep`, `web/tsconfig.tsbuildinfo`

**Interfaces:**
- Consumes: complete DNS64-aware provider validator
- Produces: green local/CI verification, clean branch, updated PR, healthy local daemon

- [ ] **Step 1: Format and run focused tests**

```bash
gofmt -w internal/server/security.go internal/server/security_test.go \
  internal/server/server.go internal/server/handlers_setup.go \
  internal/server/handlers_providers.go internal/server/cursor_provider_test.go
go test ./internal/server -run '^(TestConnectCursorPreservesActiveModel|TestExtractRFC6052|TestDiscoverNAT64Prefixes|TestValidateProviderBaseURL|TestDNS64Match)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the full Go quality gate**

```bash
env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY -u CURSOR_API_KEY \
  go test ./... -count=1
go vet ./...
```

Expected: all packages PASS and vet exits zero.

- [ ] **Step 3: Run web verification**

```bash
cd web
bun test
bun x tsc -b --noEmit
bun run build
cd ..
```

Expected: tests, typecheck, and production build all exit zero.

- [ ] **Step 4: Confirm the affected network still presents DNS64**

```bash
getent ahosts api.cursor.com
curl -sS -o /dev/null -w 'remote_ip=%{remote_ip} http=%{http_code}\n' \
  --max-time 10 https://api.cursor.com/v1/me
```

Expected: DNS includes public IPv4 plus `fd00:aa:bb:2090::/96` synthetic IPv6;
the unauthenticated request reaches Cursor and returns HTTP 401.

- [ ] **Step 5: Build a clean-version binary, install, and restart**

```bash
git restore internal/server/dist/.gitkeep web/tsconfig.tsbuildinfo
version=$(git describe --tags --always)
commit=$(git rev-parse --short HEAD)
make install-cli VERSION="$version" COMMIT="$commit"
"$HOME/.local/bin/antares" stop
"$HOME/.local/bin/antares" serve
"$HOME/.local/bin/antares" status
curl -fsS -o /dev/null -w 'health_http=%{http_code}\n' \
  http://127.0.0.1:8787/api/health
git restore internal/server/dist/.gitkeep web/tsconfig.tsbuildinfo
git status --short --branch
```

Expected: installed version contains the final commit without `-dirty`, daemon
is running, health is HTTP 200, and only the plan/spec state expected for the
next commit remains.

- [ ] **Step 6: Push the updated branch**

```bash
git push origin HEAD
```

Expected: `feature/cursor-agent-provider-impl` updates the existing PR without
a force push.

- [ ] **Step 7: Wait for and verify pull-request checks**

```bash
gh pr checks 25 --repo enowdev/antares --watch --interval 5
```

Expected: `go` and `web` both pass.

- [ ] **Step 8: Hand off credential retry**

Report that:

- the daemon is running the final commit;
- the health endpoint returns 200;
- DNS64-aware validation is covered by hermetic tests and the live network
  still resolves through NAT64;
- the user should retry Providers → Cursor with a newly rotated key; and
- no pasted credential was committed or used in test output.
