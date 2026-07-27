package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/hub"
)

// skillDir is where an installed skill is written.
func skillDir(d Deps) string {
	if dirs := d.config().Skills.Dirs; len(dirs) > 0 && strings.TrimSpace(dirs[0]) != "" {
		return config.Expand(dirs[0])
	}
	return config.Path("skills")
}

func hubSkillSearch(ctx context.Context, d Deps, query string) (Result, error) {
	found, err := hub.SearchSkills(ctx, query)
	if err != nil {
		return Result{}, err
	}
	if len(found) == 0 {
		return Result{Output: "Nothing matched. Try `/skills search` with no words to see everything, " +
			"or pass a GitHub repository like `owner/repo`."}, nil
	}

	installed := map[string]bool{}
	if d.Skills != nil {
		for _, s := range d.Skills.List() {
			installed[s.Name] = true
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%d skill(s) available**\n\n", len(found))
	for i, e := range found {
		if i >= 25 {
			fmt.Fprintf(&b, "- … and %d more\n", len(found)-25)
			break
		}
		mark := ""
		if installed[e.Name] {
			mark = " _(installed)_"
		}
		fmt.Fprintf(&b, "- `%s`%s — %s\n", e.ID, mark, orDash(e.Summary))
	}
	b.WriteString("\nInstall one with `/skills install <id>`.")
	return Result{Output: b.String()}, nil
}

func hubSkillInstall(ctx context.Context, d Deps, id string) (Result, error) {
	if id == "" {
		return Result{}, errors.New("usage: /skills install <id> — an id from `/skills search`, a GitHub repo, or a URL")
	}
	entry, path, err := hub.InstallSkill(ctx, id, skillDir(d))
	if err != nil {
		return Result{}, err
	}
	if d.Skills != nil {
		_ = d.Skills.Reload()
	}
	out := fmt.Sprintf("Installed **%s** to `%s`.", entry.Name, path)
	if entry.Summary != "" {
		out += "\n\n" + entry.Summary
	}
	return Result{Output: out, Action: Action{Kind: "skills-changed"}}, nil
}

func hubMCPSearch(ctx context.Context, d Deps, query string) (Result, error) {
	found := hub.SearchMCP(ctx, query, d.config())
	if len(found) == 0 {
		return Result{Output: "No server in the catalogue matches that."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%d MCP server(s)**\n\n", len(found))
	for _, e := range found {
		mark := ""
		if e.Installed {
			mark = " _(configured)_"
		}
		fmt.Fprintf(&b, "- `%s`%s — %s\n", e.ID, mark, e.Summary)
	}
	b.WriteString("\nAdd one with `/mcp install <id>`.")
	return Result{Output: b.String()}, nil
}

func hubMCPInstall(ctx context.Context, d Deps, id string) (Result, error) {
	if id == "" {
		return Result{}, errors.New("usage: /mcp install <id> — an id from `/mcp search`")
	}
	cfg, err := config.Reload()
	if err != nil {
		return Result{}, err
	}
	missing, err := hub.InstallMCP(id, cfg, nil)
	if err != nil {
		return Result{}, err
	}
	cfg.MCP.Enabled = true
	if err := config.Save(cfg); err != nil {
		return Result{}, err
	}
	if err := d.reload(); err != nil {
		return Result{}, err
	}
	if d.MCP != nil {
		go d.MCP.Connect(context.Background(), config.Get())
	}

	entry, _ := hub.LookupMCP(id)
	out := fmt.Sprintf("Added **%s**.", entry.Name)
	if entry.Setup != "" {
		out += "\n\n" + entry.Setup
	}
	if len(missing) > 0 {
		out += fmt.Sprintf("\n\nIt still needs %s — set them with `/config` or in the environment, then restart.",
			"`"+strings.Join(missing, "`, `")+"`")
	}
	return Result{Output: out, Action: Action{Kind: "config-changed"}}, nil
}
