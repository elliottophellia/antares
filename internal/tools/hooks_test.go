package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/hookpack"
)

// hookToolInstances returns one of each hook tool, with the same constructor
// the registry uses. Keeping the list in one place keeps the tests honest
// when a tool is added.
func hookToolInstances() []hookTool {
	return []hookTool{
		newAttackScriptTool(), newAwshookTool(), newAzurehookTool(),
		newKubehookTool(), newWinhookTool(), newMachookTool(),
		newCipipeTool(), newEbpfTool(),
	}
}

func TestHookToolsRegisteredAndNamed(t *testing.T) {
	reg := Default()
	want := []string{
		"attack_script", "awshook", "azurehook", "kubehook",
		"winhook", "machook", "cipipe", "ebpf",
	}
	for _, name := range want {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("tool %q is not in the default registry", name)
		}
	}
}

func TestHookToolsRequireApproval(t *testing.T) {
	for _, h := range hookToolInstances() {
		if !h.RequiresApproval() {
			t.Errorf("%s: hook tools must require approval — they execute code on targets", h.Name())
		}
	}
}

func TestHookToolSchemaShape(t *testing.T) {
	for _, h := range hookToolInstances() {
		s := h.Schema()
		props, ok := s["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s: schema has no properties map", h.Name())
			continue
		}
		prog, ok := props["program"].(map[string]any)
		if !ok {
			t.Errorf("%s: schema has no program property", h.Name())
			continue
		}
		if _, ok := prog["enum"].([]string); !ok {
			t.Errorf("%s: program.enum is not a []string", h.Name())
		}
		required, _ := s["required"].([]string)
		foundProgram, foundArgs := false, false
		for _, r := range required {
			if r == "program" {
				foundProgram = true
			}
			if r == "args" {
				foundArgs = true
			}
		}
		if !foundProgram || !foundArgs {
			t.Errorf("%s: schema must require program and args, got %v", h.Name(), required)
		}
	}
}

func TestHookToolSchemaEnumMatchesCatalog(t *testing.T) {
	for _, h := range hookToolInstances() {
		s := h.Schema()
		props, _ := s["properties"].(map[string]any)
		prog, _ := props["program"].(map[string]any)
		got, _ := prog["enum"].([]string)
		want := hookpack.ProgramNames(h.category)
		if len(got) != len(want) {
			t.Errorf("%s: enum length %d does not match catalog length %d", h.Name(), len(got), len(want))
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: enum[%d] = %q, want %q", h.Name(), i, got[i], want[i])
			}
		}
	}
}

func TestHookToolDescriptionMentionsProgramsAndCleanup(t *testing.T) {
	for _, h := range hookToolInstances() {
		desc := h.Description()
		// Every description must name at least the first program so a model
		// that only reads the description can discover it.
		first := hookpack.Catalog[h.category][0].Name
		if !strings.Contains(desc, first) {
			t.Errorf("%s: description does not mention its first program %q", h.Name(), first)
		}
		// Categories that ship a cleanup_* program must say so.
		if hasCleanup(hookpack.Catalog[h.category]) {
			if !strings.Contains(desc, "cleanup") {
				t.Errorf("%s: description should mention cleanup_* programs", h.Name())
			}
		}
	}
}

func TestHookToolExecutionUnknownProgramReturnsCatalogue(t *testing.T) {
	for _, h := range hookToolInstances() {
		// Skip the platform gate — we test that separately below. For
		// platform-restricted tools we can still hit "unknown program" if
		// the platform happens to match (e.g. ebpf on Linux).
		if h.platform != "" && runtime.GOOS != h.platform {
			continue
		}
		res := h.Execute(nil, Input{Args: []byte(`{"program":"__does_not_exist__","args":[]}`)})
		if !res.IsError {
			t.Errorf("%s: unknown program must surface as error", h.Name())
		}
		if !strings.Contains(res.Content, "Available programs") {
			t.Errorf("%s: unknown-program output should list available programs, got: %s", h.Name(), res.Content)
		}
		if meta, _ := res.Meta["program"]; meta != nil {
			// notFound leaves Meta nil — that's fine; the contract is only
			// that the catalogue is in Content.
		}
	}
}

func TestHookToolMissingArgsErrorsCleanly(t *testing.T) {
	for _, h := range hookToolInstances() {
		// No Args at all: Bind leaves the zero value, args.Program is "",
		// we hit the "program is required" path before the platform gate.
		res := h.Execute(nil, Input{Args: []byte(`{}`)})
		if !res.IsError {
			t.Errorf("%s: empty args must error", h.Name())
		}
		if !strings.Contains(res.Content, "program is required") {
			t.Errorf("%s: expected 'program is required', got: %s", h.Name(), res.Content)
		}
	}
}

