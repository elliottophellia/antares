# Cursor Agent Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Cursor Cloud Agents as a first-class agent-capability provider that can be configured from Antares and invoked through safe, observable delegation tools.

**Architecture:** A new `internal/cursor` package implements Cursor's official REST and SSE protocols without pretending they are chat completions. Provider metadata distinguishes LLM providers from agent integrations, server handlers validate and enumerate Cursor without changing the active Antares model, and two tools separate mutating run operations from read-only status/stream operations.

**Tech Stack:** Go 1.26 standard library (`net/http`, `httptest`, `encoding/json`, `bufio`), Cursor Cloud Agents v1 REST/SSE API, React 19 + TypeScript, Bun tests.

## Global Constraints

- Cursor remains an agent integration and must never implement or enter `llm.Client`.
- Use only documented endpoints under `https://api.cursor.com/v1`.
- A deployment owns one shared credential resolved from `CURSOR_API_KEY` or the existing provider-key store.
- Never emit an API key in logs, errors, test output, tool results, browser responses, fixtures, docs, or command arguments.
- Connecting Cursor must not modify `model.provider` or `model.default`.
- Cursor models must not enter Antares' primary model picker.
- Initial support is cloud-only; do not add SDK Bridge, local-agent execution, per-user keys, arbitrary environment variables, MCP definitions, or worker targets.
- `cursor_agent` is always approval-gated; `cursor_agent_status` is read-only and not approval-gated.
- Do not automatically retry create-agent or create-run requests. Retry only idempotent reads and SSE reconnections.
- All non-live tests must run without network access.

---

## File Structure

### New files

- `internal/cursor/types.go` — public request/response and stream-event types.
- `internal/cursor/client.go` — authenticated REST transport, metadata, agent, run, cancellation, and typed API errors.
- `internal/cursor/stream.go` — SSE parser and resumable run streaming.
- `internal/cursor/client_test.go` — metadata, lifecycle, auth redaction, and error tests.
- `internal/cursor/stream_test.go` — SSE parsing, reconnect, expiry, and cancellation tests.
- `internal/cursor/live_test.go` — opt-in `/v1/me` and `/v1/models` smoke test.
- `internal/providers/catalog_test.go` — capability and non-activation regression tests.
- `internal/server/cursor_provider_test.go` — provider connection/model-list/onboarding regression tests.
- `internal/tools/cursor_agent.go` — mutating and read-only Cursor tools.
- `internal/tools/cursor_agent_test.go` — schemas, approval, validation, API, progress, and secret-redaction tests.
- `internal/agent/cursor_timeout_test.go` — tool-envelope timeout regression test.
- `web/src/lib/providerCapabilities.ts` — small UI capability helpers.
- `web/src/lib/providerCapabilities.test.mjs` — Bun tests for provider classification and model endpoint selection.

### Modified files

- `internal/config/defaults.go` — built-in enabled-but-disconnected Cursor entry.
- `internal/config/config.go` — provider comment/kind documentation includes agent integrations.
- `internal/providers/catalog.go` — provider capability type, Cursor catalogue entry, and connect-without-LLM-activation behavior.
- `cmd/antares/main.go` — CLI messaging and refusal to make an agent integration the active LLM.
- `internal/tui/pickers.go` — connect Cursor without switching the active model.
- `internal/server/server.go` — injectable Cursor metadata-client factory for handler tests.
- `internal/server/handlers_setup.go` — Cursor catalogue entry, credential verification, and onboarding filtering.
- `internal/server/handlers_config.go` — provider capability and resolved-key status in model options; exclude agent providers from model aggregation.
- `internal/server/handlers_providers.go` — Cursor model catalogue endpoint.
- `internal/server/routes.go` — route for provider-specific agent models.
- `internal/tools/register.go` — register both tools.
- `internal/tools/registry.go` — include both tools in `coding`, `vibecoder`, and `default`.
- `web/src/pages/ProvidersPage.tsx` — agent-integration badge and read-only Cursor model catalogue.
- `web/src/lib/i18n.tsx` — English and Indonesian agent-integration copy.
- `docs/configuration.md` — Cursor configuration and environment variable.
- `docs/tools.md` — tool actions, approval, and long-run behavior.
- `docs/verification.md` — safe live metadata test.

---

### Task 1: Cursor Metadata Client and Secret-Safe Errors

**Files:**
- Create: `internal/cursor/types.go`
- Create: `internal/cursor/client.go`
- Create: `internal/cursor/client_test.go`

**Interfaces:**
- Produces: `cursor.New(Options) (*Client, error)`
- Produces: `(*Client).Me(context.Context) (*Me, error)`
- Produces: `(*Client).Models(context.Context) (*ModelCatalog, error)`
- Produces: `APIError`, `IsAuthError(error)`, `IsRateLimit(error)`, and `IsStatus(error, int)`
- Consumes: only Go standard library

- [ ] **Step 1: Write failing metadata/auth tests**

Add tests that assert the exact bearer header, decode metadata, and prove a
malicious upstream body cannot echo the key:

```go
func TestMeAndModelsUseBearerAndDecodeCatalog(t *testing.T) {
	const key = "synthetic-cursor-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/v1/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiKeyName": "test key", "createdAt": "2026-08-12T00:00:00Z",
			})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{
				map[string]any{
					"id": "composer-2", "displayName": "Composer 2",
					"parameters": []any{map[string]any{
						"id": "fast", "values": []any{map[string]any{"value": "true"}},
					}},
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	me, err := client.Me(context.Background())
	if err != nil || me.APIKeyName != "test key" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	models, err := client.Models(context.Background())
	if err != nil || len(models.Items) != 1 || models.Items[0].ID != "composer-2" {
		t.Fatalf("Models = %+v, %v", models, err)
	}
}

func TestAPIErrorNeverLeaksAPIKey(t *testing.T) {
	const key = "synthetic-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"rejected synthetic-secret"}}`)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	_, err := client.Me(context.Background())
	if err == nil || !IsAuthError(err) {
		t.Fatalf("expected auth error, got %v", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaked key: %v", err)
	}
}

