package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/sardanioss/httpcloak"
)

// defaultHTTPPreset is the browser fingerprint used when none is configured.
const defaultHTTPPreset = "chrome-131"

// httpClients caches one fingerprinted client per preset+proxy so connections
// and the TLS session pool are reused across calls. The clients live for the
// process; there is no per-conversation state to reap.
var httpClients sync.Map // key string -> *httpcloak.Client

func httpClientFor(preset, proxy string) *httpcloak.Client {
	key := preset + "|" + proxy
	if c, ok := httpClients.Load(key); ok {
		return c.(*httpcloak.Client)
	}
	var opts []httpcloak.Option
	if strings.TrimSpace(proxy) != "" {
		opts = append(opts, httpcloak.WithProxy(proxy))
	}
	c := httpcloak.New(preset, opts...)
	actual, loaded := httpClients.LoadOrStore(key, c)
	if loaded {
		c.Close()
	}
	return actual.(*httpcloak.Client)
}

// validPreset returns preset if httpcloak knows it, else the default.
func validPreset(preset string) string {
	preset = strings.TrimSpace(preset)
	if preset == "" {
		return defaultHTTPPreset
	}
	for _, p := range httpcloak.Presets() {
		if p == preset {
			return preset
		}
	}
	return defaultHTTPPreset
}

// ---- http_request -----------------------------------------------------------

type httpRequestTool struct{}

func (httpRequestTool) Name() string { return "http_request" }

func (httpRequestTool) Description() string {
	return "Call an HTTP(S) API with a real browser's TLS and HTTP/2 fingerprint, so requests are not " +
		"rejected by bot-detection layers that inspect the handshake (JA3/JA4, header order, HTTP/2 settings). " +
		"Use it for REST/JSON APIs and any endpoint that blocks a plain HTTP client. " +
		"Returns the status, protocol, response headers, and body. For reading web pages as text, web_fetch is simpler."
}

func (httpRequestTool) Schema() map[string]any {
	return schema(map[string]any{
		"method":       propEnum("HTTP method.", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"),
		"url":          prop("string", "Absolute http(s) URL."),
		"headers":      prop("object", "Request headers as a JSON object, e.g. {\"Authorization\": \"Bearer …\"}."),
		"body":         prop("string", "Raw request body (for POST/PUT/PATCH)."),
		"json":         prop("object", "JSON body to send; sets Content-Type: application/json. Overrides body."),
		"content_type": prop("string", "Content-Type for a raw body. Defaults to application/json when body looks like JSON."),
		"preset":       prop("string", "Fingerprint override, e.g. chrome-131, chrome-131-windows, chrome-133. Defaults to config."),
		"max_chars":    propDefault("integer", "Maximum characters of the response body to return.", 20000),
	}, "url")
}

// RequiresApproval gates the tool: it reaches the network and can send
// state-changing requests (POST/PUT/DELETE) to external services.
func (httpRequestTool) RequiresApproval() bool { return true }

func (httpRequestTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Method      string            `json:"method"`
		URL         string            `json:"url"`
		Headers     map[string]string `json:"headers"`
		Body        string            `json:"body"`
		JSON        json.RawMessage   `json:"json"`
		ContentType string            `json:"content_type"`
		Preset      string            `json:"preset"`
		MaxChars    int               `json:"max_chars"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	target := strings.TrimSpace(args.URL)
	if target == "" {
		return Errorf("url is required")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	method := strings.ToUpper(strings.TrimSpace(args.Method))
	if method == "" {
		method = "GET"
	}
	if args.MaxChars <= 0 || args.MaxChars > 200000 {
		args.MaxChars = 20000
	}

	headers := map[string]string{}
	for k, v := range args.Headers {
		headers[k] = v
	}

	// Body: a json object wins; otherwise a raw body with an inferred or given
	// content type.
	var body []byte
	if len(args.JSON) > 0 && string(args.JSON) != "null" {
		body = []byte(args.JSON)
		if _, ok := headerLookup(headers, "content-type"); !ok {
			headers["Content-Type"] = "application/json"
		}
	} else if args.Body != "" {
		body = []byte(args.Body)
		if _, ok := headerLookup(headers, "content-type"); !ok {
			ct := strings.TrimSpace(args.ContentType)
			if ct == "" {
				ct = inferContentType(args.Body)
			}
			if ct != "" {
				headers["Content-Type"] = ct
			}
		}
	}

	var cfg *config.Config
	if in.Deps != nil {
		cfg = in.Deps.Config
	}
	preset := args.Preset
	proxy := ""
	timeout := time.Duration(0)
	if cfg != nil {
		if preset == "" {
			preset = cfg.Tools.HTTP.Preset
		}
		proxy = cfg.Tools.HTTP.Proxy
		if s := cfg.Tools.HTTP.TimeoutSeconds; s > 0 {
			timeout = time.Duration(s) * time.Second
		}
	}
	preset = validPreset(preset)

	client := httpClientFor(preset, proxy)

	in.Emit(Progress{Tool: "http_request", Message: method + " " + target})

	// httpcloak v1.6+ takes multi-value headers and an io.Reader body.
	hdr := make(map[string][]string, len(headers))
	for k, v := range headers {
		hdr[k] = []string{v}
	}
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	resp, err := client.Do(ctx, &httpcloak.Request{
		Method:  method,
		URL:     target,
		Headers: hdr,
		Body:    bodyReader,
		Timeout: timeout,
	})
	if err != nil {
		return Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// resp.Body is an io.ReadCloser in httpcloak v1.6+; read it here (while the
	// connection is still open) so httpResult works on the bytes.
	respBody, _ := io.ReadAll(resp.Body)

	return httpResult(resp, respBody, method, preset, args.MaxChars)
}

// httpResult formats a response for the model: a status line, the protocol and
// fingerprint used, the response headers, then the (truncated) body.
func httpResult(resp *httpcloak.Response, body []byte, method, preset string, maxChars int) Result {
	var b strings.Builder
	fmt.Fprintf(&b, "%s → HTTP %d (%s, fingerprint %s)\n", method, resp.StatusCode, orUnknown(resp.Protocol), preset)
	if resp.FinalURL != "" {
		fmt.Fprintf(&b, "Final URL: %s\n", resp.FinalURL)
	}

	if len(resp.Headers) > 0 {
		keys := make([]string, 0, len(resp.Headers))
		for k := range resp.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nResponse headers:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", k, strings.Join(resp.Headers[k], ", "))
		}
	}

	b.WriteString("\nBody:\n")
	b.WriteString(truncateText(string(body), maxChars))

	return Result{
		Content: b.String(),
		Meta: map[string]any{
			"status":   resp.StatusCode,
			"protocol": resp.Protocol,
			"url":      resp.FinalURL,
		},
		IsError: resp.StatusCode >= 400,
	}
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown protocol"
	}
	return s
}

// headerLookup finds a header case-insensitively.
func headerLookup(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// inferContentType guesses JSON from a body that starts like a JSON value, so
// the common "paste some JSON" case does not need an explicit content type.
func inferContentType(body string) string {
	t := strings.TrimSpace(body)
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return "application/json"
	}
	return ""
}
