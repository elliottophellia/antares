package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/intercept"
)

// interceptState holds the process-wide MITM proxy so the tool and the dashboard
// share one instance, plus the interceptor registry (browsers, terminal, …).
var interceptState struct {
	mu  sync.Mutex
	p   *intercept.Proxy
	reg *intercept.Registry
}

// InterceptProxy returns the shared proxy, creating it (with a persisted CA)
// on first use. Exported so the HTTP server can surface the same instance.
func InterceptProxy() (*intercept.Proxy, error) {
	interceptState.mu.Lock()
	defer interceptState.mu.Unlock()
	if interceptState.p != nil {
		return interceptState.p, nil
	}
	ca, err := intercept.LoadOrCreateCA(config.Path("intercept"))
	if err != nil {
		return nil, err
	}
	interceptState.p = intercept.New(ca)
	return interceptState.p, nil
}

// InterceptRegistry returns the shared interceptor registry, seeded once with
// every available interceptor (browsers, terminal, and the dependency-gated
// ones). Exported so the HTTP server surfaces the same set.
func InterceptRegistry() *intercept.Registry {
	interceptState.mu.Lock()
	defer interceptState.mu.Unlock()
	if interceptState.reg != nil {
		return interceptState.reg
	}
	r := intercept.NewRegistry()
	for _, i := range intercept.Browsers() {
		r.Register(i)
	}
	for _, i := range intercept.OtherInterceptors() {
		r.Register(i)
	}
	interceptState.reg = r
	return r
}

// InterceptActivate resolves the shared proxy+CA and activates an interceptor
// by id, auto-starting the proxy if it is down. It is the one path the tool and
// the HTTP handlers share for "hook a client to the proxy".
func InterceptActivate(ctx context.Context, id string, extra map[string]any) (intercept.Session, error) {
	p, err := InterceptProxy()
	if err != nil {
		return nil, err
	}
	if running, _ := p.Status(); !running {
		if err := p.Start("127.0.0.1:8899"); err != nil {
			return nil, err
		}
	}
	reg := InterceptRegistry()
	ic, ok := reg.Get(id)
	if !ok {
		return nil, fmt.Errorf("no interceptor %q", id)
	}
	if ok, reason := ic.Available(ctx); !ok {
		return nil, fmt.Errorf("%s", reason)
	}
	_, addr := p.Status()
	if extra == nil {
		extra = map[string]any{}
	}
	// The Android interceptor's root cert-push needs the CA subject hash.
	extra["subject_hash"] = intercept.SubjectHashOld(p.CA().Cert())
	sess, err := ic.Activate(ctx, intercept.ActivateOpts{
		ProxyAddr:       addr,
		CACertPath:      config.Path("intercept", "ca-cert.pem"),
		SPKIFingerprint: p.SPKIFingerprint(),
		Extra:           extra,
	})
	if err != nil {
		return nil, err
	}
	reg.PutSession(sess)
	return sess, nil
}

type interceptTool struct{}

func (interceptTool) Name() string { return "intercept" }
func (interceptTool) Description() string {
	return "Man-in-the-middle HTTP(S) proxy for authorized debugging/testing of properties you control. Actions:\n" +
		"- Proxy: `start` (begin listening), `stop`, `status`, `clear` (empty the capture log).\n" +
		"- Traffic: `list` (recent exchanges, newest first), `get` (one exchange by id — full headers+bodies).\n" +
		"- Rules (shape traffic by URL substring): `rule_add` with match + one of block=true / mock_status+mock_body / breakpoint=true; `rule_list`; `rule_delete`.\n" +
		"- Breakpoints (from a breakpoint rule): `bp_list` shows paused requests; `bp_resume` id=<id> forwards it; `bp_abort` id=<id> refuses it.\n" +
		"- Trust the CA: `ca` prints the cert path; `cert_install` prints ready per-OS trust commands (macOS/Linux/Windows/Firefox-NSS/Android).\n" +
		"- Interceptors (hook a real client to the proxy, auto-starting it): `interceptors` lists them with availability; `activate` interceptor=<id> (e.g. fresh-chrome opens a throwaway Chrome already trusting the proxy; terminal returns the env exports to paste; android sets the device proxy over adb and, if rooted, pushes the CA); `deactivate` session=<id>.\n" +
		"Use it to observe, block, mock, or pause-and-edit a target's requests, or to launch/point a client through the proxy."
}
func (interceptTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("What to do.",
			"start", "stop", "status", "list", "get", "ca", "cert_install",
			"rule_add", "rule_list", "rule_delete", "clear",
			"interceptors", "activate", "deactivate",
			"bp_list", "bp_resume", "bp_abort"),
		"port":        propDefault("integer", "Port to listen on for start.", 8899),
		"id":          prop("integer", "Exchange id (get), rule id (rule_delete), or paused id (bp_resume/bp_abort)."),
		"match":       prop("string", "URL substring a rule matches (rule_add)."),
		"block":       propDefault("boolean", "rule_add: block matching requests.", false),
		"breakpoint":  propDefault("boolean", "rule_add: pause matching requests for editing before forwarding.", false),
		"mock_status": prop("integer", "rule_add: mock response status."),
		"mock_body":   prop("string", "rule_add: mock response body."),
		"limit":       propDefault("integer", "How many recent exchanges to list.", 30),
		"interceptor": prop("string", "activate: interceptor id (e.g. fresh-chrome, terminal). See action=interceptors."),
		"url":         prop("string", "activate (browser): URL to open."),
		"session":     prop("string", "deactivate: session id from activate."),
	}, "action")
}
func (interceptTool) RequiresApproval() bool { return true }

