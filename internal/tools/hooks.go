package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/hookpack"
)

// hookTool is one of the eight CyberStrike offensive-script wrappers
// (attack_script, awshook, azurehook, kubehook, winhook, machook, cipipe,
// ebpf). Each one runs a bundled Python or PowerShell program with the
// arguments the model chose, captures stdout/stderr, applies a per-category
// timeout, and returns the structured result. Programs that mutate a target
// ship with a cleanup_* companion; the descriptions tell the model to run it
// before leaving. Every one of these mutates state on the target, so all of
// them require approval.
//
// The scripts themselves live in internal/hookpack and are unpacked to
// ~/.antares/hooks/<category>/<name><ext> on first use, so the binary stays
// a single file but the model can read and modify the extracted script if
// the engagement needs a tweak.
type hookTool struct {
	name     string          // tool name, e.g. "awshook"
	category hookpack.Category
	platform string          // "", "linux", "darwin", "windows" — gate if set
	timeout  time.Duration   // default per-invocation cap
	// diagnose inspects stderr/exit-code and appends a remediation hint for
	// the most common failure modes (missing SDK, missing credentials, etc.).
	// It returns "" when it has nothing to add.
	diagnose func(stderr string, exitCode int) string
}

func (h hookTool) Name() string { return h.name }

func (h hookTool) RequiresApproval() bool { return true }

func (h hookTool) Description() string {
	progs := hookpack.Catalog[h.category]
	names := make([]string, 0, len(progs))
	for _, p := range progs {
		names = append(names, p.Name)
	}
	cleanup := ""
	if hasCleanup(progs) {
		cleanup = " ALWAYS run cleanup_* before leaving a target."
	}
	platformNote := ""
	switch h.platform {
	case "windows":
		platformNote = " Windows only — use 'ebpf' on Linux or 'machook' on macOS."
	case "darwin":
		platformNote = " macOS only — use 'ebpf' on Linux or 'winhook' on Windows."
	case "linux":
		platformNote = " Linux only."
	}
	return fmt.Sprintf(
		"Execute a bundled post-exploitation or vulnerability-testing program. "+
			"Available programs: %s.%s%s "+
			"Each program is a short Python (or PowerShell on Windows) script that prints structured results; "+
			"pass --json-output for machine-readable output. Programs run with the credentials and "+
			"privileges of this Antares process, so AWS_* / AZURE_* / KUBECONFIG environment variables "+
			"and the local keychain all work without further configuration.",
		strings.Join(names, ", "), cleanup, platformNote,
	)
}

// hasCleanup reports whether a category ships a cleanup_* program. The
// description leans on this to nudge the model toward the cleanup step.
func hasCleanup(progs []hookpack.Program) bool {
	for _, p := range progs {
		if strings.HasPrefix(p.Name, "cleanup") {
			return true
		}
	}
	return false
}

func (h hookTool) Schema() map[string]any {
	progs := hookpack.Catalog[h.category]
	var enum, hint strings.Builder
	enum.WriteString("Program to execute. Options: ")
	for i, p := range progs {
		if i > 0 {
			enum.WriteString("; ")
		}
		fmt.Fprintf(&enum, "%s — %s", p.Name, p.Description)
		if i == 0 {
			hint.WriteString(p.Args)
		}
	}
	timeoutDefault := int(h.timeout / time.Second)
	return schema(map[string]any{
		"program": map[string]any{
			"type":        "string",
			"description": enum.String(),
			"enum":        hookpack.ProgramNames(h.category),
		},
		"args": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Arguments to pass to the program, in argv form. Each script accepts --json-output for machine-readable output.",
		},
		"timeout_seconds": propDefault("integer",
			fmt.Sprintf("Maximum execution time in seconds. Default: %d.", timeoutDefault),
			timeoutDefault),
	}, "program", "args")
}

