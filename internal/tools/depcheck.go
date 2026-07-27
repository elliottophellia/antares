package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enowdev/antares/internal/depcheck"
)

// dependenciesTool reports whether the external programs an antares feature
// needs are installed, and — for missing ones — the command that would install
// them. It never installs anything; the agent must ask the user for permission
// and then run the install through the terminal (which itself is gated).
type dependenciesTool struct{}

func (dependenciesTool) Name() string { return "check_dependencies" }
func (dependenciesTool) Description() string {
	return "Check whether external tools a feature needs (e.g. Ghidra, a JDK, radare2, nmap, mitmproxy) are " +
		"installed on this host. Returns each dependency's status and, if missing, the suggested install command. " +
		"Call this BEFORE using a dependency-heavy feature. This tool never installs anything — if something is " +
		"missing, ask the user for permission, then install it via the terminal."
}
func (dependenciesTool) Schema() map[string]any {
	return schema(map[string]any{
		"names": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Dependency ids to check (e.g. [\"ghidra\",\"java\"]). Omit to check all known dependencies.",
		},
	})
}
func (dependenciesTool) RequiresApproval() bool { return false }

func (dependenciesTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Names []string `json:"names"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	names := args.Names
	if len(names) == 0 {
		for n := range depcheck.Registry {
			names = append(names, n)
		}
		sort.Strings(names)
	}

	var b strings.Builder
	b.WriteString("Dependency status:\n\n")
	missing := 0
	for _, n := range names {
		st, known := depcheck.CheckByName(n)
		if !known {
			fmt.Fprintf(&b, "- %s: UNKNOWN dependency\n", n)
			continue
		}
		if st.Installed {
			fmt.Fprintf(&b, "- %s: installed (%s) — %s\n", st.Name, st.Found, st.Purpose)
		} else {
			missing++
			fmt.Fprintf(&b, "- %s: MISSING — %s\n    install: %s\n", st.Name, st.Purpose, st.InstallHint)
		}
	}
	if missing > 0 {
		b.WriteString("\nSome dependencies are missing. Do NOT install them silently — tell the user what is " +
			"needed and why, ask for permission, and only then run the install command via the terminal.\n")
	}
	return Text(b.String())
}
