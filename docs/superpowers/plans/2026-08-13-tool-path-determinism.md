# Tool Path Determinism Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the four assumptions the tool path is built on — position as identity, byte as character, name as capability, failure as success — from the code that carries tool calls and tool results.

**Architecture:** Each task fixes one assumption at one site, guarded by a test written first. No task changes a provider's wire format beyond the specific defect named. Shared behaviour (rune-safe truncation, capability interfaces) is introduced once and adopted at every call site in the same task, so no site is left on the old path.

**Tech Stack:** Go 1.x, standard library only. Tests use `go test`, fakes, and recorded fixtures — never a live provider or a credential.

## Global Constraints

- Design spec: `docs/superpowers/specs/2026-08-13-tool-path-determinism-design.md`. Its wording governs; where this plan and the spec disagree, stop and ask.
- Repository: worktree `/home/nvdorman/antares/.worktrees/harness-review`, branch `refactor/harness-tool-calls`, based on upstream `enowdev/antares` main at `51d860f`.
- `internal/agent/harness_hypothesis_probe_test.go` holds five probes that are red today. They are acceptance criteria, not scratch work. Do not delete or weaken them; Tasks 2, 3, 1 and 5 turn them green.
- Every task writes its failing test first, runs it, confirms it fails for the intended reason, then implements.
- No new third-party dependency.
- No test may require network access, an API key, or a running daemon.
- Run `gofmt -l .` before each commit; it must print nothing.
- Do not fix defects outside your task. The spec's "Deferred" section is out of scope; if you believe a deferred item blocks your task, stop and report rather than widening scope.
- Existing behaviour not named in your task must keep working: run the full package test suite for every package you touch.

---

### Task 1: Rune-safe truncation everywhere

**Files:**
- Create: `internal/textutil/truncate.go`
- Create: `internal/textutil/truncate_test.go`
- Modify: `internal/agent/agent.go` (`trimForModel`, ~1169-1179)
- Modify: `internal/agent/compact.go` (`truncate`, ~291-296)
- Modify: `internal/agent/prompt.go` (`readCapped`, ~233-242)
- Modify: `internal/agent/ragcontext.go` (~177-179)
- Modify: `internal/rag/rerank.go` (candidate body slice, ~`body[:1200]`)
- Modify: `internal/plugin/plugin.go` (`truncate`, ~342-347)

**Interfaces:**
- Produces: `textutil.TruncateRunes(s string, limit int) string` and
  `textutil.TruncateMiddle(s string, limit int) (out string, removed int)`.
  `limit` counts runes. `TruncateMiddle` keeps a head and a tail and returns how
  many runes it removed, for callers that print a notice.

- [ ] **Step 1: Write the failing test**

```go
package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunesNeverSplitsARune(t *testing.T) {
	for _, limit := range []int{1, 7, 33, 99} {
		got := TruncateRunes(strings.Repeat("é", 200), limit)
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
		}
		if n := utf8.RuneCountInString(got); n > limit {
			t.Fatalf("limit %d produced %d runes", limit, n)
		}
	}
}

func TestTruncateMiddleKeepsBothEndsAndCountsRunes(t *testing.T) {
	in := strings.Repeat("あ", 300)
	out, removed := TruncateMiddle(in, 51)
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8: %q", out)
	}
	if removed != 300-51 {
		t.Fatalf("removed = %d, want %d", removed, 300-51)
	}
	if !strings.HasPrefix(out, "あ") || !strings.HasSuffix(out, "あ") {
		t.Fatalf("head or tail missing: %q", out)
	}
}

func TestTruncateShorterThanLimitIsUnchanged(t *testing.T) {
	if got := TruncateRunes("héllo", 50); got != "héllo" {
		t.Fatalf("got %q", got)
	}
	if out, removed := TruncateMiddle("héllo", 50); out != "héllo" || removed != 0 {
		t.Fatalf("got %q, %d", out, removed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/textutil/ -v`
Expected: build failure — package does not exist.

- [ ] **Step 3: Implement the helper**

