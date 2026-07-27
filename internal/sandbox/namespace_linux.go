//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// userNamespacesWork reports whether this kernel lets an unprivileged process
// create one. Some distributions and container runtimes switch it off, and
// finding out by trying is more reliable than reading the sysctl, which does
// not exist everywhere.
func userNamespacesWork() bool {
	cmd := exec.Command("/proc/self/exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
	}
	// Starting is the test; the process is killed immediately.
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	return true
}

// namespaceCommand puts the process in its own namespaces. Without bubblewrap
// there is no filesystem confinement to be had this way, but taking the
// network away is most of the value: an injected instruction that cannot make
// a request cannot send anything anywhere.
func namespaceCommand(p Policy, name string, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(name, args...)
	attr := &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS |
			syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC | syscall.CLONE_NEWUTS,
		// Map the invoking user to root inside the namespace, which is what
		// makes the other namespaces available unprivileged.
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getuid(), Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getgid(), Size: 1},
		},
		GidMappingsEnableSetgroups: false,
		// A shell left behind by a crashed agent is a process nobody will find.
		Pdeathsig: syscall.SIGKILL,
	}
	if !p.AllowNetwork {
		attr.Cloneflags |= syscall.CLONE_NEWNET
	}
	cmd.SysProcAttr = attr
	return cmd, nil
}
