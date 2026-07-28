package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// activeRoleKey stores which role a session runs as, so it persists across
// turns rather than being passed on every message.
func activeRoleKey(sessionID string) string { return "role:" + sessionID }

// cmdRoles lists the specialists available, grouped by category.
func cmdRoles(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil || d.Agent.Roles() == nil {
		return Result{}, errors.New("roles are not available")
	}
	list := d.Agent.Roles().List()
	if len(list) == 0 {
		return Result{Output: "No roles are defined."}, nil
	}

	// Group by category, keeping the registry's category order.
	type group struct {
		name  string
		lines []string
	}
	var groups []group
	index := map[string]int{}
	for _, r := range list {
		cat := r.Category
		if cat == "" {
			cat = "other"
		}
		if _, ok := index[cat]; !ok {
			index[cat] = len(groups)
			groups = append(groups, group{name: cat})
		}
		mark := ""
		if r.Danger {
			mark = " ⚠"
		}
		line := fmt.Sprintf("- `%s`%s — %s", r.Name, mark, r.Summary)
		groups[index[cat]].lines = append(groups[index[cat]].lines, line)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%d role(s)**\n", len(list))
	for _, g := range groups {
		// A bold header with blank lines around it: emphasis (_x_) renders as an
		// underline in many clients, and a list needs a blank line before it to
		// render as bullets rather than one run-on paragraph.
		fmt.Fprintf(&b, "\n**%s**\n\n", humanCategory(g.name))
		b.WriteString(strings.Join(g.lines, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\nSwitch this conversation with `/role <name>`, or delegate to one with the delegate tool. " +
		"Roles marked ⚠ do security testing and require authorization.")
	return Result{Output: b.String()}, nil
}

// cmdRole shows or changes the active role for the session.
func cmdRole(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil || d.Agent.Roles() == nil {
		return Result{}, errors.New("roles are not available")
	}
	name := strings.ToLower(strings.TrimSpace(in.Args))

	if name == "" {
		current := ""
		if d.Store != nil && in.SessionID != "" {
			current, _ = d.Store.GetKV(ctx, activeRoleKey(in.SessionID))
		}
		if current == "" {
			return Result{Output: "This conversation runs as the general assistant. " +
				"Switch with `/role <name>` — see `/roles` for the list."}, nil
		}
		role, _ := d.Agent.Roles().Get(current)
		return Result{Output: fmt.Sprintf("This conversation runs as **%s**.\n\n%s\n\n"+
			"Return to the general assistant with `/role assistant`.", role.Title, role.Summary)}, nil
	}

	if name == "none" || name == "default" || name == "clear" {
		if d.Store != nil && in.SessionID != "" {
			_ = d.Store.DeleteKV(ctx, activeRoleKey(in.SessionID))
		}
		return Result{Output: "Back to the general assistant.", Action: Action{Kind: "role-changed", Value: ""}}, nil
	}

	role, ok := d.Agent.Roles().Get(name)
	if !ok {
		return Result{}, fmt.Errorf("no role named %q — see `/roles`", name)
	}
	if d.Store == nil || in.SessionID == "" {
		return Result{}, errors.New("start a conversation first — a role attaches to one")
	}
	if err := d.Store.SetKV(ctx, activeRoleKey(in.SessionID), role.Name); err != nil {
		return Result{}, err
	}

	out := fmt.Sprintf("Now running as **%s**.\n\n%s", role.Title, role.Summary)
	if role.Danger {
		out += "\n\n⚠ This role does security testing. Work only against targets you are authorized to test."
	}
	return Result{Output: out, Action: Action{Kind: "role-changed", Value: role.Name}}, nil
}

func humanCategory(c string) string {
	switch c {
	case "general":
		return "General"
	case "engineering":
		return "Engineering"
	case "research":
		return "Research"
	case "writing":
		return "Writing"
	case "security":
		return "Security (authorized testing)"
	default:
		words := strings.Fields(strings.ReplaceAll(c, "-", " "))
		for i, w := range words {
			if w != "" {
				words[i] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		return strings.Join(words, " ")
	}
}
