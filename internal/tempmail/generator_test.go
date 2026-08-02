package tempmail

import (
	"strings"
	"testing"
)

func TestGenerateCreatesPlausibleAddress(t *testing.T) {
	generator := NewGenerator(nil)
	address, err := generator.Generate(t.Context(), "@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(address, "@example.com") {
		t.Fatalf("address = %q, want example.com domain", address)
	}
	if strings.Count(address, "@") != 1 {
		t.Fatalf("address = %q, want exactly one @", address)
	}
}

func TestGenerateRejectsInvalidDomain(t *testing.T) {
	generator := NewGenerator(nil)
	for _, domain := range []string{"", "two@example.com", "has space.com"} {
		if _, err := generator.Generate(t.Context(), domain); err == nil {
			t.Errorf("Generate(%q) succeeded, want error", domain)
		}
	}
}

func TestParseDomains(t *testing.T) {
	domains, err := parseDomains([]byte(`[{"ascii":"first.test"},{"ascii":"second.test"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 2 || domains[0] != "first.test" || domains[1] != "second.test" {
		t.Fatalf("domains = %#v", domains)
	}
}

func TestParseInboxDropsHeaderAndExtractsCode(t *testing.T) {
	page := `<div class="from_div_45g45gg">From</div>` +
		`<div class="subj_div_45g45gg">Subject</div>` +
		`<div class="time_div_45g45gg">Time (UTC)</div>` +
		`<div class="from_div_45g45gg">noreply@example.com</div>` +
		`<div class="subj_div_45g45gg">Your verification code is ABC-123</div>` +
		`<div class="time_div_45g45gg">2026-08-02 10:11:12</div>`
	messages := parseInbox(page, "person@example.com")
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if code := ExtractCode(messages[0]); code != "ABC-123" {
		t.Fatalf("code = %q, want ABC-123", code)
	}
	if messages[0].ReceivedAt.IsZero() {
		t.Fatal("received time was not parsed")
	}
}

func TestParseBodyRemovesTrailingScript(t *testing.T) {
	page := `<div class="mess_bodiyy"><p>Verify here</p><script>bad()</script>`
	if body := parseBody(page); body != "<p>Verify here</p>" {
		t.Fatalf("body = %q", body)
	}
}
