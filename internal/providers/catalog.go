// Package providers holds the catalogue of well-known LLM providers and the
// logic to connect one, shared by the TUI picker and the CLI so both stay in
// sync.
package providers

import (
	"os"

	"github.com/enowdev/antares/internal/config"
)

// Info describes a provider users can connect to in one step.
type Info struct {
	ID       string
	Label    string
	Kind     string
	KeyEnv   string
	BaseURL  string
	NeedsKey bool
	Models   []string
}

// contextWindows records the true context window (in tokens) for models whose
// provider API does not report one, keyed by model id. The agent consults this
// when a config has no explicit model_meta, so the context gauge and compaction
// use the real window instead of the 200k default. Source: provider docs.
var contextWindows = map[string]int{
	// Z.ai GLM — https://docs.z.ai/guides/llm
	"glm-5.2": 1_000_000, // 1M context
	"glm-4.7": 200_000,
	"glm-4.6": 200_000,
}

// ContextWindow returns the known context window for a model id, or 0 if none
// is catalogued.
func ContextWindow(model string) int { return contextWindows[model] }

var catalog = []Info{
	{"anthropic", "Anthropic", "anthropic", "ANTHROPIC_API_KEY", "", true,
		[]string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"}},
	{"openai", "OpenAI", "openai", "OPENAI_API_KEY", "", true,
		[]string{"gpt-5", "gpt-5-mini", "o4-mini"}},
	{"google", "Google Gemini", "gemini", "GEMINI_API_KEY", "", true,
		[]string{"gemini-2.5-pro", "gemini-2.5-flash"}},
	{"openrouter", "OpenRouter", "openai-compatible", "OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", true,
		[]string{"anthropic/claude-sonnet-5", "openai/gpt-5", "google/gemini-2.5-pro"}},
	{"groq", "Groq", "openai-compatible", "GROQ_API_KEY", "https://api.groq.com/openai/v1", true,
		[]string{"llama-3.3-70b-versatile", "moonshotai/kimi-k2-instruct"}},
	{"xai", "xAI Grok", "openai-compatible", "XAI_API_KEY", "https://api.x.ai/v1", true,
		[]string{"grok-4", "grok-3-mini"}},
	{"deepseek", "DeepSeek", "openai-compatible", "DEEPSEEK_API_KEY", "https://api.deepseek.com", true,
		[]string{"deepseek-chat", "deepseek-reasoner"}},
	{"zai", "Z.ai GLM (Coding Plan)", "anthropic", "ZAI_API_KEY", "https://api.z.ai/api/anthropic/v1", true,
		[]string{"glm-5.2", "glm-4.7", "glm-4.6"}},
	// OpenCode's /models endpoint is live and unauthenticated, so the picker
	// fetches the current catalogue; these are only the starting suggestions.
	{"opencode", "OpenCode Go (Zen)", "opencode", "OPENCODE_API_KEY", "https://opencode.ai/zen/go/v1", true,
		[]string{"glm-5.2", "kimi-k3", "deepseek-v4-pro", "minimax-m3", "qwen3.8-max"}},
	{"ollama", "Ollama (local)", "openai-compatible", "", "http://localhost:11434/v1", false,
		[]string{"llama3.1", "qwen2.5"}},
}

// Catalog returns the well-known providers, in display order.
func Catalog() []Info { return catalog }

// For looks up a catalogue entry by id.
func For(id string) (Info, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Info{}, false
}

// Connected reports whether a provider already has a usable credential (an API
// key in config, a set key-env, or being a local provider that needs none).
func Connected(cfg *config.Config, id string) bool {
	info, _ := For(id)
	if info.ID != "" && !info.NeedsKey {
		return true
	}
	if cfg != nil {
		if p, ok := cfg.Providers[id]; ok {
			if p.APIKey != "" {
				return true
			}
			if p.APIKeyEnv != "" && os.Getenv(p.APIKeyEnv) != "" {
				return true
			}
		}
	}
	if info.KeyEnv != "" && os.Getenv(info.KeyEnv) != "" {
		return true
	}
	return false
}

// Activate records credentials (when a key is given), makes the provider the
// active one, and points the default model at it when the current one doesn't
// belong to it. It mutates cfg but does not persist — the caller saves.
func Activate(cfg *config.Config, id, key string) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.Provider{}
	}
	p := cfg.Providers[id]
	if info, ok := For(id); ok {
		if p.Kind == "" {
			p.Kind = info.Kind
		}
		if p.BaseURL == "" {
			p.BaseURL = info.BaseURL
		}
		if p.APIKeyEnv == "" {
			p.APIKeyEnv = info.KeyEnv
		}
		if p.Label == "" {
			p.Label = info.Label
		}
		if len(p.Models) == 0 {
			p.Models = info.Models
		}
		// Record known context windows so the gauge/compaction use the real
		// value (e.g. glm-5.2's 1M) rather than the generic default, and so it
		// is persisted to config for models the provider API cannot report.
		for _, m := range info.Models {
			if w := ContextWindow(m); w > 0 {
				if p.ModelMeta == nil {
					p.ModelMeta = map[string]config.ModelMeta{}
				}
				if p.ModelMeta[m].ContextWindow == 0 {
					p.ModelMeta[m] = config.ModelMeta{ContextWindow: w}
				}
			}
		}
	}
	if key != "" {
		p.APIKey = key
	}
	p.Enabled = true
	cfg.Providers[id] = p

	cfg.Model.Provider = id
	if !contains(p.Models, cfg.Model.Default) && len(p.Models) > 0 {
		cfg.Model.Default = p.Models[0]
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
