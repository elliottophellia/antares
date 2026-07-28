// Crawl state: global (cross-credential) and per-page. Includes the helpers
// that drive the empty-state re-discovery queue, the auth-phase classifier,
// and the structural fingerprint the planner uses to decide whether a page
// has changed between visits.
//
// Ported from packages/hackbrowser/src/state.ts. Constants and behavioural
// rules carry over verbatim — they are tuned by experience against real
// apps, not theoretical.

package hackbrowser

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// MaxRevisitsPerURL caps how many times one URL can come back for a
// re-explore after a mutation. Combined with the fingerprint skip, this
// makes re-discovery loops impossible.
const MaxRevisitsPerURL = 2

// AnyMutation is the wildcard keyword — a URL marked with it drains on any
// successful mutation. Specific keywords (e.g. "user-created") drain only
// on exact match.
const AnyMutation = "*"

// MaxActionsInPrompt bounds how many ActionRecords we send to the LLM.
const MaxActionsInPrompt = 10

// MaxFailedElements bounds the failed-element list in the prompt.
const MaxFailedElements = 20

// PageState is everything that resets on every page transition.
type PageState struct {
	CurrentURL           string
	Elements             []RawElement
	ViewportCenterBlocked bool
	ActionsThisPage      []ActionRecord
	FailedElementIDs     map[string]bool
	LastActionResult     *ActionResult
	Step                 int
}

// OcclusionSignal is one element found covered by an overlay at click time.
type OcclusionSignal struct {
	Label        string
	Role         string
	OccluderText string
}

// OcclusionState threads through a page visit to bound dismiss loops.
type OcclusionState struct {
	Pending  []OcclusionSignal
	Signaled map[string]bool
}

// CreateGlobalState builds an empty GlobalState with the given out-of-scope
// labels snapshotted in. The caller's slice is not retained.
func CreateGlobalState(outOfScope []string) *GlobalState {
	cp := make([]string, len(outOfScope))
	copy(cp, outOfScope)
	return &GlobalState{
		VisitedPages:             map[string]bool{},
		CapturedPaths:            map[string]bool{},
		AuthPhase:                AuthAnonymous,
		PageQueue:                []string{},
		OutOfScope:               cp,
		IntelligenceByCredential: map[string]*IntelligenceState{},
	}
}

// CreatePageState builds an empty PageState for one URL.
func CreatePageState(url string) *PageState {
	return &PageState{
		CurrentURL:       url,
		FailedElementIDs: map[string]bool{},
	}
}

// GetIntelligence returns (lazily creating) the credential-scoped
// intelligence state. Pass SingleCred in single-credential mode.
func GetIntelligence(state *GlobalState, credID string) *IntelligenceState {
	if intel, ok := state.IntelligenceByCredential[credID]; ok {
		return intel
	}
	intel := &IntelligenceState{
		EmptyStateQueue:  map[string]string{},
		RevisitCount:     map[string]int{},
		PageFingerprints: map[string]string{},
	}
	state.IntelligenceByCredential[credID] = intel
	return intel
}

// MarkPageEmpty queues a URL for revisit after a mutation, scoped to one
// credential. Returns false if the hard limit rejects it.
func MarkPageEmpty(state *GlobalState, credID, pageURL, expectedMutation string) bool {
	intel := GetIntelligence(state, credID)
	if intel.RevisitCount[pageURL] >= MaxRevisitsPerURL {
		return false
	}
	if expectedMutation == "" {
		expectedMutation = AnyMutation
	}
	intel.EmptyStateQueue[pageURL] = expectedMutation
	return true
}

// DrainOnMutation unqueues empty-state URLs whose expected mutation matches
// the given keyword (or expects AnyMutation). Matched URLs are pushed onto
// the front of the page queue and have their revisit counter incremented.
// Returns the URLs that were drained.
func DrainOnMutation(state *GlobalState, credID, taskMutation string) []string {
	intel := GetIntelligence(state, credID)
	if len(intel.EmptyStateQueue) == 0 {
		return nil
	}
	var drained []string
	for u, expected := range intel.EmptyStateQueue {
		matches := expected == AnyMutation || (taskMutation != "" && expected == taskMutation)
		if !matches {
			continue
		}
		state.PageQueue = prependString(state.PageQueue, u)
		intel.RevisitCount[u]++
		delete(intel.PageFingerprints, u)
		drained = append(drained, u)
	}
	for _, u := range drained {
		delete(intel.EmptyStateQueue, u)
	}
	return drained
}

