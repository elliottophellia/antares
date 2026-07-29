package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicStreamUsageFromMessageDelta covers Anthropic-compatible
// providers (z.ai / GLM) that report input_tokens only in the message_delta
// event, not in message_start the way real Anthropic does. The adapter must
// still capture the input count so analytics is not stuck at zero.
func TestAnthropicStreamUsageFromMessageDelta(t *testing.T) {
	// message_start carries no input_tokens; message_delta carries the real
	// counts — exactly the shape z.ai's endpoint returns.
	sse := "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"glm-5.2","usage":{"output_tokens":1}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":13,"output_tokens":6,"cache_read_input_tokens":2}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	c := &anthropicClient{opts: Options{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}}
	resp, err := c.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if resp.Usage.InputTokens != 13 {
		t.Errorf("input tokens = %d, want 13", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 6 {
		t.Errorf("output tokens = %d, want 6", resp.Usage.OutputTokens)
	}
	if resp.Usage.CacheReadTokens != 2 {
		t.Errorf("cache read tokens = %d, want 2", resp.Usage.CacheReadTokens)
	}
}

// TestAnthropicStreamUsageFromMessageStart guards the real-Anthropic path:
// input_tokens arrives in message_start and message_delta only updates output.
// The message_delta must not clobber the input count back to zero.
func TestAnthropicStreamUsageFromMessageStart(t *testing.T) {
	sse := "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude","usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":5}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	c := &anthropicClient{opts: Options{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}}
	resp, err := c.Stream(context.Background(), Request{Model: "claude"}, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("input tokens = %d, want 100 (message_delta must not clobber)", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 20 {
		t.Errorf("output tokens = %d, want 20", resp.Usage.OutputTokens)
	}
	if resp.Usage.CacheReadTokens != 5 {
		t.Errorf("cache read tokens = %d, want 5", resp.Usage.CacheReadTokens)
	}
}
