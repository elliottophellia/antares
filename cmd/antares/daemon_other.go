//go:build !linux

package main

import (
	"errors"
	"os"
	"os/exec"
)

func configureDaemonProcess(_ *exec.Cmd) {}
func processAlive(_ int) (bool, error) {
	return false, errors.New("daemon process inspection is only implemented on Linux")
}
func processStartTime(_ int) (string, error) {
	return "", errors.New("daemon process inspection is only implemented on Linux")
}
func processExecutable(_ int) (string, error) {
	return "", errors.New("daemon process inspection is only implemented on Linux")
}
func processArguments(_ int) ([]string, error) {
	return nil, errors.New("daemon process inspection is only implemented on Linux")
}
func processOwnerUID(_ int) (int, error) {
	return 0, errors.New("daemon process inspection is only implemented on Linux")
}
func processesListeningOnPort(_ int) ([]int, error)         { return nil, nil }
func daemonTargetAlive(pid int, _ bool) (bool, error)       { return processAlive(pid) }
func terminateDaemonProcess(proc *os.Process, _ bool) error { return proc.Signal(os.Interrupt) }
func killDaemonProcess(proc *os.Process, _ bool) error      { return proc.Kill() }
