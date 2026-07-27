package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// cmdFindings lists what the current engagement has recorded, and can remove
// or clear entries. The report is compiled separately with /report.
func cmdFindings(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil || d.Agent.Findings() == nil {
		return Result{}, errors.New("the findings ledger is not available")
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no engagement in this conversation yet")
	}
	store := d.Agent.Findings()

	verb, rest, _ := strings.Cut(strings.TrimSpace(in.Args), " ")
	verb = strings.ToLower(strings.TrimSpace(verb))
	rest = strings.TrimSpace(rest)

	switch verb {
	case "remove", "rm":
		if rest == "" {
			return Result{}, errors.New("usage: /findings remove <id>")
		}
		ok, err := store.Remove(in.SessionID, strings.ToUpper(rest))
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, fmt.Errorf("no finding %q in this engagement", rest)
		}
		return Result{Output: "Removed " + rest + "."}, nil
	case "clear":
		if err := store.Clear(in.SessionID); err != nil && !os.IsNotExist(err) {
			return Result{}, err
		}
		return Result{Output: "Cleared the findings for this engagement."}, nil
	}

	list, err := store.List(in.SessionID)
	if err != nil {
		return Result{}, err
	}
	if len(list) == 0 {
		return Result{Output: "No findings recorded yet. The security roles add them with the report_finding tool."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%d finding(s)**\n\n", len(list))
	for _, f := range list {
		target := ""
		if f.Target != "" {
			target = " · `" + f.Target + "`"
		}
		fmt.Fprintf(&b, "- `%s` **%s** — %s%s\n", f.ID, strings.ToUpper(string(f.Severity)), f.Title, target)
	}
	b.WriteString("\nCompile the report with `/report`.")
	return Result{Output: b.String()}, nil
}

// cmdReport writes the engagement's findings into a Markdown report file.
func cmdReport(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil || d.Agent.Findings() == nil {
		return Result{}, errors.New("the findings ledger is not available")
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no engagement in this conversation yet")
	}
	title := strings.TrimSpace(in.Args)
	body, err := d.Agent.Findings().Report(in.SessionID, title)
	if err != nil {
		return Result{}, err
	}

	dir := config.Path("reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	name := sanitise(title)
	if name == "" {
		name = "security-report"
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Output: fmt.Sprintf("Wrote the report to `%s`.\n\n%s", path, body)}, nil
}
