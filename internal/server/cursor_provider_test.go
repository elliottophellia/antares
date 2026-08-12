package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
)

// fakeCursorMetadata is the injectable metadata-client double used across
// these tests. Both calls return the same err, mirroring the brief's shape.
type fakeCursorMetadata struct {
	me     cursor.Me
	models cursor.ModelCatalog
	err    error
}

func (f *fakeCursorMetadata) Me(context.Context) (*cursor.Me, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &f.me, nil
}

func (f *fakeCursorMetadata) Models(context.Context) (*cursor.ModelCatalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &f.models, nil
}

// fakeCursorMetadataSplit lets Me and Models fail independently, so a test can
// pin down the "only save after both calls succeed" guarantee.
type fakeCursorMetadataSplit struct {
	me         cursor.Me
	meErr      error
	models     cursor.ModelCatalog
	modelsErr  error
	meCalls    int
	modelCalls int
}

func (f *fakeCursorMetadataSplit) Me(context.Context) (*cursor.Me, error) {
	f.meCalls++
	if f.meErr != nil {
		return nil, f.meErr
	}
	return &f.me, nil
}

func (f *fakeCursorMetadataSplit) Models(context.Context) (*cursor.ModelCatalog, error) {
	f.modelCalls++
	if f.modelsErr != nil {
		return nil, f.modelsErr
	}
	return &f.models, nil
}

// newCursorTestServer seeds an isolated ANTARES_HOME, saves and reloads cfg
// (so env-derived provider credentials are merged the way production does),
// and returns a Server wired for handler-level tests.
func newCursorTestServer(t *testing.T, seed func(*config.Config)) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	cfg.Server.DashboardPasswordHash = "test-hash"
	if seed != nil {
		seed(cfg)
	}
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	reloaded, err := config.Reload()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	s := &Server{cfg: reloaded, agent: &agent.Agent{}}
	s.agent.SetConfig(reloaded)
	s.reloadFn = func() error { return nil }
	return s
}

// TestConnectCursorPreservesActiveModel guards the primary model boundary:
// connecting Cursor must never touch cfg.Model, even on success.
func TestConnectCursorPreservesActiveModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	cfg.Server.DashboardPasswordHash = "test-hash"
	cfg.Model.Provider = "openrouter"
	cfg.Model.Default = "openai/gpt-5"
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}

	s := &Server{cfg: cfg, agent: &agent.Agent{}}
	s.agent.SetConfig(cfg)
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return &fakeCursorMetadata{
			me:     cursor.Me{APIKeyName: "test"},
			models: cursor.ModelCatalog{Items: []cursor.Model{{ID: "composer-2"}}},
		}, nil
	}
	s.reloadFn = func() error { return nil }

	req := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key"}`))
	req.SetPathValue("id", "cursor")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Provider != "openrouter" || saved.Model.Default != "openai/gpt-5" {
		t.Fatalf("active model changed: %+v", saved.Model)
	}
	if saved.Providers["cursor"].APIKey != "synthetic-key" {
		t.Fatalf("cursor credential was not saved: %+v", saved.Providers["cursor"])
	}
}

