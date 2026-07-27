package llm

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
)

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

// A provider that is simply not running should say so, not print Go's layered
// dial error with the URL repeated twice.
func TestDescribeTransport(t *testing.T) {
	cases := []struct {
		name string
		err  error
		url  string
		want string
	}{
		{
			"connection refused",
			&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			"http://127.0.0.1:1234/v1/models",
			"nothing is listening at http://127.0.0.1:1234",
		},
		{
			"dns failure",
			&net.DNSError{Name: "api.nowhere.invalid", Err: "no such host", IsNotFound: true},
			"https://api.nowhere.invalid/v1/models",
			"cannot resolve api.nowhere.invalid",
		},
		{
			"timeout",
			context.DeadlineExceeded,
			"https://api.example.com/v1/models",
			"https://api.example.com did not respond in time",
		},
		{
			"other",
			errors.New("broken pipe"),
			"https://api.example.com/v1/models",
			"cannot reach https://api.example.com",
		},
	}
	for _, c := range cases {
		got := describeTransport(c.err, c.url)
		if got.Error() != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got.Error(), c.want)
		}
		if !IsUnreachable(got) {
			t.Errorf("%s: IsUnreachable should be true", c.name)
		}
	}

	// A real response, however bad, is not a transport failure.
	if IsUnreachable(&apiError{Status: 401, Body: `{"error":"nope"}`}) {
		t.Error("an API error must not be classified as unreachable")
	}
}
