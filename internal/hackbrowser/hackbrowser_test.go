package hackbrowser

import (
	"testing"
)

func TestNormalizeScope(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://app.test.com:8080/foo?x=1", "app.test.com"},
		{"http://TEST.com/", "test.com"},
		{"*.example.com/path", "*.example.com"},
		{"  APP.test.com  ", "app.test.com"},
		{"https://app.test.com.", "app.test.com"},
		{"", ""},
	}
	for _, c := range cases {
		got := NormalizeScope(c.in)
		if got != c.want {
			t.Errorf("NormalizeScope(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeriveScope(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://test.com", "*.test.com"},
		{"https://app.test.com", "*.test.com"},
		{"https://sub.deep.example.com", "*.example.com"},
		// IP fallback — publicsuffix cannot resolve, falls back to the host.
		{"http://10.0.0.1:8080", "*.10.0.0.1"},
		// Localhost fallback.
		{"http://localhost:3000", "*.localhost"},
	}
	for _, c := range cases {
		got := DeriveScope(c.url)
		if got != c.want {
			t.Errorf("DeriveScope(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestMakeMatcher(t *testing.T) {
	m := MakeMatcher([]string{"*.test.com", "api.other.com"})
	cases := []struct {
		host string
		want bool
	}{
		{"test.com", true},          // base match
		{"app.test.com", true},      // subdomain
		{"deep.app.test.com", true}, // deep subdomain
		{"api.other.com", true},     // bare host match
		{"other.com", false},        // parent of bare host — no match
		{"evil.com", false},         // unrelated
		{"test.com.evil.com", false}, // suffix attack
	}
	for _, c := range cases {
		if got := m(c.host); got != c.want {
			t.Errorf("MakeMatcher: host %q = %v, want %v", c.host, got, c.want)
		}
	}

	// Empty scope → reject everything.
	empty := MakeMatcher(nil)
	if empty("anything.com") {
		t.Errorf("empty MakeMatcher should reject all hosts")
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://test.com/", "https://test.com/"},     // root keeps "/"
		{"https://test.com", "https://test.com/"},      // bare host → root
		{"https://test.com/a/", "https://test.com/a"},  // trailing slash dropped
		{"https://test.com/a/b/", "https://test.com/a/b"},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		got := NormalizeURL(c.in)
		if got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePlan(t *testing.T) {
	t.Run("form and click", func(t *testing.T) {
		raw := `{"tasks":[
			{"type":"form","fields":[{"role":"textbox","label":"Email","value":"a@b.com"},{"role":"textbox","label":"Password","value":"hunter2"}],"submit":{"role":"button","label":"Sign in"}},
			{"type":"click","role":"button","label":"Open menu"}
		]}`
		plan, err := parsePlan(raw)
		if err != nil {
			t.Fatalf("parsePlan: %v", err)
		}
		if len(plan.Tasks) != 2 {
			t.Fatalf("expected 2 tasks, got %d", len(plan.Tasks))
		}
		if plan.Tasks[0].Type != "form" || len(plan.Tasks[0].Fields) != 2 {
			t.Errorf("form task shape wrong: %+v", plan.Tasks[0])
		}
		if plan.Tasks[0].Submit == nil || plan.Tasks[0].Submit.Label != "Sign in" {
			t.Errorf("submit wrong: %+v", plan.Tasks[0].Submit)
		}
		if plan.Tasks[1].Type != "click" || plan.Tasks[1].Label != "Open menu" {
			t.Errorf("click task wrong: %+v", plan.Tasks[1])
		}
	})

	t.Run("intelligence fields", func(t *testing.T) {
		raw := `{"tasks":[],"pageState":"empty","revisitAfter":"any-mutation","revisitReason":"no rows yet","revisitOn":"user-added"}`
		plan, err := parsePlan(raw)
		if err != nil {
			t.Fatalf("parsePlan: %v", err)
		}
		if plan.PageState != PageStateEmpty {
			t.Errorf("PageState = %q, want empty", plan.PageState)
		}
		if plan.RevisitAfter != "any-mutation" {
			t.Errorf("RevisitAfter = %q", plan.RevisitAfter)
		}
		if plan.RevisitOn != "user-added" {
			t.Errorf("RevisitOn = %q", plan.RevisitOn)
		}
	})

	t.Run("empty without reason downgrades", func(t *testing.T) {
		raw := `{"tasks":[],"pageState":"empty"}`
		plan, _ := parsePlan(raw)
		if plan.PageState != PageStateUnknown {
			t.Errorf("pageState=empty without reason should downgrade to unknown, got %q", plan.PageState)
		}
	})

	t.Run("no JSON returns empty plan", func(t *testing.T) {
		raw := `The page has nothing to do.`
		plan, err := parsePlan(raw)
		if err != nil {
			t.Fatalf("empty-plan case should not error: %v", err)
		}
		if len(plan.Tasks) != 0 {
			t.Errorf("expected 0 tasks, got %d", len(plan.Tasks))
		}
	})

	t.Run("triggersMutation preserved", func(t *testing.T) {
		raw := `{"tasks":[{"type":"click","role":"button","label":"Add user","triggersMutation":"user-added"}]}`
		plan, _ := parsePlan(raw)
		if len(plan.Tasks) != 1 || plan.Tasks[0].TriggersMutation != "user-added" {
			t.Errorf("triggersMutation not preserved: %+v", plan.Tasks)
		}
	})
}

func TestGenerateFingerprint(t *testing.T) {
	elements := []RawElement{
		{Role: "textbox", Label: "Email", Type: "email", Enabled: true},
		{Role: "textbox", Label: "Name", Type: "text", Enabled: true},
		{Role: "button", Label: "Save", InChrome: false},
		{Role: "button", Label: "Logout", InChrome: true}, // site chrome — excluded
		{Role: "link", Label: "Home", Href: "/"},          // links always excluded
	}
	fp1 := GenerateFingerprint(elements)
	if fp1 == "" {
		t.Fatal("fingerprint should not be empty")
	}
	// Same inputs → same fingerprint (sorted, deterministic).
	fp2 := GenerateFingerprint(elements)
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", fp1, fp2)
	}
	// Reordering inputs → same fingerprint (sort normalizes).
	reordered := []RawElement{elements[1], elements[0], elements[3], elements[2], elements[4]}
	fp3 := GenerateFingerprint(reordered)
	if fp1 != fp3 {
		t.Errorf("fingerprint should be order-independent: %q vs %q", fp1, fp3)
	}
	// Different content → different fingerprint.
	changed := append([]RawElement(nil), elements...)
	changed[0] = RawElement{Role: "textbox", Label: "Email", Type: "email", Enabled: false}
	if fp4 := GenerateFingerprint(changed); fp4 == fp1 {
		t.Errorf("fingerprint should differ when enabled flips")
	}
}

func TestHasSuccessfulMutation(t *testing.T) {
	cases := []struct {
		name string
		logs []string
		want bool
	}{
		{"empty", nil, false},
		{"only GETs", []string{"GET /api/users [200]"}, false},
		{"2xx POST", []string{"POST /api/users [201]"}, true},
		{"2xx PUT", []string{"PUT /api/users/1 [200]"}, true},
		{"4xx POST", []string{"POST /api/users [400]"}, false},
		{"5xx PATCH", []string{"PATCH /api/users/1 [500]"}, false},
		{"2xx DELETE", []string{"DELETE /api/users/1 [204]"}, true},
		{"mixed", []string{"GET /x [200]", "POST /y [401]", "PUT /z [200]"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasSuccessfulMutation(c.logs); got != c.want {
				t.Errorf("HasSuccessfulMutation(%v) = %v, want %v", c.logs, got, c.want)
			}
		})
	}
}

func TestBuildRawRequest(t *testing.T) {
	raw := BuildRawRequest("POST", "https://api.test.com/users?x=1",
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer xyz"},
		`{"name":"alice"}`)
	if got := "POST /users?x=1 HTTP/1.1\r\n"; !contains(raw, got) {
		t.Errorf("request line missing:\nwant prefix: %q\ngot:\n%s", got, raw)
	}
	if !contains(raw, "Host: api.test.com\r\n") {
		t.Errorf("auto Host header missing:\n%s", raw)
	}
	if !contains(raw, "Authorization: Bearer xyz\r\n") {
		t.Errorf("Authorization header missing:\n%s", raw)
	}
	if !contains(raw, "\r\n\r\n{\"name\":\"alice\"}") {
		t.Errorf("body separator / body missing:\n%s", raw)
	}

	// When the headers already include Host, do not duplicate it.
	raw2 := BuildRawRequest("GET", "https://test.com/x",
		map[string]string{"Host": "other.com"}, "")
	count := 0
	for i := 0; i < len(raw2)-6; i++ {
		if raw2[i:i+6] == "Host: " {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one Host header, got %d in:\n%s", count, raw2)
	}
}

func TestParseRequestParams(t *testing.T) {
	t.Run("query only", func(t *testing.T) {
		p := ParseRequestParams("", "https://api.test.com/x?a=1&b=two")
		if p["a"] != "1" || p["b"] != "two" {
			t.Errorf("query params wrong: %v", p)
		}
	})
	t.Run("json body", func(t *testing.T) {
		body := `{"user":{"name":"alice","age":30},"tags":["a","b"]}`
		p := ParseRequestParams(body, "https://api.test.com/x")
		if p["user.name"] != "alice" {
			t.Errorf("nested JSON not flattened: %v", p)
		}
		if p["user.age"] != "30" {
			t.Errorf("numeric JSON value not stringified: %v", p)
		}
	})
	t.Run("form-encoded body", func(t *testing.T) {
		p := ParseRequestParams("name=alice&role=admin", "https://api.test.com/x")
		if p["name"] != "alice" || p["role"] != "admin" {
			t.Errorf("form params wrong: %v", p)
		}
	})
}

func TestCorrelateWithUI(t *testing.T) {
	ui := &UIContext{
		PageURL: "https://test.com/form",
		Fields: []UIField{
			{Name: "username"},
			{Name: "password"},
		},
	}
	params := map[string]string{
		"username": "alice",
		"password": "hunter2",
		"csrf_token": "abc123", // hidden
		"debug":     "1",       // hidden
	}
	out := CorrelateWithUI(ui, params)
	if out == nil {
		t.Fatal("CorrelateWithUI returned nil")
	}
	if len(out.HiddenParams) != 2 {
		t.Errorf("expected 2 hidden params, got %v", out.HiddenParams)
	}
	// Original ui should be untouched (out is a copy).
	if len(ui.HiddenParams) != 0 {
		t.Errorf("CorrelateWithUI mutated its input")
	}
}

func TestClassifyAuthURL(t *testing.T) {
	cases := map[string]string{
		"https://test.com/login":         "login",
		"https://test.com/signin":        "login",
		"https://test.com/auth/login":    "login",
		"https://test.com/register":      "register",
		"https://test.com/signup":        "register",
		"https://test.com/logout":        "logout",
		"https://test.com/users":         "",
		"https://test.com/dashboard":     "",
	}
	for url, want := range cases {
		if got := ClassifyAuthURL(url); got != want {
			t.Errorf("ClassifyAuthURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestCapElements(t *testing.T) {
	// Build 100 elements (more than maxElements=50); include 30 actions and 70 inputs.
	var els []rawScannerElement
	for i := 0; i < 30; i++ {
		els = append(els, rawScannerElement{Role: "button", Label: "b" + string(rune('a'+i%26))})
	}
	for i := 0; i < 70; i++ {
		els = append(els, rawScannerElement{Role: "textbox", Label: "t" + string(rune('a'+i%26))})
	}
	capped := capElements(els)
	if len(capped) > maxElements {
		t.Errorf("capped list exceeds limit: %d > %d", len(capped), maxElements)
	}
	// All 30 actions should survive (action priority).
	actionCount := 0
	for _, e := range capped {
		if e.Role == "button" {
			actionCount++
		}
	}
	if actionCount != 30 {
		t.Errorf("expected all 30 action elements to survive cap, got %d", actionCount)
	}
}

func TestSampleTemplates(t *testing.T) {
	// 10 numbered siblings + 2 unique buttons.
	var els []rawScannerElement
	for i := 1; i <= 10; i++ {
		// Simulate "Item 1".."Item 10" — same role+digit-masked template.
		els = append(els, rawScannerElement{Role: "button", Label: "Item " + itoa(i)})
	}
	els = append(els, rawScannerElement{Role: "button", Label: "Save"})
	els = append(els, rawScannerElement{Role: "button", Label: "Cancel"})
	sampled := sampleTemplates(els)
	// Expected: first 5 of the templated cluster (MAX_PER_TEMPLATE) + the 2 uniques.
	if len(sampled) != maxPerTemplate+2 {
		t.Errorf("expected %d after sampling, got %d", maxPerTemplate+2, len(sampled))
	}
}

// itoa is the smallest possible int-to-string converter — avoids pulling strconv
// into a test that only needs single-digit numbers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
