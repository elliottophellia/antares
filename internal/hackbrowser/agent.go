// Agent: the BFS crawl loop.
//
// Pop URLs off the page queue, navigate, scan, ask the planner what to do,
// execute the planned tasks, drain the resulting HTTP captures, enqueue
// any new same-host links the scan revealed, repeat. Auth phases
// (anonymous → registered → authenticated) drive when login/logout pages
// appear in the queue.
//
// v1 scope (this file):
//   - single-credential crawl (anonymous or one credential id)
//   - one LLM plan per page, executed in queue order
//   - per-page fingerprint for re-discovery after login
//   - cancellation via thecrawl's Done channel
//
// Deferred to a later iteration (the original TS carries these; marked
// here so we don't forget):
//   - re-plan when new elements appear mid-task
//   - occlusion probe + recovery
//   - combobox → option mechanical expansion
//   - multi-credential page-diff
//   - "unexplored elements" second pass

package hackbrowser

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/browser"
)

var agentLog = Log.Create("hackbrowser:agent")

const (
	maxStepsPerPage      = 30
	postGotoWaitMs       = 400
	spaRenderRetryWaitMs = 600
	defaultMaxSteps      = 50
)

// Agent owns one crawl's mutable state: the BFS queue, the visited set,
// the planner/executor, the sink for captures, and the event sink for
// telemetry. Methods are not safe for concurrent use — one crawl per
// Agent instance.
type Agent struct {
	Sess     *browser.Session
	Planner  *LLMPlanner
	Executor *Executor
	Scope    ScopeMatcher

	// State accumulates across the whole crawl.
	Global *GlobalState

	// Cred is the credential id tagging this crawl's captures. SingleCred
	// for anonymous; otherwise the credential label.
	Cred string

	// Sink receives captured HTTP requests, scoped by Scope. Nil drops them.
	Sink func(CapturedRequest)

	// EventSink receives telemetry events (page-change, plan-received, ...).
	EventSink func(CSEvent)

	// Done, when closed, signals graceful cancellation. The agent finishes
	// the current step and exits.
	Done <-chan struct{}

	// Usage accumulates LLM token usage across the whole crawl.
	Usage *CrawlUsage

	mu sync.Mutex
}

// Crawl runs the BFS loop until the queue empties, MaxSteps is hit, or
// Done fires. Returns the list of errors encountered (also pushed onto
// the agent's CrawlResult.Errors via the caller).
func (a *Agent) Crawl(ctx context.Context, startURL string, maxSteps int) []string {
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	a.Global.PageQueue = []string{NormalizeURL(startURL)}
	var errors []string

	for len(a.Global.PageQueue) > 0 && a.Global.TotalSteps < maxSteps {
		select {
		case <-a.Done:
			agentLog.Info("crawl cancelled mid-queue", F("visited", len(a.Global.VisitedPages)))
			return errors
		case <-ctx.Done():
			errors = append(errors, fmt.Sprintf("context cancelled: %v", ctx.Err()))
			return errors
		default:
		}

		pageURL := a.Global.PageQueue[0]
		a.Global.PageQueue = a.Global.PageQueue[1:]

		if a.Global.VisitedPages[pageURL] {
			continue
		}
		a.Global.VisitedPages[pageURL] = true
		a.Global.TotalSteps++

		a.emit(CSEvent{Type: "page-change", URL: pageURL, PageNum: a.Global.TotalSteps, MaxPages: maxSteps, Credential: a.Cred})

		if err := a.visitPage(ctx, pageURL); err != nil {
			agentLog.Warn("page visit failed — continuing", F("url", pageURL), F("err", err.Error()))
			errors = append(errors, fmt.Sprintf("%s: %v", pageURL, err))
		}

		// Auth-phase transitions: if we just visited an auth-related URL,
		// advance the phase so subsequent queue items are explored as
		// authenticated.
		a.advanceAuthPhase(pageURL)

		// Post-login re-discovery: when login completes, re-queue every
		// visited page so the planner sees the authenticated version. The
		// fingerprint check in visitPage skips pages that haven't changed.
		if a.Global.PendingReDiscovery {
			a.flushReDiscovery()
		}
	}

	a.emit(CSEvent{
		Type: "crawl-done",
		PagesExplored:     len(a.Global.VisitedPages),
		CapturedEndpoints: len(a.Global.CapturedPaths),
		Credentials:       []string{a.Cred},
	})
	return errors
}

