package llm

import (
	"reflect"
	"testing"
)

func TestOfficialReasoningKnownFamilies(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		baseURL  string
		model    string
		wantVals []string
		wantOff  bool
		wantWire string
	}{
		{
			name:     "openai gpt-5.6",
			kind:     "openai",
			baseURL:  "https://api.openai.com/v1",
			model:    "gpt-5.6-sol",
			wantVals: []string{"none", "low", "medium", "high", "xhigh", "max"},
			wantOff:  true,
			wantWire: wireOpenAIEffort,
		},
		{
			name:     "openai o4-mini",
			kind:     "openai",
			baseURL:  "https://api.openai.com/v1",
			model:    "o4-mini",
			wantVals: []string{"low", "medium", "high"},
			wantOff:  false,
			wantWire: wireOpenAIEffort,
		},
		{
			name:    "openai gpt-4o has no reasoning",
			kind:    "openai",
			baseURL: "https://api.openai.com/v1",
			model:   "gpt-4o",
		},
		{
			name:     "anthropic sonnet 4.6 adaptive",
			kind:     "anthropic",
			baseURL:  "https://api.anthropic.com/v1",
			model:    "claude-sonnet-4-6",
			wantVals: []string{"low", "medium", "high", "max"},
			wantOff:  false,
			wantWire: wireAnthropicAdaptive,
		},
		{
			name:     "anthropic 3.7 budget",
			kind:     "anthropic",
			baseURL:  "https://api.anthropic.com/v1",
			model:    "claude-3-7-sonnet-20250219",
			wantVals: []string{"none", "low", "medium", "high"},
			wantOff:  true,
			wantWire: wireAnthropicBudget,
		},
		{
			name:     "gemini 3.6 flash levels",
			kind:     "gemini",
			baseURL:  "https://generativelanguage.googleapis.com/v1beta",
			model:    "gemini-3.6-flash",
			wantVals: []string{"minimal", "low", "medium", "high"},
			wantOff:  false,
			wantWire: wireGeminiLevel,
		},
		{
			name:     "gemini 3.1 pro",
			kind:     "gemini",
			baseURL:  "https://generativelanguage.googleapis.com/v1beta",
			model:    "gemini-3.1-pro-preview",
			wantVals: []string{"low", "medium", "high"},
			wantOff:  false,
			wantWire: wireGeminiLevel,
		},
		{
			name:     "gemini 2.5 flash budget including off",
			kind:     "gemini",
			baseURL:  "https://generativelanguage.googleapis.com/v1beta",
			model:    "gemini-2.5-flash",
			wantVals: []string{"none", "low", "medium", "high"},
			wantOff:  true,
			wantWire: wireGeminiBudget,
		},
		{
			name:     "openrouter unified effort",
			kind:     "openai-compatible",
			baseURL:  "https://openrouter.ai/api/v1",
			model:    "anthropic/claude-sonnet-4.5",
			wantVals: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			wantOff:  true,
			wantWire: wireOpenRouter,
		},
		{
			name:     "xai grok 4.5 cannot disable",
			kind:     "openai-compatible",
			baseURL:  "https://api.x.ai/v1",
			model:    "grok-4.5",
			wantVals: []string{"low", "medium", "high"},
			wantOff:  false,
			wantWire: wireOpenAIEffort,
		},
		{
			name:     "xai grok 4.6 adds xhigh",
			kind:     "xai",
			baseURL:  "https://api.x.ai/v1",
			model:    "grok-4.6",
			wantVals: []string{"low", "medium", "high", "xhigh"},
			wantOff:  false,
			wantWire: wireOpenAIEffort,
		},
		{
			name:     "deepseek v4",
			kind:     "openai-compatible",
			baseURL:  "https://api.deepseek.com",
			model:    "deepseek-v4-pro",
			wantVals: []string{"none", "low", "high", "max"},
			wantOff:  true,
			wantWire: wireDeepSeek,
		},
		{
			name:     "zai glm-5.2",
			kind:     "anthropic",
			baseURL:  "https://api.z.ai/api/anthropic/v1",
			model:    "glm-5.2",
			wantVals: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			wantOff:  true,
			wantWire: wireZai,
		},
		{
			name:     "groq gpt-oss",
			kind:     "openai-compatible",
			baseURL:  "https://api.groq.com/openai/v1",
			model:    "openai/gpt-oss-120b",
			wantVals: []string{"low", "medium", "high"},
			wantOff:  false,
			wantWire: wireOpenAIEffort,
		},
		{
			name:     "groq qwen",
			kind:     "openai-compatible",
			baseURL:  "https://api.groq.com/openai/v1",
			model:    "qwen/qwen3.6-27b",
			wantVals: []string{"none", "default"},
			wantOff:  true,
			wantWire: wireOpenAIEffort,
		},
		{
			name:     "ollama think levels",
			kind:     "openai-compatible",
			baseURL:  "http://localhost:11434/v1",
			model:    "qwen3",
			wantVals: []string{"none", "low", "medium", "high", "max"},
			wantOff:  true,
			wantWire: wireOllama,
		},
		{
			name:     "custom endpoint gets the full official value list",
			kind:     "openai-compatible",
			baseURL:  "http://localhost:8080/v1",
			model:    "glm-5.2",
			wantVals: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			wantOff:  true,
			wantWire: wireOpenAIEffort,
		},
		{
			name:     "custom anthropic-kind host is still custom",
			kind:     "anthropic",
			baseURL:  "http://127.0.0.1:8080/antigravity/v1",
			model:    "claude-opus-4-6",
			wantVals: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
			wantOff:  true,
			wantWire: wireOpenAIEffort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OfficialReasoning(tt.kind, tt.baseURL, tt.model)
			if !reflect.DeepEqual(got.Values, tt.wantVals) {
				t.Fatalf("Values = %#v, want %#v", got.Values, tt.wantVals)
			}
			if got.AllowsOff() != tt.wantOff {
				t.Fatalf("AllowsOff = %v, want %v", got.AllowsOff(), tt.wantOff)
			}
			if got.Wire != tt.wantWire {
				t.Fatalf("Wire = %q, want %q", got.Wire, tt.wantWire)
			}
		})
	}
}

