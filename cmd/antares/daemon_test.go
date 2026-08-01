//go:build linux

package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestDaemonStateRoundTripAndStaleCleanup(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	if err := config.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigFile(), []byte("server:\n  host: 127.0.0.1\n  port: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := daemonState{PID: os.Getpid(), StartTime: "wrong-reused-token", Exe: "/does/not/matter", URL: "http://127.0.0.1:1"}
	if err := writeDaemonState(state); err != nil {
		t.Fatal(err)
	}
	got, err := readDaemonState()
	if err != nil || got.PID != state.PID || got.StartTime != state.StartTime {
		t.Fatalf("read state = %#v, %v", got, err)
	}
	_, live, err := currentDaemon()
	if err != nil || live {
		t.Fatalf("stale daemon = live %v, err %v", live, err)
	}
	if _, err := os.Stat(daemonPIDFile()); !os.IsNotExist(err) {
		t.Fatalf("stale pid file was not removed: %v", err)
	}
}

func TestReadDaemonStateRejectsCorruption(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	if err := config.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonPIDFile(), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDaemonState(); err == nil || !strings.Contains(err.Error(), "invalid daemon state") {
		t.Fatalf("corrupt state error = %v", err)
	}
}

func TestDaemonStartLockSerializesStartersAndReapsStaleLock(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	if err := config.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	release, err := acquireDaemonStartLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireDaemonStartLock(80 * time.Millisecond); err == nil {
		t.Fatal("second starter acquired an active lock")
	}
	release()

	if err := os.Mkdir(daemonLockDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-daemonReadyTimeout - 10*time.Second)
	if err := os.Chtimes(daemonLockDir(), old, old); err != nil {
		t.Fatal(err)
	}
	release, err = acquireDaemonStartLock(time.Second)
	if err != nil {
		t.Fatalf("stale lock was not reaped: %v", err)
	}
	release()
}

func TestProcessStartTimeAndIdentity(t *testing.T) {
	start, err := processStartTime(os.Getpid())
	if err != nil || start == "" {
		t.Fatalf("start time = %q, %v", start, err)
	}
	exe, err := processExecutable(os.Getpid())
	if err != nil || exe == "" {
		t.Fatalf("exe = %q, %v", exe, err)
	}
	args, err := processArguments(os.Getpid())
	if err != nil || len(args) == 0 {
		t.Fatalf("args = %v, %v", args, err)
	}
}

func TestProcessesListeningOnPortFindsOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	pids, err := processesListeningOnPort(port)
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		if pid == os.Getpid() {
			return
		}
	}
	t.Fatalf("listener owner %d not found in %v", os.Getpid(), pids)
}

func TestDaemonCLILifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts a real daemon")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "antares-test")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	configYAML := "server:\n  host: 127.0.0.1\n  port: " + strconv.Itoa(port) + "\nmodel:\n  default: test\n  provider: custom\nproviders:\n  custom:\n    enabled: true\n    kind: custom\n    base_url: http://127.0.0.1:1/v1\ndatabase:\n  driver: sqlite\n  dsn: " + filepath.Join(home, "antares.db") + "\ncron:\n  enabled: false\nmcp:\n  enabled: false\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "ANTARES_HOME="+home)
	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	startAt := time.Now()
	out, err := run("serve")
	if err != nil {
		t.Fatalf("serve: %v\n%s", err, out)
	}
	if time.Since(startAt) > 10*time.Second || !strings.Contains(out, "started in background") {
		t.Fatalf("serve did not detach promptly: %s after %s", out, time.Since(startAt))
	}
	stateBytes, err := os.ReadFile(filepath.Join(home, "antares.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var state daemonState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if state.PID > 1 {
			_ = syscall.Kill(state.PID, syscall.SIGKILL)
		}
	})

	status, err := run("status")
	if err != nil || !strings.Contains(status, "pid "+strconv.Itoa(state.PID)) {
		t.Fatalf("status: %v\n%s", err, status)
	}
	again, err := run("serve")
	if err != nil || !strings.Contains(again, "already running") {
		t.Fatalf("second serve: %v\n%s", err, again)
	}
	stateAgain, _ := os.ReadFile(filepath.Join(home, "antares.pid"))
	if string(stateAgain) != string(stateBytes) {
		t.Fatal("duplicate serve replaced the running daemon state")
	}

	stop, err := run("stop")
	if err != nil || !strings.Contains(stop, "Antares stopped") {
		t.Fatalf("stop: %v\n%s", err, stop)
	}
	if alive, _ := processAlive(state.PID); alive {
		t.Fatalf("daemon pid %d survived stop", state.PID)
	}
	secondStop, err := run("stop")
	if err != nil || !strings.Contains(secondStop, "not running") {
		t.Fatalf("second stop: %v\n%s", err, secondStop)
	}
}

func TestDaemonServeRejectsOccupiedPortWithoutLeavingState(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts a real daemon")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "antares-test")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	configYAML := "server:\n  host: 127.0.0.1\n  port: " + strconv.Itoa(port) + "\nmodel:\n  default: test\n  provider: custom\nproviders:\n  custom:\n    enabled: true\n    kind: custom\n    base_url: http://127.0.0.1:1/v1\ndatabase:\n  driver: sqlite\n  dsn: " + filepath.Join(home, "antares.db") + "\ncron:\n  enabled: false\nmcp:\n  enabled: false\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(), "ANTARES_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "failed to become ready") {
		t.Fatalf("occupied serve = %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(home, "antares.pid")); !os.IsNotExist(statErr) {
		t.Fatalf("failed startup left pid state: %v", statErr)
	}
}