func TestHookToolPlatformGate(t *testing.T) {
	cases := []struct {
		tool     hookTool
		expected string // runtime.GOOS value where it should be allowed
	}{
		{newWinhookTool(), "windows"},
		{newMachookTool(), "darwin"},
		{newEbpfTool(), "linux"},
	}
	noopEmit := func(Progress) {}
	for _, c := range cases {
		// Use a known-good program name so we hit the platform gate, not
		// the catalogue fallback.
		progName := hookpack.ProgramNames(c.tool.category)[0]
		args := `{"program":"` + progName + `","args":[]}`
		res := c.tool.Execute(context.Background(), Input{Args: []byte(args), Emit: noopEmit})
		if runtime.GOOS == c.expected {
			// On the right platform we should pass the platform gate; the
			// subsequent failure (no python3, no credentials, etc.) is
			// environment-specific and we don't assert on it.
			if res.IsError && strings.Contains(res.Content, "requires "+c.expected) {
				t.Errorf("%s: should not be platform-gated on %s, but got: %s",
					c.tool.Name(), c.expected, res.Content)
			}
		} else {
			if !res.IsError {
				t.Errorf("%s: should error on %s (expected %s)", c.tool.Name(), runtime.GOOS, c.expected)
			}
			if !strings.Contains(res.Content, "requires "+c.expected) {
				t.Errorf("%s: error should name required platform %s, got: %s",
					c.tool.Name(), c.expected, res.Content)
			}
			// Platform-gated result should suggest an alternative tool.
			if !strings.Contains(res.Content, "ebpf") || !strings.Contains(res.Content, "winhook") || !strings.Contains(res.Content, "machook") {
				t.Errorf("%s: error should suggest alternatives, got: %s", c.tool.Name(), res.Content)
			}
		}
	}
}

func TestHookToolNotFoundListsAllPrograms(t *testing.T) {
	h := newAwshookTool()
	out := h.notFound("definitely_missing")
	for _, p := range hookpack.Catalog[h.category] {
		if !strings.Contains(out, p.Name) {
			t.Errorf("notFound output missing program %q in %s", p.Name, out)
		}
		if !strings.Contains(out, p.Args) {
			t.Errorf("notFound output missing args hint %q for %s", p.Args, p.Name)
		}
	}
}

func TestHookToolDiagnoseHelpers(t *testing.T) {
	cases := []struct {
		tool    hookTool
		stderr  string
		code    int
		want    string
	}{
		{newAwshookTool(), "ModuleNotFoundError: No module named 'boto3'", 1, "boto3"},
		{newAwshookTool(), "Unable to locate credentials", 1, "AWS credentials"},
		{newAzurehookTool(), "ModuleNotFoundError: No module named 'azure.identity'", 1, "Azure SDK"},
		{newAzurehookTool(), "EnvironmentCredential authentication unavailable", 1, "Azure credentials"},
		{newKubehookTool(), "ModuleNotFoundError: No module named 'kubernetes'", 1, "kubernetes client"},
		{newKubehookTool(), "ConfigException: Invalid kube-config", 1, "Kubernetes credentials"},
		{newCipipeTool(), "ModuleNotFoundError: No module named 'requests'", 1, "requests"},
		{newCipipeTool(), "401 Unauthorized", 1, "Authentication failed"},
		{newWinhookTool(), "Access is denied", 1, "Administrator"},
		{newMachookTool(), "Operation not permitted; must be root", 1, "root"},
		{newEbpfTool(), "stderr: must be root to use bpf", 1, "root"},
		// Unrelated stderr should produce no hint.
		{newAwshookTool(), "some unrelated error", 1, ""},
	}
	for _, c := range cases {
		got := c.tool.diagnose(c.stderr, c.code)
		if c.want == "" {
			if got != "" {
				t.Errorf("%s diagnose(%q): expected no hint, got %q", c.tool.Name(), c.stderr, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s diagnose(%q): expected hint containing %q, got %q",
				c.tool.Name(), c.stderr, c.want, got)
		}
	}
}

func TestHookToolDefaultTimeouts(t *testing.T) {
	cases := []struct {
		tool hookTool
		want int // seconds
	}{
		{newAttackScriptTool(), 120},
		{newAwshookTool(), 300},
		{newAzurehookTool(), 300},
		{newKubehookTool(), 300},
		{newWinhookTool(), 120},
		{newMachookTool(), 120},
		{newCipipeTool(), 300},
		{newEbpfTool(), 120},
	}
	for _, c := range cases {
		got := int(c.tool.timeout.Seconds())
		if got != c.want {
			t.Errorf("%s: default timeout %ds, want %ds", c.tool.Name(), got, c.want)
		}
	}
}