Count runes, never slice mid-rune. `TruncateMiddle` splits the budget two-thirds head, one-third tail, matching the existing `trimForModel` proportions.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/textutil/ -v`
Expected: PASS.

- [ ] **Step 5: Adopt it at every call site**

Replace each byte slice listed under Files. `trimForModel` uses
`TruncateMiddle` and prints the rune count it returns, so the notice reads
`… %d characters truncated …` with a number that is now actually the count of
characters removed rather than bytes. Every other site uses `TruncateRunes`.
`Tools.MaxOutputChars` keeps its name and its default of 60000; it is now
interpreted as runes, which is what the field already claims to be.

- [ ] **Step 6: Verify the harness probe turns green**

Run: `go test ./internal/agent/ -run TestProbeTrimForModelKeepsValidUTF8 -v`
Expected: PASS.

- [ ] **Step 7: Run every touched package**

Run: `go test ./internal/textutil/ ./internal/agent/ ./internal/rag/ ./internal/plugin/ -count=1`
Expected: all pass except the four probes owned by Tasks 2, 3 and 5, which stay red.

- [ ] **Step 8: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Cut text on runes, not bytes"
```

---

### Task 2: Correlate tool results by call id

**Files:**
- Modify: `internal/agent/agent.go` (`ensureToolResults`, ~1199-1246)
- Test: `internal/agent/tool_results_test.go` (create)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `ensureToolResults([]llm.Message) []llm.Message` keeps its signature. Its contract becomes: every tool message whose `ToolCallID` matches a call in the transcript is emitted directly after that call's assistant turn, in the order the calls were made; messages of other roles keep their relative order but are emitted after any tool results they were interleaved with; a call with no matching result gets a stub.

- [ ] **Step 1: Write the failing test**

```go
package agent

import (
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func TestEnsureToolResultsMatchesByCallID(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "read_file"},
			{ID: "c2", Name: "grep"},
		}},
		{Role: llm.RoleUser, Content: "a nudge that slipped in"},
		{Role: llm.RoleTool, ToolCallID: "c2", Name: "grep", Content: "GREP OUT"},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "read_file", Content: "FILE OUT"},
	}

	out := ensureToolResults(history)

	// The assistant turn is followed immediately by its results, in call order.
	var ai int = -1
	for i, m := range out {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) == 2 {
			ai = i
		}
	}
	if ai < 0 {
		t.Fatal("assistant turn missing")
	}
	if out[ai+1].ToolCallID != "c1" || out[ai+1].Content != "FILE OUT" {
		t.Fatalf("first result = %+v", out[ai+1])
	}
	if out[ai+2].ToolCallID != "c2" || out[ai+2].Content != "GREP OUT" {
		t.Fatalf("second result = %+v", out[ai+2])
	}
	// The interleaved user message survives, after the results.
	found := false
	for _, m := range out[ai+3:] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "nudge") {
			found = true
		}
	}
	if !found {
		t.Fatal("interleaved user message was dropped")
	}
}

func TestEnsureToolResultsStubsOnlyMissingCallsAndTellsTheTruth(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "read_file"},
			{ID: "c2", Name: "grep"},
		}},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "read_file", Content: "FILE OUT"},
	}

	out := ensureToolResults(history)

	var stub string
	for _, m := range out {
		if m.ToolCallID == "c2" {
			stub = m.Content
		}
		if m.ToolCallID == "c1" && m.Content != "FILE OUT" {
			t.Fatalf("real result was replaced: %q", m.Content)
		}
	}
	if stub == "" {
		t.Fatal("missing call c2 was not stubbed")
	}
	if strings.Contains(stub, "interrupted") {
		t.Fatalf("stub asserts an interruption that did not happen: %q", stub)
	}
}

func TestEnsureToolResultsDropsResultsWithNoMatchingCall(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleTool, ToolCallID: "orphan", Name: "read_file", Content: "x"},
	}
	for _, m := range ensureToolResults(history) {
		if m.Role == llm.RoleTool {
			t.Fatalf("orphan tool result was kept: %+v", m)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestEnsureToolResults -v`
