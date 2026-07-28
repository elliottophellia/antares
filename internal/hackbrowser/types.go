// Package hackbrowser is an autonomous web-crawling engine built for offensive
// security reconnaissance. It drives a real Chromium over CDP (via
// internal/browser), uses a language model to decide where to click and what
// to type, and records every HTTP request the page issues as it goes — the
// raw material the rest of an engagement analyzes for vulnerabilities.
//
// Architecture (ported from the TypeScript hackbrowser library at
// packages/hackbrowser/src/*.ts):
//
//   agent.go     BFS crawl loop, auth-phase transitions, post-login re-discovery
//   scanner.go   DOM collection of every interactive element on a page, via
//                embedded JavaScript executed through Runtime.evaluate
//   navigator.go LLM-driven page planner — given a page snapshot, decide
//                what to click and what to fill in
//   executor.go  resolve a planned task against the live DOM and run it
//   capture.go   record Network.* events emitted while an action runs, and
//                correlate them with the UI element that triggered them
//   auth.go      anonymous → register → login → authenticated phase flow
//   scope.go     which hosts the crawl is allowed to capture
//   state.go     global + per-page crawl state, fingerprinting
//   api.go       public RunCrawl entry point and CrawlOptions
//
// The original TS codebase kept the playwright dependency in a worker
// subprocess because Bun's compiled binary crashed at startup when playwright
// was imported eagerly. Antares drives Chromium directly over CDP and has no
// such problem, so the worker subprocess + IPC bridge is collapsed entirely
// into the parent — the engine runs in-process.
package hackbrowser

import "time"

// RawElement is one interactive element collected from the DOM by the
// scanner. The model sees id/role/label/value but never sees the selector —
// selectors are private to the executor so the model cannot be tricked into
// producing an injection via selector.
type RawElement struct {
	ID          string `json:"id"`           // "E1", "E2", ... assigned by scanner
	Tag         string `json:"tag"`          // "button", "input", "a", ...
	Role        string `json:"role"`         // ARIA role or implicit role
	Label       string `json:"label"`        // aria-label || label[for] || innerText || placeholder || name
	Value       string `json:"value"`        // current value (input/select/aria-valuenow)
	Enabled     bool   `json:"enabled"`      // !disabled
	Href        string `json:"href"`         // for links
	Type        string `json:"type"`         // input type
	Placeholder string `json:"placeholder"`
	Options     string `json:"options"`      // comma-separated <option> values for <select>
	Constraints string `json:"constraints"`  // "min:0 max:1000 step:10", "maxlength:160", ...
	Selector    string `json:"selector"`     // CSS or role selector — never shown to the LLM
	InChrome    bool   `json:"in_chrome"`    // inside nav/header/footer/aside landmark
}

// DeferredAuthPage is an auth-related URL discovered during the anonymous
// phase and queued for processing later, in pentester order:
// register → login → logout.
type DeferredAuthPage struct {
	URL  string `json:"url"`
	Type string `json:"type"` // "register" | "login" | "logout"
}

// AuthPhase tracks where the crawl is in its auth journey.
type AuthPhase string

const (
	AuthAnonymous    AuthPhase = "anonymous"
	AuthRegistered   AuthPhase = "registered"
	AuthAuthenticated AuthPhase = "authenticated"
)

// IntelligenceState is credential-scoped crawl metadata: empty-state signals,
// revisit counters, and DOM fingerprints. Each credential in a multi-cred
// crawl owns its own instance so signals cannot leak between identities.
type IntelligenceState struct {
	// URL → mutation keyword to match. "*" means any-mutation (legacy fallback).
	EmptyStateQueue map[string]string
	// URL → revisit count (hard cap: 2 per URL per credential).
	RevisitCount map[string]int
	// URL → element fingerprint, for re-visit comparison.
	PageFingerprints map[string]string
}

// GlobalState is the cross-credential state of a crawl.
type GlobalState struct {
	VisitedPages    map[string]bool
	CapturedPaths   map[string]bool // "METHOD /path" dedup
	AuthPhase       AuthPhase
	TotalSteps      int
	PageQueue       []string
	DeferredAuth    []DeferredAuthPage
	PendingReDiscovery bool
	PathPatternCount map[string]int
	OutOfScope      []string
	// Credential-scoped intelligence, keyed by credential id. Single-cred
	// crawls use the SingleCred sentinel key.
	IntelligenceByCredential map[string]*IntelligenceState
}

// SingleCred is the credential-id sentinel for anonymous or
// single-credential crawls.
const SingleCred = "__single__"

// ActionRecord is one executed action on the current page.
type ActionRecord struct {
	ElementID      string   `json:"element_id"`
	Action         string   `json:"action"`
	Value          string   `json:"value,omitempty"`
	Success        bool     `json:"success"`
	HTTPSideEffects []string `json:"http_side_effects,omitempty"` // "POST /api/Users [201]"
}

