package tools

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/depcheck"
)

// Native reverse-engineering toolset. Binary triage (format, architecture,
// imports, strings) runs in pure Go with no external dependency. Deeper analysis
// and decompilation shell out to radare2 / Ghidra, which are dependency-gated:
// if they are missing the tool reports what to install rather than failing, and
// the agent asks the user before installing anything.

func reReadFile(in Input, path string) ([]byte, string, error) {
	p := resolveWorkspacePath(in.Workspace, path)
	data, err := os.ReadFile(p)
	return data, p, err
}

// resolveWorkspacePath keeps relative paths inside the workspace, matching the
// file tools' behaviour.
func resolveWorkspacePath(workspace, path string) string {
	if path == "" {
		return workspace
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~") {
		return path
	}
	if workspace == "" {
		return path
	}
	return workspace + "/" + path
}

// ---- re_info ----------------------------------------------------------------

type reInfoTool struct{}

func (reInfoTool) Name() string { return "re_info" }
func (reInfoTool) Description() string {
	return "Identify a binary: file format (ELF/PE/Mach-O), architecture, bit-width, endianness, type " +
		"(exec/library/object), and — where available — sections and imported libraries. Pure Go, no dependency."
}
func (reInfoTool) Schema() map[string]any {
	return schema(map[string]any{"path": prop("string", "Path to the binary to inspect.")}, "path")
}
func (reInfoTool) RequiresApproval() bool { return false }

func (reInfoTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path string `json:"path"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	data, p, err := reReadFile(in, args.Path)
	if err != nil {
		return Errorf("read %s: %v", args.Path, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Binary: %s (%d bytes)\n\n", p, len(data))

	switch {
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}):
		f, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			return Errorf("ELF parse: %v", err)
		}
		defer f.Close()
		fmt.Fprintf(&b, "Format: ELF\nClass: %s\nData: %s\nType: %s\nMachine: %s\n",
			f.Class, f.Data, f.Type, f.Machine)
		if libs, err := f.ImportedLibraries(); err == nil && len(libs) > 0 {
			fmt.Fprintf(&b, "Imported libs: %s\n", strings.Join(libs, ", "))
		}
		fmt.Fprintf(&b, "Sections: %d\n", len(f.Sections))
	case len(data) >= 2 && data[0] == 'M' && data[1] == 'Z':
		f, err := pe.NewFile(bytes.NewReader(data))
		if err != nil {
			return Errorf("PE parse: %v", err)
		}
		defer f.Close()
		arch := "unknown"
		switch f.Machine {
		case pe.IMAGE_FILE_MACHINE_AMD64:
			arch = "x86-64"
		case pe.IMAGE_FILE_MACHINE_I386:
			arch = "x86"
		case pe.IMAGE_FILE_MACHINE_ARM64:
			arch = "arm64"
		}
		fmt.Fprintf(&b, "Format: PE (Windows)\nMachine: %s\nSections: %d\n", arch, len(f.Sections))
		if f.OptionalHeader != nil {
			if oh, ok := f.OptionalHeader.(*pe.OptionalHeader64); ok {
				fmt.Fprintf(&b, "Entry point: 0x%x\nImage base: 0x%x\n", oh.AddressOfEntryPoint, oh.ImageBase)
			}
		}
	case len(data) >= 4 && (bytes.Equal(data[:4], []byte{0xfe, 0xed, 0xfa, 0xce}) ||
		bytes.Equal(data[:4], []byte{0xfe, 0xed, 0xfa, 0xcf}) ||
		bytes.Equal(data[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) ||
		bytes.Equal(data[:4], []byte{0xce, 0xfa, 0xed, 0xfe})):
		f, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			return Errorf("Mach-O parse: %v", err)
		}
		defer f.Close()
		fmt.Fprintf(&b, "Format: Mach-O\nType: %s\nCPU: %s\nSections: %d\n", f.Type, f.Cpu, len(f.Sections))
		if libs, err := f.ImportedLibraries(); err == nil && len(libs) > 0 {
			fmt.Fprintf(&b, "Imported libs: %s\n", strings.Join(libs, ", "))
		}
	default:
		fmt.Fprintf(&b, "Format: unrecognized (not ELF/PE/Mach-O). First bytes: % x\n", data[:min2(16, len(data))])
	}
	return Text(b.String())
}

// ---- re_strings -------------------------------------------------------------

type reStringsTool struct{}

func (reStringsTool) Name() string { return "re_strings" }
func (reStringsTool) Description() string {
	return "Extract printable strings from a binary (like `strings`), useful for spotting URLs, paths, keys, " +
		"and messages. Pure Go, no dependency."
}
func (reStringsTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":   prop("string", "Path to the binary."),
		"min":    propDefault("integer", "Minimum string length.", 4),
		"filter": prop("string", "Optional substring; only strings containing it are returned."),
	}, "path")
}
func (reStringsTool) RequiresApproval() bool { return false }

func (reStringsTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path   string `json:"path"`
		Min    int    `json:"min"`
		Filter string `json:"filter"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.Min <= 0 {
		args.Min = 4
	}
	data, p, err := reReadFile(in, args.Path)
	if err != nil {
		return Errorf("read %s: %v", args.Path, err)
	}
	var out []string
	var cur []byte
	flush := func() {
		if len(cur) >= args.Min {
			s := string(cur)
			if args.Filter == "" || strings.Contains(s, args.Filter) {
				out = append(out, s)
			}
		}
		cur = cur[:0]
	}
	for _, c := range data {
		if c >= 0x20 && c < 0x7f {
			cur = append(cur, c)
		} else {
			flush()
		}
	}
	flush()

	const cap = 400
	total := len(out)
	if len(out) > cap {
		out = out[:cap]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Strings in %s (min %d, %d found%s):\n\n", p, args.Min, total,
		ternary(total > cap, fmt.Sprintf(", showing first %d", cap), ""))
	b.WriteString(strings.Join(out, "\n"))
	return Text(b.String())
}