// DrainEmptyStateQueue is the legacy alias — drains only AnyMutation URLs.
func DrainEmptyStateQueue(state *GlobalState, credID string) []string {
	return DrainOnMutation(state, credID, "")
}

// HasSuccessfulMutation reports whether any line in the httpRequests log
// shows a 2xx POST/PUT/PATCH/DELETE. Format: "METHOD /path [status]".
func HasSuccessfulMutation(httpRequests []string) bool {
	for _, line := range httpRequests {
		m := mutationLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var status int
		for _, r := range m[2] {
			status = status*10 + int(r-'0')
		}
		if status >= 200 && status < 300 {
			return true
		}
	}
	return false
}

var mutationLineRE = regexp.MustCompile(`^(POST|PUT|PATCH|DELETE)\s+\S+\s+\[(\d+)\]`)

// ============================================================
// Auth URL classification
// ============================================================

var authPatterns = []struct {
	t  string
	re *regexp.Regexp
}{
	{"register", regexp.MustCompile(`(?i)/(register|signup|sign-up|create-account|join)`)},
	{"login", regexp.MustCompile(`(?i)/(login|signin|sign-in|authenticate|auth/login)`)},
	{"logout", regexp.MustCompile(`(?i)/(logout|signout|sign-out|auth/logout)`)},
}

// ClassifyAuthURL returns "register", "login", "logout", or "" if the URL
// does not look auth-related.
func ClassifyAuthURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := u.Path + u.Fragment
	for _, p := range authPatterns {
		if p.re.MatchString(path) {
			return p.t
		}
	}
	return ""
}

// ============================================================
// Structural fingerprint
// ============================================================

var inputRoles = map[string]bool{
	"textbox": true, "combobox": true, "checkbox": true, "radio": true, "slider": true,
}

// contentActionRoles are the action roles whose presence signals a real
// post-auth change when added to/removed from a page. Excludes links (BFS
// discovery handles navigation).
var contentActionRoles = map[string]bool{
	"button": true, "menuitem": true, "tab": true,
}