func TestAPIErrorClassificationAndRetryAfter(t *testing.T) {
	for _, status := range []int{400, 404, 409, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":"synthetic","message":"request failed"}`)
			}))
			defer srv.Close()
			client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
			_, err := client.Me(context.Background())
			if !IsStatus(err, status) {
				t.Fatalf("status %d classified as %v", status, err)
			}
			if status == 429 {
				if !IsRateLimit(err) {
					t.Fatalf("429 not classified as rate limit: %v", err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.RetryAfter != 7*time.Second {
					t.Fatalf("RetryAfter = %v, want 7s", apiErr)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/cursor -run 'Test(MeAndModels|APIError)' -count=1 -v
```

Expected: compilation fails because package/types/functions do not exist.

- [ ] **Step 3: Define exact metadata and model types**

Create `types.go` with JSON names matching Cursor:

```go
package cursor

import "encoding/json"

type Me struct {
	APIKeyName   string `json:"apiKeyName"`
	CreatedAt    string `json:"createdAt"`
	UserID       int64  `json:"userId,omitempty"`
	UserEmail    string `json:"userEmail,omitempty"`
	UserFirstName string `json:"userFirstName,omitempty"`
	UserLastName  string `json:"userLastName,omitempty"`
}

type ModelParameterValue struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName,omitempty"`
}

type ModelParameter struct {
	ID          string                `json:"id"`
	DisplayName string                `json:"displayName,omitempty"`
	Values      []ModelParameterValue `json:"values"`
}

type ModelParameterSelection struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type ModelVariant struct {
	Params      []ModelParameterSelection `json:"params"`
	DisplayName string                    `json:"displayName"`
	Description string                    `json:"description,omitempty"`
	IsDefault   bool                      `json:"isDefault,omitempty"`
}

type Model struct {
	ID          string           `json:"id"`
	DisplayName string           `json:"displayName"`
	Description string           `json:"description,omitempty"`
	Aliases     []string         `json:"aliases,omitempty"`
	Parameters []ModelParameter `json:"parameters,omitempty"`
	Variants    []ModelVariant   `json:"variants,omitempty"`
}

type ModelCatalog struct {
	Items []Model `json:"items"`
}

type StreamEvent struct {
	ID       string
	Type     string
	Status   string
	Text     string
	ToolName string
	Raw      json.RawMessage
}
```

- [ ] **Step 4: Implement the transport and typed/redacted errors**

Create `client.go` around this interface:

```go
type Options struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func New(o Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if base == "" {
		base = "https://api.cursor.com"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid Cursor base URL")
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: base, apiKey: strings.TrimSpace(o.APIKey), http: hc}, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.decodeAPIError(resp)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	return &out, c.doJSON(ctx, http.MethodGet, "/v1/me", nil, &out)
}

func (c *Client) Models(ctx context.Context) (*ModelCatalog, error) {
	var out ModelCatalog
	return &out, c.doJSON(ctx, http.MethodGet, "/v1/models", nil, &out)
}
```

`decodeAPIError` must read at most 64 KiB, decode top-level
`code`/`message` and nested `error.code`/`error.message`, replace every
occurrence of `c.apiKey` with `[REDACTED]`, and cap the final message at 240
characters. Parse `Retry-After` as either integer seconds or an HTTP date.
`IsAuthError` accepts 401/403; `IsRateLimit` accepts 429; `IsStatus` uses
`errors.As` against `*APIError`.

- [ ] **Step 5: Run metadata tests and package tests**

Run:

```bash
gofmt -w internal/cursor/types.go internal/cursor/client.go internal/cursor/client_test.go
go test ./internal/cursor -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

```bash
git add internal/cursor/types.go internal/cursor/client.go internal/cursor/client_test.go
git commit -m "Add secret-safe Cursor metadata client"
```

---

### Task 2: Cursor Agent Lifecycle and Resumable SSE

**Files:**
- Modify: `internal/cursor/types.go`
- Modify: `internal/cursor/client.go`
- Create: `internal/cursor/stream.go`
- Modify: `internal/cursor/client_test.go`
- Create: `internal/cursor/stream_test.go`

**Interfaces:**
- Consumes: `cursor.Client` and `APIError` from Task 1
- Produces: `CreateAgent`, `CreateRun`, `GetAgent`, `GetRun`, `CancelRun`
- Produces: `StreamRun(ctx, agentID, runID string, emit func(StreamEvent) error) (*Run, error)`

- [ ] **Step 1: Write failing lifecycle request/response tests**

Use an `httptest.Server` that records method/path/body and returns fixed agent
and run IDs:

```go
func TestCreateAgentRepoAndFollowUpPayloads(t *testing.T) {
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen = append(seen, body)
		switch r.URL.Path {
		case "/v1/agents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agent": map[string]any{
					"id": "bc-agent", "status": "ACTIVE",
					"url": "https://cursor.com/agents/bc-agent",
					"latestRunId": "run-one",
				},
				"run": map[string]any{
					"id": "run-one", "agentId": "bc-agent", "status": "CREATING",
				},
			})
		case "/v1/agents/bc-agent/runs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"id": "run-two", "agentId": "bc-agent", "status": "CREATING",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	created, err := client.CreateAgent(context.Background(), CreateAgentRequest{
		Prompt: Prompt{Text: "fix it"},
		Model: &ModelSelection{ID: "composer-2"},
		Repos: []Repository{{URL: "https://github.com/acme/repo", StartingRef: "main"}},
		AutoCreatePR: true,
	})
	if err != nil || created.Agent.ID != "bc-agent" || created.Run.ID != "run-one" {
		t.Fatalf("CreateAgent = %+v, %v", created, err)
	}
	run, err := client.CreateRun(context.Background(), "bc-agent", CreateRunRequest{
		Prompt: Prompt{Text: "add tests"}, Mode: "agent",
	})
	if err != nil || run.ID != "run-two" {
		t.Fatalf("CreateRun = %+v, %v", run, err)
	}
	if seen[0]["autoCreatePR"] != true {
		t.Fatalf("create payload = %#v", seen[0])
	}
}
```

Add tests for no-repo omission, `GetAgent`, `GetRun`, and
`POST /v1/agents/{agentID}/runs/{runID}/cancel`.

- [ ] **Step 2: Write failing SSE and reconnect tests**

The test server must close the first stream after one event, verify the second
request carries `Last-Event-ID`, then send a terminal result:

```go
func TestStreamRunReconnectsFromLastEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n")
		default:
			if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
				t.Fatalf("Last-Event-ID = %q", got)
			}
			_, _ = io.WriteString(w,
				"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
					"id: evt-3\nevent: done\ndata: {}\n\n")
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
}
```

Also test multiline `data:`, heartbeat ignoring, tool-call envelope parsing,
context cancellation, a line larger than the parser limit returning an explicit
error, `410 stream_expired` fallback to `GetRun`, and one-time reset after
`400 invalid_last_event_id`.

- [ ] **Step 3: Run lifecycle/stream tests and verify RED**

Run:

```bash
go test ./internal/cursor -run 'Test(CreateAgent|GetAgent|GetRun|CancelRun|StreamRun)' -count=1 -v
```

Expected: compilation failures for lifecycle and stream APIs.

- [ ] **Step 4: Add exact lifecycle types and methods**

Extend `types.go`:

```go
type Prompt struct {
	Text string `json:"text"`
}

type ModelSelection struct {
	ID     string                    `json:"id"`
	Params []ModelParameterSelection `json:"params,omitempty"`
}

type Repository struct {
	URL         string `json:"url"`
	StartingRef string `json:"startingRef,omitempty"`
	PRURL       string `json:"prUrl,omitempty"`
}

type CreateAgentRequest struct {
	Prompt              Prompt          `json:"prompt"`
	Model               *ModelSelection `json:"model,omitempty"`
	Name                string          `json:"name,omitempty"`
	Repos               []Repository    `json:"repos,omitempty"`
	WorkOnCurrentBranch bool            `json:"workOnCurrentBranch,omitempty"`
	AutoCreatePR        bool            `json:"autoCreatePR,omitempty"`
	SkipReviewerRequest bool            `json:"skipReviewerRequest,omitempty"`
	Mode                string          `json:"mode,omitempty"`
}

type CreateRunRequest struct {
	Prompt Prompt `json:"prompt"`
	Mode   string `json:"mode,omitempty"`
}

type GitBranch struct {
	RepoURL string `json:"repoUrl"`
	Branch  string `json:"branch,omitempty"`
	PRURL   string `json:"prUrl,omitempty"`
}

type GitState struct {
	Branches []GitBranch `json:"branches"`
}

type Agent struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	URL         string     `json:"url"`
	LatestRunID string     `json:"latestRunId"`
	Git         *GitState  `json:"git,omitempty"`
	Repos       []Repository `json:"repos,omitempty"`
}

type Run struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agentId"`
	Status     string    `json:"status"`
	CreatedAt  string    `json:"createdAt"`
	UpdatedAt  string    `json:"updatedAt"`
	DurationMS int64     `json:"durationMs,omitempty"`
	Result     string    `json:"result,omitempty"`
	Git        *GitState `json:"git,omitempty"`
}

type CreateAgentResponse struct {
	Agent Agent `json:"agent"`
	Run   Run   `json:"run"`
}

type CreateRunResponse struct {
	Run Run `json:"run"`
}
```

Implement methods with `url.PathEscape` for IDs:

```go
func (c *Client) CreateAgent(ctx context.Context, in CreateAgentRequest) (*CreateAgentResponse, error)
func (c *Client) CreateRun(ctx context.Context, agentID string, in CreateRunRequest) (*Run, error)
func (c *Client) GetAgent(ctx context.Context, agentID string) (*Agent, error)
func (c *Client) GetRun(ctx context.Context, agentID, runID string) (*Run, error)
func (c *Client) CancelRun(ctx context.Context, agentID, runID string) error
```

`CreateRun` decodes `CreateRunResponse` and returns its `Run`; the other
response shapes are direct. Each method calls `doJSON` exactly once. Validate
non-empty prompt/IDs before network I/O. Do not add retries.

- [ ] **Step 5: Implement SSE parsing and bounded reconnection**

Create `stream.go` with:

```go
var errStreamDone = errors.New("Cursor stream done")

func parseSSE(r io.Reader, emit func(StreamEvent) error) (lastID string, terminal *Run, err error)

func (c *Client) streamOnce(
	ctx context.Context,
	agentID, runID, lastID string,
	emit func(StreamEvent) error,
) (nextID string, terminal *Run, done bool, err error)

func (c *Client) StreamRun(
	ctx context.Context,
	agentID, runID string,
	emit func(StreamEvent) error,
) (*Run, error)
```

`parseSSE` uses a `bufio.Scanner` with a 1 MiB maximum token, checks
`scanner.Err()`, and collects `id`, `event`, and all `data` lines until a blank
line. It must never silently return partial success after an overlong event.
Decode simplified events as follows:

```go
switch eventName {
case "assistant", "thinking":
	var payload struct{ Text string `json:"text"` }
	err = json.Unmarshal(raw, &payload)
	out.Text = payload.Text
case "status":
	var payload struct {
		RunID  string `json:"runId"`
		Status string `json:"status"`
	}
	err = json.Unmarshal(raw, &payload)
	out.Status = payload.Status
case "tool_call":
	var payload struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	err = json.Unmarshal(raw, &payload)
	out.ToolName, out.Status = payload.Name, payload.Status
case "result":
	var payload struct {
		RunID     string    `json:"runId"`
		Status    string    `json:"status"`
		Text      string    `json:"text"`
		DurationMS int64    `json:"durationMs"`
		Git       *GitState `json:"git,omitempty"`
	}
	err = json.Unmarshal(raw, &payload)
	terminal = &Run{
		ID: payload.RunID, Status: payload.Status, Result: payload.Text,
		DurationMS: payload.DurationMS, Git: payload.Git,
	}
case "error":
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	err = json.Unmarshal(raw, &payload)
	if err == nil {
		err = &APIError{Code: payload.Code, Message: payload.Message}
	}
case "done", "heartbeat", "interaction_update":
}
```

`streamOnce` clones `c.http`, sets the clone's `Timeout` to zero, sends
`Accept: text/event-stream`, and leaves lifetime control to `ctx`; Cursor
heartbeats prevent intermediaries from treating a healthy run as idle. The
metadata/lifecycle client's ordinary 30-second timeout remains unchanged.

`StreamRun` makes at most four connection attempts, sleeps 250 ms, 500 ms, and
1 second between retryable disconnects, preserves the last event ID, resets an
invalid event ID only once, and calls `GetRun` when the stream returns 410 or
ends with `done` but no terminal `result`. It must return immediately on caller
cancellation or an `emit` error.

- [ ] **Step 6: Run all Cursor package tests**

Run:

```bash
gofmt -w internal/cursor
go test ./internal/cursor -count=1 -race
```

Expected: PASS with no network access.

- [ ] **Step 7: Commit Task 2**

```bash
git add internal/cursor
git commit -m "Add Cursor cloud agent lifecycle and streaming"
```

---

### Task 3: Provider Capability, Defaults, CLI, and TUI

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/providers/catalog.go`
- Create: `internal/providers/catalog_test.go`
- Modify: `cmd/antares/main.go`
- Modify: `internal/tui/pickers.go`

**Interfaces:**
- Produces: `providers.CapabilityLLM`, `providers.CapabilityAgent`
- Produces: `providers.CapabilityForKind(string) Capability`
- Produces: `providers.CapabilityOf(*config.Config, string) Capability`
- Produces: `Info.Capability() Capability`
- Produces: `providers.Connect(*config.Config, id, key string) (Info, bool)`
- Changes: `providers.Activate` returns whether the provider can be an active LLM

- [ ] **Step 1: Write provider capability regression tests**

```go
func TestCursorConnectDoesNotChangeActiveModel(t *testing.T) {
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default

	info, known := Connect(cfg, "cursor", "synthetic-key")
	if !known || info.Capability() != CapabilityAgent {
		t.Fatalf("cursor info = %+v, known=%v", info, known)
	}
	if activated := Activate(cfg, "cursor", ""); activated {
		t.Fatal("agent provider was activated as an LLM")
	}
	if cfg.Model.Provider != beforeProvider || cfg.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", cfg.Model.Provider, cfg.Model.Default)
	}
	if p := cfg.Providers["cursor"]; !p.Enabled || p.APIKey != "synthetic-key" {
		t.Fatalf("cursor provider not connected: %+v", p)
	}
}

func TestDefaultCursorProviderUsesEnvironmentKey(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("CURSOR_API_KEY", "synthetic-env-key")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, p := cfg.ResolveProvider("cursor")
	if !p.Enabled || p.APIKey != "synthetic-env-key" || p.Kind != "cursor-agent" {
		t.Fatalf("cursor provider = %+v", p)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/providers ./internal/config -run Cursor -count=1 -v
```

Expected: failures because Cursor/capability/connect behavior is absent.

- [ ] **Step 3: Add the default Cursor provider**

Add this keyed entry to `config.Default().Providers`:

```go
"cursor": {
	Kind: "cursor-agent", Label: "Cursor Cloud Agents", Enabled: true,
	BaseURL: "https://api.cursor.com", APIKeyEnv: "CURSOR_API_KEY",
	TimeoutSecs: 900,
},
```

No default Cursor model belongs in `Model` or `Provider.Models`.

Change the `Provider` comment to “one configured external AI service” and add
`cursor-agent` to the `Kind` field's documented values; do not add new config
fields.

- [ ] **Step 4: Add provider capabilities and separate connect from activate**

Add:

```go
type Capability string

const (
	CapabilityLLM   Capability = "llm"
	CapabilityAgent Capability = "agent"
)

func CapabilityForKind(kind string) Capability {
	if strings.EqualFold(strings.TrimSpace(kind), "cursor-agent") {
		return CapabilityAgent
	}
	return CapabilityLLM
}

func (i Info) Capability() Capability { return CapabilityForKind(i.Kind) }

func CapabilityOf(cfg *config.Config, id string) Capability {
	if info, ok := For(id); ok {
		return info.Capability()
	}
	if cfg != nil {
		if p, ok := cfg.Providers[id]; ok {
			return CapabilityForKind(p.Kind)
		}
	}
	return CapabilityLLM
}
```

Add Cursor to `catalog`:

```go
{"cursor", "Cursor Cloud Agents", "cursor-agent", "CURSOR_API_KEY",
	"https://api.cursor.com", true, nil},
```

Extract the existing provider-population portion of `Activate` into:

```go
func Connect(cfg *config.Config, id, key string) (Info, bool) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.Provider{}
	}
	info, known := For(id)
	p := cfg.Providers[id]
	if known {
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
	return info, known
}
```

Keep context-window metadata population in `Connect`. Then make:

```go
func Activate(cfg *config.Config, id, key string) bool {
	_, _ = Connect(cfg, id, key)
	if CapabilityOf(cfg, id) == CapabilityAgent {
		return false
	}
	cfg.Model.Provider = id
	p := cfg.Providers[id]
	if !contains(p.Models, cfg.Model.Default) && len(p.Models) > 0 {
		cfg.Model.Default = p.Models[0]
	}
	return true
}
```

- [ ] **Step 5: Make CLI/TUI agent-provider behavior explicit**

In CLI `provider use`, reject agent providers:

```go
if providers.CapabilityOf(cfg, id) == providers.CapabilityAgent {
	return fmt.Errorf("%s is an agent integration, not a chat model provider; use the cursor_agent tool", id)
}
```

In CLI `provider add`, call `providers.Connect` for an agent provider and print:

```go
fmt.Printf("connected %s agent integration; active model remains %s (%s)\n",
	id, cfg.Model.Default, cfg.Model.Provider)
```

For LLM providers, retain current activation behavior.

In TUI `selectProvider`, connected agent providers display a system message
pointing to `cursor_agent` instead of switching. Connecting one calls
`providers.Connect`, saves config, and reports that the active model is
unchanged.

- [ ] **Step 6: Verify provider, config, CLI, and TUI packages**

Run:

```bash
gofmt -w internal/config/config.go internal/config/defaults.go internal/providers/catalog.go internal/providers/catalog_test.go cmd/antares/main.go internal/tui/pickers.go
go test ./internal/providers ./internal/config ./internal/tui ./cmd/antares -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add internal/config/config.go internal/config/defaults.go internal/providers/catalog.go internal/providers/catalog_test.go cmd/antares/main.go internal/tui/pickers.go
git commit -m "Add Cursor agent provider capability"
```

---

### Task 4: Provider API, Credential Verification, and Model Isolation

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/handlers_setup.go`
- Modify: `internal/server/handlers_config.go`
- Modify: `internal/server/handlers_providers.go`
- Modify: `internal/server/routes.go`
- Create: `internal/server/cursor_provider_test.go`

**Interfaces:**
- Consumes: metadata client from Task 1 and capability metadata from Task 3
- Produces: `GET /api/providers/{id}/models`
- Produces: provider JSON field `capability`
- Guarantees: Cursor connection cannot mutate the active model or enter `/api/model/list-all`

- [ ] **Step 1: Write failing server regression tests**

Define a fake metadata client in `cursor_provider_test.go`:

```go
type fakeCursorMetadata struct {
	me     cursor.Me
	models cursor.ModelCatalog
	err    error
}

func (f *fakeCursorMetadata) Me(context.Context) (*cursor.Me, error) {
	return &f.me, f.err
}

func (f *fakeCursorMetadata) Models(context.Context) (*cursor.ModelCatalog, error) {
	return &f.models, f.err
}
```

Add:

```go
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
			me: cursor.Me{APIKeyName: "test"},
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
}
```

Also add tests that:

- `/api/setup/status` omits capability `agent`.
- `/api/setup/complete` rejects `provider=cursor`.
- `/api/model/options` reports Cursor `capability:"agent"` and environment
  credentials as `has_key:true`.
- `/api/providers/cursor/models` returns Cursor model IDs/display names.
- `/api/model/list-all` does not call or include Cursor.
- Cursor auth errors never contain the supplied key.

- [ ] **Step 2: Run server tests and verify RED**

Run:

```bash
go test ./internal/server -run Cursor -count=1 -v
```

Expected: compilation failures for factory/capability/handler behavior.

- [ ] **Step 3: Add injectable metadata-client boundary**

In `server.go`:

```go
type cursorMetadataClient interface {
	Me(context.Context) (*cursor.Me, error)
	Models(context.Context) (*cursor.ModelCatalog, error)
}

type cursorClientFactory func(cursor.Options) (cursorMetadataClient, error)
```

Add `cursorFactory cursorClientFactory` to `Server` and this helper:

```go
func (s *Server) newCursorMetadataClient(o cursor.Options) (cursorMetadataClient, error) {
	if s.cursorFactory != nil {
		return s.cursorFactory(o)
	}
	return cursor.New(o)
}
```

No production caller injects a factory.

- [ ] **Step 4: Add capability-aware provider catalogue behavior**

Add `Capability string` to `setupProvider` and set `"llm"` for existing entries
and `"agent"` for Cursor:

```go
{
	ID: "cursor", Label: "Cursor Cloud Agents", Kind: "cursor-agent",
	Capability: "agent",
	Hint: "Delegate coding tasks to durable Cursor Cloud Agents.",
	KeyHint: "crsr_…", KeyURL: "https://cursor.com/dashboard/api",
	BaseURL: "https://api.cursor.com",
	Note: "This deployment key and Cursor quota are shared by users allowed to invoke Cursor tools.",
}
```

When populating `HasKey`, resolve the provider:

```go
_, resolved := cfg.ResolveProvider(out[i].ID)
out[i].HasKey = strings.TrimSpace(resolved.APIKey) != ""
```

Filter `Capability == "agent"` out of `handleSetupStatus`, and reject it in
`handleSetupComplete` before any config mutation. This keeps initial onboarding
limited to actual chat-model providers.

- [ ] **Step 5: Special-case Cursor credential verification**

Factor provider verification in `handleSetProviderKey`:

```go
func (s *Server) verifyCursorProvider(
	ctx context.Context,
	baseURL, apiKey string,
) (*cursor.ModelCatalog, error) {
	client, err := s.newCursorMetadataClient(cursor.Options{
		BaseURL: baseURL, APIKey: apiKey,
	})
	if err != nil {
		return nil, err
	}
	if _, err := client.Me(ctx); err != nil {
		return nil, err
	}
	return client.Models(ctx)
}
```

For `Capability == "agent"`, call this helper rather than `llm.New`. Map
`cursor.IsAuthError` to the same 200/`ok:false` form used by other providers and
map transport/invalid response failures to 502. Only save the provider after
both calls succeed. Do not touch `cfg.Model`.

- [ ] **Step 6: Add provider-specific Cursor model endpoint and isolation**

Register:

```go
m.HandleFunc("GET /api/providers/{id}/models", s.handleProviderModels)
```

Implement `handleProviderModels` to require dashboard access, resolve
`providers.cursor`, call `cursor.Models`, and return:

```json
{
  "models": [
    {
      "id": "composer-2",
      "name": "Composer 2",
      "description": "",
      "parameters": []
    }
  ]
}
```

Return `needs_key:true` without network access when no resolved key exists.

Add `capability` to `/api/model/options`. In `/api/model/list-all`, skip any
configured or catalogued provider whose kind maps to
`providers.CapabilityAgent`.

- [ ] **Step 7: Run server and adjacent package tests**

Run:

```bash
gofmt -w internal/server
go test ./internal/server ./internal/providers ./internal/config -count=1 -race
```

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

```bash
git add internal/server
git commit -m "Connect Cursor without changing the active LLM"
```

---

### Task 5: Cursor Agent Tools

**Files:**
- Create: `internal/tools/cursor_agent.go`
- Create: `internal/tools/cursor_agent_test.go`
- Create: `internal/agent/cursor_timeout_test.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/tools/register.go`
- Modify: `internal/tools/registry.go`

**Interfaces:**
- Consumes: all lifecycle/stream APIs from Tasks 1–2 and `providers.cursor` config from Task 3
- Produces: tool `cursor_agent` with actions `start`, `follow_up`, `cancel`
- Produces: tool `cursor_agent_status` with snapshot/wait behavior

- [ ] **Step 1: Write failing schema, approval, and validation tests**

```go
func TestCursorToolApprovalClassification(t *testing.T) {
	if !NeedsApproval(cursorAgentTool{}) {
		t.Fatal("cursor_agent must require approval")
	}
	if NeedsApproval(cursorAgentStatusTool{}) {
		t.Fatal("cursor_agent_status must be read-only")
	}
}

func TestCursorAgentRejectsMissingConfigAndInvalidRepo(t *testing.T) {
	in := Input{
		Args: []byte(`{"action":"start","prompt":"fix it"}`),
		Deps: &Deps{Config: config.Default()},
		Emit: func(Progress) {},
	}
	result := (cursorAgentTool{}).Execute(context.Background(), in)
	if !result.IsError || !strings.Contains(result.Content, "CURSOR_API_KEY") {
		t.Fatalf("missing-key result = %+v", result)
	}

	cfg := config.Default()
	p := cfg.Providers["cursor"]
	p.APIKey = "synthetic-key"
	cfg.Providers["cursor"] = p
	in.Deps.Config = cfg
	in.Args = []byte(`{"action":"start","prompt":"fix it","repository_url":"http://github.com/acme/repo"}`)
	result = (cursorAgentTool{}).Execute(context.Background(), in)
	if !result.IsError || !strings.Contains(result.Content, "HTTPS GitHub") {
		t.Fatalf("invalid repo result = %+v", result)
	}
}
```

Add table tests for required fields per action, mode enum, ID prefixes, no-repo
start, status latest-run resolution, and context timeout preserving IDs.

Add an agent-envelope regression test:

```go
func TestCursorToolTimeoutsAllowLongCloudRuns(t *testing.T) {
	a := agentWithConfig(config.Default())
	for _, name := range []string{"cursor_agent", "cursor_agent_status"} {
		if got := a.toolTimeout(name); got < 16*time.Minute {
			t.Fatalf("%s timeout = %s, want at least 16m", name, got)
		}
	}
}
```

- [ ] **Step 2: Write failing API/progress tests with `httptest.Server`**

Configure `providers.cursor.BaseURL` to the server URL and assert:

- `start` posts expected repo/model/mode fields.
- `follow_up` posts to the existing agent.
- `cancel` posts to the cancel endpoint.
- `cursor_agent_status(wait=false)` reads agent then run.
- `wait=true` emits bounded progress for status/assistant/thinking/tool calls
  and returns final text/git metadata.
- A server error containing `synthetic-key` cannot leak it into `Result.Content`,
  `Result.Display`, `Result.Meta`, or progress messages.

- [ ] **Step 3: Run tool tests and verify RED**

Run:

```bash
go test ./internal/tools -run CursorAgent -count=1 -v
```

Expected: compilation failures because the tools are absent.

- [ ] **Step 4: Implement shared client/config helpers**

Create:

```go
func cursorClientFromInput(in Input) (*cursor.Client, config.Provider, error) {
	if in.Deps == nil || in.Deps.Config == nil {
		return nil, config.Provider{}, errors.New("Cursor is unavailable in this runtime")
	}
	_, p := in.Deps.Config.ResolveProvider("cursor")
	if !p.Enabled || strings.TrimSpace(p.APIKey) == "" {
		return nil, p, errors.New("connect Cursor in Providers or set CURSOR_API_KEY")
	}
	client, err := cursor.New(cursor.Options{
		BaseURL: p.BaseURL, APIKey: p.APIKey,
	})
	return client, p, err
}

func validateCursorRepository(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return errors.New("repository_url must be an HTTPS GitHub URL")
	}
	return nil
}

