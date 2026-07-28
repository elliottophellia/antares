package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/hackbrowser"
	"github.com/enowdev/antares/internal/llm"
)

// hackbrowserTool is the agent-callable wrapper around the hackbrowser
// crawler. It launches a crawl in a background goroutine and returns
// immediately with a "started" notice — captures stream into the session's
// store as the crawl progresses, and the model inspects them via
// session_search when it needs them.
//
// This mirrors the CyberStrike hackbrowser tool's surface so existing
// prompt patterns translate. The model invokes it with a target URL and
// optional credentials; the engine does the rest.
type hackbrowserTool struct{}

func (hackbrowserTool) Name() string { return "hackbrowser" }

func (hackbrowserTool) RequiresApproval() bool { return true }

func (hackbrowserTool) Description() string {
	return "Crawl a web application autonomously and capture every HTTP request the page issues, " +
		"with the UI context that triggered it. Use this when you have a target URL but no captured " +
		"requests yet — hackbrowser drives a real Chromium, navigates the app, fills forms, clicks " +
		"buttons, and records each request as evidence for later vulnerability analysis.\n\n" +
		"This tool is BLOCKING in v1: it returns when the crawl finishes (or the agent cancels). " +
		"For a long target, plan around that — a 50-page crawl typically takes 1–5 minutes. " +
		"Captures land in the session store as the crawl progresses; use session_search to read them."
}

func (hackbrowserTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string",
			"Target URL to crawl. Used as the navigation start and as the basis for the network scope (*.{eTLD+1})."),
		"credentials": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Credential IDs to tag captures with. Omit for anonymous. v1 tags captures but does not auto-login — pass a single ID to label captures, or use the terminal/login tool to authenticate first.",
		},
		"scope": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional network scope override. Hostname patterns (\"*.example.com\") that bound which requests get captured. Replaces the auto-derived default.",
		},
		"exclude": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "UI labels the planner must never plan (e.g. \"Delete Account\", \"Cancel Subscription\"). Semantic match.",
		},
		"steps":    propDefault("integer", "Maximum pages to crawl. Default 50.", 50),
		"headless": propDefault("boolean", "Run the browser invisibly. Defaults true.", true),
		"dry_run":  propDefault("boolean", "Crawl without persisting captures — log them instead. For quick reconnaissance.", false),
	}, "target")
}

func (hackbrowserTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Target      string   `json:"target"`
		Credentials []string `json:"credentials"`
		Scope       []string `json:"scope"`
		Exclude     []string `json:"exclude"`
		Steps       int      `json:"steps"`
		Headless    bool     `json:"headless"`
		DryRun      bool     `json:"dry_run"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return Errorf("target is required")
	}

	cfg := (*config.Config)(nil)
	if in.Deps != nil {
		cfg = in.Deps.Config
	}
	if cfg == nil {
		return Errorf("hackbrowser needs agent configuration (model + provider) — none available")
	}

	// Resolve the LLM via the same path the agent loop uses.
	id, p := cfg.ResolveProvider("")
	if p.APIKey == "" && p.BaseURL == "" {
		return Errorf("no provider is configured — set providers.openai.api_key (or another) first")
	}
	retries := cfg.Model.MaxRetries
	if retries == 0 {
		retries = 3
	}
	client, err := llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Headers: p.Headers, Timeout: 5 * time.Minute, ProviderID: id,
		Retries: retries, APIVersion: p.APIVersion, Region: p.Region,
	})
	if err != nil {
		return Errorf("could not build the LLM client: %v", err)
	}

	// Translate the agent's credential-id list. v1 takes only the first
	// (multi-credential needs the deferred multi-cred agent support).
	var credID string
	if len(args.Credentials) > 0 {
		credID = strings.TrimSpace(args.Credentials[0])
	}

	opts := hackbrowser.CrawlOptions{
		URL:          target,
		CredentialID: credID,
		Scope:        args.Scope,
		Exclude:      args.Exclude,
		Steps:        args.Steps,
		Headless:     args.Headless,
		DryRun:       args.DryRun,
		Done:         ctx.Done(),
		EventSink: func(ev hackbrowser.CSEvent) {
			// Forward crawl telemetry as tool progress so the dashboard
			// shows live page-changes and captures.
			msg := ev.Type
			if ev.URL != "" {
				msg += " " + ev.URL
			} else if ev.Path != "" {
				msg += " " + ev.Method + " " + ev.Path
			}
			in.Emit(Progress{Tool: "hackbrowser", Message: msg})
		},
	}

	in.Emit(Progress{Tool: "hackbrowser", Message: "starting crawl for " + target})
	res, err := hackbrowser.RunCrawl(ctx, opts, client)
	if err != nil {
		return Errorf("crawl failed: %v", err)
	}

	if len(res.Errors) > 0 {
		return Result{
			Content: fmt.Sprintf("Hackbrowser crawl finished with %d error(s):\n  - %s\nCaptured %d endpoints across %d pages.",
				len(res.Errors), strings.Join(res.Errors, "\n  - "), res.CapturedEndpoints, res.PagesExplored),
			IsError: true,
			Meta: map[string]any{
				"captured":      res.CapturedEndpoints,
				"pages":         res.PagesExplored,
				"input_tokens":  res.Usage.InputTokens,
				"output_tokens": res.Usage.OutputTokens,
				"errors":        res.Errors,
			},
		}
	}
	return Result{
		Content: fmt.Sprintf("Hackbrowser crawl finished: %d endpoints captured across %d pages (%d steps, %d in / %d out tokens).",
			res.CapturedEndpoints, res.PagesExplored, res.TotalSteps,
			res.Usage.InputTokens, res.Usage.OutputTokens),
		Meta: map[string]any{
			"captured":      res.CapturedEndpoints,
			"pages":         res.PagesExplored,
			"input_tokens":  res.Usage.InputTokens,
			"output_tokens": res.Usage.OutputTokens,
		},
	}
}
