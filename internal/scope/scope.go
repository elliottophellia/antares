// Package scope decides whether a target is authorized for testing.
//
// A security role is only lawful against a target its owner has authorized. The
// difference between a penetration test and an attack is that list, so the list
// has to be real — checked in code, not left to a prompt to remember.
//
// A target is authorized only when it matches an entry a person put there. The
// default is deny: an empty scope authorizes nothing, so the security tools
// fail closed until someone says what is in bounds.
package scope

import (
	"fmt"
	"net"
	"strings"
)

// Scope is a set of authorized targets.
type Scope struct {
	// Entries are exact hosts, wildcard domains (*.example.com), or CIDR
	// ranges (10.0.0.0/24).
	Entries []string
}

// Result is the outcome of a check.
type Result struct {
	Target     string
	Authorized bool
	// Matched is the scope entry that authorized it, when one did.
	Matched string
	// Reason explains a denial.
	Reason string
}

// Check reports whether a target is in scope. It parses a bare host, a URL, or
// an address, and compares it against every entry.
func (s Scope) Check(target string) Result {
	host := hostOf(target)
	if host == "" {
		return Result{Target: target, Reason: "could not read a host or address from the target"}
	}
	if len(s.Entries) == 0 {
		return Result{
			Target: host,
			Reason: "the authorized scope is empty — nothing is in bounds until a target is added to it",
		}
	}

	ip := net.ParseIP(host)
	for _, entry := range s.Entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if matches(host, ip, entry) {
			return Result{Target: host, Authorized: true, Matched: entry}
		}
	}
	return Result{
		Target: host,
		Reason: fmt.Sprintf("%s is not in the authorized scope", host),
	}
}

// matches compares one host against one scope entry.
func matches(host string, ip net.IP, entry string) bool {
	el := strings.ToLower(entry)
	hl := strings.ToLower(host)

	// A CIDR range: the host has to be an address inside it.
	if _, cidr, err := net.ParseCIDR(entry); err == nil {
		return ip != nil && cidr.Contains(ip)
	}
	// A wildcard domain authorizes its subdomains, and the bare domain too:
	// *.example.com covers api.example.com and example.com.
	if rest, ok := strings.CutPrefix(el, "*."); ok {
		return hl == rest || strings.HasSuffix(hl, "."+rest)
	}
	// An exact host, or the scope entry being a parent domain of the host.
	// example.com in scope authorizes api.example.com — a common expectation
	// that "the domain" means the domain and what is under it.
	return hl == el || strings.HasSuffix(hl, "."+el)
}

// hostOf extracts the host from a bare host, a URL, or a host:port.
func hostOf(target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	// Strip a scheme.
	if i := strings.Index(t, "://"); i >= 0 {
		t = t[i+3:]
	}
	// Strip a path, query, or fragment.
	for _, sep := range []string{"/", "?", "#"} {
		if i := strings.Index(t, sep); i >= 0 {
			t = t[:i]
		}
	}
	// Strip credentials.
	if i := strings.LastIndex(t, "@"); i >= 0 {
		t = t[i+1:]
	}
	// Strip a port, but not the colons of an IPv6 literal.
	if strings.HasPrefix(t, "[") {
		if i := strings.Index(t, "]"); i >= 0 {
			return t[1:i]
		}
	}
	if strings.Count(t, ":") == 1 {
		if h, _, err := net.SplitHostPort(t); err == nil {
			t = h
		} else if i := strings.LastIndex(t, ":"); i >= 0 {
			t = t[:i]
		}
	}
	return strings.TrimSpace(t)
}

// Valid reports whether an entry is a well-formed host, wildcard, or CIDR, so a
// typo is caught when it is added rather than when a test is refused.
func Valid(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("empty")
	}
	if _, _, err := net.ParseCIDR(entry); err == nil {
		return nil
	}
	host := entry
	if rest, ok := strings.CutPrefix(entry, "*."); ok {
		host = rest
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	// A hostname: letters, digits, hyphens, and dots, with at least one dot.
	if !strings.Contains(host, ".") {
		return fmt.Errorf("%q is not a domain, IP, or CIDR range", entry)
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			return fmt.Errorf("%q has a character that is not valid in a hostname", entry)
		}
	}
	return nil
}
