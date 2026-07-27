//go:build !linux

package sandbox

import (
	"errors"
	"os/exec"
)

// userNamespacesWork is Linux-only. Everywhere else the answer is no, and
// Resolve falls back to running commands directly.
func userNamespacesWork() bool { return false }

func namespaceCommand(Policy, string, ...string) (*exec.Cmd, error) {
	return nil, errors.New("namespace isolation is only available on Linux")
}