Expected: `TestEnsureToolResultsMatchesByCallID` fails — the results are stubbed and the real content is dropped.

- [ ] **Step 3: Implement**

Build a map from `ToolCallID` to the tool message, over the whole slice. Walk the transcript once emitting non-tool messages; on an assistant turn with `ToolCalls`, emit the turn, then for each call emit its mapped result or a stub reading `[no result was recorded for this tool call]`. Skip tool messages during the walk — they are placed by the map. A tool message whose id matches no call is dropped, as today. A duplicate id is consumed once.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -run TestEnsureToolResults -v`
Expected: PASS.

- [ ] **Step 5: Verify the harness probe turns green**

Run: `go test ./internal/agent/ -run TestProbeInterleavedNudgeKeepsRealToolResults -v`
Expected: PASS.

- [ ] **Step 6: Run the package**

Run: `go test ./internal/agent/ -count=1`
Expected: pass except the probes owned by Tasks 3 and 5.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Match a tool result to its call by id"
```

---

### Task 3: Fingerprint every tool the same way

**Files:**
- Modify: `internal/agent/harness.go` (`repeatKey`, ~154-177)
- Modify: `internal/agent/agent.go` (repeat-guard block, ~612-630)
- Test: `internal/agent/repeat_guard_test.go` (create)

**Interfaces:**
- Consumes: `ensureToolResults` from Task 2 (its ordering contract is what makes the nudge move safe).
- Produces: `repeatKey(llm.ToolCall) string` now returns `name + "\x00" + normaliseArgs(args)` for every tool, with no per-tool special case.

- [ ] **Step 1: Write the failing test**

```go
package agent

import (
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func TestRepeatKeyDistinguishesDifferentArguments(t *testing.T) {
	a := llm.ToolCall{Name: "edit_file", Arguments: `{"path":"m.go","old":"a","new":"b"}`}
	b := llm.ToolCall{Name: "edit_file", Arguments: `{"path":"m.go","old":"c","new":"d"}`}
	if repeatKey(a) == repeatKey(b) {
		t.Fatal("two different edits to one file share a fingerprint")
	}
}

func TestRepeatKeyStillCatchesAnIdenticalCall(t *testing.T) {
	a := llm.ToolCall{Name: "edit_file", Arguments: `{"path":"m.go","old":"a","new":"b"}`}
	b := llm.ToolCall{Name: "edit_file", Arguments: `{"new":"b","old":"a","path":"m.go"}`}
	if repeatKey(a) != repeatKey(b) {
		t.Fatal("the same call re-serialised was not recognised as a repeat")
	}
}

func TestRepeatTrackerTripsOnIdenticalCallsOnly(t *testing.T) {
	r := newRepeatTracker(3)
	same := llm.ToolCall{Name: "grep", Arguments: `{"pattern":"x"}`}
	for i := 1; i <= 2; i++ {
		if tripped := r.record([]llm.ToolCall{same}); len(tripped) > 0 {
			t.Fatalf("tripped early at %d", i)
		}
	}
	if tripped := r.record([]llm.ToolCall{same}); len(tripped) == 0 {
		t.Fatal("three identical calls did not trip the guard")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestRepeatKey -v`
Expected: `TestRepeatKeyDistinguishesDifferentArguments` fails — both keys are `edit_file\x00m.go`.

- [ ] **Step 3: Implement**

Delete the `write_file`/`edit_file` and `vps_upload` cases from `repeatKey`, leaving the single normalised-arguments return. In `agent.go`, move the `history = append(history, ...)` nudge so it runs after the results loop rather than before `executeTools`; keep the `EventNotice` where it is so the user still sees it immediately.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -run 'TestRepeatKey|TestRepeatTracker' -v`
Expected: PASS.

- [ ] **Step 5: Verify the harness probe turns green**

Run: `go test ./internal/agent/ -run TestProbeDistinctEditsToSameFileAreNotRepeats -v`
Expected: PASS.

- [ ] **Step 6: Run the package**

Run: `go test ./internal/agent/ -count=1`
Expected: pass except the two danger probes owned by Task 5.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Tell a stuck loop apart from ordinary progress"
```

