package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/tools"
)

// lookupTool resolves a tool from the process registry, so these tests fail if
// the capability is declared on anything other than the object that runs.
func lookupTool(t *testing.T, name string) tools.Tool {
	t.Helper()
	tool, ok := tools.Default().Get(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	return tool
}

func TestDangerScanFollowsCapabilityNotName(t *testing.T) {
	cases := []struct {
		tool string
		args string
		want bool
	}{
		{"terminal", `{"command":"rm -rf /"}`, true},
		{"terminal", `{"command":"rm -rf /home/someone"}`, true},
		{"terminal", `{"command":"rm -rf ~/projects"}`, true},
		{"terminal", `{"command":"ls -la"}`, false},
		{"vps_run", `{"vps":"prod","command":"rm -rf / --no-preserve-root"}`, true},
		{"vps_run", `{"vps":"prod","command":"mkfs.ext4 /dev/sda1"}`, true},
		{"vps_run", `{"vps":"prod","command":"systemctl status nginx"}`, false},
	}
	for _, c := range cases {
		got := dangerInTool(lookupTool(t, c.tool), c.args) != ""
		if got != c.want {
			t.Errorf("%s %s -> danger=%v, want %v", c.tool, c.args, got, c.want)
		}
	}
}

func TestUnparseableArgumentsFailClosed(t *testing.T) {
	if dangerInTool(lookupTool(t, "terminal"), "not json at all") == "" {
		t.Fatal("arguments that cannot be parsed were treated as safe")
	}
}

// A tool that runs no commands has nothing for the scan to read, however
// alarming its arguments look.
func TestAToolWithoutAShellIsNotScanned(t *testing.T) {
	if why := dangerInTool(lookupTool(t, "write_file"), `{"path":"sudo.txt"}`); why != "" {
		t.Errorf("write_file was scanned for shell danger: %s", why)
	}
}

// Execute reads the arguments with a decoder that stops at the end of the
// first JSON value, so a scan that rejected the trailing byte would answer
// "could not be read" about a root wipe the shell is still about to run.
func TestTrailingDataCannotHideTheCommand(t *testing.T) {
	why := dangerInTool(lookupTool(t, "terminal"), `{"command":"rm -rf /"} x`)
	if why != "it deletes everything at that path" {
		t.Fatalf("reason = %q, want the root wipe named", why)
	}
}

// The reason is the sentence approvalMessage puts in front of whoever is
// deciding, so it has to describe the command in hand. Calling routine cleanup
// a wipe of home or root teaches people to skim the question.
func TestDeleteReasonsDescribeWhatIsDeleted(t *testing.T) {
	const (
		everything = "it deletes everything at that path"
		wholeTree  = "it deletes a whole directory tree"
	)
	cases := []struct {
		cmd  string
		want string
	}{
		{"rm -rf /", everything},
		{"rm -rf ~", everything},
		{"rm -rf $HOME", everything},
		{"rm -f *", everything},
		{"rm -rf /home/someone", wholeTree},
		{"rm -rf ~/projects", wholeTree},
		{"rm -rf /tmp/scratch-1234", wholeTree},
		{"rm -Rf /var/lib/thing", wholeTree},
		{"rm -f /tmp/build.log", ""},
		{"rm -f ~/.cache/thumb.png", ""},
		{"rm -f *.o", ""},
		{"npm run build && rm -f /tmp/x.log", ""},
		{"git rm -f /tmp/x", ""},
	}
	for _, c := range cases {
		got := dangerInTool(lookupTool(t, "terminal"), `{"command":`+quote(c.cmd)+`}`)
		if got != c.want {
			t.Errorf("%s -> %q, want %q", c.cmd, got, c.want)
		}
	}
}

// The remote wipe this refactor exists for: auto mode runs it, but it has to
// say so.
func TestAutoModeNamesADangerousRemoteCommand(t *testing.T) {
	a := agentWithMode("auto")
	var notices []string
	emit := func(e Event) error {
		if e.Type == EventNotice {
			notices = append(notices, e.Message)
		}
		return nil
	}
	call := llm.ToolCall{ID: "1", Name: "vps_run", Arguments: `{"vps":"prod","command":"rm -rf / --no-preserve-root"}`}
	if res := a.checkApproval(context.Background(), call, lookupTool(t, "vps_run"), "s", emit); res != nil {
		t.Fatalf("auto mode blocked: %s", res.Content)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "deletes") {
		t.Fatalf("expected a notice that the remote command deletes a tree, got %v", notices)
	}
}
