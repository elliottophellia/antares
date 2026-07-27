package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/scope"
)

// scopeCheckTool tells a security role whether a target is authorized before it
// acts. It is the difference between a penetration test and an attack: the
// tools that follow are only lawful against a target on this list.
type scopeCheckTool struct{}

func (scopeCheckTool) Name() string { return "scope_check" }

func (scopeCheckTool) Description() string {
	return "Check whether a target (a domain, URL, or IP address) is within the authorized testing scope. " +
		"Call this before any active security testing against a target. If it is not in scope, do not test it — " +
		"stop and ask the user to confirm authorization and add it to the scope."
}

func (scopeCheckTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string", "The domain, URL, or IP address to check."),
	}, "target")
}

func (scopeCheckTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Target string `json:"target"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if strings.TrimSpace(args.Target) == "" {
		return Errorf("target is required")
	}

	var sc scope.Scope
	requireScope := false
	if in.Deps != nil && in.Deps.Config != nil {
		sc = scope.Scope{Entries: in.Deps.Config.Security.Scope}
		requireScope = in.Deps.Config.Security.RequireScope
	}

	res := sc.Check(args.Target)
	if res.Authorized {
		return Text(fmt.Sprintf("IN SCOPE: %s is authorized (matched %q). You may test it, within the rules of engagement.",
			res.Target, res.Matched))
	}

	msg := fmt.Sprintf("OUT OF SCOPE: %s. %s.\n\nDo not perform active testing against this target. "+
		"Confirm the user has authorization, then add it with `/scope add %s`.",
		res.Target, res.Reason, res.Target)
	if requireScope {
		// Fail closed as an error the model cannot read past as success.
		return Errorf("%s", msg)
	}
	return Text(msg)
}
