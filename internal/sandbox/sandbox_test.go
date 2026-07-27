package sandbox

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveFallsBackRatherThanFailing(t *testing.T) {
	// Whatever this machine has, resolving must return something runnable and
	// explain any downgrade. Refusing to run commands because a sandbox is
	// missing would be worse than running them unconfined.
	for _, mode := range []Mode{None, Auto, Bubblewrap, Namespace, "nonsense"} {
		got, note := Resolve(mode)
		switch got {
		case None, Bubblewrap, Namespace:
		default:
			t.Fatalf("Resolve(%q) returned an unusable mode %q", mode, got)
		}
		if got != mode && got != None && note == "" && mode != Auto {
			t.Errorf("Resolve(%q) downgraded to %q without saying why", mode, got)
		}
	}
}

func TestResolveNoneIsAlwaysNone(t *testing.T) {
	got, note := Resolve(None)
	if got != None || note != "" {
		t.Fatalf("Resolve(none) = %q, %q", got, note)
	}
	got, _ = Resolve("")
	if got != None {
		t.Fatalf("an empty mode should mean none, got %q", got)
	}
}

func TestCommandWithoutSandboxIsPlain(t *testing.T) {
	cmd, err := Command(None, Policy{}, "echo", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cmd.Path, "echo") && cmd.Path != "echo" {
		t.Fatalf("path = %q", cmd.Path)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("output = %q", out)
	}
}

func TestNamespaceSandboxRunsCommands(t *testing.T) {
	if runtime.GOOS != "linux" || !userNamespacesWork() {
		t.Skip("user namespaces are unavailable")
	}
	cmd, err := Command(Namespace, Policy{AllowNetwork: true}, "/bin/echo", "confined")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("a confined command failed to run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "confined" {
		t.Fatalf("output = %q", out)
	}
}

func TestNamespaceSandboxTakesTheNetworkAway(t *testing.T) {
	if runtime.GOOS != "linux" || !userNamespacesWork() {
		t.Skip("user namespaces are unavailable")
	}
	// Inside a network namespace with nothing in it, only loopback exists —
	// and it is down. Listing interfaces is enough to tell.
	script := "ip -o link show 2>/dev/null | wc -l || cat /proc/net/dev | tail -n +3 | wc -l"

	confined, err := Command(Namespace, Policy{AllowNetwork: false}, "/bin/sh", "-c", script)
	if err != nil {
		t.Fatal(err)
	}
	confinedOut, err := confined.Output()
	if err != nil {
		t.Fatalf("the confined command failed: %v", err)
	}

	open, err := Command(Namespace, Policy{AllowNetwork: true}, "/bin/sh", "-c", script)
	if err != nil {
		t.Fatal(err)
	}
	openOut, err := open.Output()
	if err != nil {
		t.Fatalf("the unconfined command failed: %v", err)
	}

	confinedCount := strings.TrimSpace(string(confinedOut))
	openCount := strings.TrimSpace(string(openOut))
	if confinedCount == openCount {
		t.Fatalf("the network was not taken away: %s interfaces either way", confinedCount)
	}
}

func TestNamespaceSandboxBlocksOutboundConnections(t *testing.T) {
	if runtime.GOOS != "linux" || !userNamespacesWork() {
		t.Skip("user namespaces are unavailable")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}

	// The point of taking the network away: an instruction the agent picked up
	// from a page it read cannot send anything anywhere.
	cmd, err := Command(Namespace, Policy{AllowNetwork: false},
		"curl", "-s", "--max-time", "5", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a confined command reached the network")
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the confined command hung")
	}
}

func TestDescribeSaysWhatYouActuallyHave(t *testing.T) {
	// The description is what a person reads to decide whether to trust the
	// setup, so it has to be specific about the weak case too.
	plain := Describe(None, Policy{})
	if !strings.Contains(plain, "directly") {
		t.Errorf("the unconfined description is not honest: %q", plain)
	}
	ns := Describe(Namespace, Policy{})
	if !strings.Contains(ns, "no network") {
		t.Errorf("namespace description = %q", ns)
	}
	nsOpen := Describe(Namespace, Policy{AllowNetwork: true})
	if strings.Contains(nsOpen, "no network") {
		t.Errorf("it claims there is no network when there is: %q", nsOpen)
	}
	bw := Describe(Bubblewrap, Policy{Workspace: "/w"})
	if !strings.Contains(bw, "/w") || !strings.Contains(bw, "read-only") {
		t.Errorf("bubblewrap description = %q", bw)
	}
}

func TestBubblewrapCommandShape(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	cmd, err := bubblewrapCommand(Policy{
		Workspace: "/tmp/work",
		Hidden:    []string{"/home/me/.ssh"},
	}, "/bin/sh", "-c", "true")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--unshare-net", "--ro-bind / /", "--bind /tmp/work", "--tmpfs /home/me/.ssh"} {
		if !strings.Contains(args, want) {
			t.Errorf("the invocation is missing %q:\n%s", want, args)
		}
	}
}

func TestDefaultHiddenCoversCredentials(t *testing.T) {
	joined := strings.Join(DefaultHidden, " ")
	for _, want := range []string{".ssh", ".aws", ".gnupg", ".env"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the default hidden list does not cover %s", want)
		}
	}
}
