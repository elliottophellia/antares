package llm

import (
	"context"
	"strings"
)

// OpenCode Zen ("OpenCode Go") is a subscription gateway that serves two wire
// formats from one base URL, chosen per model rather than per provider:
//
//	MiniMax and Qwen  → {base}/messages          Anthropic Messages API,
//	                                             auth via x-api-key.
//	everything else   → {base}/chat/completions  OpenAI Chat Completions,
//	                                             auth via Authorization: Bearer.
//
// Every other adapter in this package picks its wire format once, from the
// provider's `kind`. OpenCode cannot: a single configured provider serves both.
// So openCodeClient is a thin router that builds the right underlying adapter
// for the model in the request and forwards to it.
//
// Reference: 9router's open-sse/executors/opencode-go.js, which does the same
// split (MESSAGES_FORMAT_MODELS → /messages + x-api-key, else Bearer).
const openCodeDefaultBaseURL = "https://opencode.ai/zen/go/v1"

// openCodeAnthropicModels are the model families OpenCode serves over the
// Anthropic Messages API. Matching is on the bare id after any "provider/"
// prefix, and is prefix-based rather than an exact set so a new revision of a
// known family routes correctly on its own (the live catalogue already carries
// qwen3.8-max, which no static list predicted).
//
// Everything else defaults to OpenAI /chat/completions, which is right for the
// GLM, Kimi, DeepSeek, MiMo, GPT, Grok and Hunyuan families currently served.
var openCodeAnthropicModels = []string{
	"minimax-",
	"qwen3",
}

// openCodeUsesMessagesAPI reports whether model must go to /messages rather
// than /chat/completions.
func openCodeUsesMessagesAPI(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	// A "provider/model" spec reaches the adapter already resolved, but be
	// tolerant of a stray prefix so routing never silently falls to the wrong
	// wire format.
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	for _, p := range openCodeAnthropicModels {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// openCodeClient routes each request to the adapter matching the model's wire
// format. Both underlying adapters are built once and share the caller's
// Options (timeout, retries, proxy, headers).
type openCodeClient struct {
	anthropic *anthropicClient
	openai    *openAIClient
}

func newOpenCode(o Options) (Client, error) {
	if o.BaseURL == "" {
		o.BaseURL = openCodeDefaultBaseURL
	}
	base := strings.TrimRight(o.BaseURL, "/")

	// Both adapters append their own endpoint path to BaseURL, so they take the
	// same root. Copy Options per adapter: they must not share a headers map.
	antOpts := o
	antOpts.BaseURL = base
	antOpts.Headers = cloneHeaders(o.Headers)

	oaOpts := o
	oaOpts.BaseURL = base
	oaOpts.Headers = cloneHeaders(o.Headers)

	return &openCodeClient{
		anthropic: &anthropicClient{opts: antOpts},
		openai:    &openAIClient{opts: oaOpts, vendor: "compat"},
	}, nil
}

func cloneHeaders(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Kind reports "opencode" so callers can recognise the provider, while the
// per-request adapter decides the actual wire format.
func (c *openCodeClient) Kind() string { return "opencode" }

// pick returns the adapter for a model.
func (c *openCodeClient) pick(model string) Client {
	if openCodeUsesMessagesAPI(model) {
		return c.anthropic
	}
	return c.openai
}

func (c *openCodeClient) Chat(ctx context.Context, req Request) (*Response, error) {
	return c.pick(req.Model).Chat(ctx, req)
}

func (c *openCodeClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	return c.pick(req.Model).Stream(ctx, req, emit)
}

// Models lists what the subscription exposes. OpenCode serves its catalogue in
// OpenAI shape at /models, so ask the OpenAI adapter regardless of which wire
// format any individual model uses.
func (c *openCodeClient) Models(ctx context.Context) ([]ModelInfo, error) {
	return c.openai.Models(ctx)
}

// Embed is not offered by OpenCode Zen.
func (c *openCodeClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	return nil, ErrUnsupported
}
