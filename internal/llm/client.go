package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/version"
)

// ErrUnsupported is returned by adapters that lack a capability.
var ErrUnsupported = errors.New("operation not supported by this provider")

// Options configures an adapter instance.
type Options struct {
	Kind       string
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	Timeout    time.Duration
	ProviderID string
	HTTPClient *http.Client
}

// New builds the adapter matching kind. Unknown kinds fall back to the
// OpenAI-compatible adapter, which covers most self-hosted endpoints.
func New(o Options) (Client, error) {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: o.Timeout}
	}
	o.BaseURL = strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")

	switch strings.ToLower(strings.TrimSpace(o.Kind)) {
	case "anthropic", "claude":
		if o.BaseURL == "" {
			o.BaseURL = "https://api.anthropic.com/v1"
		}
		return &anthropicClient{opts: o}, nil
	case "gemini", "google":
		if o.BaseURL == "" {
			o.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
		return &geminiClient{opts: o}, nil
	case "openai":
		if o.BaseURL == "" {
			o.BaseURL = "https://api.openai.com/v1"
		}
		return &openAIClient{opts: o, vendor: "openai"}, nil
	case "", "openai-compatible", "openai_compatible", "compat", "custom":
		if o.BaseURL == "" {
			return nil, errors.New("base_url is required for OpenAI-compatible providers")
		}
		return &openAIClient{opts: o, vendor: "compat"}, nil
	default:
		if o.BaseURL == "" {
			return nil, fmt.Errorf("unknown provider kind %q and no base_url set", o.Kind)
		}
		return &openAIClient{opts: o, vendor: "compat"}, nil
	}
}

// apiError carries an upstream HTTP failure with its body for diagnostics.
type apiError struct {
	Status int
	Body   string
	URL    string
}

func (e *apiError) Error() string {
	if msg := extractAPIMessage(e.Body); msg != "" {
		return msg
	}
	if txt := http.StatusText(e.Status); txt != "" {
		return fmt.Sprintf("the provider returned %d %s", e.Status, txt)
	}
	return fmt.Sprintf("the provider returned %d", e.Status)
}

// extractAPIMessage pulls the human sentence out of a provider's error body.
// Vendors disagree on the shape, and none of them are worth showing raw:
//
//	{"error": {"message": "..."}}          OpenAI, Gemini, most compatibles
//	{"type": "error", "error": {...}}      Anthropic
//	{"message": "..."} / {"error": "..."}  everyone else
func extractAPIMessage(body string) string {
	body = strings.TrimSpace(body)
	if body == "" || body[0] != '{' {
		return ""
	}

	var envelope struct {
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return ""
	}

	if len(envelope.Error) > 0 {
		// Either a bare string or a nested object.
		var text string
		if json.Unmarshal(envelope.Error, &text) == nil && text != "" {
			return sentence(text)
		}
		var nested struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil {
			if m := firstNonEmpty(nested.Message, nested.Detail); m != "" {
				return sentence(m)
			}
		}
	}
	return sentence(firstNonEmpty(envelope.Message, envelope.Detail))
}

// sentence trims a provider message to something a UI can show inline.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 240 {
		s = strings.TrimSpace(s[:240]) + "…"
	}
	return s
}

// IsAuthError reports whether err came back as 401/403.
func IsAuthError(err error) bool {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden
	}
	return false
}

// IsRateLimit reports whether err came back as 429.
func IsRateLimit(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests
}

// doJSON issues a JSON request and decodes the response into out.
func (o Options) doJSON(ctx context.Context, method, url string, body any, headers map[string]string, out any) error {
	resp, err := o.do(ctx, method, url, body, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(data), 400))
	}
	return nil
}

func (o Options) do(ctx context.Context, method, url string, body any, headers map[string]string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range o.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		return nil, &apiError{Status: resp.StatusCode, Body: string(data), URL: url}
	}
	return resp, nil
}

// sseLines walks an SSE body, calling fn with each `data:` payload.
// Returning io.EOF from fn stops iteration cleanly.
func sseLines(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	event := ""
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		payload := data.String()
		data.Reset()
		ev := event
		event = ""
		return fn(ev, payload)
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment/keepalive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// toolCallAccumulator reassembles streamed tool-call fragments in order.
type toolCallAccumulator struct {
	order []int
	calls map[int]*ToolCall
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{calls: map[int]*ToolCall{}}
}

func (a *toolCallAccumulator) ensure(idx int) *ToolCall {
	if c, ok := a.calls[idx]; ok {
		return c
	}
	c := &ToolCall{}
	a.calls[idx] = c
	a.order = append(a.order, idx)
	return c
}

func (a *toolCallAccumulator) result() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		c := a.calls[idx]
		if c.Name == "" && c.Arguments == "" {
			continue
		}
		if strings.TrimSpace(c.Arguments) == "" {
			c.Arguments = "{}"
		}
		out = append(out, *c)
	}
	return out
}