---

### Task 4: Keep a slow SSE follower from panicking

**Files:**
- Modify: `internal/server/livechat.go` (`follow`, ~60-90)
- Test: `internal/server/livechat_follow_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `follow` never indexes below `lr.base`; a follower whose cursor was trimmed past resumes at the oldest retained event.

- [ ] **Step 1: Write the failing test**

Drive a `liveRun` from two goroutines: one follower whose send function blocks on a channel until released, one publisher that emits more than `maxLiveEvents` events while the follower is blocked. Release the follower and assert `follow` returns without panicking.

```go
func TestFollowSurvivesWindowTrimWhileSending(t *testing.T) {
	lr := newLiveRun() // use whatever the package's constructor is
	release := make(chan struct{})
	done := make(chan any, 1)

	go func() {
		defer func() { done <- recover() }()
		first := true
		_ = lr.follow(context.Background(), 0, func(agent.Event, int) error {
			if first {
				first = false
				<-release // stall inside send, with the lock dropped
			}
			return nil
		})
		done <- nil
	}()

	// Let the follower enter its first send, then overrun the window.
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < maxLiveEvents+200; i++ {
		lr.publish(agent.Event{Type: agent.EventToolProgress, Chunk: "x"})
	}
	close(release)
	lr.finish()

	if r := <-done; r != nil {
		t.Fatalf("follow panicked: %v", r)
	}
}
```

The names above are the real ones: `newLiveRun() *liveRun` (`livechat.go:31`),
`publish` (`:39`), `finish` (`:53`), `follow(ctx, cursor int, send func(agent.Event, int) error) error` (`:65`),
and `maxLiveEvents = 4000` (`:21`). The assertion — no panic — is what matters.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestFollowSurvivesWindowTrim -v`
Expected: FAIL with `index out of range [-N]`.

- [ ] **Step 3: Implement**

Inside the inner loop, after re-acquiring the lock, clamp the cursor: if `i < lr.base`, set `i = lr.base` before computing the slice offset. Do it on every iteration, not only on entry.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/server/ -run TestFollowSurvivesWindowTrim -v -race`
Expected: PASS with no race reported.

- [ ] **Step 5: Run the package**

Run: `go test ./internal/server/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Stop a stalled follower from crashing its own stream"
```

---

### Task 5: Classify danger by capability

**Files:**
- Modify: `internal/tools/registry.go` (add the interface next to `Approval`, ~97)
- Modify: `internal/tools/shell.go` (`terminalTool`, ~581)
- Modify: `internal/tools/vps.go` (`vpsRunTool`, ~85)
- Modify: `internal/tools/register.go` (add the accessor next to `NeedsApproval`, ~106)
- Modify: `internal/agent/approval.go` (`dangerIn`, `checkApproval`, the `dangerous` table)
- Test: `internal/agent/danger_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `tools.ShellCommander interface { ShellCommand(args json.RawMessage) (string, bool) }`
  - `tools.CommandOf(t Tool, args json.RawMessage) (string, bool)` — returns the command when the tool implements the interface.
  - `dangerIn(tool tools.Tool, arguments string) string` — signature changes from `(toolName, arguments string)`. Its only caller is `checkApproval`, which already holds the resolved tool.

- [ ] **Step 1: Write the failing test**

```go
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
		got := dangerIn(lookupTool(t, c.tool), c.args) != ""
		if got != c.want {
			t.Errorf("%s %s -> danger=%v, want %v", c.tool, c.args, got, c.want)
		}
	}
}

func TestUnparseableArgumentsFailClosed(t *testing.T) {
	if dangerIn(lookupTool(t, "terminal"), "not json at all") == "" {
		t.Fatal("arguments that cannot be parsed were treated as safe")
	}
}
```

Define `lookupTool` in the same test file. It resolves from the process registry
so the test proves the real wiring rather than a stub:

```go
func lookupTool(t *testing.T, name string) tools.Tool {
	t.Helper()
	tool, ok := tools.Default().Resolve(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	return tool
}
```

