package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/engagement"
)

// addIntelTool records a discovered fact into the engagement's intel ledger.
// The methodology reads these to work out which phases are actually done.
type addIntelTool struct{}

func (addIntelTool) Name() string { return "add_intel" }
func (addIntelTool) Description() string {
	return "Record a fact discovered during an assessment — a host, subdomain, service, endpoint, technology, " +
		"credential, parameter, or note. These build the picture of the target and drive the methodology: a phase " +
		"is only complete once it has produced the evidence it should. Record as you go, not at the end."
}
func (addIntelTool) Schema() map[string]any {
	return schema(map[string]any{
		"type":   propEnum("What kind of fact this is.", "host", "subdomain", "service", "endpoint", "technology", "credential", "parameter", "vulnerability", "note"),
		"value":  prop("string", "The fact itself — the hostname, the URL, the technology name."),
		"detail": prop("string", "Optional context: a version, a port, how it was found."),
	}, "type", "value")
}

func (addIntelTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Type   string `json:"type"`
		Value  string `json:"value"`
		Detail string `json:"detail"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Intel == nil {
		return Errorf("the intel ledger is not available in this runtime")
	}
	rec, added, err := in.Deps.Intel.Add(in.SessionID, engagement.Intel{
		Type:   engagement.NormalizeType(args.Type),
		Value:  args.Value,
		Detail: args.Detail,
	})
	if err != nil {
		return Errorf("%v", err)
	}
	if !added {
		return Text(fmt.Sprintf("Already recorded as %s.", rec.ID))
	}
	return Text(fmt.Sprintf("Recorded %s (%s): %s.", rec.ID, rec.Type, rec.Value))
}

// methodologyStatusTool reports where the assessment stands.
type methodologyStatusTool struct{}

func (methodologyStatusTool) Name() string { return "methodology_status" }
func (methodologyStatusTool) Description() string {
	return "Show the assessment's progress: which phases have evidence, which are still open, and what to do next. " +
		"Call it to orient yourself before deciding what to test — it keeps you from writing the report while a " +
		"service sits untested."
}
func (methodologyStatusTool) Schema() map[string]any { return schema(map[string]any{}) }

func (methodologyStatusTool) Execute(_ context.Context, in Input) Result {
	if in.Deps == nil || in.Deps.Intel == nil {
		return Errorf("the intel ledger is not available in this runtime")
	}
	hasScope := false
	if in.Deps.Config != nil {
		hasScope = len(in.Deps.Config.Security.Scope) > 0
	}
	// Scope was de-gated; pass true so the engagement phase treats
	// scope-gathering as satisfied. Security.Scope (if set) is purely
	// informational — listed at the bottom of this output as "declared
	// targets" for the user's own bookkeeping.
	states, err := in.Deps.Intel.State(in.SessionID, true, false)
	if err != nil {
		return Errorf("%v", err)
	}

	var b strings.Builder
	b.WriteString("Assessment progress:\n\n")
	for _, st := range states {
		mark := map[engagement.PhaseStatus]string{
			engagement.Complete: "[x]", engagement.InProgress: "[~]",
			engagement.Blocked: "[!]", engagement.NotStarted: "[ ]",
		}[st.Status]
		fmt.Fprintf(&b, "%s %s — %s", mark, st.Title, st.Summary)
		if st.Evidence > 0 {
			fmt.Fprintf(&b, " (%d recorded)", st.Evidence)
		}
		b.WriteString("\n")
	}
	// Testing-area coverage, from intel and any recorded findings.
	evidence := []string{}
	if list, err := in.Deps.Intel.List(in.SessionID); err == nil {
		for _, it := range list {
			evidence = append(evidence, it.Value, it.Detail)
		}
	}
	if in.Deps.Findings != nil {
		if fs, err := in.Deps.Findings.List(in.SessionID); err == nil {
			for _, f := range fs {
				evidence = append(evidence, f.Title, f.CWE, f.Description)
			}
		}
	}
	cov := engagement.Coverage(evidence)
	fmt.Fprintf(&b, "\nTesting coverage (%d%%):\n", engagement.CoveragePercent(cov))
	for _, c := range cov {
		mark := "[ ]"
		if c.Covered {
			mark = "[x]"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, c.Area.Title)
	}

	_, directive := engagement.NextStep(states)
	fmt.Fprintf(&b, "\nNext: %s", directive)

	if hasScope && in.Deps.Config != nil {
		b.WriteString("\n\nDeclared targets: " + strings.Join(in.Deps.Config.Security.Scope, ", "))
	}
	return Text(b.String())
}
