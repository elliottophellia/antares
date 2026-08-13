package tools

import (
	"encoding/json"
	"testing"
)

func TestCommandOfReadsWhateverToolHoldsAShell(t *testing.T) {
	cases := []struct {
		tool Tool
		args string
		want string
	}{
		{terminalTool{}, `{"command":"ls -la"}`, "ls -la"},
		{vpsRunTool{}, `{"vps":"prod","command":"systemctl restart nginx"}`, "systemctl restart nginx"},
		// Listing the saved servers takes no command at all.
		{vpsRunTool{}, `{"vps":"prod"}`, ""},
	}
	for _, c := range cases {
		got, ok := CommandOf(c.tool, json.RawMessage(c.args))
		if !ok {
			t.Errorf("%s %s: arguments were not readable", c.tool.Name(), c.args)
			continue
		}
		if got != c.want {
			t.Errorf("%s %s -> %q, want %q", c.tool.Name(), c.args, got, c.want)
		}
	}
}

func TestCommandOfSeparatesNoShellFromUnreadableArguments(t *testing.T) {
	if RunsShellCommands(namedTestTool("write_file")) {
		t.Error("a tool that runs no commands was reported as holding a shell")
	}
	if _, ok := CommandOf(namedTestTool("write_file"), json.RawMessage(`{"path":"a"}`)); ok {
		t.Error("a tool that runs no commands returned a command")
	}

	if !RunsShellCommands(terminalTool{}) {
		t.Fatal("the terminal was not reported as holding a shell")
	}
	if _, ok := CommandOf(terminalTool{}, json.RawMessage("not json at all")); ok {
		t.Error("unreadable arguments were reported as read")
	}
}

func TestUntrustedOutputIsDeclaredByTheToolsThatFetch(t *testing.T) {
	for _, tool := range []Tool{webFetchTool{}, webSearchTool{}, browserTool{}, httpRequestTool{}} {
		if !ReturnsUntrustedOutput(tool) {
			t.Errorf("%s does not declare that its output comes from outside", tool.Name())
		}
	}
	for _, tool := range []Tool{terminalTool{}, readFileTool{}} {
		if ReturnsUntrustedOutput(tool) {
			t.Errorf("%s declares outside content it does not fetch", tool.Name())
		}
	}
}
