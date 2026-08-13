# Tool Path Determinism Design

## Summary

An end-to-end review of the harness, the tool implementations, the provider
adapters, the MCP bridge, and context assembly found roughly 36 substantiated
defects. They are not 36 unrelated mistakes. They are four assumptions, each
repeated in many places:

1. **Position stands in for identity.** A tool result is recognised by sitting
   next to its call rather than by carrying its id.
2. **A byte stands in for a character.** Text is cut at byte offsets.
3. **A name stands in for a capability.** What a tool may do is decided by how
   it is spelled.
4. **A failure is reported as a success.** Parse errors, early stream ends, and
   skipped files all produce cheerful empty results.

This design removes those four assumptions from the paths that carry tool calls
and tool results. It does not attempt all 36 findings; the rest are recorded
here as deferred, with the reason.

## Goals

- A tool result reaches the model whenever the tool actually ran.
- Text handed to a provider, stored, or displayed is always valid UTF-8.
- A destructive command is classified by what the tool can do, not its name.
- A failure surfaces as a failure, naming what went wrong.
- Every fix is pinned by a test that fails before it and passes after.

## Non-goals

- Fixing all 36 findings in one pass.
- Changing the tool-calling wire protocol or any provider's request shape
  beyond the specific defects named below.
- Reworking compaction strategy, context-window sizing, or RAG ranking.
- Any behaviour that cannot be proven by a test in this repository.

## Evidence

Every defect below was reproduced by running code, not by reading it. Five were
proven directly in the repository worktree
(`internal/agent/harness_hypothesis_probe_test.go`, currently red on purpose);
the rest were proven in throwaway copies by four parallel reviewers.

One finding was checked against the live provider the deployment actually uses
(`glm-5.2` through the local OpenAI-compatible proxy) and found **latent**: that
proxy emits `index` on every tool-call frame, sends `[DONE]`, and does not
repeat the function name. The streaming-correlation defects are therefore real
for other providers but are not currently harming this deployment, which is why
they sit at the end of the plan rather than the front.

## Pattern 1 — Identity comes from `tool_call_id`

### The defect

`ensureToolResults` (`internal/agent/agent.go:1199-1246`) accepts a tool message
only when it sits immediately after the assistant turn that called it, and
silently drops any other tool message. The agent loop violates that adjacency
itself: when the repetition guard fires it appends a user-role nudge at
`agent.go:620` *before* `executeTools` appends the results, producing
`[assistant(tool_calls), user(nudge), tool(result)]`.

Every real result is then discarded and replaced with
`"[no result recorded — the previous run was interrupted before this tool
finished]"` — a statement that is not true. The model concludes its work did not
happen and repeats it, which drives the same guard to `exceeded()` and aborts
the run.

The guard fires on ordinary work because `repeatKey`
(`internal/agent/harness.go:159-177`) fingerprints `edit_file` and `write_file`
by path alone, discarding the arguments. Three different edits to one file are
therefore "the same call three times" at the default `repeat_limit: 3`.

### The change

`ensureToolResults` indexes every tool message in the transcript by
`ToolCallID`, then for each assistant turn emits that turn followed by its
results in call order. Messages of other roles that were interleaved are emitted
after the results they were mixed into, so nothing is dropped and ordering stays
valid for providers. A stub is written only when no result exists for a call id,
and it says that no result was recorded — it does not assert an interruption.

`repeatKey` loses its `write_file`/`edit_file` special case and fingerprints
every tool the same way, on the normalised arguments. Identical arguments remain
a repeat; different arguments are progress. The `vps_upload` special case goes
for the same reason.

The nudge is appended after the tool results rather than before, so the guard
cannot produce an invalid transcript even if a future caller reintroduces
adjacency assumptions.

### Same pattern, separate site

`follow` (`internal/server/livechat.go:71-75`) clamps its cursor to `lr.base`
only in the outer loop, and drops the lock around `send`. A publisher trimming
the event window while a slow follower is inside a send moves `lr.base` past the
cursor, and `lr.events[i-lr.base]` panics with a negative index, killing the SSE
connection mid-turn. The cursor is re-clamped after every lock re-acquisition.

## Pattern 2 — Cut on runes, report runes

### The defect

Byte slicing on UTF-8 strings appears in at least six places:
`trimForModel` (`agent.go:1169-1179`), `truncate` (`compact.go:291-296`),
`readCapped` (`prompt.go:233-242`), the auto-context assembler
(`ragcontext.go:177-179`), the reranker's candidate body (`rag/rerank.go`), and
`plugin.truncate` (`plugin/plugin.go:342-347`).

Any non-ASCII content over the limit reaches the model with broken bytes at the
seams, which `json.Marshal` rewrites as U+FFFD. `trimForModel` additionally
reports the count in bytes while calling them characters, which for CJK text is
wrong by a factor of three.

### The change

One helper, `textutil.TruncateRunes`, is introduced and used at every site. It
never splits a rune, and it reports the number of runes removed. Head-and-tail
truncation keeps both ends on rune boundaries. The notice text names runes,
because that is what was measured.

