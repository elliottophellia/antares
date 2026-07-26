package llm

import "testing"

// Provider error bodies must never reach a user interface raw; each vendor
// wraps its message differently.
func TestExtractAPIMessage(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			"anthropic",
			`{"type":"error","error":{"type":"authentication_error","message":"x-api-key header is required"},"request_id":"req_01"}`,
			"x-api-key header is required",
		},
		{
			"openai",
			`{"error":{"message":"Incorrect API key provided: sk-xxx.","type":"invalid_request_error","code":"invalid_api_key"}}`,
			"Incorrect API key provided: sk-xxx.",
		},
		{
			"gemini",
			`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`,
			"API key not valid. Please pass a valid API key.",
		},
		{"bare error string", `{"error":"unauthorized"}`, "unauthorized"},
		{"top level message", `{"message":"quota exceeded"}`, "quota exceeded"},
		{"html", `<html><body>502</body></html>`, ""},
		{"empty", ``, ""},
		{"unparseable json", `{oops`, ""},
	}
	for _, c := range cases {
		if got := extractAPIMessage(c.body); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestAPIErrorFallsBackToStatus(t *testing.T) {
	e := &apiError{Status: 502, Body: "<html>bad gateway</html>"}
	if got, want := e.Error(), "the provider returned 502 Bad Gateway"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
