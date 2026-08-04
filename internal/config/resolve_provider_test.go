package config

import "testing"

func TestResolveProviderDoesNotClobberNamedProviderCredentials(t *testing.T) {
	cfg := Default()
	cfg.Model.Provider = "antigravity"
	cfg.Model.BaseURL = "http://localhost:8080/v1" // stale CodeBuddy
	cfg.Model.APIKey = "sk-codebuddy-stale"
	cfg.Providers = map[string]Provider{
		"antigravity": {
			Kind: "anthropic", Enabled: true,
			BaseURL: "http://localhost:8080/antigravity/v1",
			APIKey:  "sk-antigravity-real",
		},
		"custom": {
			Kind: "openai-compatible", Enabled: true,
			BaseURL: "http://localhost:8080/v1",
			APIKey:  "sk-codebuddy-stale",
		},
	}

	_, p := cfg.ResolveProvider("antigravity")
	if p.BaseURL != "http://localhost:8080/antigravity/v1" {
		t.Fatalf("BaseURL = %q, want antigravity path (must not inherit model.base_url)", p.BaseURL)
	}
	if p.APIKey != "sk-antigravity-real" {
		t.Fatalf("APIKey clobbered by top-level model.api_key")
	}
}

func TestResolveProviderAllowsInlineWhenProviderHasNoCredentials(t *testing.T) {
	cfg := Default()
	cfg.Model.Provider = "inline"
	cfg.Model.BaseURL = "http://localhost:9/v1"
	cfg.Model.APIKey = "sk-inline"
	cfg.Providers = map[string]Provider{
		"inline": {Kind: "openai-compatible", Enabled: true},
	}
	_, p := cfg.ResolveProvider("inline")
	if p.BaseURL != "http://localhost:9/v1" || p.APIKey != "sk-inline" {
		t.Fatalf("expected inline fallback, got base=%q key=%q", p.BaseURL, p.APIKey)
	}
}

// ANTARES_BASE_URL / ANTARES_API_KEY are documented overrides. An operator who
// exports them for a run must win over the named provider's stored credentials —
// otherwise the env vars are silently inert whenever providers.<id> already has
// its own key, which is the common case.
func TestResolveProviderEnvCredentialsOverrideNamedProvider(t *testing.T) {
	t.Setenv("ANTARES_BASE_URL", "http://env-gateway:9000/v1")
	t.Setenv("ANTARES_API_KEY", "sk-from-env")

	cfg := Default()
	cfg.Model.Provider = "zai"
	cfg.Providers = map[string]Provider{
		"zai": {
			Kind: "anthropic", Enabled: true,
			BaseURL: "https://api.z.ai/api/anthropic/v1",
			APIKey:  "sk-stored-in-yaml",
		},
	}
	applyEnv(cfg)

	_, p := cfg.ResolveProvider("zai")
	if p.BaseURL != "http://env-gateway:9000/v1" {
		t.Fatalf("ANTARES_BASE_URL ignored: BaseURL = %q", p.BaseURL)
	}
	if p.APIKey != "sk-from-env" {
		t.Fatalf("ANTARES_API_KEY ignored: APIKey = %q", p.APIKey)
	}
}

func TestClearInlineModelCredentials(t *testing.T) {
	cfg := Default()
	cfg.Model.BaseURL = "http://x"
	cfg.Model.APIKey = "sk-x"
	cfg.ClearInlineModelCredentials()
	if cfg.Model.BaseURL != "" || cfg.Model.APIKey != "" {
		t.Fatalf("not cleared: %+v", cfg.Model)
	}
}
