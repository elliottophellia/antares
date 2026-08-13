package plugin

import (
	"context"
	"strings"
	"testing"
)

// A reply is an object. Every other JSON value unmarshals into the zero Reply
// without error, which reads as a plugin that answered and had no objection —
// so a gate that printed one and then failed was recorded as having permitted
// the call. `null` is not a contrived case: it is exactly what a jq pipeline
// prints when nothing matches its filter.
func TestGateDeniesWhenAPluginPrintsANonObject(t *testing.T) {
	skipWithoutShell(t)
	for _, tc := range []struct {
		name, printed string
	}{
		{"null", "null"},
		{"a bare string", `"deny"`},
		{"a list", `[{"deny":true}]`},
		{"a number", "0"},
		{"a boolean", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// Printing it and then failing is the shape that was measured: the
			// failure alone would close the gate, and the unreadable reply is
			// what stopped it from doing so.
			writePlugin(t, root, "jq-ish", `
name: jq-ish
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '"+tc.printed+"'\nexit 1\n")

			m := NewManager([]string{root})
			_ = m.Load()

			reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
			if !reply.Deny {
				t.Fatalf("a plugin that printed %s and failed was read as permission: %+v", tc.printed, reply)
			}
			if !strings.Contains(reply.Reason, "jq-ish") {
				t.Fatalf("reason = %q, want it to name the plugin that failed", reply.Reason)
			}
		})
	}
}

// The same on a clean exit: printing something that is not a reply is not the
// same as declining to answer, which is what saying nothing at all means.
func TestGateDeniesWhenAPluginPrintsNullAndSucceeds(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "nuller", `
name: nuller
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho null\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if !reply.Deny {
		t.Fatalf("a plugin that printed null was read as an answer: %+v", reply)
	}
}

// An object is still an object however empty it is: "{}" is a plugin saying it
// has looked and has no objection, and must stay distinguishable from `null`.
func TestGateStillAcceptsAnEmptyObject(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "shrug", `
name: shrug
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{}'\nexit 1\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if reply.Deny {
		t.Fatalf("an empty object was read as no answer at all: %+v", reply)
	}
}

// An observer that prints a non-object has said nothing usable either, but a
// broken observer must not break the agent: only the gate refuses.
func TestObserverPrintingANonObjectIsIgnored(t *testing.T) {
	skipWithoutShell(t)
	root := t.TempDir()
	writePlugin(t, root, "watcher", `
name: watcher
command: ./run.sh
hooks: [post_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho null\nexit 1\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PostToolCall, Tool: "terminal", Result: "original"})
	if reply.Deny {
		t.Fatalf("a failing observer denied something: %+v", reply)
	}
	if reply.Result != "" {
		t.Fatalf("result = %q, want nothing adopted from an unreadable reply", reply.Result)
	}
}