func TestEncodeOfficialReasoningWire(t *testing.T) {
	t.Run("anthropic adaptive uses output_config not budget", func(t *testing.T) {
		cap := OfficialReasoning("anthropic", "https://api.anthropic.com/v1", "claude-opus-4-6")
		enc := cap.Encode("high")
		if enc.Omit {
			t.Fatal("expected encode")
		}
		if enc.Body["thinking"] == nil {
			t.Fatalf("missing thinking: %#v", enc.Body)
		}
		th := enc.Body["thinking"].(map[string]any)
		if th["type"] != "adaptive" {
			t.Fatalf("thinking.type = %v, want adaptive", th["type"])
		}
		if _, ok := th["budget_tokens"]; ok {
			t.Fatal("adaptive must not send budget_tokens")
		}
		oc := enc.Body["output_config"].(map[string]any)
		if oc["effort"] != "high" {
			t.Fatalf("output_config.effort = %v", oc["effort"])
		}
	})

	t.Run("anthropic legacy budget", func(t *testing.T) {
		cap := OfficialReasoning("anthropic", "https://api.anthropic.com/v1", "claude-3-7-sonnet-20250219")
		enc := cap.Encode("medium")
		th := enc.Body["thinking"].(map[string]any)
		if th["type"] != "enabled" {
			t.Fatalf("type = %v", th["type"])
		}
		if th["budget_tokens"] != 8192 {
			t.Fatalf("budget_tokens = %v, want 8192", th["budget_tokens"])
		}
	})

	t.Run("openai effort omitted when empty", func(t *testing.T) {
		cap := OfficialReasoning("openai", "https://api.openai.com/v1", "gpt-5.6")
		if !cap.Encode("").Omit {
			t.Fatal("empty effort must omit so the provider default applies")
		}
	})

	t.Run("openai rejects unknown effort", func(t *testing.T) {
		cap := OfficialReasoning("openai", "https://api.openai.com/v1", "o4-mini")
		if !cap.Encode("xhigh").Omit {
			t.Fatal("o4-mini does not accept xhigh")
		}
	})

	t.Run("openrouter uses reasoning object", func(t *testing.T) {
		cap := OfficialReasoning("openai-compatible", "https://openrouter.ai/api/v1", "openai/gpt-5")
		enc := cap.Encode("minimal")
		if _, ok := enc.Body["reasoning_effort"]; ok {
			t.Fatal("openrouter must not send reasoning_effort")
		}
		r := enc.Body["reasoning"].(map[string]any)
		if r["effort"] != "minimal" {
			t.Fatalf("reasoning.effort = %v", r["effort"])
		}
	})

	t.Run("gemini 3 level", func(t *testing.T) {
		cap := OfficialReasoning("gemini", "", "gemini-3.6-flash")
		enc := cap.Encode("minimal")
		tc := enc.GeminiThinking
		if tc["thinkingLevel"] != "MINIMAL" {
			t.Fatalf("thinkingLevel = %v", tc["thinkingLevel"])
		}
		if _, ok := tc["thinkingBudget"]; ok {
			t.Fatal("gemini 3 must not send thinkingBudget")
		}
	})

	t.Run("gemini 2.5 off is budget 0", func(t *testing.T) {
		cap := OfficialReasoning("gemini", "", "gemini-2.5-flash")
		enc := cap.Encode("none")
		if enc.GeminiThinking["thinkingBudget"] != 0 {
			t.Fatalf("off budget = %v", enc.GeminiThinking["thinkingBudget"])
		}
	})

	t.Run("deepseek none disables thinking", func(t *testing.T) {
		cap := OfficialReasoning("openai-compatible", "https://api.deepseek.com", "deepseek-chat")
		enc := cap.Encode("none")
		th := enc.Body["thinking"].(map[string]any)
		if th["type"] != "disabled" {
			t.Fatalf("thinking.type = %v", th["type"])
		}
	})

	t.Run("deepseek max", func(t *testing.T) {
		cap := OfficialReasoning("openai-compatible", "https://api.deepseek.com", "deepseek-v4-pro")
		enc := cap.Encode("max")
		if enc.Body["reasoning_effort"] != "max" {
			t.Fatalf("reasoning_effort = %v", enc.Body["reasoning_effort"])
		}
	})

	t.Run("zai off disables thinking", func(t *testing.T) {
		cap := OfficialReasoning("anthropic", "https://api.z.ai/api/anthropic/v1", "glm-5.2")
		enc := cap.Encode("none")
		th := enc.Body["thinking"].(map[string]any)
		if th["type"] != "disabled" {
			t.Fatalf("thinking.type = %v", th["type"])
		}
	})

	t.Run("grok cannot send none", func(t *testing.T) {
		cap := OfficialReasoning("openai-compatible", "https://api.x.ai/v1", "grok-4.5")
		if !cap.Encode("none").Omit {
			t.Fatal("grok-4.5 must not send none")
		}
	})

	t.Run("custom endpoint sends the chosen official value", func(t *testing.T) {
		cap := OfficialReasoning("openai-compatible", "http://127.0.0.1:8080/v1", "glm-5.2")
		enc := cap.Encode("max")
		if enc.Omit {
			t.Fatal("custom must send the selected value")
		}
		if enc.Body["reasoning_effort"] != "max" {
			t.Fatalf("reasoning_effort = %v", enc.Body["reasoning_effort"])
		}
	})

	t.Run("custom auto omits the field", func(t *testing.T) {
		cap := OfficialReasoning("openai-compatible", "http://127.0.0.1:8080/v1", "default")
		if !cap.Encode("").Omit {
			t.Fatal("Auto must send nothing")
		}
	})
}
