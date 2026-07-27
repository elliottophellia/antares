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
		"title":       prop("string", "A short, specific title for the finding."),
		"severity":    propEnum("How serious it is.", "critical", "high", "medium", "low", "info"),
		"target":      prop("string", "The affected host, URL, or endpoint."),
		"description": prop("string", "What the issue is."),
		"reproduce":   prop("string", "Exact steps to reproduce it."),
		"impact":      prop("string", "What an attacker gains from it."),
		"remediation": prop("string", "A concrete fix the team can implement."),
		"cwe":         prop("string", "Optional classification, e.g. CWE-89."),
	}, "title", "severity", "description")
}

func (reportFindingTool) RequiresApproval() bool { return true }

func (reportFindingTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Title       string `json:"title"`
		Severity    string `json:"severity"`
		Target      string `json:"target"`
		Description string `json:"description"`
		Reproduce   string `json:"reproduce"`
		Impact      string `json:"impact"`
		Remediation string `json:"remediation"`
		CWE         string `json:"cwe"`
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
		Title:       args.Title,
		Severity:    findings.NormalizeSeverity(args.Severity),
		Target:      args.Target,
		Description: args.Description,
		Reproduce:   args.Reproduce,
		Impact:      args.Impact,
		Remediation: args.Remediation,
		CWE:         args.CWE,
		CreatedAt:   time.Now(),
	})
	if err != nil {
		return Errorf("%v", err)
	}
	return Text(fmt.Sprintf("Recorded %s: %s (%s). Compile the report when the engagement is done.",
		f.ID, f.Title, f.Severity))
}
