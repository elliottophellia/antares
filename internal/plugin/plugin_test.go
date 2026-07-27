package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePlugin creates a plugin directory with a shell script for a body.
func writePlugin(t *testing.T, root, name, manifest, script string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if script != "" {
		path := filepath.Join(dir, "run.sh")
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadAndDispatch(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "watcher", `
name: watcher
description: watches tool calls
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"notice\":\"seen\"}'\n")

	m := NewManager([]string{root})
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	if m.Count() != 1 {
		t.Fatalf("loaded %d plugins", m.Count())
	}

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if reply.Notice != "seen" {
		t.Fatalf("notice = %q", reply.Notice)
	}
}

func TestPluginCanDenyACall(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "guard", `
name: guard
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"deny\":true,\"reason\":\"not on my watch\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "terminal"})
	if !reply.Deny || reply.Reason != "not on my watch" {
		t.Fatalf("got %+v", reply)
	}
}

func TestDenyStopsLaterPlugins(t *testing.T) {
	root := t.TempDir()
	// Ordered by name, so "a" runs first and refuses.
	writePlugin(t, root, "a-guard", `
name: a-guard
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"deny\":true,\"reason\":\"no\"}'\n")
	writePlugin(t, root, "b-rewriter", `
name: b-rewriter
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"arguments\":\"{}\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall})
	if !reply.Deny {
		t.Fatal("a refusal was overridden by a later plugin")
	}
	if reply.Arguments != "" {
		t.Fatal("a later plugin still changed the arguments after a refusal")
	}
}

func TestPluginSeesThePayload(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "echoer", `
name: echoer
command: ./run.sh
hooks: [pre_tool_call]
`, "#!/bin/sh\nIN=$(cat)\nprintf '{\"notice\":%s}' \"$(printf '%s' \"$IN\" | tr -d '\\n' | sed 's/.*\"tool\":\"\\([^\"]*\\)\".*/\"\\1\"/')\"\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall, Tool: "write_file"})
	if reply.Notice != "write_file" {
		t.Fatalf("the plugin did not receive the tool name, got %q", reply.Notice)
	}
}

func TestChainedRewrites(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "a-first", `
name: a-first
command: ./run.sh
hooks: [post_tool_call]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"result\":\"one\"}'\n")
	writePlugin(t, root, "b-second", `
name: b-second
command: ./run.sh
hooks: [post_tool_call]
`, "#!/bin/sh\nIN=$(cat)\ncase \"$IN\" in *'\"result\":\"one\"'*) echo '{\"result\":\"two\"}' ;; *) echo '{\"result\":\"did-not-see-first\"}' ;; esac\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: PostToolCall, Result: "original"})
	if reply.Result != "two" {
		t.Fatalf("result = %q — the second plugin should see the first one's change", reply.Result)
	}
}

func TestBrokenPluginIsReportedNotFatal(t *testing.T) {
	root := t.TempDir()
	// No manifest at all.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A manifest with no command.
	writePlugin(t, root, "nocommand", "name: nocommand\nhooks: [turn_end]\n", "")
	// A manifest naming a hook that does not exist.
	writePlugin(t, root, "badhook", "name: badhook\ncommand: ./run.sh\nhooks: [not_a_hook]\n", "#!/bin/sh\n")
	// One that works.
	writePlugin(t, root, "good", "name: good\ncommand: ./run.sh\nhooks: [turn_end]\n",
		"#!/bin/sh\ncat > /dev/null\necho '{}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	list := m.List()
	if len(list) != 4 {
		t.Fatalf("expected all four to be listed, got %d", len(list))
	}
	broken := 0
	for _, p := range list {
		if p.Error != "" {
			broken++
		}
	}
	if broken != 3 {
		t.Fatalf("expected three broken plugins, got %d", broken)
	}
	if m.Count() != 1 {
		t.Fatalf("only the working plugin should count, got %d", m.Count())
	}

	// Dispatching still works, and does not blow up on the broken ones.
	m.Dispatch(context.Background(), Payload{Event: TurnEnd})
}

func TestFailingPluginDoesNotStopTheRest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "a-crash", `
name: a-crash
command: ./run.sh
hooks: [turn_end]
`, "#!/bin/sh\nexit 1\n")
	writePlugin(t, root, "b-works", `
name: b-works
command: ./run.sh
hooks: [turn_end]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"notice\":\"still here\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: TurnEnd})
	if reply.Notice != "still here" {
		t.Fatalf("a crashing plugin stopped the others: %+v", reply)
	}
}

func TestTimeoutIsEnforced(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "slow", `
name: slow
command: ./run.sh
hooks: [turn_end]
timeout_ms: 200
`, "#!/bin/sh\nsleep 5\necho '{\"notice\":\"too late\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	// A plugin that hangs must not hang the agent.
	reply := m.Dispatch(context.Background(), Payload{Event: TurnEnd})
	if reply.Notice != "" {
		t.Fatalf("a timed-out plugin still influenced the run: %+v", reply)
	}
}

func TestOnlySubscribedHooksAreCalled(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "only-session", `
name: only-session
command: ./run.sh
hooks: [session_start]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"notice\":\"called\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	if reply := m.Dispatch(context.Background(), Payload{Event: PreToolCall}); reply.Notice != "" {
		t.Fatal("a plugin was called for an event it did not subscribe to")
	}
	if reply := m.Dispatch(context.Background(), Payload{Event: SessionStart}); reply.Notice != "called" {
		t.Fatal("a plugin was not called for the event it did subscribe to")
	}
}

func TestSetEnabled(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "toggleable", `
name: toggleable
command: ./run.sh
hooks: [turn_end]
`, "#!/bin/sh\ncat > /dev/null\necho '{\"notice\":\"on\"}'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	if !m.SetEnabled("toggleable", false) {
		t.Fatal("could not disable a plugin that exists")
	}
	if reply := m.Dispatch(context.Background(), Payload{Event: TurnEnd}); reply.Notice != "" {
		t.Fatal("a disabled plugin still ran")
	}
	if m.SetEnabled("does-not-exist", false) {
		t.Fatal("disabling an unknown plugin reported success")
	}
}

func TestNonJSONOutputIsIgnored(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "chatty", `
name: chatty
command: ./run.sh
hooks: [turn_end]
`, "#!/bin/sh\ncat > /dev/null\necho 'hello, I am a plugin'\n")

	m := NewManager([]string{root})
	_ = m.Load()

	// Garbage on stdout is a broken plugin, not an instruction.
	reply := m.Dispatch(context.Background(), Payload{Event: TurnEnd})
	if reply.Deny || reply.Notice != "" || reply.Result != "" {
		t.Fatalf("non-JSON output was acted on: %+v", reply)
	}
}

func TestSilenceIsAValidReply(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "quiet", `
name: quiet
command: ./run.sh
hooks: [turn_end]
`, "#!/bin/sh\ncat > /dev/null\n")

	m := NewManager([]string{root})
	_ = m.Load()

	reply := m.Dispatch(context.Background(), Payload{Event: TurnEnd})
	if reply.Deny || reply.Notice != "" {
		t.Fatalf("silence was misread: %+v", reply)
	}
}

func TestManifestErrorsAreDescriptive(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "badyaml", "name: [unclosed\n", "")

	m := NewManager([]string{root})
	_ = m.Load()

	list := m.List()
	if len(list) != 1 || list[0].Error == "" {
		t.Fatalf("got %+v", list)
	}
	if !strings.Contains(list[0].Error, "YAML") {
		t.Fatalf("the error does not say what is wrong: %q", list[0].Error)
	}
}
