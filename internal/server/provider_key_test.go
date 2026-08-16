package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
)

// fakeOpenAI serves GET /v1/models the way an OpenAI-compatible endpoint does,
// recording the Authorization header each request carried so tests can tell
// which credential the connection probe used.
func fakeOpenAI(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-a","owned_by":"test"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &auths
}

// newProviderKeyServer seeds an isolated ANTARES_HOME and returns a Server
// wired the way handleSetProviderKey expects.
func newProviderKeyServer(t *testing.T, seed func(*config.Config)) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	seed(cfg)
	// Satisfy the dashboard-password gate the other handler tests keep.
	cfg.Server.DashboardPasswordHash = "test-hash"
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg)
	return s
}

func postProviderKey(s *Server, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/providers/"+id+"/key", strings.NewReader(body))
	r.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.handleSetProviderKey(rr, r)
	return rr
}

// TestSetProviderKeyBlankKeepsStoredKey guards the regression where
// reconnecting a custom provider with a blank key field overwrote the stored
// credential with nothing: the modal starts empty, so hitting Connect to
// update the endpoint silently destroyed a working key. A blank (or redacted)
// key must mean "keep what is stored", and the connection probe must run with
// the kept key.
func TestSetProviderKeyBlankKeepsStoredKey(t *testing.T) {
	endpoint, auths := fakeOpenAI(t)
	s := newProviderKeyServer(t, func(cfg *config.Config) {
		cfg.Providers["my-gateway"] = config.Provider{
			Kind: "openai-compatible", BaseURL: endpoint.URL + "/v1",
			APIKey: "secret-1", Enabled: true, Label: "My Gateway",
		}
	})

	rr := postProviderKey(s, "my-gateway", `{"api_key":"","base_url":"`+endpoint.URL+`/v1"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("blank reconnect: status = %d (body=%s)", rr.Code, rr.Body.String())
	}

	reloaded, err := config.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Providers["my-gateway"].APIKey; got != "secret-1" {
		t.Fatalf("blank key overwrote the stored credential: api_key = %q, want secret-1", got)
	}
	if n := len(*auths); n == 0 || !strings.Contains((*auths)[n-1], "secret-1") {
		t.Fatalf("connection probe did not use the kept key: authorizations = %v", *auths)
	}

	// A real new key still replaces the old one.
	rr = postProviderKey(s, "my-gateway", `{"api_key":"secret-2","base_url":"`+endpoint.URL+`/v1"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("key update: status = %d (body=%s)", rr.Code, rr.Body.String())
	}
	reloaded, err = config.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Providers["my-gateway"].APIKey; got != "secret-2" {
		t.Fatalf("api_key = %q, want the updated secret-2", got)
	}
}
