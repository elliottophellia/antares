package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// listProxiesTool exposes the global proxy store to the agent. Proxies are pure
// storage — the agent decides when to use one (e.g. when a request is rate- or
// geo-blocked, or when the user asks to go through a proxy) by passing an entry's
// id or label to a tool that accepts a `proxy` argument (osint_email_full today).
type listProxiesTool struct{}

func (listProxiesTool) Name() string { return "list_proxies" }
func (listProxiesTool) Description() string {
	return "List the proxies saved in the global proxy store (id, label, scheme, host:port — passwords redacted). " +
		"Use it to pick a proxy to route a request through: pass the id or label to a tool's `proxy` argument. " +
		"Handy when a direct request is rate-limited/blocked or the user asks to use a proxy."
}
func (listProxiesTool) Schema() map[string]any { return schema(map[string]any{}) }
func (listProxiesTool) RequiresApproval() bool { return false }

func (listProxiesTool) Execute(_ context.Context, in Input) Result {
	if in.Deps == nil || in.Deps.Config == nil {
		return Errorf("no config available in this runtime")
	}
	entries := in.Deps.Config.Proxies.Entries
	if len(entries) == 0 {
		return Text("No proxies are saved. Add some on the dashboard's Proxies page, then reference them by id or label.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Saved proxies (%d) — pass an id or label as a tool's `proxy` argument:\n\n", len(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "  - id=%s  label=%q  %s\n", e.ID, e.Label, redactProxy(e.ProxyURL()))
	}
	return Text(b.String())
}

// redactProxy hides any password in a proxy URL before showing it to the model.
func redactProxy(full string) string {
	if full == "" {
		return ""
	}
	u, err := url.Parse(full)
	if err != nil {
		return ""
	}
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			u.User = url.UserPassword(u.User.Username(), "••••")
		}
	}
	return u.String()
}
