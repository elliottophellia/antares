// Package sandbox confines the commands the agent runs.
//
// The agent has a shell. On a machine that also holds your files, your keys,
// and your network, that is a lot of reach for something driven by a model
// following instructions from a web page it just read.
//
// Nothing here needs root. It uses what an unprivileged user already has:
// bubblewrap when installed, and Linux user namespaces when not. Where neither
// is available it says so rather than pretending — a sandbox you believe in
// but do not have is worse than none.
package sandbox

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Mode names how much confinement to apply.
type Mode string

const (
	// None runs commands directly.
	None Mode = "none"
	// Auto picks the strongest mechanism available and falls back quietly.
	Auto Mode = "auto"
	// Bubblewrap confines filesystem and network. Needs bwrap installed.
	Bubblewrap Mode = "bubblewrap"
	// Namespace blocks the network with a user namespace. Needs no tools, but
	// leaves the filesystem alone.
	Namespace Mode = "namespace"
)

// Policy is what a confined command may reach.
type Policy struct {
	// Workspace is writable. Everything else is read-only under bubblewrap.
	Workspace string
	// AllowNetwork leaves the network reachable. Off means no network at all,
	// not a filtered one.
	AllowNetwork bool
	// ReadOnly adds paths the command may read but not change.
	ReadOnly []string
	// Hidden names paths to keep out of the sandbox entirely — credentials,
	// mostly.
	Hidden []string
}

// DefaultHidden is what stays out of a sandbox unless someone says otherwise.
// These are the paths an agent has no business reading, and the ones an
// injected instruction would go for first.
var DefaultHidden = []string{
	"~/.ssh",
	"~/.aws",
	"~/.gnupg",
	"~/.config/gh",
	"~/.antares/.env",
	"~/.kube",
	"~/.docker/config.json",
}

// Available reports which mechanisms this machine can actually use.
func Available() []Mode {
	var out []Mode
	if runtime.GOOS != "linux" {
		return out
	}
	if _, err := exec.LookPath("bwrap"); err == nil {
		out = append(out, Bubblewrap)
	}
	if userNamespacesWork() {
		out = append(out, Namespace)
	}
	return out
}

// Resolve turns a configured mode into one that will work here, and explains
// the choice. An unavailable mode falls back rather than failing: refusing to
// run commands because a sandbox is missing helps nobody.
func Resolve(mode Mode) (Mode, string) {
	available := Available()
	has := func(m Mode) bool {
		for _, a := range available {
			if a == m {
				return true
			}
		}
		return false
	}

	switch mode {
	case "", None:
		return None, ""
	case Bubblewrap:
		if has(Bubblewrap) {
			return Bubblewrap, ""
		}
		if has(Namespace) {
			return Namespace, "bubblewrap is not installed; falling back to network isolation only"
		}
		return None, "bubblewrap is not installed and namespaces are unavailable; commands are not confined"
	case Namespace:
		if has(Namespace) {
			return Namespace, ""
		}
		return None, "user namespaces are unavailable; commands are not confined"
	case Auto:
		if has(Bubblewrap) {
			return Bubblewrap, ""
		}
		if has(Namespace) {
			return Namespace, "bubblewrap is not installed; confining the network only"
		}
		return None, "no sandbox is available on this machine; commands are not confined"
	}
	return None, fmt.Sprintf("unknown sandbox mode %q", mode)
}

// Command builds an exec.Cmd that runs the given program confined.
//
// The returned command is ready to start; the caller still sets Dir, Env, and
// the pipes.
func Command(mode Mode, p Policy, name string, args ...string) (*exec.Cmd, error) {
	switch mode {
	case Bubblewrap:
		return bubblewrapCommand(p, name, args...)
	case Namespace:
		return namespaceCommand(p, name, args...)
	default:
		return exec.Command(name, args...), nil
	}
}

// bubblewrapCommand builds the bwrap invocation.
//
// The shape is: everything readable, the workspace writable, credentials
// absent, and no network unless asked for. That is the useful default — an
// agent that cannot read /usr is an agent that cannot run anything.
func bubblewrapCommand(p Policy, name string, args ...string) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bubblewrap is not installed")
	}

	wrapped := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-ipc",
		// The root filesystem readable but not writable.
		"--ro-bind", "/", "/",
		// A private /tmp, so scratch files do not accumulate on the host.
		"--tmpfs", "/tmp",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	if !p.AllowNetwork {
		wrapped = append(wrapped, "--unshare-net")
	}
	if p.Workspace != "" {
		wrapped = append(wrapped, "--bind", p.Workspace, p.Workspace)
	}
	for _, ro := range p.ReadOnly {
		if ro != "" {
			wrapped = append(wrapped, "--ro-bind-try", ro, ro)
		}
	}
	// Hiding comes last so it wins over the binds above.
	for _, h := range p.Hidden {
		if h != "" {
			wrapped = append(wrapped, "--tmpfs", h)
		}
	}

	wrapped = append(wrapped, name)
	wrapped = append(wrapped, args...)
	return exec.Command(bwrap, wrapped...), nil
}

// Describe explains in one sentence what a mode actually does, for the places
// that have to tell a person what protection they have.
func Describe(mode Mode, p Policy) string {
	switch mode {
	case Bubblewrap:
		net := "no network"
		if p.AllowNetwork {
			net = "network allowed"
		}
		return fmt.Sprintf("commands run with the filesystem read-only apart from %s, credentials hidden, and %s",
			orElse(p.Workspace, "the workspace"), net)
	case Namespace:
		if p.AllowNetwork {
			return "commands run in their own process namespace"
		}
		return "commands run with no network, in their own process namespace"
	default:
		return "commands run directly, with everything you can reach"
	}
}

func orElse(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
