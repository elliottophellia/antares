package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/hub"
)

// cmdGoal sets, inspects, or clears the standing goal for a session. A goal
// outlives one turn: when the agent thinks it is finished, a judge decides
// whether the goal is actually met and, if not, what to do next.
func cmdGoal(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	if in.SessionID == "" {
		return Result{}, errors.New("start a conversation first — a goal attaches to one")
	}

	verb, rest, _ := strings.Cut(in.Args, " ")
	verb = strings.ToLower(strings.TrimSpace(verb))
	rest = strings.TrimSpace(rest)

	switch verb {
	case "", "status":
		g, ok := d.Agent.GetGoal(ctx, in.SessionID)
		if !ok {
			return Result{Output: "No standing goal. Set one with `/goal <what you want done>`."}, nil
		}
		state := "running"
		switch {
		case g.Done:
			state = "met"
		case g.Paused:
			state = "paused"
		}
		out := fmt.Sprintf("**Goal** (%s, %d iteration(s))\n\n%s", state, g.Iterations, g.Text)
		if g.Note != "" {
			out += "\n\n_" + g.Note + "_"
		}
		return Result{Output: out}, nil

	case "clear", "stop", "off":
		if err := d.Agent.SetGoal(ctx, in.SessionID, nil); err != nil {
			return Result{}, err
		}
		return Result{Output: "Goal cleared."}, nil

	case "pause":
		g, ok := d.Agent.GetGoal(ctx, in.SessionID)
		if !ok {
			return Result{}, errors.New("there is no goal to pause")
		}
		g.Paused = true
		if err := d.Agent.SetGoal(ctx, in.SessionID, g); err != nil {
			return Result{}, err
		}
		return Result{Output: "Goal paused. Resume it with `/goal resume`."}, nil

	case "resume":
		g, ok := d.Agent.GetGoal(ctx, in.SessionID)
		if !ok {
			return Result{}, errors.New("there is no goal to resume")
		}
		g.Paused, g.Done, g.Note = false, false, ""
		if err := d.Agent.SetGoal(ctx, in.SessionID, g); err != nil {
			return Result{}, err
		}
		return Result{Output: "Goal resumed."}, nil
	}

	// Anything else is the goal itself.
	text := strings.TrimSpace(in.Args)
	if text == "" {
		return Result{}, errors.New("usage: /goal <what you want done>")
	}
	g := &agent.Goal{Text: text}
	if err := d.Agent.SetGoal(ctx, in.SessionID, g); err != nil {
		return Result{}, err
	}
	return Result{Output: "Goal set. I will keep working on it across turns until it is met, " +
		"or you run `/goal clear`.\n\n" + text}, nil
}

// cmdSteer redirects a run that is already in flight. The note is delivered
// after the current batch of tools rather than immediately, so nothing already
// underway is thrown away.
func cmdSteer(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	note := strings.TrimSpace(in.Args)
	if note == "" {
		return Result{}, errors.New("usage: /steer <what to do instead>")
	}
	if in.SessionID == "" || !d.Agent.Steer(in.SessionID, note) {
		return Result{}, errors.New("nothing is running — send it as an ordinary message instead")
	}
	return Result{Output: "Passed along. It lands after the current step."}, nil
}

// cmdLearn turns what happened in this session into a reusable skill.
func cmdLearn(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no session to learn from yet")
	}

	body, err := d.Agent.Distil(ctx, in.SessionID, strings.TrimSpace(in.Args))
	if err != nil {
		return Result{}, err
	}
	if body == "" {
		return Result{Output: "Nothing general enough to keep — this session was specific to the moment."}, nil
	}

	name := skillNameFrom(body)
	dir := skillDir(d)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, err
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		return Result{}, err
	}
	if d.Skills != nil {
		_ = d.Skills.Reload()
	}
	return Result{
		Output: fmt.Sprintf("Learned **%s** and saved it to `%s`.\n\nIt will be offered the next time "+
			"something similar comes up. Edit or remove it from the Skills page.", name, path),
		Action: Action{Kind: "skills-changed"},
	}, nil
}

// skillNameFrom reads the name out of generated front matter, falling back to
// something safe when the model omitted it.
func skillNameFrom(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "name:") {
			name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `"'`)
			if name != "" {
				return hub.SafeFileName(name)
			}
		}
		if strings.HasPrefix(line, "# ") {
			break
		}
	}
	return "learned-skill"
}

// cmdRollback shows what a session changed on disk, and puts it back.
func cmdRollback(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no session to roll back")
	}

	cp, err := d.Agent.Checkpoint(in.SessionID)
	if err != nil {
		return Result{}, err
	}
	if len(cp.Entries) == 0 {
		return Result{Output: "No files have been changed in this session."}, nil
	}

	// Bare /rollback shows what would be undone. Undoing work without being
	// asked twice is not a thing to do by accident.
	if strings.TrimSpace(in.Args) == "" {
		seen := map[string]bool{}
		var b strings.Builder
		b.WriteString("**Changed in this session**\n\n")
		for _, e := range cp.Entries {
			if seen[e.Path] {
				continue
			}
			seen[e.Path] = true
			state := "modified"
			if !e.Existed {
				state = "created"
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", e.Path, state)
		}
		b.WriteString("\nUndo all of it with `/rollback all`, or one file with `/rollback <path>`.\n" +
			"A created file is deleted; a modified one goes back to how it was before this session.")
		return Result{Output: b.String()}, nil
	}

	var paths []string
	if arg := strings.TrimSpace(in.Args); arg != "all" {
		paths = strings.Fields(arg)
	}

	res, err := d.Agent.Rollback(in.SessionID, paths)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	if len(res.Restored) > 0 {
		fmt.Fprintf(&b, "**Restored %d file(s)**\n\n", len(res.Restored))
		for _, p := range res.Restored {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	if len(res.Deleted) > 0 {
		fmt.Fprintf(&b, "\n**Deleted %d file(s) that did not exist before**\n\n", len(res.Deleted))
		for _, p := range res.Deleted {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	if len(res.Failed) > 0 {
		b.WriteString("\n**Could not restore**\n\n")
		for p, why := range res.Failed {
			fmt.Fprintf(&b, "- `%s` — %s\n", p, why)
		}
	}
	return Result{Output: strings.TrimSpace(b.String())}, nil
}

// cmdPanel asks several models the same question and returns one answer.
func cmdPanel(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	question := strings.TrimSpace(in.Args)
	if question == "" {
		models := d.config().Model.Panel
		if len(models) == 0 {
			return Result{Output: "No panel is configured. Set `model.panel` to two or more model ids, " +
				"then ask with `/panel <question>`.\n\nThe point is the disagreement: where independent " +
				"answers diverge is usually where the question was ambiguous or the problem is genuinely hard."}, nil
		}
		return Result{Output: fmt.Sprintf("The panel is %s.\n\nAsk it something with `/panel <question>`.",
			"`"+strings.Join(models, "`, `")+"`")}, nil
	}

	answer, all, err := d.Agent.Panel(ctx, question, nil, nil)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	b.WriteString(answer)
	// Name who answered and who did not, so a quiet failure is not mistaken
	// for a smaller panel.
	var failed []string
	for _, a := range all {
		if a.Err != "" {
			failed = append(failed, a.Model)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(&b, "\n\n_%s did not answer._", strings.Join(failed, ", "))
	}
	return Result{Output: b.String()}, nil
}
