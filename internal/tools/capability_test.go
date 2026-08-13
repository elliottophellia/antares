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

// Whatever inspects a call and whatever runs it are handed the same bytes, so
// they have to read them the same way. Input.Bind stops at the end of the first
// JSON value; a stricter read here would let one trailing byte give the two a
// different view of the same command.
func TestCommandOfReadsArgumentsTheWayExecuteWill(t *testing.T) {
	const args = `{"command":"rm -rf /"} x`

	scanned, ok := CommandOf(terminalTool{}, json.RawMessage(args))
	if !ok {
		t.Fatal("trailing data made the command unreadable to the scan")
	}
	var bound struct {
		Command string `json:"command"`
	}
	in := Input{Args: json.RawMessage(args)}
	if err := in.Bind(&bound); err != nil {
		t.Fatalf("Bind rejected what Execute will be given: %v", err)
	}
	if scanned != bound.Command {
		t.Fatalf("the scan reads %q, Execute will run %q", scanned, bound.Command)
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
