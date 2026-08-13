package plugin

import (
	"context"
	"strings"
	"testing"
	"time"
)

// timeout_ms is the only bound a manifest can put on a plugin, and it has to
// bound what Dispatch costs the turn. It did not: Run waits on the goroutines
// copying the child's stdout, and those cannot finish while anything still
// holds the write end of the pipe. Backgrounding a child is enough to hold it —
// the child inherits the descriptor and outlives the shell that spawned it — so
// killing the shell at the deadline frees nothing and Dispatch blocks for as
// long as the grandchild lives.
//
// The existing timeout test uses `exec sleep 5`, which replaces the shell and
// so leaves nothing behind to hold the pipe. That is the one shape of slow
// plugin this bug does not affect.
func TestPluginTimeoutBoundsDispatchWhenAChildOutlivesTheShell(t *testing.T) {
	skipWithoutShell(t)
	for _, tc := range []struct {
		name, script string
	}{
		// The shell exits at once and cleanly. Nothing times out and nothing
		// fails; the grandchild alone holds Dispatch for its whole lifetime.
		{"shell exits and leaves the child behind", "#!/bin/sh\ncat > /dev/null\nsleep 3 &\n"},
		// The shell is still running at the deadline and is killed, but both
		// its children hold the pipe past it.
		{"shell is killed with a child still running", "#!/bin/sh\ncat > /dev/null\nsleep 3 &\nsleep 5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writePlugin(t, root, "leaky", `
name: leaky
command: ./run.sh
hooks: [pre_tool_call]
timeout_ms: 200
`, tc.script)

			m := NewManager([]string{root})
			_ = m.Load()

			start := time.Now()
			reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
			elapsed := time.Since(start)

			if elapsed > 2*time.Second {
				t.Fatalf("Dispatch took %s for a plugin declaring timeout_ms: 200 — a backgrounded child "+
					"holding the stdout pipe open is not bounded by the deadline", elapsed)
			}
			if !reply.Deny {
				t.Fatalf("a plugin that never answered was read as permission: %+v", reply)
			}
			if !strings.Contains(reply.Reason, "leaky") {
				t.Fatalf("reason = %q, want it to name the plugin that failed", reply.Reason)
			}
		})
	}
}

// The bound must not turn a captured verdict into a fabricated one. A plugin
// that prints its answer and then leaks a child has answered: the answer was on
// the wire before the deadline and is in hand when the wait is cut short. Only
// a plugin that printed nothing usable gets a synthesised refusal.
func TestPluginTimeoutHonoursAVerdictPrintedBeforeTheDeadline(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "decisive", `
name: decisive
command: ./run.sh
hooks: [pre_tool_call]
timeout_ms: 500
`, "#!/bin/sh\ncat > /dev/null\necho '{\"deny\":true,\"reason\":\"policy says no\"}'\nsleep 3 &\nsleep 5\n")

	m := NewManager([]string{root})
	_ = m.Load()

	start := time.Now()
	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("Dispatch took %s for a plugin declaring timeout_ms: 500", elapsed)
	}
	if !reply.Deny {
		t.Fatalf("the refusal the plugin printed was thrown away: %+v", reply)
	}
	if reply.Reason != "policy says no" {
		t.Fatalf("reason = %q, want the plugin's own words rather than a synthesised refusal", reply.Reason)
	}
}

// The same for an observer, where the answer is content rather than a verdict:
// what the plugin printed before the wait was cut short still stands.
func TestPluginTimeoutKeepsWhatAnObserverPrintedBeforeTheDeadline(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "annotator", `
name: annotator
command: ./run.sh
hooks: [post_tool_call]
timeout_ms: 500
`, "#!/bin/sh\ncat > /dev/null\necho '{\"result\":\"rewritten\"}'\nsleep 3 &\nsleep 5\n")

	m := NewManager([]string{root})
	_ = m.Load()

	start := time.Now()
	reply := m.Dispatch(context.Background(), Payload{Event: PostToolCall, Tool: "terminal", Result: "original"})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("Dispatch took %s for a plugin declaring timeout_ms: 500", elapsed)
	}
	if reply.Result != "rewritten" {
		t.Fatalf("result = %q, want what the plugin printed before the wait was cut short", reply.Result)
	}
}

// A plugin that finishes well inside its budget must not be made to wait for it.
func TestPluginTimeoutDoesNotDelayAPromptPlugin(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "prompt", `
name: prompt
command: ./run.sh
hooks: [pre_tool_call]
timeout_ms: 5000
`, "#!/bin/sh\ncat > /dev/null\necho '{\"notice\":\"fine\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	start := time.Now()
	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a plugin that answered at once took %s", elapsed)
	}
	if reply.Deny || reply.Notice != "fine" {
		t.Fatalf("reply = %+v, want the notice it printed", reply)
	}
}