// visitPage drives one page through the scan → plan → execute pipeline.
func (a *Agent) visitPage(ctx context.Context, pageURL string) error {
	if err := a.Sess.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}
	// Brief settle for SPA first paint.
	select {
	case <-a.Done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-afterMs(postGotoWaitMs):
	}

	// Reveal lazy content + expand safe disclosures so the scan sees the
	// whole attack surface.
	RevealLazyContent(ctx, a.Sess)
	ExpandDisclosures(ctx, a.Sess)

	// Initial scan; retry once on empty (SPA still booting).
	elements, err := CollectElements(ctx, a.Sess)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	if len(elements) == 0 {
		select {
		case <-a.Done:
			return nil
		case <-afterMs(spaRenderRetryWaitMs):
		}
		elements, err = CollectElements(ctx, a.Sess)
		if err != nil {
			return fmt.Errorf("scan retry: %w", err)
		}
	}
	elements = FilterVisitedLinks(elements, pageURL, a.Global.VisitedPages)

	// Skip re-visit when nothing changed since the last visit (post-login
	// re-discovery optimization — avoid burning an LLM call on an
	// unchanged page).
	if !a.shouldPlan(pageURL, elements) {
		agentLog.Debug("fingerprint unchanged — skipping plan", F("url", pageURL))
		a.enqueueLinks(elements, pageURL)
		return nil
	}

	// Build the planner snapshot and ask the LLM what to do.
	vcBlocked := IsViewportCenterBlocked(ctx, a.Sess)
	snap := BuildPlannerSnapshot(pageURL, elements, a.Global, a.Cred, vcBlocked, nil)
	a.emit(CSEvent{Type: "llm-thinking", Reason: "page-plan", Elements: len(elements), Credential: a.Cred})

	plan, err := a.Planner.Plan(ctx, snap)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	a.emit(CSEvent{
		Type:      "plan-received",
		Tasks:     len(plan.Tasks),
		PageState: plan.PageState,
		Credential: a.Cred,
		Summary:    summaryOf(plan.Tasks),
	})
	applyPlanIntelligence(plan, pageURL, a.Global, a.Cred)

	agentLog.Info("plan received",
		F("url", pageURL),
		F("tasks", len(plan.Tasks)),
		F("pageState", string(plan.PageState)),
	)

	// Execute the planned tasks in order. The LLM is called at most once
	// per page in v1; if mid-task the DOM changes, we keep going with the
	// original plan (the deferred replan-on-new-elements feature would go
	// here).
	for _, task := range plan.Tasks {
		select {
		case <-a.Done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		a.runTaskWithCapture(ctx, task, pageURL)
	}

	// Enqueue same-host links discovered during the scan.
	a.enqueueLinks(elements, pageURL)

	// Store the page fingerprint for future re-visit comparison.
	finalElements, _ := CollectElements(ctx, a.Sess)
	GetIntelligence(a.Global, a.Cred).PageFingerprints[pageURL] = GenerateFingerprint(finalElements)
	return nil
}

