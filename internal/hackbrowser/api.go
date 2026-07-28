// Public API: CrawlOptions → RunCrawl → CrawlResult.
//
// Mirrors the TS library's packages/hackbrowser/src/api.ts surface so
// existing caller docs translate. Three responsibilities:
//
//   1. Validate options (multi-cred headless mismatch, missing URL, ...).
//   2. Preflight (browser session can start; planner has a model).
//   3. Wire the pieces (Scope matcher, credential tag, sinks) and run
//      the agent BFS loop, aggregating any per-page errors into the
//      result instead of letting them propagate.
//
// Runtime errors during the crawl land in CrawlResult.Errors — only truly
// fatal cases (the browser cannot start, the model is unconfigured) throw.
// This is the same contract as the TS original so the agent tool that
// wraps RunCrawl can surface a clean "crawl finished with N errors"
// instead of an exception.

package hackbrowser

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/browser"
	"github.com/enowdev/antares/internal/llm"
)

// RunCrawl is the single public entry point. It is blocking — the caller
// is expected to wrap it in a goroutine if it needs to run in the
// background. Cancellation is via opts.Done; the agent finishes the
// current step and exits.
func RunCrawl(ctx context.Context, opts CrawlOptions, model llm.Client) (CrawlResult, error) {
	if err := validate(opts); err != nil {
		return CrawlResult{}, err
	}
	if model == nil {
		return CrawlResult{}, ErrNoModel
	}

	// Set the log threshold if the caller asked for one.
	if opts.LogLevel != "" {
		Log.Init(parseLogLevel(opts.LogLevel))
	}

	sess := buildSession(opts)
	if err := sess.Start(ctx); err != nil {
		return CrawlResult{}, fmt.Errorf("could not start the browser: %w", err)
	}
	defer sess.Stop()

	// Apply auth: load saved session cookies, or auto-login if creds given.
	if opts.SessionFile != "" {
		_, _ = LoadSession(ctx, sess, opts.SessionFile)
	}

	// Set up the scope matcher — either the explicit list or derived from
	// the target URL via publicsuffix.
	scopes := opts.Scope
	if len(scopes) == 0 {
		scopes = []string{DeriveScope(opts.URL)}
	}
	matcher := MakeMatcher(scopes)

	// Set up the credential tag for captures.
	cred := SingleCred
	if opts.CredentialID != "" {
		cred = opts.CredentialID
	}

	// Optional capture sink: when DryRun is on, route captures to the log
	// instead of discarding them. The agent-tool wrapper in
	// internal/tools installs its own sink that persists captures into
	// the antares session store.
	var sink func(CapturedRequest)
	if opts.DryRun {
		sink = dryRunSink
	}

	usage := &CrawlUsage{}
	agent := &Agent{
		Sess:      sess,
		Planner:   &LLMPlanner{Model: model, Usage: usage},
		Executor:  &Executor{Sess: sess},
		Scope:     matcher,
		Global:    CreateGlobalState(opts.Exclude),
		Cred:      cred,
		Sink:      sink,
		EventSink: opts.EventSink,
		Done:      opts.Done,
		Usage:     usage,
	}

	// Optional auto-login BEFORE the crawl starts. If it fails the caller
	// is told via the error path — credentials that don't work mean the
	// crawl runs anonymous, which is rarely what the user wanted.
	if opts.Credentials != nil {
		// Navigate to the target first so the login form is in view.
		if err := sess.Navigate(ctx, opts.URL); err != nil {
			return CrawlResult{Errors: []string{fmt.Sprintf("navigate for login: %v", err)}}, nil
		}
		if err := AutoLogin(ctx, sess, *opts.Credentials); err != nil {
			// Log and continue — the crawl may still discover endpoints
			// worth capturing.
			agentLog.Warn("auto-login failed — continuing anonymous", F("err", err.Error()))
		}
	}

	errs := agent.Crawl(ctx, opts.URL, opts.Steps)

	// Save cookies on the way out so the next run starts authenticated.
	if opts.SessionFile != "" {
		_ = SaveSession(ctx, sess, opts.SessionFile, []string{opts.URL})
	}

	return CrawlResult{
		SessionID:         opts.SessionID,
		CapturedEndpoints: len(agent.Global.CapturedPaths),
		PagesExplored:     len(agent.Global.VisitedPages),
		TotalSteps:        agent.Global.TotalSteps,
		Errors:            errs,
		Usage:             *usage,
	}, nil
}

// validate catches option combinations that cannot work before the browser
// starts, so the failure is a clean error rather than a confusing runtime
// crash mid-crawl.
func validate(opts CrawlOptions) error {
	if strings.TrimSpace(opts.URL) == "" {
		return errors.New("hackbrowser: opts.URL is required")
	}
	if !strings.Contains(opts.URL, "://") {
		// Allow "example.com" → "https://example.com".
		opts.URL = "https://" + opts.URL
	}
	if opts.Credentials != nil && opts.Authenticated {
		return errors.New("hackbrowser: Credentials and Authenticated are mutually exclusive")
	}
	if len(opts.MultiCredentials) >= 2 && opts.Headless {
		return errors.New("hackbrowser: multi-credential mode requires a visible browser (Headless=false) for manual login")
	}
	return nil
}

// buildSession constructs the browser session from crawl options.
func buildSession(opts CrawlOptions) *browser.Session {
	sopts := browser.Options{
		Headless: opts.Headless,
		Width:    1280,
		Height:   800,
	}
	if sopts.Headless == false && !opts.Authenticated && opts.Credentials == nil {
		// Default to headless when nothing about the crawl needs a window.
		sopts.Headless = true
	}
	if opts.Authenticated {
		// Manual login requires a visible window.
		sopts.Headless = false
	}
	return browser.New(sopts)
}

// dryRunSink logs captures instead of persisting them. Used when DryRun
// is on so a caller can see what the crawl saw without involving the
// session store.
func dryRunSink(req CapturedRequest) {
	agentLog.Info("captured (dry-run)",
		F("method", firstLine(req.Raw)),
		F("trigger", req.TriggerElement),
		F("page", req.PageURL),
	)
}

// firstLine returns the first line of s — used to summarize a raw HTTP
// request in the dry-run log.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