func (h hookTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Program         string   `json:"program"`
		Args            []string `json:"args"`
		TimeoutSeconds  int      `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	args.Program = strings.TrimSpace(args.Program)
	if args.Program == "" {
		return Errorf("program is required")
	}

	// Platform gate before we touch the filesystem, so the model gets a
	// clear "use the other tool" message instead of a confusing runtime error.
	if h.platform != "" && runtime.GOOS != h.platform {
		alternative := "ebpf (Linux), winhook (Windows), or machook (macOS)"
		return Errorf("%s requires %s. Current platform: %s. Use %s instead.",
			h.name, h.platform, runtime.GOOS, alternative)
	}

	if _, ok := hookpack.FindProgram(h.category, args.Program); !ok {
		return Result{Content: h.notFound(args.Program), IsError: true}
	}

	// Find the script on disk, extracting the embedded copy if needed. The
	// Windows category prefers .ps1 and falls back to .py; every other one
	// is Python only.
	scriptPath, interpreter, err := h.resolveScript(args.Program)
	if err != nil {
		return Errorf("%v", err)
	}

	timeout := h.timeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}

	in.Emit(Progress{Tool: h.name, Message: fmt.Sprintf("%s %s", h.name, args.Program)})

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, interpreter, append([]string{scriptPath}, args.Args...)...)
	// Inherit environment so AWS_*, AZURE_*, KUBECONFIG, GITHUB_TOKEN, etc.
	// are available to the script. PYTHONDONTWRITEBYTECODE keeps the binary
	// from dropping __pycache__ directories into the hooks tree.
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	var output bytes.Buffer
	output.WriteString(stdoutStr)
	if strings.TrimSpace(stderrStr) != "" {
		fmt.Fprintf(&output, "\n--- stderr ---\n%s", stderrStr)
	}

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		// Context-deadline errors come out as the sentinel "signal: killed";
		// either way, a cancelled run reports the timeout cleanly.
		if runCtx.Err() == context.DeadlineExceeded {
			return Result{
				Content: fmt.Sprintf("Timed out after %s. Partial output:\n%s", timeout, output.String()),
				IsError: true,
				Meta:    map[string]any{"program": args.Program, "exit_code": exitCode, "timed_out": true},
			}
		}
		fmt.Fprintf(&output, "\nExit code: %d", exitCode)
		if h.diagnose != nil {
			if hint := h.diagnose(stderrStr, exitCode); hint != "" {
				fmt.Fprintf(&output, "\n%s", hint)
			}
		}
	} else if exitCode != 0 {
		fmt.Fprintf(&output, "\nExit code: %d", exitCode)
		if h.diagnose != nil {
			if hint := h.diagnose(stderrStr, exitCode); hint != "" {
				fmt.Fprintf(&output, "\n%s", hint)
			}
		}
	}

	maxOutput := 60000
	if in.Deps != nil && in.Deps.Config != nil && in.Deps.Config.Tools.MaxOutputChars > 0 {
		maxOutput = in.Deps.Config.Tools.MaxOutputChars
	}
	body := truncateTool(output.String(), maxOutput)

	return Result{
		Content: body,
		Meta: map[string]any{
			"program":   args.Program,
			"exit_code": exitCode,
			"has_stderr": strings.TrimSpace(stderrStr) != "",
		},
		IsError: exitCode != 0 || err != nil,
	}
}

// resolveScript locates the on-disk script for a program, returning the
// absolute path and the interpreter to run it with. Windows programs can
// be either PowerShell (.ps1, run via powershell.exe) or Python (.py);
// every other category is Python only.
func (h hookTool) resolveScript(program string) (path, interpreter string, err error) {
	for _, ext := range hookpack.Extensions(h.category) {
		p, err := hookpack.ScriptPath(h.category, program, ext)
		if err != nil {
			continue
		}
		if ext == ".ps1" {
			return p, "powershell.exe", nil
		}
		return p, "python3", nil
	}
	return "", "", fmt.Errorf("could not locate script for %s/%s", h.category, program)
}

// notFound is the helpful catalogue printed when the model asks for a
// program that does not exist. Mirrors the CyberStrike wrappers.
func (h hookTool) notFound(requested string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Program %q not found in %s. Available programs:\n\n", requested, h.name)
	for _, p := range hookpack.Catalog[h.category] {
		fmt.Fprintf(&b, "  %s: %s\n    usage: %s\n", p.Name, p.Description, p.Args)
	}
	return b.String()
}

// ---- the eight tool instances ----------------------------------------------

func newAttackScriptTool() hookTool {
	return hookTool{name: "attack_script", category: hookpack.CategoryAttackScript, timeout: 120 * time.Second}
}

func newAwshookTool() hookTool {
	return hookTool{
		name:     "awshook",
		category: hookpack.CategoryAWS,
		timeout:  300 * time.Second,
		diagnose: func(stderr string, code int) string {
			s := strings.ToLower(stderr)
			switch {
			case strings.Contains(s, "modulenotfounderror") && strings.Contains(s, "boto3"):
				return "\u26a0 boto3 is required. Install with: pip3 install boto3"
			case strings.Contains(s, "nocredentialserror") || strings.Contains(s, "unable to locate credentials"):
				return "\u26a0 AWS credentials not found. Set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, configure a profile, or run on an EC2 instance with an IMDS role."
			}
			return ""
		},
	}
}

func newAzurehookTool() hookTool {
	return hookTool{
		name:     "azurehook",
		category: hookpack.CategoryAzure,
		timeout:  300 * time.Second,
		diagnose: func(stderr string, code int) string {
			s := strings.ToLower(stderr)
			switch {
			case strings.Contains(s, "modulenotfounderror") && (strings.Contains(s, "azure") || strings.Contains(s, "msal")):
				return "\u26a0 Azure SDK required. Install with: pip3 install azure-identity azure-mgmt-resource azure-keyvault-secrets azure-storage-blob msal"
			case strings.Contains(s, "defaultazurecredential") || strings.Contains(s, "environmentcredential"):
				return "\u26a0 Azure credentials not found. Set AZURE_CLIENT_ID / AZURE_CLIENT_SECRET / AZURE_TENANT_ID, or run on a host with a managed identity."
			}
			return ""
		},
	}
}

func newKubehookTool() hookTool {
	return hookTool{
		name:     "kubehook",
		category: hookpack.CategoryKube,
		timeout:  300 * time.Second,
		diagnose: func(stderr string, code int) string {
			s := strings.ToLower(stderr)
			switch {
			case strings.Contains(s, "modulenotfounderror") && strings.Contains(s, "kubernetes"):
				return "\u26a0 kubernetes client required. Install with: pip3 install kubernetes"
			case strings.Contains(s, "configexception") || strings.Contains(s, "kubeconfig"):
				return "\u26a0 Kubernetes credentials not found. Set KUBECONFIG, pass --kubeconfig, or run inside a cluster pod with a service account."
			}
			return ""
		},
	}
}

func newWinhookTool() hookTool {
	return hookTool{
		name:     "winhook",
		category: hookpack.CategoryWin,
		platform: "windows",
		timeout:  120 * time.Second,
		diagnose: func(stderr string, code int) string {
			s := strings.ToLower(stderr)
			if code == 1 && (strings.Contains(s, "administrator") || strings.Contains(s, "access is denied")) {
				return "\u26a0 This program requires Administrator privileges. Escalate before re-running it."
			}
			return ""
		},
	}
}

func newMachookTool() hookTool {
	return hookTool{
		name:     "machook",
		category: hookpack.CategoryMac,
		platform: "darwin",
		timeout:  120 * time.Second,
		diagnose: func(stderr string, code int) string {
			if code == 1 && strings.Contains(strings.ToLower(stderr), "root") {
				return "\u26a0 This program requires root. Escalate to root on the target before re-running it."
			}
			return ""
		},
	}
}

func newCipipeTool() hookTool {
	return hookTool{
		name:     "cipipe",
		category: hookpack.CategoryCI,
		timeout:  300 * time.Second,
		diagnose: func(stderr string, code int) string {
			s := strings.ToLower(stderr)
			switch {
			case strings.Contains(s, "modulenotfounderror") && strings.Contains(s, "requests"):
				return "\u26a0 requests library required. Install with: pip3 install requests"
			case strings.Contains(s, "401") || strings.Contains(s, "403") || strings.Contains(s, "unauthorized"):
				return "\u26a0 Authentication failed. Provide a valid token via --token, or set GITHUB_TOKEN / JENKINS_TOKEN / GITLAB_TOKEN."
			}
			return ""
		},
	}
}

func newEbpfTool() hookTool {
	return hookTool{
		name:     "ebpf",
		category: hookpack.CategoryEBPF,
		platform: "linux",
		timeout:  120 * time.Second,
		diagnose: func(stderr string, code int) string {
			if code == 1 && strings.Contains(strings.ToLower(stderr), "root") {
				return "\u26a0 eBPF programs require root. Escalate to root on the target before re-running it."
			}
			return ""
		},
	}
}