// runTaskWithCapture wraps one task execution with a Network drain so
// every HTTP request the action fired lands in the capture sink. The
// pre-action UI snapshot is paired with the captures for correlation.
func (a *Agent) runTaskWithCapture(ctx context.Context, task PageTask, pageURL string) {
	// Take a fresh element list right before the action — the DOM may have
	// moved since the last scan.
	elements, _ := CollectElements(ctx, a.Sess)
	if len(elements) == 0 {
		// Page navigated away mid-step — nothing to act on.
		return
	}

	// Resolve the trigger selector for the UI snapshot (forms → submit,
	// clicks → the clicked element).
	triggerSel := ""
	if task.Type == "form" && task.Submit != nil {
		if el := resolveByRoleLabel(elements, task.Submit.Role, task.Submit.Label); el != nil {
			triggerSel = el.Selector
		}
	} else if task.Type == "click" {
		if el := resolveByRoleLabel(elements, task.Role, task.Label); el != nil {
			triggerSel = el.Selector
		}
	}

	uiCtx, _ := SnapshotPageUI(ctx, a.Sess, triggerSel, "")
	label := taskLabel(task)
	a.emit(CSEvent{
		Type:        "action-start",
		Kind:        task.Type,
		TargetLabel: label,
		Credential:  a.Cred,
	})

	// Drain any stale Network events that arrived before the action so we
	// only attribute the post-action burst to this task.
	_ = a.Sess.DrainNetwork(ctx)

	res := a.Executor.ExecuteTask(ctx, task, elements)

	// Drain everything that arrived during the action.
	requests := a.Sess.DrainNetwork(ctx)
	for _, req := range requests {
		a.capture(req, uiCtx, pageURL, label)
	}

	if res.Success {
		// Drain mutation matching — if the task was tagged with a
		// triggersMutation keyword, empty-state URLs waiting on that
		// keyword come back into the queue.
		if task.TriggersMutation != "" {
			drained := DrainOnMutation(a.Global, a.Cred, task.TriggersMutation)
			if len(drained) > 0 {
				a.Global.PageQueue = append(a.Global.PageQueue, drained...)
				agentLog.Info("drained empty-state queue", F("keyword", task.TriggersMutation), F("count", len(drained)))
			}
		}
	}
	a.emit(CSEvent{
		Type:       "action-end",
		OK:         res.Success,
		Mutation:   task.TriggersMutation != "",
		Credential: a.Cred,
	})
}

// capture filters one NetworkRequest by scope, builds a CapturedRequest,
// and forwards it to the sink. Out-of-scope hosts (CDN, analytics, ...) are
// dropped silently.
func (a *Agent) capture(req browser.NetworkRequest, ui *UIContext, pageURL, trigger string) {
	if a.Sink == nil {
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return
	}
	if !a.Scope(u.Hostname()) {
		return
	}
	endpointKey := fmt.Sprintf("%s %s", req.Method, u.Path)
	a.mu.Lock()
	first := !a.Global.CapturedPaths[endpointKey]
	if first {
		a.Global.CapturedPaths[endpointKey] = true
	}
	a.mu.Unlock()

	scheme := "https"
	if u.Scheme != "" {
		scheme = u.Scheme
	}
	raw := BuildRawRequest(req.Method, req.URL, req.Headers, req.PostData)
	captured := CapturedRequest{
		Raw:            raw,
		Scheme:         scheme,
		UIContext:      ui,
		TriggerElement: triggerLabelForTask(trigger),
		PageURL:        pageURL,
		Timestamp:      time.Now(),
	}
	if req.Response != nil {
		captured.Response = &CapturedResponse{
			Status:  req.Response.Status,
			Headers: req.Response.Headers,
			Body:    "", // body fetching is expensive; deferred to v2
		}
	}
	a.Sink(captured)

	status := 0
	if req.Response != nil {
		status = req.Response.Status
	}
	a.emit(CSEvent{
		Type:       "capture",
		Method:     req.Method,
		Path:       u.Path,
		Status:     status,
		Trigger:    trigger,
		Credential: a.Cred,
		IsMutation: isMutationMethod(req.Method),
	})
}

// enqueueLinks scans the element list for in-scope, unvisited same-host
// links and appends them to the BFS queue.
func (a *Agent) enqueueLinks(elements []RawElement, fromURL string) {
	seen := map[string]bool{}
	for _, el := range elements {
		if el.Role != "link" || el.Href == "" {
			continue
		}
		u, err := url.Parse(el.Href)
		if err != nil || u.Host == "" {
			continue
		}
		if !a.Scope(u.Hostname()) {
			continue
		}
		n := NormalizeURL(el.Href)
		if seen[n] || a.Global.VisitedPages[n] {
			continue
		}
		seen[n] = true
		a.Global.PageQueue = append(a.Global.PageQueue, n)
	}
}