If the registry accessor is spelled differently in `internal/tools/registry.go`,
use that spelling — the requirement is that the tool comes from the real
registry, not a hand-built fake.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run 'TestDangerScan|TestUnparseable' -v`
Expected: compile failure on the new `dangerIn` signature, then failures on the `vps_run` and home-directory rows.

- [ ] **Step 3: Implement**

Add `ShellCommander` and `CommandOf`. `terminalTool.ShellCommand` decodes `{"command":...}`; `vpsRunTool.ShellCommand` decodes its own `{"command":...}`. `dangerIn` asks `CommandOf`; a tool that does not implement it is not scanned, and a tool that does but whose arguments fail to decode returns a fixed reason such as `its arguments could not be read, so it could not be checked`. Correct the recursive-delete regex so it matches a delete of any absolute or home-relative path, not only a bare `/`, `~`, `$HOME` or `*`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -run 'TestDangerScan|TestUnparseable' -v`
Expected: PASS.

- [ ] **Step 5: Move untrusted-output classification to the same shape**

Add `tools.UntrustedOutputer interface { UntrustedOutput() bool }`, implement it on `web_fetch`, `web_search`, `browser` and `http_request`, and rewrite `untrustedTool` in `agent.go` to consult the resolved tool, keeping the `tools.MCPPrefix` rule for dynamically registered tools. Add a test asserting each of those four is still wrapped and an ordinary tool is not.

- [ ] **Step 6: Verify both harness probes turn green**

Run: `go test ./internal/agent/ -run TestProbeDanger -v`
Expected: PASS for both.

- [ ] **Step 7: Run every touched package**

Run: `go test ./internal/agent/ ./internal/tools/ -count=1`
Expected: PASS, and every probe in `harness_hypothesis_probe_test.go` is now green.

- [ ] **Step 8: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Scan for danger by what a tool can do"
```

---

### Task 6: Make the policy gate fail closed

**Files:**
- Modify: `internal/plugin/plugin.go` (`call` ~320-329, `Dispatch` ~245-250)
- Test: `internal/plugin/gate_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: for `PreToolCall`, a plugin's stdout is parsed regardless of exit status, and a plugin that cannot be run or times out yields `Deny` with a reason. All other events keep today's fail-open behaviour.

- [ ] **Step 1: Write the failing test**

Three cases, each a small shell script written to `t.TempDir()`: one printing `{"deny":true,"reason":"policy"}` then `exit 1`; one sleeping past the timeout; one printing valid JSON and exiting 0. Assert the first two deny and the third is honoured. Add a fourth asserting a failing plugin on `PostToolCall` still does **not** deny.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/plugin/ -run TestGate -v`
Expected: the first two cases fail — the call is permitted.

- [ ] **Step 3: Implement**

In `call`, capture stdout and attempt to parse it before returning the process error. In `Dispatch`, when the event is `PreToolCall` and the plugin errored, use the parsed reply if it decoded; otherwise synthesise `Deny` with a reason naming the plugin and the failure. Leave the other events on the existing `continue`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/plugin/ -run TestGate -v`
Expected: PASS.

- [ ] **Step 5: Run the package**

Run: `go test ./internal/plugin/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Let a denying plugin deny even when it exits badly"
```

---

### Task 7: Represent MCP content honestly

**Files:**
- Modify: `internal/mcp/client.go` (`content` struct ~34-40, `Call` ~256-272, `ReadResource` ~340-344)
- Test: `internal/mcp/content_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `Call` returns the embedded resource's text; a result carrying only content types the client cannot represent is an error naming the type; an empty `contents` from `ReadResource` is "not found here" rather than a successful empty string.

- [ ] **Step 1: Write the failing test**

Table-driven over decoded `tools/call` results, using the package's existing in-process server fixture:

```go
{name: "embedded resource text", raw: `{"content":[{"type":"resource","resource":{"uri":"file:///a","mimeType":"text/plain","text":"HELLO"}}]}`, wantText: "HELLO", wantErr: false},
{name: "audio only",             raw: `{"content":[{"type":"audio","data":"...","mimeType":"audio/wav"}]}`, wantErr: true},
{name: "empty content",          raw: `{"content":[]}`,  wantErr: false, wantText: "(no content returned)"},
```

Assert the audio case's error message names `audio`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestCallContent -v`
Expected: the resource case yields `[resource: text/plain]` with no text; the audio case succeeds with `(no content returned)`.

