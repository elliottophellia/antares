package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/findings"
)

// reportFindingTool records a security finding into the engagement ledger, so
// the report role can compile it later. A finding discovered and not written
// down is a finding lost.
type reportFindingTool struct{}

func (reportFindingTool) Name() string { return "report_finding" }

func (reportFindingTool) Description() string {
	return "Record a confirmed security finding into this engagement's ledger. " +
		"Report only findings you have confirmed, not suspicions. Include the exact steps to reproduce it, " +
		"its impact, and a concrete remediation. The report role compiles these into the final report."
}

func (reportFindingTool) Schema() map[string]any {
	return schema(map[string]any{
		"title":         prop("string", "A short, specific title for the finding."),
		"severity":      propEnum("How serious it is.", "critical", "high", "medium", "low", "info"),
		"target":        prop("string", "The affected host, URL, or endpoint."),
		"endpoint":      prop("string", "The specific URL, parameter, or API route, if finer than target."),
		"description":   prop("string", "What the issue is."),
		"reproduce":     prop("string", "Exact steps to reproduce it."),
		"poc":           prop("string", "A proof of concept: the request, payload, or snippet that shows it."),
		"attack_vector": prop("string", "How it is reached, e.g. network, authenticated user."),
		"impact":        prop("string", "What an attacker gains from it."),
		"remediation":   prop("string", "A concrete fix the team can implement."),
		"cwe":           prop("string", "Optional classification, e.g. CWE-89."),
	}, "title", "severity", "description")
}

func (reportFindingTool) RequiresApproval() bool { return true }

func (reportFindingTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Title        string `json:"title"`
		Severity     string `json:"severity"`
		Target       string `json:"target"`
		Endpoint     string `json:"endpoint"`
		Description  string `json:"description"`
		Reproduce    string `json:"reproduce"`
		PoC          string `json:"poc"`
		AttackVector string `json:"attack_vector"`
		Impact       string `json:"impact"`
		Remediation  string `json:"remediation"`
		CWE          string `json:"cwe"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Findings == nil {
		return Errorf("the findings ledger is not available in this runtime")
	}
	if strings.TrimSpace(args.Title) == "" {
		return Errorf("title is required")
	}

	f, err := in.Deps.Findings.Add(in.SessionID, findings.Finding{
		Title:        args.Title,
		Severity:     findings.NormalizeSeverity(args.Severity),
		Target:       args.Target,
		Endpoint:     args.Endpoint,
		Description:  args.Description,
		Reproduce:    args.Reproduce,
		PoC:          args.PoC,
		AttackVector: args.AttackVector,
		Impact:       args.Impact,
		Remediation:  args.Remediation,
		CWE:          args.CWE,
		CreatedAt:    time.Now(),
	})
	if err != nil {
		return Errorf("%v", err)
	}
	if f.Status == findings.StatusDuplicate {
		return Text(fmt.Sprintf("Recorded %s: %s (%s) — flagged as a duplicate of %s. It will be listed but not written up twice; triage it if that is wrong.",
			f.ID, f.Title, f.Severity, f.DuplicateOf))
	}
	return Text(fmt.Sprintf("Recorded %s: %s (%s). Compile the report when the engagement is done.",
		f.ID, f.Title, f.Severity))
}

// triageFindingTool sets a finding's triage status.
type triageFindingTool struct{}

func (triageFindingTool) Name() string { return "triage_finding" }
func (triageFindingTool) Description() string {
	return "Set a recorded finding's triage status: confirmed (validated), duplicate (repeats another — give its id), " +
		"wontfix (out of scope or accepted), or new. Duplicates and won't-fixes are kept but left out of the main report."
}
func (triageFindingTool) Schema() map[string]any {
	return schema(map[string]any{
		"id":           prop("string", "The finding id, e.g. F-002."),
		"status":       propEnum("The triage status.", "confirmed", "duplicate", "wontfix", "new"),
		"duplicate_of": prop("string", "For duplicate: the id of the finding this repeats."),
	}, "id", "status")
}
func (triageFindingTool) RequiresApproval() bool { return false }

func (triageFindingTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		DuplicateOf string `json:"duplicate_of"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Findings == nil {
		return Errorf("the findings ledger is not available in this runtime")
	}
	if strings.TrimSpace(args.ID) == "" {
		return Errorf("id is required")
	}
	status := findings.NormalizeStatus(args.Status)
	f, ok, err := in.Deps.Findings.Triage(in.SessionID, args.ID, status, args.DuplicateOf)
	if err != nil {
		return Errorf("%v", err)
	}
	if !ok {
		return Errorf("no finding %q — record it first with report_finding", args.ID)
	}
	return Text(fmt.Sprintf("%s is now %s.", f.ID, f.Status))
}
