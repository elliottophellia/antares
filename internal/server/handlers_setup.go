package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

// setupProvider is one option in the onboarding picker. The catalogue lives
// here rather than in the frontend so the terminal wizard and the web wizard
// stay in step.
type setupProvider struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Hint    string   `json:"hint"`
	KeyHint string   `json:"key_hint,omitempty"`
	KeyURL  string   `json:"key_url,omitempty"`
	BaseURL string   `json:"base_url,omitempty"`
	Local   bool     `json:"local"`
	Models  []string `json:"models,omitempty"`
	HasKey  bool     `json:"has_key"`
}

func setupProviderCatalogue(cfg *config.Config) []setupProvider {
	out := []setupProvider{
		{
			ID: "openrouter", Label: "OpenRouter", Kind: "openai-compatible",
			Hint:    "One key, most models — the easiest start.",
			KeyHint: "sk-or-v1-…", KeyURL: "https://openrouter.ai/keys",
			BaseURL: "https://openrouter.ai/api/v1",
			Models: []string{
				"anthropic/claude-sonnet-4.5", "openai/gpt-5",
				"google/gemini-2.5-pro", "deepseek/deepseek-chat",
			},
		},
		{
			ID: "anthropic", Label: "Anthropic", Kind: "anthropic",
			Hint:    "Claude, direct. Extended thinking and prompt caching.",
			KeyHint: "sk-ant-…", KeyURL: "https://console.anthropic.com/settings/keys",
			BaseURL: "https://api.anthropic.com/v1",
			Models:  []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"},
		},
		{
			ID: "openai", Label: "OpenAI", Kind: "openai",
			Hint:    "GPT, direct.",
			KeyHint: "sk-…", KeyURL: "https://platform.openai.com/api-keys",
			BaseURL: "https://api.openai.com/v1",
			Models:  []string{"gpt-5", "gpt-5-mini", "gpt-4.1"},
		},
		{
			ID: "gemini", Label: "Google Gemini", Kind: "gemini",
			Hint:    "Gemini, direct.",
			KeyURL:  "https://aistudio.google.com/apikey",
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			Models:  []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		},
		{
			ID: "ollama", Label: "Ollama", Kind: "openai-compatible",
			Hint:    "Runs on this machine. No key needed.",
			BaseURL: "http://127.0.0.1:11434/v1", Local: true,
		},
		{
			ID: "lmstudio", Label: "LM Studio", Kind: "openai-compatible",
			Hint:    "Runs on this machine. No key needed.",
			BaseURL: "http://127.0.0.1:1234/v1", Local: true,
		},
		{
			ID: "custom", Label: "Something else", Kind: "openai-compatible",
			Hint: "Any OpenAI-compatible endpoint.",
		},
	}
	for i := range out {
		if p, ok := cfg.Providers[out[i].ID]; ok {
			out[i].HasKey = p.APIKey != ""
			if p.BaseURL != "" {
				out[i].BaseURL = p.BaseURL
			}
		}
	}
	return out
}

// NeedsSetup reports whether Antares can answer at all yet.
func NeedsSetup(cfg *config.Config) bool {
	if strings.TrimSpace(cfg.Model.Default) == "" {
		return true
	}
	_, p := cfg.ResolveProvider(cfg.Model.Provider)
	return p.APIKey == "" && !isLocalEndpoint(p.BaseURL)
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	home, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup": NeedsSetup(cfg),
		"model":       cfg.Model.Default,
		"provider":    cfg.Model.Provider,
		"workspace":   cfg.Agent.Workspace,
		"home":        home,
		"config_path": configPath(),
		"providers":   setupProviderCatalogue(cfg),
		"database":    cfg.Database.Driver,
	})
}

