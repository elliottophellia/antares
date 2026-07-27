// Package engagement models a structured security assessment: the phases it
// moves through, the facts discovered along the way, and how much of the work
// is actually done.
//
// A penetration test is not one action but a sequence — map the surface,
// enumerate what is on it, test each thing, then report. Left to a single
// prompt, an agent skips ahead: it finds one bug and writes the report while
// three services sit untouched. The methodology is what keeps the work honest,
// by tracking which phases have the evidence to be called complete and naming
// what is left.
package engagement

// Phase is one stage of an assessment.
type Phase struct {
	// Name is the identifier used in status and directives.
	Name string
	// Title is the human label.
	Title string
	// Summary is one line: what happens in this phase.
	Summary string
	// Requires names phases that should complete first. A phase is not blocked
	// by an unmet prerequisite — the agent may work out of order — but the
	// status says so, which is usually enough to correct course.
	Requires []string
	// Produces are the intel types that, when present, mark this phase as
	// having done its job. Recon is done when there are hosts; enumeration when
	// there are endpoints; and so on.
	Produces []IntelType
	// Roles are the specialists suited to this phase.
	Roles []string
}

// Phases is the assessment model, in order. It is deliberately a handful of
// broad stages rather than a rigid checklist: the value is in "you have not
// enumerated the API you found", not in ticking forty boxes.
var Phases = []Phase{
	{
		Name:     "scope",
		Title:    "Scope & Authorization",
		Summary:  "Confirm what is in bounds and that testing is authorized.",
		Produces: []IntelType{IntelScope},
		Roles:    []string{"security-recon"},
	},
	{
		Name:     "recon",
		Title:    "Reconnaissance",
		Summary:  "Map the attack surface: hosts, subdomains, and exposed information.",
		Requires: []string{"scope"},
		Produces: []IntelType{IntelHost, IntelSubdomain},
		Roles:    []string{"security-recon"},
	},
	{
		Name:     "enumeration",
		Title:    "Enumeration",
		Summary:  "Identify services, endpoints, and the technology behind them.",
		Requires: []string{"recon"},
		Produces: []IntelType{IntelService, IntelEndpoint, IntelTechnology},
		Roles:    []string{"security-recon", "security-webapp"},
	},
	{
		Name:     "mapping",
		Title:    "Input & Parameter Mapping",
		Summary:  "Map the input surface: parameters, forms, and injection points to test.",
		Requires: []string{"enumeration"},
		Produces: []IntelType{IntelParameter},
		Roles:    []string{"security-webapp", "security-api"},
	},
	{
		Name:     "access",
		Title:    "Authentication & Access",
		Summary:  "Test authentication and access control; capture credentials and sessions in scope.",
		Requires: []string{"enumeration"},
		Produces: []IntelType{IntelCredential},
		Roles:    []string{"security-webapp", "security-api", "security-internal"},
	},
	{
		Name:     "testing",
		Title:    "Vulnerability Testing",
		Summary:  "Probe each surface across the testing areas, and confirm what is real.",
		Requires: []string{"mapping"},
		Produces: []IntelType{IntelVulnerability},
		Roles:    []string{"security-webapp", "security-api"},
	},
	{
		Name:     "reporting",
		Title:    "Reporting",
		Summary:  "Compile the confirmed findings into an actionable report.",
		Requires: []string{"testing"},
		Produces: []IntelType{IntelReport},
		Roles:    []string{"security-report"},
	},
}

// PhaseStatus is how far one phase has got.
type PhaseStatus string

const (
	NotStarted PhaseStatus = "not_started"
	InProgress PhaseStatus = "in_progress"
	Complete   PhaseStatus = "complete"
	// Blocked means the phase has produced something but a prerequisite has
	// not, so the work may be building on sand.
	Blocked PhaseStatus = "blocked"
)

// PhaseState is a phase paired with its computed status and the counts behind it.
type PhaseState struct {
	Phase    Phase       `json:"-"`
	Name     string      `json:"name"`
	Title    string      `json:"title"`
	Summary  string      `json:"summary"`
	Status   PhaseStatus `json:"status"`
	Evidence int         `json:"evidence"`
	Roles    []string    `json:"roles"`
	Requires []string    `json:"requires,omitempty"`
}

// phaseByName looks a phase up.
func phaseByName(name string) (Phase, bool) {
	for _, p := range Phases {
		if p.Name == name {
			return p, true
		}
	}
	return Phase{}, false
}