// ActionResult is fed back to the LLM after each action.
type ActionResult struct {
	Success     bool     `json:"success"`
	Error       string   `json:"error,omitempty"`
	Navigated   bool     `json:"navigated,omitempty"`
	NewURL      string   `json:"new_url,omitempty"`
	HTTPRequests []string `json:"http_requests,omitempty"` // "POST /api/Users [201]"
	DOMChanged  bool     `json:"dom_changed,omitempty"`
}

// ============================================================
// Planner task graph
// ============================================================

// FormFieldPlan is one field in a FormTask — role+label identifies the
// element, value is what to write.
type FormFieldPlan struct {
	Role  string `json:"role"`  // "textbox"|"combobox"|"checkbox"|"radio"|"slider"
	Label string `json:"label"` // semantic key — executor resolves to live element
	Value string `json:"value"` // computed by LLM
}

// FormTask fills a form completely and submits it.
type FormTask struct {
	Type              string         `json:"type"` // always "form"
	Fields            []FormFieldPlan `json:"fields"`
	Submit            FormSubmitRef  `json:"submit"`
	TriggersMutation  string         `json:"triggers_mutation,omitempty"`
}

// FormSubmitRef identifies the submit button by role+label.
type FormSubmitRef struct {
	Role  string `json:"role"`
	Label string `json:"label"`
}

// ClickTask clicks a button/tab/accordion/interactive element.
type ClickTask struct {
	Type             string `json:"type"` // always "click"
	Role             string `json:"role"`
	Label            string `json:"label"`
	Reason           string `json:"reason,omitempty"`
	TriggersMutation string `json:"triggers_mutation,omitempty"`
}

// PageTask is the discriminated union of FormTask and ClickTask. The Type
// field discriminates: "form" or "click".
type PageTask struct {
	Type             string          `json:"type"`
	Fields           []FormFieldPlan `json:"fields,omitempty"`           // for "form"
	Submit           *FormSubmitRef  `json:"submit,omitempty"`           // for "form"
	Role             string          `json:"role,omitempty"`             // for "click"
	Label            string          `json:"label,omitempty"`            // for "click"
	Reason           string          `json:"reason,omitempty"`           // for "click"
	TriggersMutation string          `json:"triggers_mutation,omitempty"`
}

// PageStateKind is the planner's classification of a page's content.
type PageStateKind string

const (
	PageStatePopulated PageStateKind = "populated"
	PageStateEmpty     PageStateKind = "empty"
	PageStateUnknown   PageStateKind = "unknown"
)

// PagePlan is the LLM's analysis of a page: what to do, when to revisit.
type PagePlan struct {
	Tasks         []PageTask     `json:"tasks"`
	PageState     PageStateKind  `json:"page_state,omitempty"`
	RevisitAfter  string         `json:"revisit_after,omitempty"` // "any-mutation" or ""
	RevisitReason string         `json:"revisit_reason,omitempty"`
	RevisitOn     string         `json:"revisit_on,omitempty"` // mutation keyword
}

// ============================================================
// Capture types (CDP Network events → structured records)
// ============================================================

// UIField is one form field visible in the page UI at capture time. Used to
// correlate HTTP request parameters with the controls a user actually sees.
type UIField struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Value        string `json:"value"`
	Type         string `json:"type"`
	Options      string `json:"options,omitempty"`
	IsReadOnly   bool   `json:"is_read_only"`
	IsDisabled   bool   `json:"is_disabled"`
	IsHidden     bool   `json:"is_hidden"`
	HiddenReason string `json:"hidden_reason,omitempty"`
	IsDisplayOnly bool  `json:"is_display_only"`
	Validation   struct {
		Min      string `json:"min,omitempty"`
		Max      string `json:"max,omitempty"`
		MaxLength string `json:"max_length,omitempty"`
		Pattern  string `json:"pattern,omitempty"`
		Required bool   `json:"required,omitempty"`
	} `json:"validation"`
}

// UIContext is what was visible on the page when a request fired.
type UIContext struct {
	PageURL       string    `json:"page_url"`
	PageTitle     string    `json:"page_title"`
	ComponentPath string    `json:"component_path"` // "Settings > Profile > Edit Form"
	FormName      string    `json:"form_name"`
	Fields        []UIField `json:"fields"`
	HiddenParams  []string  `json:"hidden_params"`
}

// CapturedRequest is one HTTP request the page issued during a crawl.
type CapturedRequest struct {
	Raw            string             `json:"raw"`         // wire-format HTTP request
	Scheme         string             `json:"scheme"`      // "http" or "https"
	Response       *CapturedResponse  `json:"response,omitempty"`
	UIContext      *UIContext         `json:"ui_context,omitempty"`
	TriggerElement string             `json:"trigger_element,omitempty"` // "button:Delete User"
	ElementRoles   []string           `json:"element_roles,omitempty"`
	PageURL        string             `json:"page_url,omitempty"`
	PageVisitedBy  []string           `json:"page_visited_by,omitempty"`
	Timestamp      time.Time          `json:"timestamp"`
}