func cursorWaitContext(ctx context.Context, p config.Provider) (context.Context, context.CancelFunc) {
	timeout := time.Duration(p.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return context.WithTimeout(ctx, timeout)
}
```

For `start`, require `repository_url` whenever `pull_request_url` or
`auto_create_pr` is set. Validate `pull_request_url` with the same HTTPS,
`github.com`, and no-userinfo rules and require a `/pull/<number>` path. Build
`Repos` as an empty slice for no-repo runs and as one `Repository` for repo/PR
runs. Never derive repository data by executing git or shell commands.

Create/read requests retain the client's 30-second HTTP timeout. Before calling
`waitCursorRun`, wrap the outer tool context with `cursorWaitContext`; the SSE
client itself has no transport timeout, but the provider timeout and outer
960-second agent envelope both remain effective.

- [ ] **Step 5: Implement `cursor_agent`**

Define its schema:

```go
func (cursorAgentTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("Operation to perform.", "start", "follow_up", "cancel"),
		"prompt": prop("string", "Task for start/follow_up."),
		"agent_id": prop("string", "Cursor bc- agent id for follow_up/cancel."),
		"run_id": prop("string", "Cursor run- id for cancel."),
		"model": prop("string", "Optional model id returned by Cursor."),
		"repository_url": prop("string", "Optional HTTPS GitHub repository URL."),
		"starting_ref": prop("string", "Optional branch or commit SHA."),
		"pull_request_url": prop("string", "Optional GitHub pull request URL."),
		"mode": propEnum("Cursor conversation mode.", "agent", "plan"),
		"auto_create_pr": propDefault("boolean", "Open a PR when the run completes.", false),
		"skip_reviewer_request": propDefault("boolean", "Do not request the key owner as reviewer.", true),
		"wait": propDefault("boolean", "Stream until terminal status.", true),
	}, "action")
}

