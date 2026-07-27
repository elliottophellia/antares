package scope

import "testing"

func TestEmptyScopeAuthorizesNothing(t *testing.T) {
	// Fail closed: with nothing declared in bounds, everything is out.
	r := Scope{}.Check("example.com")
	if r.Authorized {
		t.Fatal("an empty scope authorized a target")
	}
	if r.Reason == "" {
		t.Fatal("a denial with no reason is not useful")
	}
}

func TestExactHost(t *testing.T) {
	s := Scope{Entries: []string{"app.example.com"}}
	if !s.Check("app.example.com").Authorized {
		t.Fatal("the exact host was not authorized")
	}
	if s.Check("other.example.com").Authorized {
		t.Fatal("a different host was authorized")
	}
}

func TestParentDomainAuthorizesSubdomain(t *testing.T) {
	s := Scope{Entries: []string{"example.com"}}
	if !s.Check("api.example.com").Authorized {
		t.Fatal("a subdomain of an in-scope domain was refused")
	}
	if !s.Check("example.com").Authorized {
		t.Fatal("the domain itself was refused")
	}
	// A domain that merely ends with the same letters is not a subdomain.
	if s.Check("notexample.com").Authorized {
		t.Fatal("a look-alike domain was authorized")
	}
	if s.Check("evil-example.com").Authorized {
		t.Fatal("a look-alike domain was authorized")
	}
}

func TestWildcard(t *testing.T) {
	s := Scope{Entries: []string{"*.example.com"}}
	if !s.Check("api.example.com").Authorized {
		t.Fatal("a subdomain was refused")
	}
	if !s.Check("example.com").Authorized {
		t.Fatal("the bare domain was refused by a wildcard")
	}
	if s.Check("example.org").Authorized {
		t.Fatal("a different domain was authorized")
	}
}

func TestCIDR(t *testing.T) {
	s := Scope{Entries: []string{"10.0.0.0/24"}}
	if !s.Check("10.0.0.5").Authorized {
		t.Fatal("an address inside the range was refused")
	}
	if s.Check("10.0.1.5").Authorized {
		t.Fatal("an address outside the range was authorized")
	}
	if s.Check("example.com").Authorized {
		t.Fatal("a hostname matched a CIDR range")
	}
}

func TestHostExtraction(t *testing.T) {
	s := Scope{Entries: []string{"app.example.com"}}
	// The check should see through a URL, a port, credentials, and a path.
	for _, target := range []string{
		"https://app.example.com/login?next=/",
		"app.example.com:8443",
		"http://user:pass@app.example.com/admin",
		"app.example.com",
	} {
		if !s.Check(target).Authorized {
			t.Errorf("%q was not recognised as the in-scope host", target)
		}
	}
}

func TestIPv6(t *testing.T) {
	s := Scope{Entries: []string{"2001:db8::/32"}}
	if !s.Check("[2001:db8::1]:443").Authorized {
		t.Fatal("an IPv6 address inside the range was refused")
	}
	if s.Check("[2001:dead::1]").Authorized {
		t.Fatal("an IPv6 address outside the range was authorized")
	}
}

func TestMatchedEntryIsReported(t *testing.T) {
	s := Scope{Entries: []string{"other.com", "*.example.com"}}
	r := s.Check("api.example.com")
	if !r.Authorized || r.Matched != "*.example.com" {
		t.Fatalf("got %+v", r)
	}
}

func TestValid(t *testing.T) {
	good := []string{"example.com", "*.example.com", "10.0.0.0/24", "192.168.1.1", "a.b.c.example.com"}
	for _, e := range good {
		if err := Valid(e); err != nil {
			t.Errorf("Valid(%q) = %v, want nil", e, err)
		}
	}
	bad := []string{"", "localhost", "not a host", "http://example.com", "example"}
	for _, e := range bad {
		if err := Valid(e); err == nil {
			t.Errorf("Valid(%q) = nil, want an error", e)
		}
	}
}
