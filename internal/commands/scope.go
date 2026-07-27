package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/scope"
)

// cmdScope manages the authorized testing scope: the list of targets the
// security roles may act against. The list is the whole safety mechanism, so
// changing it is deliberate — add and remove, never a free-text edit.
func cmdScope(_ context.Context, d Deps, in Input) (Result, error) {
	verb, rest, _ := strings.Cut(strings.TrimSpace(in.Args), " ")
	verb = strings.ToLower(strings.TrimSpace(verb))
	rest = strings.TrimSpace(rest)
	cfg := d.config()

	switch verb {
	case "", "list", "show":
		entries := cfg.Security.Scope
		if len(entries) == 0 {
			return Result{Output: "The authorized scope is empty. Nothing is in bounds for security testing " +
				"until you add a target with `/scope add <domain|ip|cidr>`."}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Authorized scope** (%d)\n\n", len(entries))
		for _, e := range entries {
			fmt.Fprintf(&b, "- `%s`\n", e)
		}
		if cfg.Security.RequireScope {
			b.WriteString("\nOut-of-scope targets are refused (require_scope is on).")
		} else {
			b.WriteString("\nOut-of-scope targets are warned about but not blocked. " +
				"Set `security.require_scope` true to refuse them.")
		}
		return Result{Output: b.String()}, nil

	case "add":
		if rest == "" {
			return Result{}, errors.New("usage: /scope add <domain|ip|cidr>")
		}
		if err := scope.Valid(rest); err != nil {
			return Result{}, fmt.Errorf("that is not a valid target: %v", err)
		}
		next, err := config.Reload()
		if err != nil {
			return Result{}, err
		}
		for _, e := range next.Security.Scope {
			if strings.EqualFold(e, rest) {
				return Result{Output: rest + " is already in scope."}, nil
			}
		}
		next.Security.Scope = append(next.Security.Scope, rest)
		if err := config.Save(next); err != nil {
			return Result{}, err
		}
		if err := d.reload(); err != nil {
			return Result{}, err
		}
		return Result{
			Output: fmt.Sprintf("Added `%s` to the authorized scope. Test it only with authorization.", rest),
			Action: Action{Kind: "config-changed"},
		}, nil

	case "remove", "rm", "del":
		if rest == "" {
			return Result{}, errors.New("usage: /scope remove <target>")
		}
		next, err := config.Reload()
		if err != nil {
			return Result{}, err
		}
		kept := next.Security.Scope[:0]
		found := false
		for _, e := range next.Security.Scope {
			if strings.EqualFold(e, rest) {
				found = true
				continue
			}
			kept = append(kept, e)
		}
		if !found {
			return Result{}, fmt.Errorf("%q is not in the scope", rest)
		}
		next.Security.Scope = kept
		if err := config.Save(next); err != nil {
			return Result{}, err
		}
		if err := d.reload(); err != nil {
			return Result{}, err
		}
		return Result{Output: "Removed `" + rest + "` from the scope.", Action: Action{Kind: "config-changed"}}, nil

	case "check":
		if rest == "" {
			return Result{}, errors.New("usage: /scope check <target>")
		}
		res := scope.Scope{Entries: cfg.Security.Scope}.Check(rest)
		if res.Authorized {
			return Result{Output: fmt.Sprintf("`%s` is **in scope** (matched `%s`).", res.Target, res.Matched)}, nil
		}
		return Result{Output: fmt.Sprintf("`%s` is **out of scope** — %s.", res.Target, res.Reason)}, nil

	case "clear":
		next, err := config.Reload()
		if err != nil {
			return Result{}, err
		}
		next.Security.Scope = nil
		if err := config.Save(next); err != nil {
			return Result{}, err
		}
		if err := d.reload(); err != nil {
			return Result{}, err
		}
		return Result{Output: "Cleared the scope. Security testing is now blocked until you add a target.",
			Action: Action{Kind: "config-changed"}}, nil
	}
	return Result{}, fmt.Errorf("unknown scope command %q — use list, add, remove, check, or clear", verb)
}