func (interceptTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Action      string `json:"action"`
		Port        int    `json:"port"`
		ID          int64  `json:"id"`
		Match       string `json:"match"`
		Block       bool   `json:"block"`
		Breakpoint  bool   `json:"breakpoint"`
		MockStatus  int    `json:"mock_status"`
		MockBody    string `json:"mock_body"`
		Limit       int    `json:"limit"`
		Interceptor string `json:"interceptor"`
		URL         string `json:"url"`
		Session     string `json:"session"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	p, err := InterceptProxy()
	if err != nil {
		return Errorf("intercept unavailable: %v", err)
	}
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "start":
		port := args.Port
		if port <= 0 {
			port = 8899
		}
		if err := p.Start(fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			return Errorf("%v", err)
		}
		_, addr := p.Status()
		return Text(fmt.Sprintf("Intercept proxy running on http://%s\n\n"+
			"Point a browser/app at it as an HTTP proxy, and trust the antares CA (action \"ca\") to intercept HTTPS.", addr))
	case "stop":
		p.Stop()
		return Text("Intercept proxy stopped.")
	case "status":
		running, addr := p.Status()
		if !running {
			return Text("Intercept proxy is not running.")
		}
		return Text(fmt.Sprintf("Running on http://%s — %d exchanges captured, %d rules.", addr, len(p.Exchanges()), len(p.Rules())))
	case "list":
		limit := args.Limit
		if limit <= 0 {
			limit = 30
		}
		ex := p.Exchanges()
		var b strings.Builder
		fmt.Fprintf(&b, "%d exchanges (newest first):\n\n", len(ex))
		for i, e := range ex {
			if i >= limit {
				break
			}
			tag := ""
			if e.Mocked {
				tag = " [mocked]"
			} else if e.Blocked {
				tag = " [blocked]"
			}
			fmt.Fprintf(&b, "#%d %s %s → %d (%dms)%s\n", e.ID, e.Method, e.URL, e.Status, e.DurationMS, tag)
		}
		return Text(b.String())
	case "get":
		for _, e := range p.Exchanges() {
			if e.ID == args.ID {
				var b strings.Builder
				fmt.Fprintf(&b, "#%d %s %s → %d (%dms)\n\n", e.ID, e.Method, e.URL, e.Status, e.DurationMS)
				b.WriteString("Request headers:\n")
				for k, v := range e.ReqHeaders {
					fmt.Fprintf(&b, "  %s: %s\n", k, strings.Join(v, ", "))
				}
				if e.ReqBody != "" {
					fmt.Fprintf(&b, "\nRequest body:\n%s\n", truncateText(e.ReqBody, 4000))
				}
				b.WriteString("\nResponse headers:\n")
				for k, v := range e.RespHeaders {
					fmt.Fprintf(&b, "  %s: %s\n", k, strings.Join(v, ", "))
				}
				fmt.Fprintf(&b, "\nResponse body:\n%s\n", truncateText(e.RespBody, 6000))
				return Text(b.String())
			}
		}
		return Errorf("no exchange #%d", args.ID)
	case "ca":
		path := config.Path("intercept", "ca-cert.pem")
		return Text(fmt.Sprintf("The antares Intercept CA certificate is at:\n%s\n\n"+
			"Import and trust it in the OS/browser trust store to intercept HTTPS. The proxy signs per-host leaves with it.", path))
	case "rule_add":
		if strings.TrimSpace(args.Match) == "" {
			return Errorf("match is required for rule_add")
		}
		r := p.AddRule(intercept.Rule{Match: args.Match, Block: args.Block, Breakpoint: args.Breakpoint, MockStatus: args.MockStatus, MockBody: args.MockBody})
		return Text(fmt.Sprintf("Added rule #%d matching %q (block=%v, breakpoint=%v, mock_status=%d).", r.ID, r.Match, r.Block, r.Breakpoint, r.MockStatus))
	case "rule_list":
		rules := p.Rules()
		var b strings.Builder
		fmt.Fprintf(&b, "%d rule(s):\n", len(rules))
		for _, r := range rules {
			fmt.Fprintf(&b, "#%d match=%q block=%v mock=%d\n", r.ID, r.Match, r.Block, r.MockStatus)
		}
		return Text(b.String())
	case "rule_delete":
		p.DeleteRule(args.ID)
		return Text(fmt.Sprintf("Deleted rule #%d (if it existed).", args.ID))
	case "clear":
		p.Clear()
		return Text("Cleared the capture log.")

	case "cert_install":
		hash := intercept.SubjectHashOld(p.CA().Cert())
		fp := intercept.Fingerprint(p.CA().Cert())
		targets := intercept.InstallLocations(config.Path("intercept", "ca-cert.pem"), hash)
		var b strings.Builder
		fmt.Fprintf(&b, "CA SHA-1 fingerprint: %s\n\nTrust the CA with one of:\n\n", fp)
		for _, ti := range targets {
			mark := "○"
			if ti.Available {
				mark = "●"
			}
			fmt.Fprintf(&b, "%s %s\n  %s\n", mark, ti.Label, ti.Command)
			if ti.Note != "" {
				fmt.Fprintf(&b, "  (%s)\n", ti.Note)
			}
			if !ti.Available {
				fmt.Fprintf(&b, "  [needs %q on PATH]\n", ti.Tool)
			}
			b.WriteString("\n")
		}
		return Text(b.String())

	case "interceptors":
		reg := InterceptRegistry()
		var b strings.Builder
		b.WriteString("Interceptors (● available, ○ needs setup):\n\n")
		for _, ic := range reg.List() {
			ok, reason := ic.Available(ctx)
			mark := "○"
			if ok {
				mark = "●"
			}
			fmt.Fprintf(&b, "%s %s — %s [%s]\n", mark, ic.ID(), ic.Label(), ic.Category())
			if !ok && reason != "" {
				fmt.Fprintf(&b, "    %s\n", reason)
			}
		}
		b.WriteString("\nActivate one with action=activate interceptor=<id>.")
		return Text(b.String())

	case "activate":
		if strings.TrimSpace(args.Interceptor) == "" {
			return Errorf("interceptor is required for activate (see action=interceptors)")
		}
		extra := map[string]any{}
		if args.URL != "" {
			extra["url"] = args.URL
		}
		sess, err := InterceptActivate(ctx, args.Interceptor, extra)
		if err != nil {
			return Errorf("%v", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Activated %q — session %s\n", args.Interceptor, sess.ID())
		for k, v := range sess.Info() {
			fmt.Fprintf(&b, "  %s: %v\n", k, v)
		}
		return Text(b.String())

	case "deactivate":
		if strings.TrimSpace(args.Session) == "" {
			return Errorf("session is required for deactivate")
		}
		if err := InterceptRegistry().StopSession(args.Session); err != nil {
			return Errorf("%v", err)
		}
		return Text("Deactivated session " + args.Session + ".")

	case "bp_list":
		paused := p.ListPaused()
		var b strings.Builder
		fmt.Fprintf(&b, "%d request(s) paused at a breakpoint:\n", len(paused))
		for _, pe := range paused {
			fmt.Fprintf(&b, "#%d %s %s\n", pe.ID, pe.Method, pe.URL)
		}
		b.WriteString("\nResume with action=bp_resume id=<id> (or bp_abort id=<id>).")
		return Text(b.String())
	case "bp_resume":
		p.Resume(args.ID, intercept.BreakpointResume{})
		return Text(fmt.Sprintf("Resumed paused request #%d.", args.ID))
	case "bp_abort":
		p.Abort(args.ID)
		return Text(fmt.Sprintf("Aborted paused request #%d.", args.ID))

	default:
		return Errorf("unknown action %q", args.Action)
	}
}
