# Verbatim Read, Exact Edit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `read_file` returns the file's bytes verbatim with the line range in a header, and `edit_file` writes only on an exact match and writes exactly what it was given.

**Architecture:** The line-number prefix is the disease and the ~500 lines of similarity scoring, edit distance and prefix stripping are the symptom. Task 1 gives the three tools one shared idea of what a line is. Task 2 removes the prefix. Task 3 deletes the machinery that existed to undo it. Task 4 makes the prompt and docs describe the result.

**Tech Stack:** Go, standard library only. Tests drive the real tools against real files in `t.TempDir()`.

## Global Constraints

- Design spec: `docs/superpowers/specs/2026-08-14-verbatim-read-exact-edit-design.md`. Its wording governs; where this plan and the spec disagree, stop and ask.
- Repository: worktree `/home/nvdorman/antares/.worktrees/exact-editing`, branch `fix/verbatim-read-exact-edit`, based on `refactor/harness-tool-calls` (upstream main plus the tool-path determinism work, open as PR #30).
- `internal/tools/file_exactness_acceptance_test.go` holds four acceptance tests. Three are red and Task 3 turns them green; `TestEditWritesNewStringVerbatim` is already green and must stay green. Do not weaken, skip or delete that file.
- `read_file`'s header is exactly `<relative path> — lines <first>-<last> of <total>`, then one blank line, then content. It appears even when the whole file fits.
- `Meta` carries `path`, `first_line`, `last_line`, `total_lines`.
- No fuzzy, approximate or "close enough" matching may be introduced, in any form, for any reason.
- No new third-party dependency; standard library only.
- No test may require network access, an API key, or a running daemon.
- `gofmt -l` must print nothing for the files you touch. Twenty-nine files are already unformatted upstream; leave them alone.
- Never run `git checkout --` in the worktree to undo an experiment; copy to /tmp instead.
- Run the full test suite for every package you touch. `internal/llm` holds opt-in live tests, so run the repository as `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY go test ./... -count=1 -p 4`.

---

### Task 1: One idea of what a line is

**Files:**
- Modify: `internal/tools/file.go` (`lineSpans` ~564, `readFileTool.Execute` ~193-202)
- Modify: `internal/tools/search.go` (`grepTool.Execute`, the `bufio.Scanner` loop ~236-279)
- Test: `internal/tools/line_numbering_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `lineSpans(content string) []lineSpan` becomes the single splitter used by `read_file`, `grep` and `edit_file`'s occurrence reporting. A file ending in a line terminator yields no trailing empty span. LF, CRLF and lone CR each terminate a line.

- [ ] **Step 1: Write the failing test**

Drive the real tools. For each of four files — LF, CRLF, lone CR, and no trailing terminator — put a unique token on the third line, then assert `read_file`'s reported total, `grep`'s reported match line, and `edit_file`'s occurrence line all say 3.

```go
func TestLineNumbersAgreeAcrossTools(t *testing.T) {
	for _, tc := range []struct{ name, eol string }{
		{"lf", "\n"}, {"crlf", "\r\n"}, {"cr", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "alpha" + tc.eol + "beta" + tc.eol + "NEEDLE" + tc.eol
			// read_file's header must say "of 3", grep must report line 3.
		})
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/tools/ -run TestLineNumbersAgree -v`
Expected: the LF case reports 4 total lines from `read_file` (a phantom trailing line) while `grep` says 3, and the CR case disagrees entirely.

- [ ] **Step 3: Make `lineSpans` the shared splitter**

Give `read_file` and `grep` their line boundaries from `lineSpans`. Drop the trailing empty element for a terminator-ended file.

- [ ] **Step 4: Run the test and the package**

Run: `go test ./internal/tools/ -count=1`
Expected: the new test passes; existing tests still pass.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/tools/file.go internal/tools/search.go internal/tools/line_numbering_test.go
git add -A && git commit -m "Count lines the same way in every file tool"
```

---

### Task 2: read_file returns the file

**Files:**
- Modify: `internal/tools/file.go` (`readFileTool.Description` ~143, `Execute` ~154-231)
- Modify: `internal/tools/file_edit_regression_test.go` (tests that parse `NUMBER|`)
- Test: `internal/tools/read_verbatim_test.go` (create)

**Interfaces:**
- Consumes: the shared splitter from Task 1.
- Produces: `read_file` content is `header + "\n\n" + verbatim bytes of the selected range`, plus the existing continuation and truncation notes. `Meta` gains `first_line`, `last_line`, `total_lines`; `lines` is removed.

- [ ] **Step 1: Write the failing test**

A file containing a tab-indented line, a CRLF line, and a markdown table row beginning with `|`. Assert that the bytes after the header and blank line are byte-identical to the file, and that a substring copied out of that region is found by `strings.Contains` on the original.

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/tools/ -run TestReadFileReturnsVerbatim -v`
Expected: fails — every line carries a `NUMBER|` prefix and CRLF has been normalised to LF.

- [ ] **Step 3: Implement**

Slice the file by the selected spans and emit those bytes unchanged. Build the header from the range. Keep the 400 KB cap, its rune-boundary trim, its notice, the binary rejection, and the "more lines" continuation note. Remove the lone-CR-to-LF display substitution. Update the tool description to describe verbatim output.

- [ ] **Step 4: Run the test and the package**

Run: `go test ./internal/tools/ -count=1`
Expected: the new test passes. Existing tests that parse `NUMBER|` will fail; update them to the new format in this task, but do not weaken what they assert.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Show the file, not a rendering of it"
```

---

### Task 3: edit_file writes only what it was given

**Files:**
- Modify: `internal/tools/file.go` (`editFileTool.Execute` ~329-397, `resolveEditMatch` ~506, and the helper block ~610-983)
- Modify: `internal/tools/file_edit_recovery_test.go`, `internal/tools/file_edit_regression_test.go`
- Test: `internal/tools/edit_exact_test.go` (create)

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: `resolveEditMatch(content, oldIn, newIn) (oldString, newString string, count int, how string)` keeps its signature but has two stages only — verbatim, then a CRLF retry. `how` is empty for a verbatim match and names the translation otherwise.

- [ ] **Step 1: Write the failing tests**

Beyond the four acceptance tests already in the tree: that `new_string` is written byte for byte on both stages; that the CRLF recovery works and its message discloses the translation; that an exact match reports no `how` suffix; and that a tab-versus-space anchor produces the tab diagnostic rather than a write.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/tools/ -run 'TestEdit' -v`
Expected: the three known-red acceptance tests fail as recorded in the spec, and the tab-diagnostic test fails because the splice reports success first.

- [ ] **Step 3: Delete the guessing machinery**

Remove `spliceAdjacentInsertion`, `editLineSimilarity`, `editTokenSet`, `nearEditDistance`, `digitsOfLine` and `stripReadFileLinePrefixes`, along with the branch in `Execute` that calls the splice and the prefix stages in `resolveEditMatch`. Remove `readFileLinePrefixCounts` and any diagnostic text that refers to `NUMBER|`. Keep `fileEOL`, `eolOf`, `toEOL`, `lineSpans`, `editNotFoundMessage`, `editAmbiguousMessage`, `occurrenceLines`, `nearMissHint`, `identifierTokens` and `expandTabs`.

- [ ] **Step 4: Bound the CRLF recovery**

It runs only after the verbatim match fails. `new_string` is translated only when `old_string` was. The result message says so.

- [ ] **Step 5: Make errors describe the caller's anchor**

Every count and line number in `editNotFoundMessage` and `editAmbiguousMessage` must refer to the string the caller sent.

- [ ] **Step 6: Run everything**

Run: `go test ./internal/tools/ -count=1`
Expected: all four acceptance tests green, package green. Delete or rewrite recovery tests that assert the deleted behaviour — say in your report which ones and why each no longer describes something true.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "Write only what was asked, only where it matches"
```

---

### Task 4: Say what the tools do

**Files:**
- Modify: `internal/agent/prompt.go` (tool notes ~105-111)
- Modify: `docs/tools.md`
- Test: `internal/agent/prompt_file_notes_test.go` (create)

**Interfaces:**
- Consumes: Tasks 2 and 3.
- Produces: no code behaviour; the prompt and docs match the tools.

- [ ] **Step 1: Write the failing test**

Assert the assembled prompt no longer mentions `NUMBER|` or instructs stripping, and does still tell the model to re-read before editing and to preserve tabs.

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/agent/ -run TestPromptFileNotes -v`
Expected: fails on the `NUMBER|` instruction still being present.

- [ ] **Step 3: Rewrite the notes**

Drop the stripping instruction. Keep "re-read the region before editing" and "preserve tabs and spaces exactly". State that `edit_file` matches exactly and that a failed match means the anchor is wrong, not that more context is needed. Update `docs/tools.md` to match.

- [ ] **Step 4: Run the affected packages**

Run: `go test ./internal/agent/ ./internal/tools/ -count=1`

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Describe the file tools as they now behave"
```

---

## Final verification

```bash
env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY go test ./... -count=1 -p 4
go vet ./...
```

All four acceptance tests green, and `internal/tools/file.go` should be close to half its current size.
