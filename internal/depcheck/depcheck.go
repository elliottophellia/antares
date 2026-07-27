// Package depcheck inspects whether external tools a feature needs (Ghidra, a
// JDK, radare2, …) are present on the host. It never installs anything: it only
// reports status and the command the user could run, so a feature can gate
// itself and ask permission before anything is installed.
package depcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Dependency describes an external program a feature relies on.
type Dependency struct {
	Name    string // stable id, e.g. "ghidra"
	Purpose string // what antares feature needs it
	// Bins are executables to look for on PATH; any one present satisfies it.
	Bins []string
	// Envs are environment variables that, if set to an existing path, satisfy it.
	Envs []string
	// Paths are well-known install locations to probe.
	Paths []string
	// Install maps GOOS -> a suggested install command (a hint, never run).
	Install map[string]string
}

// Status is the resolved state of a dependency on this host.
type Status struct {
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Installed   bool   `json:"installed"`
	Found       string `json:"found,omitempty"` // path or env that satisfied it
	InstallHint string `json:"install_hint,omitempty"`
}

// Registry holds the dependencies antares features may need. Extend as features
// are added.
var Registry = map[string]Dependency{
	"java": {
		Name: "java", Purpose: "Java runtime (required by Ghidra)",
		Bins: []string{"java"},
		Envs: []string{"JAVA_HOME"},
		Install: map[string]string{
			"linux":   "sudo apt-get install -y openjdk-21-jdk   # or your distro's JDK 17+",
			"darwin":  "brew install openjdk@21",
			"windows": "winget install EclipseAdoptium.Temurin.21.JDK",
		},
	},
	"ghidra": {
		Name: "ghidra", Purpose: "reverse engineering (decompilation, analysis)",
		Bins: []string{"ghidraRun", "analyzeHeadless"},
		Envs: []string{"GHIDRA_INSTALL_DIR", "GHIDRA_HOME"},
		Paths: []string{
			"/opt/ghidra", "/usr/local/ghidra", "/usr/share/ghidra",
			filepath.Join(os.Getenv("HOME"), "ghidra"),
			filepath.Join(os.Getenv("HOME"), ".local", "ghidra"),
		},
		Install: map[string]string{
			"linux":   "download from https://github.com/NationalSecurityAgency/ghidra/releases and set GHIDRA_INSTALL_DIR",
			"darwin":  "brew install --cask ghidra   # then set GHIDRA_INSTALL_DIR",
			"windows": "download from https://github.com/NationalSecurityAgency/ghidra/releases and set GHIDRA_INSTALL_DIR",
		},
	},
	"radare2": {
		Name: "radare2", Purpose: "binary analysis (alternative to Ghidra)",
		Bins: []string{"r2", "radare2"},
		Install: map[string]string{
			"linux": "sudo apt-get install -y radare2", "darwin": "brew install radare2",
			"windows": "winget install radare.radare2",
		},
	},
	"nmap": {
		Name: "nmap", Purpose: "network/port scanning",
		Bins: []string{"nmap"},
		Install: map[string]string{
			"linux": "sudo apt-get install -y nmap", "darwin": "brew install nmap",
			"windows": "winget install Insecure.Nmap",
		},
	},
	"mitmproxy": {
		Name: "mitmproxy", Purpose: "HTTP(S) interception backend",
		Bins: []string{"mitmdump", "mitmproxy"},
		Install: map[string]string{
			"linux": "pipx install mitmproxy", "darwin": "brew install mitmproxy",
			"windows": "pipx install mitmproxy",
		},
	},
}

// Check resolves one dependency's status on this host.
func Check(dep Dependency) Status {
	st := Status{Name: dep.Name, Purpose: dep.Purpose}
	for _, bin := range dep.Bins {
		if p, err := exec.LookPath(bin); err == nil {
			st.Installed, st.Found = true, p
			return st
		}
	}
	for _, env := range dep.Envs {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			if _, err := os.Stat(v); err == nil {
				st.Installed, st.Found = true, env+"="+v
				return st
			}
		}
	}
	for _, p := range dep.Paths {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			st.Installed, st.Found = true, p
			return st
		}
	}
	if hint, ok := dep.Install[runtime.GOOS]; ok {
		st.InstallHint = hint
	}
	return st
}

// CheckByName resolves a registered dependency by id.
func CheckByName(name string) (Status, bool) {
	dep, ok := Registry[name]
	if !ok {
		return Status{}, false
	}
	return Check(dep), true
}

// Require reports whether every named dependency is present, returning the
// statuses of the ones that are missing so the caller can ask the user to
// install them.
func Require(names ...string) (ok bool, missing []Status) {
	ok = true
	for _, n := range names {
		st, known := CheckByName(n)
		if !known {
			ok = false
			missing = append(missing, Status{Name: n, Installed: false, InstallHint: "unknown dependency"})
			continue
		}
		if !st.Installed {
			ok = false
			missing = append(missing, st)
		}
	}
	return ok, missing
}