## Pattern 3 — Capability, not spelling

### The defect

`dangerIn` (`internal/agent/approval.go:199-207`) returns immediately unless the
tool is named exactly `terminal`. `vps_run` executes arbitrary shell on a remote
host and is never scanned; in the default `approval_mode: auto` a remote root
wipe runs without even a transcript notice. Unparseable arguments also return
`""`, which reads as "safe".

`untrustedTool` (`agent.go:1141-1147`) has the same shape: a fixed list of four
names decides which output is treated as attacker-controlled.

### The change

A tool that carries a shell command declares it:

```go
// ShellCommander is implemented by tools that execute a shell command, whether
// locally or on a remote host.
type ShellCommander interface {
    ShellCommand(args json.RawMessage) (string, bool)
}
```

`terminal` and `vps_run` implement it. `dangerIn` takes the resolved tool and
asks it for the command instead of comparing names, so a future tool that runs
commands is covered when it is written rather than when someone remembers to
extend a list. Arguments that fail to parse are treated as requiring approval
rather than as safe.

Untrusted output moves to the same shape: an `UntrustedOutput() bool` capability
on the tool, with the MCP prefix rule retained for dynamically registered tools.

The regex table stays, but only as the human-readable *reason*. It is not the
gate; the structural `NeedsApproval` check is. A denylist can never be complete,
and this design does not pretend otherwise.

One entry in it is nonetheless plainly broken and is corrected. The recursive
delete pattern requires the path to be exactly `/`, `~`, `$HOME`, or `*`
followed by whitespace or end of line, so `rm -rf /home/nvdorman` and
`rm -rf ~/projects` — deleting a home directory and a project tree — are not
matched at all, while `rm -rf /` is. The pattern is corrected to match a
recursive delete of any absolute or home-relative path, which is what the
message it prints already claims to describe.

## Pattern 4 — A failure is a failure

Four sites turn a failure into a success:

**The plugin policy gate.** `call` (`plugin/plugin.go:320-329`) returns before
parsing stdout whenever the process exits non-zero or times out, and `Dispatch`
(`plugin.go:245-250`) then continues as though the plugin had no opinion. For
observational events that is right. For `pre_tool_call`, which is the only
policy gate in the codebase, it means a script that prints `{"deny":true}` and
exits non-zero permits the call. Stdout is parsed regardless of exit status, and
a failure on `pre_tool_call` denies.

**MCP content.** `Call` (`mcp/client.go:256-272`) understands only `text`, so an
embedded resource loses its text, an unknown type vanishes, and the fallback
`"(no content returned)"` is returned with `IsError` false. Embedded resource
text and blobs are decoded; content the client cannot represent becomes an error
naming the type, distinct from a genuinely empty result.

**grep.** Files over 8 MiB are skipped silently (`tools/search.go:311-313`), and
when the skipped file held the only match the tool reports `No matches`. Skips
are recorded and surfaced in the warnings the header already carries.

**Stream framing.** No adapter requires a terminal marker, so a body that ends
early is a complete answer; and `toolCallAccumulator.result`
(`llm/client.go:526-528`) substitutes `"{}"` for arguments that never arrived,
producing a dispatchable call with no parameters. Adapters track whether the
provider's terminal event was seen and return an error otherwise. Missing
arguments are never fabricated: a tool-use block with no argument delta is an
incomplete call and fails the turn into the existing retry path.

## Testing

The five probes already in `internal/agent/harness_hypothesis_probe_test.go`
are the acceptance criteria for Patterns 1, 2 and 3; they are red today and must
be green at the end. Each task adds its own regression tests covering the
specific defect it fixes, written before the fix and confirmed failing first.

Tests use fakes rather than live providers: fake tools for the harness, an
in-process stdio server for MCP, and recorded SSE bodies for stream framing. No
test requires a credential or network access.

## Deferred

Recorded so they are not lost, with why they are not in this wave:

- **Anthropic thinking blocks replayed as text, signature discarded.** Would
  break multi-turn tool use on the `anthropic` provider. Could not be verified —
  the configured Anthropic and OpenAI keys both return 401, and the deployment's
  active path is an OpenAI-compatible proxy. Needs a working credential first.
- **`max_tokens` sent to models that require `max_completion_tokens`.** Same
  verification problem.
- **Non-atomic file writes.** A failed write truncates the target; the fix is
  write-temp-then-rename. Real, but independent of the four patterns.
- **MCP add/delete from the dashboard orphans child processes** and leaves the
  registry stale, because the handler calls `Connect` rather than `Refresh`.
- **Context window is one global number** for every model, and the measured
  token count the provider already returns is discarded.
- **Compaction boundaries** (`compact.go`) drop tool results and can delete an
  assistant turn outright.
- **MCP server-name sanitisation collides**, so which server answers a tool call
  can change between restarts.
- **Six compaction config knobs** are declared, defaulted, and never read.