- [ ] **Step 3: Implement**

Add a nested `Resource` field to the content struct carrying `uri`, `mimeType`, `text` and `blob`. Emit `text` when present; for a blob, emit a line naming the URI and media type. Track whether any content item was of a type the client could not represent, and when nothing renderable was produced, return an error naming those types. Keep `(no content returned)` only for a genuinely empty `content` array.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestCallContent -v`
Expected: PASS.

- [ ] **Step 5: Run the package**

Run: `go test ./internal/mcp/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Read what an MCP server actually returned"
```

---

### Task 8: Never let a skipped file read as no match

**Files:**
- Modify: `internal/tools/search.go` (`grepTool.Execute`, size gate ~311-313, warning join ~321-322)
- Test: `internal/tools/grep_skip_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: files skipped by the size gate are counted and reported through the existing `warnings` slice.

- [ ] **Step 1: Write the failing test**

Create a temp directory holding one 9 MiB file whose first line contains `NEEDLE_TOKEN`, run `grep` for `NEEDLE_TOKEN`, and assert the output mentions the skip. A second case asserts a normal small-file match still reports no warning.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tools/ -run TestGrepReportsSkippedFiles -v`
Expected: FAIL — output is `No matches for "NEEDLE_TOKEN" under …`.

- [ ] **Step 3: Implement**

Count skipped files, and when the count is above zero append a warning naming the count and the limit. Leave the 8 MiB gate itself in place.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tools/ -run TestGrepReportsSkippedFiles -v`
Expected: PASS.

- [ ] **Step 5: Run the package**

Run: `go test ./internal/tools/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Say when grep did not look at a file"
```

---

### Task 9: A stream that stopped early is an error

**Files:**
- Modify: `internal/llm/client.go` (`toolCallAccumulator.result`, ~526-528)
- Modify: `internal/llm/openai.go` (stream loop, terminal detection ~322-389)
- Modify: `internal/llm/anthropic.go` (stream loop, terminal detection ~324-400)
- Test: `internal/llm/stream_framing_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `Stream` returns a retryable error when the body ends without the provider's terminal signal; a tool call whose arguments never arrived is never emitted with `{}`.

- [ ] **Step 1: Write the failing test**

Serve recorded SSE bodies from `httptest.NewServer`:

```go
// OpenAI: a tool call whose arguments are cut mid-JSON and no [DONE].
// Expect: error, and llm.Retryable(err) == true.
// Anthropic: content_block_start for tool_use, then the body ends.
// Expect: error; no tool call with Arguments == "{}" is returned.
// Control: a complete stream with [DONE] still succeeds and yields the call.
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestStreamFraming -v`
Expected: both truncated cases return `err == nil` with a fabricated call.

- [ ] **Step 3: Implement**

Track a `sawTerminal` flag: `[DONE]` or a chunk carrying a `finish_reason` for OpenAI-compatible routes, `message_stop` for Anthropic. When the reader ends without it, return an error that `llm.Retryable` classifies as retryable, so the agent's existing turn-level retry handles it. In `toolCallAccumulator.result`, drop the `"{}"` substitution: a call with a name but no arguments is incomplete and must not be returned as complete.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/ -run TestStreamFraming -v`
Expected: PASS.

- [ ] **Step 5: Run the package hermetically**

Run: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY go test ./internal/llm/ -count=1`
Expected: PASS. The package holds opt-in live tests; unset the keys so they stay skipped.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Refuse to call a cut-off stream a finished answer"
```

---

## Final verification

After Task 9, run the whole repository hermetically:

```bash
env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY go test ./... -count=1
go vet ./...
gofmt -l .
```

All five probes in `internal/agent/harness_hypothesis_probe_test.go` must be green, and no package may regress.