// ---- re_analyze (radare2) ---------------------------------------------------

type reAnalyzeTool struct{}

func (reAnalyzeTool) Name() string { return "re_analyze" }
func (reAnalyzeTool) Description() string {
	return "Run automated code analysis on a binary with radare2: discover functions, entry points, and cross " +
		"references. Requires radare2 (dependency-gated — if missing, ask the user to install it first)."
}
func (reAnalyzeTool) Schema() map[string]any {
	return schema(map[string]any{"path": prop("string", "Path to the binary to analyze.")}, "path")
}
func (reAnalyzeTool) RequiresApproval() bool { return true }

func (reAnalyzeTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Path string `json:"path"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if ok, missing := depcheck.Require("radare2"); !ok {
		return Errorf("%s", gateMessage("code analysis", missing))
	}
	p := resolveWorkspacePath(in.Workspace, args.Path)
	if _, err := os.Stat(p); err != nil {
		return Errorf("read %s: %v", args.Path, err)
	}
	out, err := runRE(ctx, "r2", "-q", "-e", "scr.color=0", "-c", "aaa; afl; ii; iz~..", p)
	if err != nil {
		// r2 vs radare2 binary name.
		out, err = runRE(ctx, "radare2", "-q", "-e", "scr.color=0", "-c", "aaa; afl; ii; iz~..", p)
	}
	if err != nil {
		return Errorf("radare2 failed: %v", err)
	}
	return Text(fmt.Sprintf("radare2 analysis of %s:\n\n%s", p, out))
}

// ---- re_decompile (Ghidra) --------------------------------------------------

type reDecompileTool struct{}

func (reDecompileTool) Name() string { return "re_decompile" }
func (reDecompileTool) Description() string {
	return "Decompile a binary to C-like pseudocode with Ghidra's headless analyzer. Requires Ghidra and a JDK " +
		"(dependency-gated — if missing, ask the user to install them first)."
}
func (reDecompileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path": prop("string", "Path to the binary to decompile."),
	}, "path")
}
func (reDecompileTool) RequiresApproval() bool { return true }

func (reDecompileTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Path string `json:"path"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if ok, missing := depcheck.Require("ghidra", "java"); !ok {
		return Errorf("%s", gateMessage("decompilation", missing))
	}
	p := resolveWorkspacePath(in.Workspace, args.Path)
	if _, err := os.Stat(p); err != nil {
		return Errorf("read %s: %v", args.Path, err)
	}
	// Locate analyzeHeadless.
	headless := "analyzeHeadless"
	if lp, err := exec.LookPath("analyzeHeadless"); err == nil {
		headless = lp
	} else if dir := firstNonBlank(os.Getenv("GHIDRA_INSTALL_DIR"), os.Getenv("GHIDRA_HOME")); dir != "" {
		headless = dir + "/support/analyzeHeadless"
	}
	proj, err := os.MkdirTemp("", "antares-ghidra-*")
	if err != nil {
		return Errorf("temp project: %v", err)
	}
	defer os.RemoveAll(proj)
	out, err := runRE(ctx, headless, proj, "antares", "-import", p, "-analysisTimeoutPerFile", "120", "-scriptlog", "/dev/stdout")
	if err != nil {
		return Errorf("ghidra headless failed: %v\n%s", err, out)
	}
	return Text(fmt.Sprintf("Ghidra headless analysis of %s completed:\n\n%s", p, truncateText(out, 8000)))
}

// ---- helpers ----------------------------------------------------------------

// gateMessage renders the "missing dependency — ask before installing" text.
func gateMessage(feature string, missing []depcheck.Status) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s needs tools that are not installed:\n", strings.Title(feature))
	for _, m := range missing {
		fmt.Fprintf(&b, "- %s: %s\n  install: %s\n", m.Name, m.Purpose, m.InstallHint)
	}
	b.WriteString("\nTell the user what is needed and why, ask for permission, then install via the terminal. Do not install silently.")
	return b.String()
}

func runRE(ctx context.Context, name string, args ...string) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(tctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// keep debug/elf sort import used
