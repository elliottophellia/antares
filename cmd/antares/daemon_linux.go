//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

func configureDaemonProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func processAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		// A detached child can remain as a zombie briefly. Treat it as stopped.
		if stat, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); readErr == nil {
			fields := strings.Fields(string(stat))
			if len(fields) > 2 && fields[2] == "Z" {
				return false, nil
			}
		}
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		return true, nil
	default:
		return false, err
	}
}

func processStartTime(pid int) (string, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	// Field 2 (comm) may contain spaces and parentheses. Everything after the
	// final ')' starts at field 3; starttime is field 22, index 19 in this tail.
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", errors.New("malformed /proc stat")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) <= 19 {
		return "", errors.New("short /proc stat")
	}
	return fields[19], nil
}

func processExecutable(pid int) (string, error) {
	path, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", err
	}
	path = strings.TrimSuffix(path, " (deleted)")
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		return resolved, nil
	}
	// After an in-place binary upgrade, /proc/PID/exe names the original path
	// with " (deleted)" even though that inode is still executing. The path is
	// still valid process identity metadata; requiring its replacement file to
	// resolve would make `antares stop` unable to adopt the old instance.
	return filepath.Clean(path), nil
}

func processArguments(pid int) ([]string, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	return parts, nil
}

func processOwnerUID(pid int) (int, error) {
	info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("process owner unavailable")
	}
	return int(stat.Uid), nil
}

func processesListeningOnPort(port int) ([]int, error) {
	inodes := map[string]bool{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" { // TCP_LISTEN
				continue
			}
			parts := strings.Split(fields[1], ":")
			if len(parts) != 2 {
				continue
			}
			got, err := strconv.ParseInt(parts[1], 16, 32)
			if err == nil && int(got) == port {
				inodes[fields[9]] = true
			}
		}
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if inodes[inode] && !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	sort.Ints(pids)
	return pids, nil
}

func signalDaemonProcess(proc *os.Process, managed bool, sig syscall.Signal) error {
	if managed {
		return syscall.Kill(-proc.Pid, sig)
	}
	return proc.Signal(sig)
}

func daemonTargetAlive(pid int, managed bool) (bool, error) {
	if !managed {
		return processAlive(pid)
	}
	err := syscall.Kill(-pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func terminateDaemonProcess(proc *os.Process, managed bool) error {
	return signalDaemonProcess(proc, managed, syscall.SIGTERM)
}
func killDaemonProcess(proc *os.Process, managed bool) error {
	return signalDaemonProcess(proc, managed, syscall.SIGKILL)
}