// CapturedResponse is the HTTP response paired with a CapturedRequest.
type CapturedResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// ============================================================
// Library API: CrawlOptions and CrawlResult
// ============================================================

// CredentialConfig is one identity in a multi-credential crawl.
type CredentialConfig struct {
	ID string `json:"id"` // "admin", "user", "manager", ...
}

// CrawlOptions is the public entry point's argument shape. Flat on purpose
// — the nested AgentConfig is internal.
type CrawlOptions struct {
	// Target (required).
	URL string

	// Session that owns the captures, in antares' store. Optional — anonymous
	// dry-runs leave it empty.
	SessionID string

	// CredentialID tags captures with a single identity. Mutually exclusive
	// with MultiCredentials.
	CredentialID string

	// Log level: "DEBUG", "INFO" (default), "WARN", "ERROR".
	LogLevel string

	// EventSink, when set, receives every CSEvent the crawler emits
	// (page-change, capture, crawl-done, ...). Independent of the panel UI.
	EventSink func(event CSEvent)

	// Network scope: hostnames whose requests get captured. Empty → derive
	// from URL via eTLD+1 as "*.{eTLD+1}".
	Scope []string
	// OutOfScope are semantic labels the planner must never plan (e.g.
	// "Delete Account"). Distinct from Scope.
	Exclude []string

	// Crawl behaviour.
	Steps   int    // max navigation steps; 0 → 50
	Headless bool

	// Auth.
	SessionFile string // path to a saved cookies JSON
	Credentials *LoginCredentials
	Authenticated bool // manual login via visible browser
	MultiCredentials []CredentialConfig

	// DryRun: crawl without sending captures to the session store; print to log.
	DryRun bool

	// Cancellation. When fired, the agent finishes the current step and exits
	// the BFS loop gracefully.
	Done <-chan struct{}
}

// LoginCredentials auto-fills a login form.
type LoginCredentials struct {
	Username         string
	Password         string
	UsernameSelector string // optional CSS override
	PasswordSelector string
}

// CrawlResult is what RunCrawl returns. Errors are aggregated here rather
// than thrown — caller decides how to surface them.
type CrawlResult struct {
	SessionID        string    `json:"session_id"`
	CapturedEndpoints int      `json:"captured_endpoints"`
	PagesExplored    int       `json:"pages_explored"`
	TotalSteps       int       `json:"total_steps"`
	Errors           []string  `json:"errors"`
	Usage            CrawlUsage `json:"usage"`
}

// CrawlUsage is the LLM token usage for the whole crawl.
type CrawlUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CacheReadTokens   int `json:"cache_read_tokens"`
	CacheWriteTokens  int `json:"cache_write_tokens"`
}

// ============================================================
// Panel events (telemetry; emitted to EventSink)
// ============================================================

// CSEvent is one telemetry event. The discriminated Type field selects the
// shape of the rest of the payload. Unknown kinds are ignored by consumers,
// so new ones can be added without breaking older sinks.
type CSEvent struct {
	Type string `json:"type"`

	// Common fields — populated as relevant per Type.
	Target      string         `json:"target,omitempty"`
	Credentials []string       `json:"credentials,omitempty"`
	MaxPages    int            `json:"max_pages,omitempty"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	URL         string         `json:"url,omitempty"`
	PageNum     int            `json:"page_num,omitempty"`
	Credential  string         `json:"credential,omitempty"`
	Tasks       int            `json:"tasks,omitempty"`
	PageState   PageStateKind  `json:"page_state,omitempty"`
	Summary     []EventSummary `json:"summary,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	TargetLabel string         `json:"target_label,omitempty"`
	Value       string         `json:"value,omitempty"`
	OK          bool           `json:"ok,omitempty"`
	Mutation    bool           `json:"mutation,omitempty"`
	Method      string         `json:"method,omitempty"`
	Path        string         `json:"path,omitempty"`
	Status      int            `json:"status,omitempty"`
	Trigger     string         `json:"trigger,omitempty"`
	IsMutation  bool           `json:"is_mutation,omitempty"`
	Note        string         `json:"note,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	Elements    int            `json:"elements,omitempty"`
	From        string         `json:"from,omitempty"`
	To          string         `json:"to,omitempty"`
	PagesExplored     int      `json:"pages_explored,omitempty"`
	CapturedEndpoints int      `json:"captured_endpoints,omitempty"`
	Mutations         int      `json:"mutations,omitempty"`
}

// EventSummary is one entry in a plan-received event's task list.
type EventSummary struct {
	Kind  string `json:"kind"`  // "form" | "click"
	Label string `json:"label"`
}
