package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/engagement"
)

// cmdEngagement shows the assessment's progress: the phases, their evidence,
// and what to do next. It also lists the intel recorded so far.
func cmdEngagement(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil || d.Agent.Intel() == nil {
		return Result{}, errors.New("the engagement ledger is not available")
	}
	if in.SessionID == "" {
		return Result{}, errors.New("there is no engagement in this conversation yet")
	}
	store := d.Agent.Intel()

	verb := strings.ToLower(strings.TrimSpace(in.Args))
	if verb == "clear" {
		if err := store.Clear(in.SessionID); err != nil {
			return Result{}, err
		}
		return Result{Output: "Cleared the engagement's intel."}, nil
	}
	if verb == "intel" {
		list, err := store.List(in.SessionID)
		if err != nil {
			return Result{}, err
		}
		if len(list) == 0 {
			return Result{Output: "No intel recorded yet."}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**%d fact(s)**\n\n", len(list))
		for _, i := range list {
			detail := ""
			if i.Detail != "" {
				detail = " — " + i.Detail
			}
			fmt.Fprintf(&b, "- `%s` [%s] %s%s\n", i.ID, i.Type, i.Value, detail)
		}
		return Result{Output: b.String()}, nil
	}

	// Scope was de-gated; treat the scope-gathering phase as always satisfied.
	states, err := store.State(in.SessionID, true, false)
	if err != nil {
		return Result{}, err
	}
	var b strings.Builder
	b.WriteString("**Engagement progress**\n\n")
	for _, st := range states {
		mark := map[engagement.PhaseStatus]string{
			engagement.Complete: "✅", engagement.InProgress: "🔄",
			engagement.Blocked: "⚠️", engagement.NotStarted: "⬜",
		}[st.Status]
		fmt.Fprintf(&b, "%s **%s** — %s", mark, st.Title, st.Summary)
		if st.Evidence > 0 {
			fmt.Fprintf(&b, " _(%d recorded)_", st.Evidence)
		}
		b.WriteString("\n")
	}
	_, directive := engagement.NextStep(states)
	fmt.Fprintf(&b, "\n%s\n", directive)
	b.WriteString("\n`/engagement intel` lists the facts recorded.")
	return Result{Output: b.String()}, nil
}
