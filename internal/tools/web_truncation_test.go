package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/config"
)

// truncateText is the cap behind eight model-facing outputs: web_fetch's page
// text and its error body, web_search snippets, http_request bodies, knowledge
// hits, intercepted request and response bodies, and Ghidra output. It cut at a
// byte offset and then reported the bytes it dropped as characters, so a page
// of CJK came back as broken bytes under a notice off by a factor of three.
func TestTruncateTextKeepsValidUTF8AndCountsCharacters(t *testing.T) {
	in := strings.Repeat("あ", 100) // 300 bytes, 100 characters
	got := truncateText(in, 40)

	if !utf8.ValidString(got) {
		t.Fatalf("truncateText produced invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("あ", 40)) {
		t.Fatalf("truncateText kept the wrong text: %q", got)
	}
	if !strings.Contains(got, "truncated (60 more characters)") {
		t.Fatalf("notice does not report the 60 characters dropped: %q", got)
	}
}

// The budget is a character budget, so text inside it is returned whole and
// unannotated. At a byte offset a 100-character page under a 1000-character
// budget was fine, but the same page under a 100-character budget lost 66
// characters it had room for.
func TestTruncateTextLeavesTextInsideTheBudgetAlone(t *testing.T) {
	in := strings.Repeat("あ", 100) // 300 bytes, 100 characters
	if got := truncateText(in, 100); got != in {
		t.Fatalf("a %d-character string was cut under a 100-character budget, keeping %d characters",
			utf8.RuneCountInString(in), utf8.RuneCountInString(got))
	}
}

// End to end through web_fetch, which is the caller that was measured: a page
// of CJK returned 1094 bytes that were not valid UTF-8, under a notice reading
// "truncated (270 more characters)" for a 100-character string that had lost 90.
func TestWebFetchReturnsValidUTF8WithAnHonestCount(t *testing.T) {
	page := strings.Repeat("あ", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, page)
	}))
	t.Cleanup(srv.Close)

	args, _ := json.Marshal(map[string]any{"url": srv.URL, "max_chars": 10})
	res := (webFetchTool{}).Execute(context.Background(), Input{
		Args: args,
		Deps: &Deps{Config: &config.Config{}},
	})
	if res.IsError {
		t.Fatalf("web_fetch failed: %s", res.Content)
	}
	if !utf8.ValidString(res.Content) {
		t.Fatalf("web_fetch returned invalid UTF-8: %q", res.Content)
	}
	if !strings.Contains(res.Content, "truncated (90 more characters)") {
		t.Fatalf("web_fetch reported the wrong number of characters dropped: %q", res.Content)
	}
	if !strings.Contains(res.Content, strings.Repeat("あ", 10)) {
		t.Fatalf("web_fetch kept the wrong text: %q", res.Content)
	}
}
