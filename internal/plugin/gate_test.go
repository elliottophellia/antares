package plugin

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// Every plugin here is a POSIX shell script, so there is nothing to run these
// against on Windows.
func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("these plugins are POSIX shell scripts")
	}
}

func TestGateHonoursARefusalThatExitsNonZero(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// Refusing and then exiting non-zero is an ordinary shell idiom.
	writePlugin(t, root, "guard", `
name: guard
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"deny\":true,\"reason\":\"policy\"}'\nexit 1\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if !reply.Deny {
		t.Fatalf("a refusal was thrown away because the plugin exited non-zero: %+v", reply)
	}
	if reply.Reason != "policy" {
		t.Fatalf("reason = %q, want the plugin's own words", reply.Reason)
	}
}

func TestGateHonoursAnEmptyReplyFromAFailingGate(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// "{}" is a plugin saying it has no objection. Falling over after saying
	// it is a bug in the plugin, not a change of mind — which is the whole
	// difference between answering and never answering.
	writePlugin(t, root, "shrugger", `
name: shrugger
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{}'\nexit 1\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if reply.Deny {
		t.Fatalf("a plugin that answered before it fell over was read as a refusal: %+v", reply)
	}
}

func TestGateDeniesWhenAPluginTimesOut(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// exec so the timeout kills the sleep itself rather than leaving it
	// holding the pipe open long after the shell is gone.
	writePlugin(t, root, "slow", `
name: slow
command: ./run.sh
hooks: [pre_tool_call]
timeout_ms: 200
`, "#!/bin/sh\nexec sleep 5\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if !reply.Deny {
		t.Fatalf("a plugin that never answered was read as permission: %+v", reply)
	}
	if !strings.Contains(reply.Reason, "slow") {
		t.Fatalf("reason = %q, want it to name the plugin that failed", reply.Reason)
	}
	if !strings.Contains(reply.Reason, "timed out") {
		t.Fatalf("reason = %q, want it to say what went wrong", reply.Reason)
	}
}

func TestGateDeniesWhenAPluginCannotBeRun(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// The manifest is well formed, so the plugin loads and is asked; only
	// starting it fails.
	writePlugin(t, root, "missing", `
name: missing
command: ./not-here.sh
hooks: [pre_tool_call]
`, "")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if !reply.Deny {
		t.Fatalf("a gate that could not be started was read as permission: %+v", reply)
	}
	if !strings.Contains(reply.Reason, "missing") {
		t.Fatalf("reason = %q, want it to name the plugin that failed", reply.Reason)
	}
}

func TestGateDeniesWhenAPluginAnswersWithGibberish(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// Exits cleanly, but nothing it said can be read as a verdict.
	writePlugin(t, root, "babbler", `
name: babbler
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho 'allow, I guess?'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if !reply.Deny {
		t.Fatalf("an answer nobody can read was taken for a yes: %+v", reply)
	}
	if !strings.Contains(reply.Reason, "babbler") {
		t.Fatalf("reason = %q, want it to name the plugin that failed", reply.Reason)
	}
}

func TestGateLetsACleanReplyThrough(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "watcher", `
name: watcher
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"notice\":\"looks fine\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if reply.Deny {
		t.Fatalf("a plugin that answered and succeeded was treated as a refusal: %+v", reply)
	}
	if reply.Notice != "looks fine" {
		t.Fatalf("notice = %q", reply.Notice)
	}
}

func TestGateTreatsAQuietSuccessAsConsent(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// Saying nothing and exiting cleanly is how a plugin declines to have an
	// opinion. Only failure closes the gate, not silence.
	writePlugin(t, root, "quiet", `
name: quiet
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if reply.Deny {
		t.Fatalf("a plugin with no opinion refused the call: %+v", reply)
	}
}

func TestGateOnlyClosesForPreToolCall(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "crasher", `
name: crasher
command: ./run.sh
hooks: [post_tool_call]
`, "#!/bin/sh\nexit 1\n")

	m := NewManager([]string{root})
	_ = m.Load()

	// A result is already in hand; a broken observer has nothing to refuse.
	reply := m.Dispatch(context.Background(), Payload{Event: PostToolCall, Tool: "terminal", Result: "done"})
	if reply.Deny {
		t.Fatalf("a failing post_tool_call plugin denied something: %+v", reply)
	}
}

func TestGateHonoursAReplyFromAFailingObserver(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// Every event reads stdout, not just the gate: what the plugin said
	// stands on its own whatever the exit status was.
	writePlugin(t, root, "rewriter", `
name: rewriter
command: ./run.sh
hooks: [post_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"result\":\"rewritten\"}'\nexit 3\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PostToolCall, Tool: "terminal", Result: "original"})
	if reply.Result != "rewritten" {
		t.Fatalf("result = %q, want the reply the plugin printed before it exited badly", reply.Result)
	}
}

func TestGateDoesNotDenyForAPluginThatNeverRan(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	// Declares the gate but cannot be loaded, so it is never invoked.
	writePlugin(t, root, "unloadable", "name: unloadable\nhooks: [pre_tool_call]\n", "")
	// Loads, but wants a different event.
	writePlugin(t, root, "elsewhere", `
name: elsewhere
command: ./run.sh
hooks: [session_start]
`, "#!/bin/sh\nexit 1\n")
	// Loads and wants the gate, but the operator turned it off.
	writePlugin(t, root, "switched-off", `
name: switched-off
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\nexit 1\n")

	m := NewManager([]string{root})
	_ = m.Load()
	if !m.SetEnabled("switched-off", false) {
		t.Fatal("could not disable a plugin that exists")
	}

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if reply.Deny {
		t.Fatalf("a plugin that was never invoked still refused the call: %+v", reply)
	}
}