// handleSetupTest verifies a credential and returns the model list in one
// round trip, so the wizard can move straight to picking a model.
func (s *Server) handleSetupTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg := s.config()
	catalogue := setupProviderCatalogue(cfg)
	var chosen *setupProvider
	for i := range catalogue {
		if catalogue[i].ID == body.Provider {
			chosen = &catalogue[i]
			break
		}
	}
	if chosen == nil {
		writeError(w, http.StatusBadRequest, errors.New("unknown provider"))
		return
	}

	baseURL := firstNonEmpty(body.BaseURL, chosen.BaseURL)
	apiKey := body.APIKey
	// A redacted value means "keep what is stored".
	if strings.Contains(apiKey, "••••") || apiKey == "" {
		if p, ok := cfg.Providers[body.Provider]; ok {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" && !isLocalEndpoint(baseURL) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": "An API key is required for this provider.",
		})
		return
	}

	client, err := llm.New(llm.Options{
		Kind: chosen.Kind, BaseURL: baseURL, APIKey: apiKey,
		ProviderID: body.Provider, Timeout: 30 * time.Second,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	models, err := client.Models(ctx)
	if err != nil {
		// Some endpoints refuse /models but still answer chat; a rejected
		// credential is the case worth reporting.
		if llm.IsAuthError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "error": "The provider rejected this API key.",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "models": []any{}, "suggested": chosen.Models,
			"note": "Connected, but this provider does not publish a model list.",
		})
		return
	}

	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "models": ids, "suggested": chosen.Models,
	})
}

// handleSetupComplete writes everything the wizard collected in one save.
func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider  string `json:"provider"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		Model     string `json:"model"`
		Workspace string `json:"workspace"`
		Database  struct {
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
		} `json:"database"`
		RAG struct {
			Enabled    bool   `json:"enabled"`
			Provider   string `json:"provider"`
			EmbedModel string `json:"embed_model"`
			EnowxURL   string `json:"enowx_url"`
			EnowxToken string `json:"enowx_token"`
		} `json:"rag"`
		Telegram string `json:"telegram_token"`
		Discord  string `json:"discord_token"`
		Language string `json:"language"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	catalogue := setupProviderCatalogue(cfg)
	var chosen *setupProvider
	for i := range catalogue {
		if catalogue[i].ID == body.Provider {
			chosen = &catalogue[i]
			break
		}
	}
	if chosen == nil {
		writeError(w, http.StatusBadRequest, errors.New("unknown provider"))
		return
	}

	entry := cfg.Providers[body.Provider]
	entry.Kind = chosen.Kind
	entry.Enabled = true
	entry.Label = chosen.Label
	if url := firstNonEmpty(body.BaseURL, chosen.BaseURL); url != "" {
		entry.BaseURL = url
	}
	if key := strings.TrimSpace(body.APIKey); key != "" && !strings.Contains(key, "••••") {
		entry.APIKey = key
	}
	cfg.Providers[body.Provider] = entry

	cfg.Model.Provider = body.Provider
	cfg.Model.Default = strings.TrimSpace(body.Model)

	if ws := strings.TrimSpace(body.Workspace); ws != "" {
		cfg.Agent.Workspace = config.Expand(ws)
		if err := os.MkdirAll(cfg.Agent.Workspace, 0o755); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	if d := strings.TrimSpace(body.Database.Driver); d != "" {
		cfg.Database.Driver = d
		if dsn := strings.TrimSpace(body.Database.DSN); dsn != "" {
			cfg.Database.DSN = dsn
		}
	}

	cfg.RAG.Enabled = body.RAG.Enabled
	if body.RAG.Enabled {
		if p := strings.TrimSpace(body.RAG.Provider); p != "" {
			cfg.RAG.Provider = p
		}
		if m := strings.TrimSpace(body.RAG.EmbedModel); m != "" {
			cfg.RAG.EmbedModel = m
		}
		if cfg.RAG.EmbedProvider == "" {
			cfg.RAG.EmbedProvider = body.Provider
		}
		if u := strings.TrimSpace(body.RAG.EnowxURL); u != "" {
			cfg.RAG.EnowxBaseURL = u
		}
		if tk := strings.TrimSpace(body.RAG.EnowxToken); tk != "" {
			cfg.RAG.EnowxToken = tk
		}
	}

	if tk := strings.TrimSpace(body.Telegram); tk != "" {
		cfg.Gateway.Enabled = true
		cfg.Gateway.Telegram.Enabled = true
		cfg.Gateway.Telegram.BotToken = tk
	}
	if tk := strings.TrimSpace(body.Discord); tk != "" {
		cfg.Gateway.Enabled = true
		cfg.Gateway.Discord.Enabled = true
		cfg.Gateway.Discord.BotToken = tk
	}
	if lang := strings.TrimSpace(body.Language); lang != "" {
		cfg.Display.Language = lang
	}

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"model":    cfg.Model.Default,
		"provider": cfg.Model.Provider,
		"restart_required": cfg.Database.Driver != s.db.Driver() ||
			cfg.Gateway.Telegram.Enabled || cfg.Gateway.Discord.Enabled,
	})
}