// TestSetupStatusOmitsCursorCapability keeps first-run onboarding limited to
// chat-model providers: Cursor must never appear in the setup picker.
func TestSetupStatusOmitsCursorCapability(t *testing.T) {
	s := newCursorTestServer(t, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec := httptest.NewRecorder()
	s.handleSetupStatus(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Providers []setupProvider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, p := range body.Providers {
		if p.ID == "cursor" || p.Capability == "agent" {
			t.Fatalf("setup status exposed an agent-capability provider: %+v", p)
		}
	}
}

// TestSetupCompleteRejectsCursorProvider guards onboarding: the initial setup
// flow must never be able to activate an agent-capability provider.
func TestSetupCompleteRejectsCursorProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg, agent: &agent.Agent{}}
	s.agent.SetConfig(cfg)
	s.reloadFn = func() error { return nil }

	body := `{"provider":"cursor","model":"composer-2","api_key":"synthetic-key"}`
	r := httptest.NewRequest(http.MethodPost, "/api/setup/complete", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	s.handleSetupComplete(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Provider == "cursor" {
		t.Fatal("setup complete activated the cursor provider")
	}
	if saved.Providers["cursor"].APIKey == "synthetic-key" {
		t.Fatal("setup complete stored a cursor credential")
	}
}

// TestModelOptionsReportsCursorAgentCapabilityAndEnvKey covers resolved
// environment credentials and the capability field surfaced to the dashboard.
func TestModelOptionsReportsCursorAgentCapabilityAndEnvKey(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "env-cursor-key")
	s := newCursorTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/model/options", nil)
	rec := httptest.NewRecorder()
	s.handleModelOptions(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Providers []struct {
			ID         string `json:"id"`
			Capability string `json:"capability"`
			HasKey     bool   `json:"has_key"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range body.Providers {
		if p.ID != "cursor" {
			continue
		}
		found = true
		if p.Capability != "agent" {
			t.Fatalf("cursor capability = %q, want agent", p.Capability)
		}
		if !p.HasKey {
			t.Fatal("cursor has_key = false despite CURSOR_API_KEY being set")
		}
	}
	if !found {
		t.Fatal("cursor provider missing from /api/model/options")
	}
}

// TestProviderModelsReturnsCursorCatalog covers the provider-specific model
// endpoint's response shape (ids + display names).
func TestProviderModelsReturnsCursorCatalog(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = "synthetic-key"
		cfg.Providers["cursor"] = p
	})
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return &fakeCursorMetadata{
			models: cursor.ModelCatalog{Items: []cursor.Model{
				{ID: "composer-2", DisplayName: "Composer 2"},
			}},
		}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/providers/cursor/models", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	r.SetPathValue("id", "cursor")
	rec := httptest.NewRecorder()
	s.handleProviderModels(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Models []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Parameters  []string `json:"parameters"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 1 || body.Models[0].ID != "composer-2" || body.Models[0].Name != "Composer 2" {
		t.Fatalf("unexpected models: %+v", body.Models)
	}
}

// TestProviderModelsNeedsKeyWithoutNetworkCall covers the "no resolved key ->
// no network access" guarantee.
func TestCursorProviderModelsNeedsKeyWithoutNetworkCall(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = ""
		p.APIKeyEnv = ""
		cfg.Providers["cursor"] = p
	})
	called := false
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		called = true
		return &fakeCursorMetadata{}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/providers/cursor/models", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	r.SetPathValue("id", "cursor")
	rec := httptest.NewRecorder()
	s.handleProviderModels(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("handleProviderModels reached the network without a resolved key")
	}

	var body struct {
		NeedsKey bool `json:"needs_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.NeedsKey {
		t.Fatal("needs_key = false with no resolved credential")
	}
}

// TestModelListAllExcludesCursor guards model isolation: list-all must never
// call or include Cursor, even when it has a usable (env) credential.
func TestModelListAllExcludesCursor(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "env-cursor-key")
	s := newCursorTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/model/list-all", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleModelListAll(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"provider":"cursor"`) {
		t.Fatalf("list-all touched the cursor provider: %s", rec.Body.String())
	}
}

// TestSetProviderKeyCursorAuthErrorDoesNotLeakKey guards secret-safety: an
// auth rejection must never echo the supplied key back to the caller, and the
// key must not be persisted.
func TestSetProviderKeyCursorAuthErrorDoesNotLeakKey(t *testing.T) {
	s := newCursorTestServer(t, nil)
	secret := "super-secret-key-value"
	s.cursorFactory = func(o cursor.Options) (cursorMetadataClient, error) {
		if o.APIKey != secret {
			t.Fatalf("factory received unexpected api key: %q", o.APIKey)
		}
		return &fakeCursorMetadata{err: &cursor.APIError{
			Status: http.StatusUnauthorized, Message: "unauthorized",
		}}, nil
	}

	body := `{"api_key":"` + secret + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key", strings.NewReader(body))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var respBody struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatal(err)
	}
	if respBody.OK {
		t.Fatal("expected ok=false for an auth error")
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response leaked the supplied api key: %s", rec.Body.String())
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Providers["cursor"].APIKey == secret {
		t.Fatal("rejected credential was saved")
	}
}

// TestSetProviderKeyCursorTransportErrorMapsTo502 covers the transport /
// invalid-response mapping distinct from the auth-rejection 200/ok:false path.
func TestSetProviderKeyCursorTransportErrorMapsTo502(t *testing.T) {
	s := newCursorTestServer(t, nil)
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return &fakeCursorMetadata{err: errors.New("connection reset")}, nil
	}

	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key"}`))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, r)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s, want 502", rec.Code, rec.Body.String())
	}
}

// TestSetProviderKeyCursorSavesOnlyAfterBothCallsSucceed pins the
// verifyCursorProvider contract: a catalog fetch failure after a successful
// identity check must not persist the credential.
func TestSetProviderKeyCursorSavesOnlyAfterBothCallsSucceed(t *testing.T) {
	s := newCursorTestServer(t, nil)
	fake := &fakeCursorMetadataSplit{modelsErr: errors.New("catalog unavailable")}
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return fake, nil
	}

	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key"}`))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, r)
	if rec.Code == http.StatusOK {
		var respBody struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
			t.Fatal(err)
		}
		if respBody.OK {
			t.Fatal("provider reported ok=true despite a failed model-catalog fetch")
		}
	}
	if fake.meCalls == 0 {
		t.Fatal("Me was never called")
	}
	if fake.modelCalls == 0 {
		t.Fatal("Models was never called")
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Providers["cursor"].APIKey == "synthetic-key" {
		t.Fatal("credential was saved despite the models call failing")
	}
}
