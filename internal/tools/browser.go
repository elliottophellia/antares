package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/browser"
	"github.com/enowdev/antares/internal/config"
)

// browserSessions keeps one browser per conversation. A page that survives
// between tool calls is what makes multi-step work possible: log in once, then
// click through the site.
var browserSessions = struct {
	sync.Mutex
	byKey map[string]*browser.Session
	reap  sync.Once
}{byKey: map[string]*browser.Session{}}

// sessionFor returns the browser for a conversation, creating it on first use.
func sessionFor(key string, cfg *config.Config) *browser.Session {
	browserSessions.Lock()
	defer browserSessions.Unlock()

	if s, ok := browserSessions.byKey[key]; ok {
		return s
	}
	opts := browser.Options{Headless: true, Width: 1280, Height: 800}
	if cfg != nil {
		b := cfg.Tools.Browser
		opts.Executable = b.Executable
		opts.RemoteURL = b.RemoteURL
		opts.UserDataDir = config.Expand(b.UserDataDir)
		opts.Headless = !b.Headed
		if b.Width > 0 {
			opts.Width = b.Width
		}
		if b.Height > 0 {
			opts.Height = b.Height
		}
		opts.Stealth = b.Stealth
		opts.Proxy = b.Proxy
		opts.Timezone = b.Timezone
		opts.Locale = b.Locale
	}
	s := browser.New(opts)
	browserSessions.byKey[key] = s

	// One reaper for the process: an abandoned browser is a few hundred
	// megabytes that nobody will ever close by hand.
	browserSessions.reap.Do(func() {
		go func() {
			t := time.NewTicker(2 * time.Minute)
			defer t.Stop()
			for range t.C {
				closeIdleBrowsers(15 * time.Minute)
			}
		}()
	})
	return s
}

func closeIdleBrowsers(idle time.Duration) {
	browserSessions.Lock()
	var stale []*browser.Session
	for key, s := range browserSessions.byKey {
		if s.Started() && time.Since(s.LastUsed()) > idle {
			stale = append(stale, s)
			delete(browserSessions.byKey, key)
		}
	}
	browserSessions.Unlock()
	for _, s := range stale {
		s.Stop()
	}
}

// CloseBrowsers shuts every browser down, for process exit.
func CloseBrowsers() {
	browserSessions.Lock()
	all := make([]*browser.Session, 0, len(browserSessions.byKey))
	for key, s := range browserSessions.byKey {
		all = append(all, s)
		delete(browserSessions.byKey, key)
	}
	browserSessions.Unlock()
	for _, s := range all {
		s.Stop()
	}
}

// ---- browser tool -----------------------------------------------------------

type browserTool struct{}

func (browserTool) Name() string { return "browser" }

func (browserTool) Description() string {
	return "Drive a real web browser: open pages, read them, click, type, and submit forms. " +
		"Use it for anything that needs JavaScript, a login, or a form — web_fetch is faster for plain documents.\n\n" +
		"The usual loop is: navigate, then snapshot to see what is on the page, then click or type " +
		"using the e-numbers the snapshot returned, then snapshot again to see what changed. " +
		"References like e7 only stay valid until the page changes, so take a fresh snapshot after every action " +
		"that navigates or re-renders."
}

func (browserTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("What to do.",
			"navigate", "snapshot", "click", "type", "select", "text", "screenshot",
			"press", "scroll", "back", "wait_for", "eval", "console", "requests",
			"tabs", "close"),
		"url":     prop("string", "For navigate: the address to open."),
		"ref":     prop("string", "Element reference from a snapshot, like e7."),
		"text":    prop("string", "For type: the text to enter. For wait_for: the text to wait for. For select: the option."),
		"submit":  propDefault("boolean", "For type: press Enter afterwards.", false),
		"key":     prop("string", "For press: Enter, Tab, Escape, ArrowDown, or a single character."),
		"to":      propEnum("For scroll: which way.", "down", "up", "top", "bottom", "left", "right"),
		"amount":  propDefault("integer", "For scroll: pixels to move.", 600),
		"script":  prop("string", "For eval: JavaScript to run in the page. Its value is returned."),
		"full":    propDefault("boolean", "For screenshot: capture the whole page rather than the viewport.", false),
		"max":     propDefault("integer", "Cap on elements in a snapshot, or characters of text.", 0),
		"seconds": propDefault("integer", "For wait_for: how long to wait.", 15),
	}, "action")
}

