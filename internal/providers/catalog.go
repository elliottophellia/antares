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
