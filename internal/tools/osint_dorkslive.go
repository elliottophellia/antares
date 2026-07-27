package tools

import (
	"context"
	"fmt"
	"strings"
)

// osint_dorks_live runs the generated dorks through a search engine and returns
// the actual result links, rather than only building the query URLs. Keyless
// (reuses the web-search DuckDuckGo backend). For authorized reconnaissance.

type osintDorksLiveTool struct{}

func (osintDorksLiveTool) Name() string { return "osint_dorks_live" }
func (osintDorksLiveTool) Description() string {
	return "Run a handful of targeted dorks for a name/email/username/domain through a search engine and return " +
		"the actual result links found (not just the query URLs). Keyless."
}
func (osintDorksLiveTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string", "The name, email, username, or domain to search for."),
		"max":    propDefault("integer", "How many dork queries to run (each hits the search engine).", 4),
	}, "target")
}
func (osintDorksLiveTool) RequiresApproval() bool { return false }

func (osintDorksLiveTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Target string `json:"target"`
		Max    int    `json:"max"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return Errorf("target is required")
	}
	if args.Max <= 0 || args.Max > len(dorkTemplates) {
		args.Max = 4
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Live dork results for %q:\n\n", target)
	for _, tmpl := range dorkTemplates[:args.Max] {
		q := fmt.Sprintf(tmpl, target)
		results, _ := duckDuckGoSearch(ctx, q, 5)
		fmt.Fprintf(&b, "▸ %s — %d result(s)\n", q, len(results))
		for _, r := range results {
			fmt.Fprintf(&b, "    %s\n", r.URL)
		}
	}
	return Text(b.String())
}