// GenerateFingerprint produces the input + content-action fingerprint used
// for post-login re-discovery comparison. Inputs always count; content-area
// actions (not in site chrome) count too; links never count; sorted.
func GenerateFingerprint(elements []RawElement) string {
	parts := make([]string, 0, len(elements))
	for _, e := range elements {
		if !inputRoles[e.Role] && !(contentActionRoles[e.Role] && !e.InChrome) {
			continue
		}
		parts = append(parts, e.Role+":"+e.Label+":"+e.Type+":"+boolStr(e.Enabled))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// GenerateFullFingerprint includes buttons and all interactive roles for
// multi-credential page diffing. Excludes links and info elements (false
// positives from sidebar/navbar noise). Excludes value (user-specific data).
func GenerateFullFingerprint(elements []RawElement) string {
	parts := make([]string, 0, len(elements))
	for _, e := range elements {
		if e.Label == "" || e.Role == "link" || e.Role == "info" {
			continue
		}
		opt, ph := e.Options, e.Placeholder
		parts = append(parts, strings.Join([]string{e.Role, e.Label, e.Type, boolStr(e.Enabled), opt, ph}, ":"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// ComputeElementAvailability maps "role::label" → list of contexts that
// have that element. Used for multi-credential page diffing.
func ComputeElementAvailability(elementsByContext map[string][]RawElement) map[string][]string {
	out := map[string][]string{}
	// Preserve context iteration order for deterministic output.
	keys := make([]string, 0, len(elementsByContext))
	for k := range elementsByContext {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, ctx := range keys {
		for _, e := range elementsByContext[ctx] {
			k := e.Role + "::" + e.Label
			out[k] = append(out[k], ctx)
		}
	}
	return out
}

// AvailabilityToRecord converts the availability map to a flat "role:label"
// keyed record for JSON serialization.
func AvailabilityToRecord(availability map[string][]string) map[string][]string {
	rec := make(map[string][]string, len(availability))
	for k, v := range availability {
		// Single-colon form for readability on the wire.
		rec[strings.Replace(k, "::", ":", 1)] = v
	}
	return rec
}

// ============================================================
// State mutation
// ============================================================

// RecordAction appends one action's outcome to the page state and updates
// the failed-element set.
func RecordAction(page *PageState, elementID, action, value string, result ActionResult) {
	rec := ActionRecord{
		ElementID:       elementID,
		Action:          action,
		Value:           value,
		Success:         result.Success,
		HTTPSideEffects: result.HTTPRequests,
	}
	page.ActionsThisPage = append(page.ActionsThisPage, rec)
	if !result.Success {
		page.FailedElementIDs[elementID] = true
	}
	r := result
	page.LastActionResult = &r
	page.Step++
}

// ============================================================
// URL normalization
// ============================================================

// NormalizeURL canonicalizes a URL for storage and dedup: lowercased origin
// (via url.Parse), sorted query, no trailing slash, unified hash routing.
// On parse failure the input is returned unchanged so the caller never loses
// the URL entirely.
func NormalizeURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return rawURL
	}
	u.RawQuery = u.Query().Encode()
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/"
	}
	hash := u.Fragment
	if hash != "" && !strings.HasPrefix(hash, "/") {
		hash = "/" + hash
	}
	hash = strings.TrimRight(hash, "/")
	if hash == "/" {
		hash = ""
	}
	u.Fragment = hash
	u.Path = path
	return u.String()
}

// ============================================================
// Prompt payload
// ============================================================

// PromptElement is the model-facing view of a RawElement: id/role/label and
// selected hints, but never the selector.
type PromptElement struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Label       string `json:"label"`
	Type        string `json:"type,omitempty"`
	Value       string `json:"value,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Href        string `json:"href,omitempty"`
	Options     string `json:"options,omitempty"`
	Constraints string `json:"constraints,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"` // pointer so omitted when true
	OccludedBy  string `json:"occluded_by,omitempty"`
}

// PromptPayload is the structured user-message body the planner LLM sees.
type PromptPayload struct {
	URL                  string         `json:"url"`
	ViewportCenterBlocked bool           `json:"viewport_center_blocked"`
	TotalPagesVisited    int            `json:"total_pages_visited"`
	UnvisitedLinksOnPage int            `json:"unvisited_links_on_page"`
	LastAction           *ActionRecord  `json:"last_action,omitempty"`
	RecentActions        []ActionRecord `json:"recent_actions"`
	FailedElements       []string       `json:"failed_elements"`
	Elements             []PromptElement `json:"elements"`
}

// PlannerSnapshot is the minimal view the planner needs — no action history.
type PlannerSnapshot struct {
	URL                  string          `json:"url"`
	ViewportCenterBlocked bool           `json:"viewport_center_blocked"`
	TotalPagesVisited    int             `json:"total_pages_visited"`
	Elements             []PromptElement `json:"elements"`
	OutOfScope           []string        `json:"out_of_scope,omitempty"`
	PendingMutations     []string        `json:"pending_mutations,omitempty"`
}

// BuildPlannerSnapshot produces the minimal model-facing view of one page.
func BuildPlannerSnapshot(
	pageURL string,
	elements []RawElement,
	state *GlobalState,
	credID string,
	viewportCenterBlocked bool,
	occlusions []OcclusionSignal,
) PlannerSnapshot {
	promptElems := make([]PromptElement, 0, len(elements))
	for _, e := range elements {
		promptElems = append(promptElems, elementToPrompt(e))
	}
	if len(occlusions) > 0 {
		// Tag any element whose label+role matches a pending occluder so the
		// planner knows to dismiss the overlay first.
		for _, occ := range occlusions {
			for i := range promptElems {
				if promptElems[i].Label == occ.Label && promptElems[i].Role == occ.Role {
					promptElems[i].OccludedBy = occ.OccluderText
				}
			}
		}
	}
	snap := PlannerSnapshot{
		URL:                   pageURL,
		ViewportCenterBlocked: viewportCenterBlocked,
		TotalPagesVisited:     len(state.VisitedPages),
		Elements:              promptElems,
	}
	if len(state.OutOfScope) > 0 {
		snap.OutOfScope = append([]string(nil), state.OutOfScope...)
	}
	intel := GetIntelligence(state, credID)
	pendingSet := map[string]bool{}
	for _, kw := range intel.EmptyStateQueue {
		if kw != AnyMutation {
			pendingSet[kw] = true
		}
	}
	if len(pendingSet) > 0 {
		snap.PendingMutations = make([]string, 0, len(pendingSet))
		for k := range pendingSet {
			snap.PendingMutations = append(snap.PendingMutations, k)
		}
		sort.Strings(snap.PendingMutations)
	}
	return snap
}

// elementToPrompt projects a RawElement into the LLM-facing form. Selector
// is dropped; optional fields are included only when populated so the JSON
// stays tight.
func elementToPrompt(e RawElement) PromptElement {
	out := PromptElement{
		ID:    e.ID,
		Role:  e.Role,
		Label: e.Label,
	}
	if e.Type != "" {
		out.Type = e.Type
	}
	if e.Value != "" {
		out.Value = e.Value
	}
	if e.Placeholder != "" {
		out.Placeholder = e.Placeholder
	}
	if e.Options != "" {
		out.Options = e.Options
	}
	if e.Constraints != "" {
		out.Constraints = e.Constraints
	}
	if !e.Enabled {
		f := false
		out.Enabled = &f
	}
	if e.Href != "" {
		// Show only path+hash, not the full URL — saves tokens.
		if u, err := url.Parse(e.Href); err == nil && u.Host != "" {
			out.Href = u.Path
			if u.Fragment != "" {
				out.Href += "#" + u.Fragment
			}
		} else {
			out.Href = e.Href
		}
	}
	return out
}

// ============================================================
// Helpers
// ============================================================

// RecordAction's recentActions window: first 3 + last 7 (preserves context
// and recent memory).
func actionsForPrompt(actions []ActionRecord) []ActionRecord {
	if len(actions) <= MaxActionsInPrompt {
		return actions
	}
	head := actions[:3]
	tail := actions[len(actions)-7:]
	out := make([]ActionRecord, 0, len(head)+len(tail))
	out = append(out, head...)
	out = append(out, tail...)
	return out
}

// BuildPromptPayload assembles the structured user message body sent to the
// per-step LLM (the legacy per-action planner).
func BuildPromptPayload(page *PageState, state *GlobalState) PromptPayload {
	recent := actionsForPrompt(page.ActionsThisPage)
	failed := make([]string, 0, len(page.FailedElementIDs))
	for id := range page.FailedElementIDs {
		failed = append(failed, id)
		if len(failed) >= MaxFailedElements {
			break
		}
	}
	sort.Strings(failed)

	unvisited := 0
	for _, e := range page.Elements {
		if e.Role == "link" && e.Href != "" && !state.VisitedPages[NormalizeURL(e.Href)] {
			unvisited++
		}
	}

	var last *ActionRecord
	if n := len(page.ActionsThisPage); n > 0 {
		last = &page.ActionsThisPage[n-1]
	}

	elements := make([]PromptElement, 0, len(page.Elements))
	for _, e := range page.Elements {
		elements = append(elements, elementToPrompt(e))
	}

	return PromptPayload{
		URL:                   page.CurrentURL,
		ViewportCenterBlocked: page.ViewportCenterBlocked,
		TotalPagesVisited:     len(state.VisitedPages),
		UnvisitedLinksOnPage:  unvisited,
		LastAction:            last,
		RecentActions:         recent,
		FailedElements:        failed,
		Elements:              elements,
	}
}

// FindElement returns the RawElement with the given ID, or nil.
func FindElement(page *PageState, elementID string) *RawElement {
	for i := range page.Elements {
		if page.Elements[i].ID == elementID {
			return &page.Elements[i]
		}
	}
	return nil
}

// boolStr returns "true"/"false" — used in fingerprint composition where
// the format must match the TS port byte-for-byte.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// appendStringUnique adds s to list only if it isn't already present.
func appendStringUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// prependString returns a new slice with s at index 0. The original is left
// alone so callers that hold it for read still see the same contents.
func prependString(list []string, s string) []string {
	out := make([]string, 0, len(list)+1)
	out = append(out, s)
	return append(out, list...)
}
