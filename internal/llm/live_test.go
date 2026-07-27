package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// These are end-to-end smoke tests for the providers that need live third-party
// credentials. Each skips unless its credentials are present in the environment,
// so `go test ./...` stays hermetic, but a real request can be verified with one
// command once you have set the keys. Run them with, e.g.:
//
//	AZURE_OPENAI_ENDPOINT=… AZURE_OPENAI_KEY=… AZURE_OPENAI_DEPLOYMENT=… \
//	  go test ./internal/llm -run TestLiveAzure -v
//
// A passing test means the auth, URL routing, and request/response mapping all
// work against the real service — the last mile the unit tests cannot cover.

func livePrompt() Request {
	return Request{Messages: []Message{{Role: RoleUser, Content: "Reply with the single word: pong"}}, MaxTokens: 16}
}

func liveContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 40*time.Second)
}

func requireReply(t *testing.T, resp *Response, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("live request failed: %v", err)
	}
	if strings.TrimSpace(resp.Content) == "" && len(resp.ToolCalls) == 0 {
		t.Fatalf("live request returned nothing: %+v", resp)
	}
	t.Logf("live reply: %.120q", resp.Content)
}

func TestLiveAzure(t *testing.T) {
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	key := os.Getenv("AZURE_OPENAI_KEY")
	deployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT")
	if endpoint == "" || key == "" || deployment == "" {
		t.Skip("set AZURE_OPENAI_ENDPOINT / AZURE_OPENAI_KEY / AZURE_OPENAI_DEPLOYMENT")
	}
	c, err := New(Options{Kind: "azure", BaseURL: endpoint, APIKey: key, APIVersion: os.Getenv("AZURE_OPENAI_API_VERSION")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	req := livePrompt()
	req.Model = deployment
	resp, err := c.Chat(ctx, req)
	requireReply(t, resp, err)
}

func TestLiveBedrock(t *testing.T) {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" || os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Skip("set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (and AWS_REGION, BEDROCK_MODEL)")
	}
	model := os.Getenv("BEDROCK_MODEL")
	if model == "" {
		model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	}
	c, err := New(Options{Kind: "bedrock", Region: os.Getenv("AWS_REGION")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	req := livePrompt()
	req.Model = model
	resp, err := c.Chat(ctx, req)
	requireReply(t, resp, err)
}

func TestLiveVertex(t *testing.T) {
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("VERTEX_SA_JSON") == "" {
		t.Skip("set GOOGLE_APPLICATION_CREDENTIALS (or VERTEX_SA_JSON) and GOOGLE_CLOUD_PROJECT")
	}
	model := os.Getenv("VERTEX_MODEL")
	if model == "" {
		model = "gemini-1.5-pro"
	}
	c, err := New(Options{Kind: "vertex", APIKey: os.Getenv("VERTEX_SA_JSON"), Region: os.Getenv("GOOGLE_CLOUD_REGION")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	req := livePrompt()
	req.Model = model
	resp, err := c.Chat(ctx, req)
	requireReply(t, resp, err)
}

func TestLiveCopilot(t *testing.T) {
	gh := os.Getenv("COPILOT_GITHUB_TOKEN")
	if gh == "" {
		t.Skip("set COPILOT_GITHUB_TOKEN (from `antares auth copilot`)")
	}
	model := os.Getenv("COPILOT_MODEL")
	if model == "" {
		model = "gpt-4o"
	}
	c, err := New(Options{Kind: "copilot", APIKey: gh})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	req := livePrompt()
	req.Model = model
	resp, err := c.Chat(ctx, req)
	requireReply(t, resp, err)
}

func TestLiveCodex(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("set OPENAI_API_KEY (and optionally CODEX_MODEL) for the Responses API")
	}
	model := os.Getenv("CODEX_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	c, err := New(Options{Kind: "codex", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	req := livePrompt()
	req.Model = model
	resp, err := c.Chat(ctx, req)
	requireReply(t, resp, err)
}

func TestLiveSpeakRoundTrip(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("set OPENAI_API_KEY to verify TTS + STT end to end")
	}
	c, err := New(Options{Kind: "openai", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	ac, ok := c.(AudioClient)
	if !ok {
		t.Fatal("openai client should be an AudioClient")
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	audio, _, err := ac.Speak(ctx, "", "", "", "Testing one two three.")
	if err != nil || len(audio) == 0 {
		t.Fatalf("speak failed: %v (%d bytes)", err, len(audio))
	}
	text, err := ac.Transcribe(ctx, "", "test.mp3", audio)
	if err != nil {
		t.Fatalf("transcribe failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(text), "testing") {
		t.Fatalf("round-trip lost the words: %q", text)
	}
}