func (cursorAgentTool) RequiresApproval() bool { return true }
```

Bind into pointer booleans where omission has a semantic default:

```go
var args struct {
	Action              string `json:"action"`
	Prompt              string `json:"prompt"`
	AgentID             string `json:"agent_id"`
	RunID               string `json:"run_id"`
	Model               string `json:"model"`
	RepositoryURL       string `json:"repository_url"`
	StartingRef         string `json:"starting_ref"`
	PullRequestURL      string `json:"pull_request_url"`
	Mode                string `json:"mode"`
	AutoCreatePR        bool   `json:"auto_create_pr"`
	SkipReviewerRequest *bool  `json:"skip_reviewer_request"`
	Wait                *bool  `json:"wait"`
}
wait := true
if args.Wait != nil {
	wait = *args.Wait
}
skipReviewer := true
if args.SkipReviewerRequest != nil {
	skipReviewer = *args.SkipReviewerRequest
}
```

Use `CreateAgent`, `CreateRun`, and `CancelRun`. For start/follow-up with
`wait=true`, call a shared:

```go
func waitCursorRun(
	ctx context.Context,
	in Input,
	client *cursor.Client,
	agent cursor.Agent,
	run cursor.Run,
) Result
```

For `follow_up`, fetch `GetAgent` first so both immediate and waited results
retain the Cursor URL, then call `CreateRun`. Reject start-only fields
(`model`, repository/PR/ref, and PR options) on follow-up/cancel rather than
silently ignoring them. Reject prompt/mode/wait fields on cancel except that
an omitted `wait` pointer is allowed.

Map events to bounded progress:

```go
func emitCursorEvent(in Input, event cursor.StreamEvent) {
	message := "Cursor " + event.Type
	chunk := event.Text
	if event.ToolName != "" {
		message = "Cursor tool " + event.ToolName + " " + event.Status
	}
	runes := []rune(chunk)
	if len(runes) > 2000 {
		chunk = string(runes[:2000]) + "…"
	}
	in.Emit(Progress{Tool: "cursor_agent", Message: message, Chunk: chunk})
}
```

The final result includes `agent_id`, `run_id`, `status`, `cursor_url`,
`duration_ms`, final text, and git branches/PRs in both readable content and
`Meta`. `wait=false` returns IDs/URL immediately and explicitly says not to
busy-poll.

- [ ] **Step 6: Map errors without retrying mutations**

Add one formatter used by both tools:

```go
func cursorResultError(err error, agentID, runID string) Result {
	meta := map[string]any{"agent_id": agentID, "run_id": runID}
	switch {
	case cursor.IsAuthError(err):
		return Result{Content: "Cursor API key was rejected.", Meta: meta, IsError: true}
	case cursor.IsRateLimit(err):
		var apiErr *cursor.APIError
		_ = errors.As(err, &apiErr)
		if apiErr != nil && apiErr.RetryAfter > 0 {
			meta["retry_after_seconds"] = int(apiErr.RetryAfter.Seconds())
		}
		return Result{Content: "Cursor rate limit reached; retry later.", Meta: meta, IsError: true}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return Result{
			Content: "Stopped waiting; the remote Cursor run may still be active. Use cursor_agent_status with the returned IDs.",
			Meta: meta, IsError: true,
		}
	default:
		return Result{Content: "Cursor request failed: " + err.Error(), Meta: meta, IsError: true}
	}
}
```

For 404, prefix the result with either `Cursor agent not found` or
`Cursor run not found` according to the operation. Return 409 conflicts
verbatim but do not retry. The already-redacted client error is the only
upstream text that may be included.

- [ ] **Step 7: Implement `cursor_agent_status`**

Schema:

```go
func (cursorAgentStatusTool) Schema() map[string]any {
	return schema(map[string]any{
		"agent_id": prop("string", "Cursor bc- agent id."),
		"run_id": prop("string", "Optional run- id; latest run is used when omitted."),
		"wait": propDefault("boolean", "Stream until terminal status.", false),
	}, "agent_id")
}
```

Fetch the agent first, resolve `LatestRunID` when needed, then either call
`GetRun` or `waitCursorRun`. It has no `RequiresApproval` method.

- [ ] **Step 8: Register tools, timeout defaults, and toolset membership**

Register both tools in `init()` and add both names to `coding`, `vibecoder`,
and `default`. Do not add them to `minimal`, `research`, `browser`, `social`,
or offensive-security-specific toolsets.

Add `"cursor_agent": 960` and `"cursor_agent_status": 960` to
`config.Default().Tools.Timeouts`. This keeps the agent envelope above the
provider's 900-second default while still allowing operator overrides.

- [ ] **Step 9: Run tools tests and race tests**

Run:

```bash
gofmt -w internal/tools/cursor_agent.go internal/tools/cursor_agent_test.go internal/tools/register.go internal/tools/registry.go internal/config/defaults.go internal/agent/cursor_timeout_test.go
go test ./internal/tools ./internal/agent -run 'CursorAgent|CursorTool|Toolset' -count=1 -race
```

Expected: PASS.

- [ ] **Step 10: Commit Task 5**

```bash
git add internal/tools internal/config/defaults.go internal/agent/cursor_timeout_test.go
git commit -m "Add Cursor cloud agent delegation tools"
```

---

### Task 6: Providers Dashboard Agent UX

**Files:**
- Create: `web/src/lib/providerCapabilities.ts`
- Create: `web/src/lib/providerCapabilities.test.mjs`
- Modify: `web/src/pages/ProvidersPage.tsx`
- Modify: `web/src/lib/i18n.tsx`

**Interfaces:**
- Consumes: provider JSON `capability` and `GET /api/providers/{id}/models` from Task 4
- Produces: visible agent-integration classification and read-only Cursor model catalogue

- [ ] **Step 1: Write failing capability helper tests**

```javascript
import { describe, expect, test } from 'bun:test'
import { isAgentProvider, providerModelsPath } from './providerCapabilities.ts'