// shouldPlan reports whether the planner should be called for this page.
// v1 uses a simple fingerprint match: if the page's structural fingerprint
// is identical to the last visit's, skip it. The first visit always plans.
func (a *Agent) shouldPlan(pageURL string, elements []RawElement) bool {
	intel := GetIntelligence(a.Global, a.Cred)
	prev, ok := intel.PageFingerprints[pageURL]
	if !ok {
		return true
	}
	return GenerateFingerprint(elements) != prev
}

// advanceAuthPhase moves the auth-state machine when the visited URL looks
// auth-related. The phases drive what gets queued next.
func (a *Agent) advanceAuthPhase(pageURL string) {
	switch a.Global.AuthPhase {
	case AuthAnonymous:
		switch ClassifyAuthURL(pageURL) {
		case "register":
			a.Global.AuthPhase = AuthRegistered
		case "login":
			a.Global.AuthPhase = AuthAuthenticated
			a.Global.PendingReDiscovery = true
		}
	case AuthRegistered:
		if ClassifyAuthURL(pageURL) == "login" {
			a.Global.AuthPhase = AuthAuthenticated
			a.Global.PendingReDiscovery = true
		}
	}
}

// flushReDiscovery re-queues every visited page after login so the
// authenticated versions get scanned. The fingerprint check skips pages
// whose content did not change.
func (a *Agent) flushReDiscovery() {
	a.Global.PendingReDiscovery = false
	for u := range a.Global.VisitedPages {
		a.Global.PageQueue = append(a.Global.PageQueue, u)
	}
	agentLog.Info("post-login re-discovery queued", F("pages", len(a.Global.VisitedPages)))
}

// emit forwards an event to the sink if one is configured.
func (a *Agent) emit(ev CSEvent) {
	if a.EventSink == nil {
		return
	}
	a.EventSink(ev)
}

// ============================================================
// Helpers
// ============================================================

// applyPlanIntelligence translates the planner's pageState/revisit fields
// into the agent's empty-state queue. When the planner says "this page is
// empty now, revisit after a mutation that adds X", we queue that signal
// keyed by the mutation keyword so a later task tagged triggersMutation=X
// drains it.
func applyPlanIntelligence(plan PagePlan, pageURL string, state *GlobalState, credID string) {
	if plan.PageState != PageStateEmpty {
		return
	}
	if plan.RevisitAfter != "any-mutation" {
		return
	}
	if MarkPageEmpty(state, credID, pageURL, plan.RevisitOn) {
		agentLog.Debug("page marked empty — queued for revisit", F("url", pageURL), F("keyword", plan.RevisitOn))
	}
}

// taskLabel returns a one-line human description of a task for telemetry.
func taskLabel(task PageTask) string {
	if task.Type == "form" && task.Submit != nil {
		fields := len(task.Fields)
		return fmt.Sprintf("form(%d fields → %s)", fields, task.Submit.Label)
	}
	return task.Label
}

// triggerLabelForTask formats the trigger element for capture metadata:
// "role:label".
func triggerLabelForTask(trigger string) string {
	// Trigger already comes through as a task label; in v1 we leave it as
	// the raw task description. A future version will pass role:label here.
	return trigger
}

// summaryOf produces the per-task summary list sent in the plan-received
// event — bounded to 10 entries to keep telemetry small.
func summaryOf(tasks []PageTask) []EventSummary {
	if len(tasks) > 10 {
		tasks = tasks[:10]
	}
	out := make([]EventSummary, 0, len(tasks))
	for _, t := range tasks {
		es := EventSummary{Kind: t.Type}
		if t.Type == "form" && t.Submit != nil {
			es.Label = fmt.Sprintf("%d fields → %s", len(t.Fields), t.Submit.Label)
		} else {
			es.Label = t.Label
		}
		out = append(out, es)
	}
	return out
}

// isMutationMethod reports whether an HTTP method mutates server state.
func isMutationMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}
