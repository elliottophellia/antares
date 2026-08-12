# DNS64-Aware Provider URL Validation Design

## Summary

Antares currently rejects a provider URL when any resolved address is private,
link-local, loopback, multicast, or otherwise non-public. That is the correct
default for SSRF prevention, but it produces a false positive on DNS64/NAT64
networks: a public provider can resolve to public IPv4 addresses plus synthetic
IPv6 addresses under a network-specific ULA prefix.

On the affected network, `api.cursor.com` resolves to public IPv4 addresses and
to addresses under `fd00:aa:bb:2090::/96`. The low 32 bits of each IPv6 address
encode one of the public IPv4 results. Connecting to that synthetic IPv6
address reaches Cursor successfully, but `net.IP.IsPrivate` causes
`validateProviderBaseURL` to reject it before the credential check.

Antares will recognize standards-compliant DNS64 synthesis while retaining the
existing fail-closed behavior for ordinary private destinations.

## Goals

- Allow public provider URLs to work on RFC 7050/RFC 6052 DNS64 networks.
- Support all RFC 6052 prefix lengths: `/32`, `/40`, `/48`, `/56`, `/64`, and
  `/96`.
- Keep blocking literal private IPs, arbitrary ULA records, loopback,
  link-local, metadata, multicast, documentation, and reserved ranges.
- Prevent an attacker-controlled hostname from passing validation by returning
  one public A record and one unrelated private AAAA record.
- Apply the behavior consistently to all provider connection paths.
- Keep tests hermetic and independent of the machine's DNS configuration.

## Non-goals

- Disabling provider URL SSRF validation.
- Trusting all ULA addresses on a host that also has a public A record.
- Adding provider-specific URL bypasses.
- Implementing a DNSSEC validator inside Antares.
- Persisting a NAT64 prefix across process restarts or network changes.

## Design

### Resolver boundary

Production validation continues to use `net.DefaultResolver`, but the DNS
lookup dependency is represented by a small internal interface matching
`LookupIP`. The public helper keeps its current signature and delegates to an
internal resolver-aware helper. Tests inject deterministic records rather than
depending on external DNS.

IP literals never enter DNS64 handling. They continue through the existing
direct address check, so a literal ULA or private IPv4 address remains blocked.

### Normal provider resolution

For a hostname, the validator resolves all addresses under the existing
two-second lookup context:

1. Public IPv4 and native public IPv6 addresses pass the existing checks.
2. Any directly resolved blocked IPv4 address fails validation.
3. A blocked IPv6 address is retained as a DNS64 candidate; it is not accepted
   merely because the hostname also has a public IPv4 address.
4. If no blocked IPv6 candidate exists, behavior is unchanged.

The validator records the hostname's public IPv4 addresses. A DNS64 candidate
can only be accepted if its embedded IPv4 address exactly matches one of those
public A results.

### NAT64 prefix discovery

When a blocked IPv6 candidate is present, Antares resolves AAAA records for
`ipv4only.arpa` using the same resolver and context. RFC 7050 defines that name
and the well-known IPv4 addresses `192.0.0.170` and `192.0.0.171`.

For each returned IPv6 address, Antares tries the six prefix lengths allowed by
RFC 6052. It validates the reserved `u` octet, extracts the embedded IPv4 bits,
and accepts a prefix candidate only when the result is one of the two
well-known IPv4 addresses. Duplicate prefix candidates are discarded.

Discovery is performed only when needed and is scoped to one validation call.
This avoids stale global state when a laptop changes networks.

### Synthetic-address validation

A blocked IPv6 provider address is treated as DNS64 synthesis only when all of
the following hold:

- it matches one of the prefixes discovered from `ipv4only.arpa`;
- its RFC 6052 reserved `u` octet is zero where applicable;
- an IPv4 address can be extracted using that prefix length;
- the extracted IPv4 address is public under the existing block rules; and
- the extracted IPv4 address exactly matches a public IPv4 result for the
  provider hostname.

Every blocked IPv6 result must satisfy these rules. One unrelated private AAAA
record still rejects the URL. If prefix discovery fails or produces no valid
prefix, validation returns the original non-public-address error.

## Error Handling

- Syntax, scheme, userinfo, literal-IP, and DNS lookup errors retain their
  existing messages.
- DNS64 discovery is a conditional validation step, not a fallback that turns
  resolution failures into success.
- Discovery errors fail closed and do not expose internal resolver details to
  the provider connection response.
- No API key is involved in DNS discovery or error output.

## Testing

Unit tests will use a fake resolver to cover:

- the observed network-specific `/96` ULA prefix;
- at least one prefix that crosses the RFC 6052 `u` octet;
- all six supported prefix lengths through table-driven extraction tests;
- a synthetic IPv6 address whose embedded public IPv4 matches the hostname;
- an arbitrary ULA address alongside a public A record;
- a valid NAT64 prefix with a mismatched hostname A record;
- a synthesized private, loopback, or reserved IPv4 address;
- malformed `ipv4only.arpa` responses and missing DNS64 discovery;
- preservation of the existing literal/private/local-provider behavior.

A server-level regression test will connect the Cursor provider using its
normal `https://api.cursor.com` base URL and an injected DNS64 resolver/client,
proving that validation reaches credential verification without weakening the
provider capability boundary.

Verification will run focused server security tests, the full Go suite, race
tests for touched packages, `go vet`, the web tests/typecheck, a production
build, and a daemon restart plus health check.

## Rollout

The fix is backward-compatible and requires no configuration migration. It
will be added to the existing Cursor integration pull request, installed
locally, and verified on the affected DNS64 network before the user retries
with a newly rotated Cursor key.
