package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

type modelOptionsProvider struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Kind         string `json:"kind"`
	Enabled      bool   `json:"enabled"`
	HasKey       bool   `json:"has_key"`
	BaseURL      string `json:"base_url"`
	Active       bool   `json:"active"`
	Custom       bool   `json:"custom"`
	NeedsBaseURL bool   `json:"needs_base_url"`
}

type modelOptionsResponse struct {
	Providers []modelOptionsProvider `json:"providers"`
}

func modelOptions(t *testing.T, cfg *config.Config) modelOptionsResponse {
	t.Helper()
	s := &Server{cfg: cfg}
	rr := httptest.NewRecorder()
	s.handleModelOptions(rr, httptest.NewRequest(http.MethodGet, "/api/model/options", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("model options: status = %d (body=%s)", rr.Code, rr.Body.String())
	}
	var response modelOptionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode model options: %v", err)
	}
	return response
}

func customProviderCard(response modelOptionsResponse) (modelOptionsProvider, int) {
	var match modelOptionsProvider
	count := 0
	for _, provider := range response.Providers {
		if provider.ID != "custom" {
			continue
		}
		match = provider
		count++
	}
	return match, count
}

func TestModelOptionsShowsConfiguredLegacyCustomProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["custom"] = config.Provider{
		Kind: "openai-compatible", BaseURL: "https://legacy.example/v1",
		APIKey: "legacy-key", Enabled: true, Label: "Something else",
	}
	cfg.Model.Provider = "custom"

	got, count := customProviderCard(modelOptions(t, cfg))
	if count != 1 {
		t.Fatalf("legacy custom provider cards = %d, want 1", count)
	}
	if got.Label != "Something else" {
		t.Errorf("label = %q, want the stored legacy label", got.Label)
	}
	if got.Kind != "openai-compatible" {
		t.Errorf("kind = %q, want the stored legacy kind", got.Kind)
	}
	if got.BaseURL != "https://legacy.example/v1" {
		t.Errorf("base_url = %q, want the stored legacy endpoint", got.BaseURL)
	}
	if !got.Enabled || !got.HasKey || !got.Active || !got.Custom || !got.NeedsBaseURL {
		t.Errorf("legacy card flags = %+v, want enabled, keyed, active, custom, and needs_base_url", got)
	}
}

func TestModelOptionsHidesUnusedLegacyCustomPlaceholder(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["custom"] = config.Provider{
		Kind: "custom", Label: "Custom endpoint", TimeoutSecs: 300,
	}

	_, count := customProviderCard(modelOptions(t, cfg))
	if count != 0 {
		t.Fatalf("unused legacy custom provider cards = %d, want 0", count)
	}
}
