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
// share one instance.
var interceptState struct {
	mu sync.Mutex
	p  *intercept.Proxy
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

type interceptTool struct{}

func (interceptTool) Name() string { return "intercept" }
func (interceptTool) Description() string {
	return "Run and inspect a man-in-the-middle HTTP(S) proxy for authorized debugging/testing of properties " +
		"you control. Start it, point a browser or app at it (and trust the antares CA), then list captured " +
		"request/response exchanges, inspect one, or add rules that block or mock matching requests."
}
func (interceptTool) Schema() map[string]any {
	return schema(map[string]any{
		"action":      propEnum("What to do.", "start", "stop", "status", "list", "get", "ca", "rule_add", "rule_list", "rule_delete", "clear"),
		"port":        propDefault("integer", "Port to listen on for start.", 8899),
		"id":          prop("integer", "Exchange id (get) or rule id (rule_delete)."),
		"match":       prop("string", "URL substring a rule matches (rule_add)."),
		"block":       propDefault("boolean", "rule_add: block matching requests.", false),
		"mock_status": prop("integer", "rule_add: mock response status."),
		"mock_body":   prop("string", "rule_add: mock response body."),
		"limit":       propDefault("integer", "How many recent exchanges to list.", 30),
	}, "action")
}
func (interceptTool) RequiresApproval() bool { return true }

func (interceptTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Action     string `json:"action"`
		Port       int    `json:"port"`
		ID         int64  `json:"id"`
		Match      string `json:"match"`
		Block      bool   `json:"block"`
		MockStatus int    `json:"mock_status"`
		MockBody   string `json:"mock_body"`
		Limit      int    `json:"limit"`
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
		r := p.AddRule(intercept.Rule{Match: args.Match, Block: args.Block, MockStatus: args.MockStatus, MockBody: args.MockBody})
		return Text(fmt.Sprintf("Added rule #%d matching %q (block=%v, mock_status=%d).", r.ID, r.Match, r.Block, r.MockStatus))
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
	default:
		return Errorf("unknown action %q", args.Action)
	}
}