// RequiresApproval reports that this tool reaches the network and can act on
// a page as the signed-in user.
func (browserTool) RequiresApproval() bool { return true }

func (browserTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action  string `json:"action"`
		URL     string `json:"url"`
		Ref     string `json:"ref"`
		Text    string `json:"text"`
		Submit  bool   `json:"submit"`
		Key     string `json:"key"`
		To      string `json:"to"`
		Amount  int    `json:"amount"`
		Script  string `json:"script"`
		Full    bool   `json:"full"`
		Max     int    `json:"max"`
		Seconds int    `json:"seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	var cfg *config.Config
	if in.Deps != nil {
		cfg = in.Deps.Config
	}
	if cfg != nil && !cfg.Tools.Browser.Enabled {
		return Errorf("the browser tool is switched off (tools.browser.enabled = false)")
	}

	key := in.SessionID
	if key == "" {
		key = "default"
	}
	s := sessionFor(key, cfg)
	action := strings.ToLower(strings.TrimSpace(args.Action))

	// Everything but close and navigate expects a page that is already open.
	if action != "close" && action != "navigate" {
		if !s.Started() {
			return Errorf("no page is open yet — call browser with action \"navigate\" first")
		}
	}

	switch action {
	case "navigate":
		if strings.TrimSpace(args.URL) == "" {
			return Errorf("url is required to navigate")
		}
		in.Emit(Progress{Tool: "browser", Message: "opening " + args.URL})
		if err := s.Navigate(ctx, args.URL); err != nil {
			return Errorf("could not open %s: %v", args.URL, err)
		}
		// Automatic anti-bot handling: if the page is a Cloudflare/Turnstile/
		// captcha interstitial, wait for the stealth browser to clear it so the
		// agent gets the real content instead of the challenge page.
		autoClearChallenge(ctx, s, in.Emit)
		return browserPageSummary(ctx, s, "Opened")

	case "snapshot":
		snap, err := s.Snapshot(ctx, args.Max)
		if err != nil {
			return Errorf("%v", err)
		}
		head, _ := browserHeader(ctx, s)
		if strings.TrimSpace(snap) == "" {
			return Text(head + "\n\nNothing interactive is on this page. Try action \"text\" to read it.")
		}
		return Text(head + "\n\n" + snap)

	case "click":
		out, err := s.Click(ctx, args.Ref)
		if err != nil {
			return Errorf("%v", err)
		}
		return browserPageSummary(ctx, s, out+" —")

	case "type":
		if args.Ref == "" {
			return Errorf("ref is required — take a snapshot and use one of its e-numbers")
		}
		out, err := s.Type(ctx, args.Ref, args.Text, args.Submit)
		if err != nil {
			return Errorf("%v", err)
		}
		if args.Submit {
			return browserPageSummary(ctx, s, out+" and submitted —")
		}
		return Text(out)

	case "select":
		out, err := s.Select(ctx, args.Ref, args.Text)
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(out)

	case "text":
		body, err := s.Text(ctx, args.Ref, args.Max)
		if err != nil {
			return Errorf("%v", err)
		}
		head, _ := browserHeader(ctx, s)
		if strings.TrimSpace(body) == "" {
			return Text(head + "\n\nThe page has no readable text.")
		}
		return Text(head + "\n\n" + body)

	case "screenshot":
		png, err := s.Screenshot(ctx, args.Full)
		if err != nil {
			return Errorf("%v", err)
		}
		path, err := saveScreenshot(in.Workspace, png)
		if err != nil {
			return Errorf("%v", err)
		}
		return Result{
			Content: fmt.Sprintf("Saved a screenshot to %s (%d KB). Read it with a vision-capable model, or open it yourself.", path, len(png)/1024),
			Meta:    map[string]any{"path": path, "bytes": len(png)},
		}

	case "press":
		if args.Key == "" {
			return Errorf("key is required")
		}
		if err := s.PressKey(ctx, args.Key); err != nil {
			return Errorf("%v", err)
		}
		_ = s.WaitReady(ctx, 3*time.Second)
		return Text("pressed " + args.Key)

	case "scroll":
		out, err := s.Scroll(ctx, args.To, args.Amount)
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(out)

	case "back":
		if err := s.Back(ctx); err != nil {
			return Errorf("%v", err)
		}
		return browserPageSummary(ctx, s, "Went back —")

	case "wait_for":
		if args.Text == "" {
			return Errorf("text is required")
		}
		out, err := s.WaitFor(ctx, args.Text, time.Duration(args.Seconds)*time.Second)
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(out)

	case "eval":
		if strings.TrimSpace(args.Script) == "" {
			return Errorf("script is required")
		}
		v, err := s.Eval(ctx, args.Script)
		if err != nil {
			return Errorf("the script threw: %v", err)
		}
		out := string(v)
		if out == "" || out == "null" {
			out = "(no value)"
		}
		return Text(truncateTool(out, 8000))

	case "console":
		lines := s.Console()
		if len(lines) == 0 {
			return Text("The console has been quiet.")
		}
		return Text(truncateTool(strings.Join(lines, "\n"), 8000))

	case "requests":
		lines := s.Requests()
		if len(lines) == 0 {
			return Text("No responses have been recorded since the last check.")
		}
		return Text(truncateTool(strings.Join(lines, "\n"), 8000))

	case "tabs":
		head, err := browserHeader(ctx, s)
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(head)

	case "close":
		s.Stop()
		browserSessions.Lock()
		delete(browserSessions.byKey, key)
		browserSessions.Unlock()
		return Text("Closed the browser.")

	default:
		return Errorf("unknown action %q", args.Action)
	}
}

// browserHeader is the one-line "where am I" every result opens with.
func browserHeader(ctx context.Context, s *browser.Session) (string, error) {
	url, err := s.URL(ctx)
	if err != nil {
		return "", err
	}
	title, _ := s.Title(ctx)
	if title == "" {
		title = "(untitled)"
	}
	return fmt.Sprintf("%s\n%s", title, url), nil
}

// browserPageSummary reports where the page landed and what is on it, so a
// navigation or click does not need a second call to be useful.
func browserPageSummary(ctx context.Context, s *browser.Session, prefix string) Result {
	head, err := browserHeader(ctx, s)
	if err != nil {
		return Errorf("%v", err)
	}
	snap, err := s.Snapshot(ctx, 60)
	if err != nil {
		return Text(prefix + " " + head)
	}
	out := prefix + " " + head
	if strings.TrimSpace(snap) != "" {
		out += "\n\n" + snap
	}
	return Text(out)
}

// saveScreenshot writes a capture somewhere the user can find it.
func saveScreenshot(workspace string, png []byte) (string, error) {
	dir := filepath.Join(config.Home(), "screenshots")
	if workspace != "" {
		if info, err := os.Stat(workspace); err == nil && info.IsDir() {
			dir = filepath.Join(workspace, ".antares", "screenshots")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("shot-%d.png", time.Now().UnixMilli()))
	if err := os.WriteFile(path, png, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func truncateTool(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n\n… truncated, %d characters total", len(s))
}

// challengeDetectJS reports the kind of bot-challenge on the current page, or ""
// if the page looks like normal content.
const challengeDetectJS = `(() => {
  const t = (document.title||'').toLowerCase();
  if (t.includes('just a moment') || t.includes('attention required') || t.includes('verifying you are human') || t.includes('checking your browser')) return 'cloudflare';
  if (document.querySelector('.cf-turnstile, [name="cf-turnstile-response"]')) return 'turnstile';
  if (document.querySelector('.g-recaptcha, iframe[src*="recaptcha"]')) return 'recaptcha';
  if (document.querySelector('.h-captcha, iframe[src*="hcaptcha"]')) return 'hcaptcha';
  return '';
})()`

// autoClearChallenge waits for an anti-bot interstitial to pass. The stealth
// browser clears many challenges on its own; this polls until the challenge
// markers are gone or a short budget elapses, so normal browsing "just works"
// against bot-protected pages without a separate solve step.
func autoClearChallenge(ctx context.Context, s *browser.Session, emit func(Progress)) {
	kind := strings.Trim(strings.TrimSpace(evalStr(ctx, s)), `"`)
	if kind == "" {
		return
	}
	emit(Progress{Tool: "browser", Message: "bot challenge detected (" + kind + "), waiting for it to clear…"})
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		if strings.Trim(strings.TrimSpace(evalStr(ctx, s)), `"`) == "" {
			emit(Progress{Tool: "browser", Message: "challenge cleared"})
			return
		}
	}
	emit(Progress{Tool: "browser", Message: "challenge still present after waiting — the page may need an interactive solve"})
}

func evalStr(ctx context.Context, s *browser.Session) string {
	v, err := s.EvalString(ctx, challengeDetectJS)
	if err != nil {
		return ""
	}
	return v
}
