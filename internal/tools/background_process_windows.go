//go:build windows

package tools

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func killProcessGroup(cmd *exec.Cmd) { terminateProcessGroup(cmd) }