describe('provider capabilities', () => {
  test('classifies Cursor as an agent integration', () => {
    expect(isAgentProvider({ capability: 'agent' })).toBe(true)
    expect(isAgentProvider({ capability: 'llm' })).toBe(false)
  })

  test('uses provider-specific models for agents only', () => {
    expect(providerModelsPath({ id: 'cursor', capability: 'agent' }))
      .toBe('/providers/cursor/models')
    expect(providerModelsPath({ id: 'openai', capability: 'llm' })).toBeNull()
  })
})
```

- [ ] **Step 2: Run Bun test and verify RED**

Run:

```bash
cd web && bun test src/lib/providerCapabilities.test.mjs
```

Expected: module-not-found failure.

- [ ] **Step 3: Implement capability helpers**

```ts
export type ProviderCapability = 'llm' | 'agent'

export interface ProviderCapabilityInfo {
  id: string
  capability?: ProviderCapability
}

export function isAgentProvider(provider: Pick<ProviderCapabilityInfo, 'capability'>): boolean {
  return provider.capability === 'agent'
}

export function providerModelsPath(provider: ProviderCapabilityInfo): string | null {
  return isAgentProvider(provider)
    ? `/providers/${encodeURIComponent(provider.id)}/models`
    : null
}
```

- [ ] **Step 4: Update provider card and modal types**

Add `capability: 'llm' | 'agent'` to `ProviderInfo` and:

```tsx
{isAgentProvider(p) ? (
  <Badge variant="outline">{t('providers.agentIntegration')}</Badge>
) : null}
```

Do not render the active-model badge for an agent provider.

In `ProviderModal`, fetch:

```tsx
interface AgentModel {
  id: string
  name: string
  description?: string
  parameters?: unknown[]
}

