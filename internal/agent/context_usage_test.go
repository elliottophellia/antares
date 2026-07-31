package agent

import (
	"encoding/json"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

func TestContextWindowForPrefersActiveModelMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Model.ContextWindow = 200000
	p := cfg.Providers["custom"]
	p.ModelMeta = map[string]config.ModelMeta{
		"cb-kimi-k3": {ContextWindow: 256000},
	}
	cfg.Providers["custom"] = p

	a := &Agent{cfg: cfg}
	if got := a.contextWindowFor("cb-kimi-k3"); got != 256000 {
		t.Fatalf("contextWindowFor() = %d, want 256000", got)
	}
}

func TestEstimateRequestTokensIncludesToolSchemas(t *testing.T) {
	history := []llm.Message{{Role: llm.RoleUser, Content: "hello"}}
	tools := []llm.Tool{{
		Name:        "large_tool",
		Description: "tool description",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"payload": map[string]any{"type": "string", "description": string(make([]byte, 4000))},
			},
		},
	}}

	base := estimateTokens(history, "system")
	got := estimateRequestTokens(history, "system", tools)
	schema, err := json.Marshal(tools[0].Parameters)
	if err != nil {
		t.Fatal(err)
	}
	wantMinimum := base + len(tools[0].Name)/4 + len(tools[0].Description)/4 + 8 + len(schema)/4
	if got < wantMinimum {
		t.Fatalf("estimateRequestTokens() = %d, want at least %d", got, wantMinimum)
	}
}

func TestUsageEventKeepsCumulativeAndLatestContextSeparate(t *testing.T) {
	e := Event{
		Type:          EventUsage,
		InputTokens:   333000,
		ContextTokens: llm.Usage{InputTokens: 145878, CacheReadTokens: 145408, ContextTokens: 145878}.ContextSize(),
		ContextWindow: 256000,
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["input_tokens"] != float64(333000) {
		t.Fatalf("input_tokens = %v", got["input_tokens"])
	}
	if got["context_tokens"] != float64(145878) {
		t.Fatalf("context_tokens = %v; cached tokens were counted twice", got["context_tokens"])
	}
}