const agentOnly = isAgentProvider(p)
const agentModelsState = useApi<{ models: AgentModel[]; needs_key?: boolean }>(providerModelsPath(p))
const llmModelsState = useApi<{ models: AllModel[] }>(agentOnly ? null : '/model/list-all')
const myModels = (llmModelsState.data?.models ?? []).filter((m) => m.provider === p.id)
```

For an agent provider, the Models section is read-only: show ID, display name,
and description from `agentModelsState`; hide add, context-window, and delete
controls. For LLM providers, retain the existing UI unchanged and replace the
old `modelsState.reload()` calls with `llmModelsState.reload()`.
Render `t('models.needsKey')` when `needs_key` is true and render
`agentModelsState.error.message` in the existing destructive error style when
model discovery fails.

Change modal description to:

```tsx
<DialogDescription>
  {agentOnly ? t('providers.agentManageDesc') : t('providers.manageDesc')}
</DialogDescription>
```

- [ ] **Step 5: Add source and Indonesian translations**

Add English source keys:

```ts
'providers.agentIntegration': 'Agent integration',
'providers.agentManageDesc': 'Credentials, available agent models, and advanced settings.',
'providers.agentModelsReadOnly': 'Models available to this Cursor API key. Cursor chooses the default when no model is specified.',
```

Add Indonesian overrides:

```ts
'providers.agentIntegration': 'Integrasi agent',
'providers.agentManageDesc': 'Kredensial, model agent yang tersedia, dan pengaturan lanjutan.',
'providers.agentModelsReadOnly': 'Model yang tersedia untuk API key Cursor ini. Cursor memilih default bila model tidak ditentukan.',
```

Other locales fall back to English through the existing partial dictionaries.

- [ ] **Step 6: Run frontend tests, typecheck, and build**

Run:

```bash
cd web
bun test
bun x tsc -b --noEmit
bun run build
```

Expected: all tests and typecheck pass; Vite build completes.

- [ ] **Step 7: Commit Task 6**

```bash
git add web/src/lib/providerCapabilities.ts web/src/lib/providerCapabilities.test.mjs web/src/pages/ProvidersPage.tsx web/src/lib/i18n.tsx
git commit -m "Show Cursor as an agent integration"
```

---

### Task 7: Documentation, Opt-in Live Test, and End-to-End Verification

**Files:**
- Create: `internal/cursor/live_test.go`
- Modify: `docs/configuration.md`
- Modify: `docs/tools.md`
- Modify: `docs/verification.md`
- Track: `docs/superpowers/specs/2026-08-12-cursor-agent-provider-design.md`
- Track: `docs/superpowers/plans/2026-08-12-cursor-agent-provider.md`

**Interfaces:**
- Consumes: final Cursor client, provider, tools, and UI behavior
- Produces: operator instructions and hermetic/live verification commands

- [ ] **Step 1: Add an opt-in metadata-only live test**

```go
func TestLiveCursorMetadata(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("CURSOR_API_KEY"))
	if key == "" {
		t.Skip("set CURSOR_API_KEY to run Cursor metadata smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := New(Options{APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	me, err := client.Me(ctx)
	if err != nil {
		t.Fatalf("Cursor /v1/me: %v", err)
	}
	models, err := client.Models(ctx)
	if err != nil {
		t.Fatalf("Cursor /v1/models: %v", err)
	}
	if me.APIKeyName == "" || len(models.Items) == 0 {
		t.Fatalf("incomplete metadata: me=%+v models=%d", me, len(models.Items))
	}
	t.Logf("Cursor key %q exposes %d models", me.APIKeyName, len(models.Items))
}
```

This test must not create an agent or print the key.

- [ ] **Step 2: Document exact configuration and safety model**

Add to `docs/configuration.md`:

```yaml
providers:
  cursor:
    kind: cursor-agent
    base_url: https://api.cursor.com
    api_key_env: CURSOR_API_KEY
    enabled: true
    timeout_seconds: 900
```

Explain that Cursor is an agent integration, not a primary Antares model; one
deployment key/quota is shared; repository-backed runs use the repo state
available to Cursor, not unpushed local changes.

Document both tools and approval/background behavior in `docs/tools.md`.
Document the metadata-only command in `docs/verification.md`:

```bash
read -rsp 'Cursor API key: ' CURSOR_API_KEY
export CURSOR_API_KEY
go test ./internal/cursor -run TestLiveCursorMetadata -count=1 -v
unset CURSOR_API_KEY
```

Never put a real credential in docs or shell history during implementation.

- [ ] **Step 3: Run focused backend/frontend verification**

Run:

```bash
go test ./internal/cursor ./internal/providers ./internal/config ./internal/server ./internal/tools -count=1 -race
go vet ./internal/cursor ./internal/providers ./internal/server ./internal/tools
cd web && bun test && bun x tsc -b --noEmit
```

Expected: PASS.

- [ ] **Step 4: Run the complete hermetic suite**

Unset unrelated live credentials so pre-existing live provider tests skip:

```bash
env -u OPENAI_API_KEY -u AZURE_OPENAI_KEY -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY \
  go test ./...
go build ./...
```

Expected: all packages pass and build succeeds.

- [ ] **Step 5: Run Cursor live metadata test only when the operator has exported the key**

Check without printing it:

```bash
if test -n "${CURSOR_API_KEY:-}"; then
  go test ./internal/cursor -run TestLiveCursorMetadata -count=1 -v
else
  printf '%s\n' 'CURSOR_API_KEY is not set; live Cursor test skipped'
fi
```

Do not reconstruct the credential from chat content or put it on a command line.

- [ ] **Step 6: Build/install and restart Antares after tests pass**

Run:

```bash
make install-cli
~/.local/bin/antares stop
~/.local/bin/antares serve
~/.local/bin/antares status
```

Expected: the daemon reports the newly built commit/version and `/api/health`
returns HTTP 200.

Restore tracked build-only noise before committing:

```bash
git restore internal/server/dist/.gitkeep web/tsconfig.tsbuildinfo
```

- [ ] **Step 7: Final secret and diff audit**

Run:

```bash
git diff --check
git status --short
git diff --stat origin/main...HEAD
rg -n 'crsr_[A-Za-z0-9]+' --glob '!docs/superpowers/**' .
```

Expected: no real Cursor key match, no unrelated generated artifacts, and only
Cursor integration files in the feature diff.

- [ ] **Step 8: Commit Task 7**

```bash
git add internal/cursor/live_test.go docs/configuration.md docs/tools.md docs/verification.md docs/superpowers
git commit -m "Document and verify Cursor agent integration"
```

---

## Final Acceptance Checklist

- [ ] Cursor appears in Providers as an **Agent integration**.
- [ ] A valid key connects and lists account-available Cursor models.
- [ ] An invalid key is rejected without secret leakage.
- [ ] `CURSOR_API_KEY` works without writing a key to YAML.
- [ ] Connecting Cursor leaves the active Antares model/provider unchanged.
- [ ] Cursor models are absent from the primary model picker.
- [ ] `cursor_agent` starts, follows up, and cancels cloud runs with approval.
- [ ] `cursor_agent_status` snapshots or streams without approval.
- [ ] No-repo and HTTPS GitHub repo/PR runs encode correctly.
- [ ] SSE reconnect uses `Last-Event-ID`; expired streams fall back to run status.
- [ ] Context cancellation reports recoverable IDs and does not silently cancel remote work.
- [ ] Unit tests, race tests, frontend tests, typecheck, full Go suite, and build pass.
- [ ] Live Cursor metadata test passes only from an environment-provided key.
- [ ] Installed daemon is restarted and healthy.
- [ ] No credential or generated build noise appears in the git diff.
