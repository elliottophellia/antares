# Verbatim Read, Exact Edit

## Summary

`read_file` will return the file's bytes verbatim, with the line range stated
in a header rather than stamped onto every line. `edit_file` will go back to
writing only when `old_string` is present in the file exactly, and writing
exactly `new_string`.

This removes roughly 500 lines of matching machinery that exists only to undo
the damage the line-number prefix causes.

## The defect this fixes

At `v0.2.0` `edit_file` was 49 lines with no helpers: count the occurrences,
refuse zero, refuse more than one without `replace_all`, replace, write. It
held two properties that everything since has eroded:

1. It never wrote unless `old_string` was in the file byte for byte.
2. What it wrote was exactly `new_string`.

Both are now violated, and the violations are silent. Driven through the real
tool against real files:

```
old_string = "    if enabled:"   against a tab-indented Python file
  tool said : Edited app.py (1 replacement(s)) [matched unique near line for adjacent insertion]
  on disk   : "def main():\n\tif enabled:\n        setup()\n\t\trun()\n\t\treturn 0\n"
  python3   : TabError: inconsistent use of tabs and spaces in indentation, line 3
```

`old_string` appears nowhere in that file. The tool wrote anyway, produced a
file that will not compile, and reported success.

```
old_string = "2|2|bob|user", new_string = "2|bob|admin"
  tool said : Edited rows.psv (1 replacement(s)) [stripped read_file NUMBER| prefixes]
  on disk   : "1|alice|admin\nbob|admin\n"
```

The row's id was deleted, because prefix stripping is applied to `new_string`
— which the model authors and never copies from `read_file`.

```
old_string = "9|pending", replace_all = true, against three rows numbered 1..3
  tool said : Edited queue.psv (3 replacement(s)) [stripped read_file NUMBER| prefixes]
  on disk   : "1|done\n2|done\n3|done\n"
```

An anchor matching zero times rewrote every row.

```
old_string = "9|pending", replace_all = false
  tool said : old_string appears 3 times in queue.psv; add unique surrounding
              context ... Current match line(s): 1, 2, 3.
```

That string appears zero times. The advice cannot succeed, because the problem
is the anchor, not the context — which is what a retry loop looks like from the
inside.

## Root cause

Commit `d6762c7`, titled "fix(tools): preserve exact file editing", changed one
line:

```diff
-	fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
+	fmt.Fprintf(&b, "%d|%s\n", i+1, lines[i])
```

The original separator was a tab, and tab-indented source starts with a tab, so
the boundary between prefix and content was ambiguous — and the number's
padding was spaces, so a model copying what it saw produced spaces where the
file had tabs. That is the whitespace ambiguity behind the original patch
failures.

The change swapped one colliding separator for another. Pipes occur legitimately
at the start of a line in markdown tables and pipe-separated data, so
`read_file` now emits lines like `3|| Date | Event |`, and the prompt's
instruction that "the `|` is metadata only" is true of the first pipe only.

Every helper added since is an attempt to recover from that collision at
match time: `stripReadFileLinePrefixes`, `resolveEditMatch`'s staged fallbacks,
and the `spliceAdjacentInsertion` family with its similarity, token-set,
edit-distance and digit gates. Three separate commits each fixed a silent
corruption in that family. The cure was applied to the matching layer; the
disease is in the format.

## Goals

- What `read_file` shows is what the file contains, with nothing to strip.
- `edit_file` writes only on an exact match, and writes exactly what it was given.
- A failed edit produces an error that names the real reason and a next step
  that can work.
- `grep` and `read_file` agree on line numbers.
- The prompt describes what the code does.

## Non-goals

- Fuzzy, approximate, or "close enough" matching in any form.
- Changing `write_file`, `list_files`, or path resolution.
- Changing the checkpoint or approval behaviour of `edit_file`.
- Preserving the `NUMBER|CONTENT` format for compatibility. Nothing outside the
  file tools, their tests, the prompt and `docs/tools.md` consumes it.

## `read_file`

Content is returned verbatim: the file's bytes, unmodified, including CRLF and
tabs. The only additions are a header line and, when the read was clipped, the
existing continuation note.

```
app.py — lines 1-40 of 120

def main():
	if enabled:
		run()
```

The header is exactly `<relative path> — lines <first>-<last> of <total>`,
followed by one blank line, then the content. When the whole file fits it still
appears, so the shape never varies. The same numbers are carried in `Meta` as
`path`, `first_line`, `last_line` and `total_lines`, so callers that want them
do not parse prose.

Two current behaviours are kept, because they protect against something real
rather than guessing at intent: the 400 KB cap with its explicit notice, and
the rejection of content that is not valid UTF-8. The cap continues to trim on
a rune boundary.

One behaviour is dropped: the current code replaces lone `\r` with `\n` for
display. Verbatim means verbatim, and `edit_file` now matches what was shown.

## `edit_file`

The contract returns to `v0.2.0`: count exact occurrences of `old_string`;
zero is an error; more than one without `replace_all` is an error; otherwise
replace and write exactly `new_string`.

Deleted entirely: `spliceAdjacentInsertion`, `editLineSimilarity`,
`editTokenSet`, `nearEditDistance`, `digitsOfLine`, and
`stripReadFileLinePrefixes`. With verbatim reads there is no prefix to strip
and no reason to guess which line was meant.

Kept, because the CRLF recovery below needs them: `fileEOL`, `eolOf` and
`toEOL`. `resolveEditMatch` survives as well, reduced to two stages — verbatim,
then the CRLF retry — with its staged prefix-stripping fallbacks removed.
`lineSpans` stays and becomes the shared line splitter described under "Line
numbering".

### The one recovery that stays

If the file uses CRLF and `old_string` does not match, the tool retries with
`old_string` translated from LF to CRLF. This is not a guess: it is a lossless
normalisation of a well-defined ambiguity, since a model may emit `\n` for a
line break regardless of what it read.

Three rules bound it. It runs only after the verbatim match has already failed.
`new_string` is translated only when `old_string` had to be, so a replacement is
never rewritten on a path that matched exactly. And the result message says the
translation happened, so the caller is never told "exact" when it was not.

### Errors

The existing diagnostics are good and are kept — the near-miss hint, the
occurrence line numbers, and above all the message that names a tab-versus-space
mismatch, which today is unreachable because the splice fires first and reports
success instead.

One rule is added: every count and every line number in an error must describe
the string the caller actually sent. Reporting occurrences of a rewritten anchor
is what turns one failed edit into a loop.

## Line numbering

`read_file`'s header, `grep`'s match lines, and `edit_file`'s occurrence lines
must count the same way. Today they do not: `read_file` splits on `\n` and so
reports a phantom trailing line for a newline-terminated file, while `grep`'s
scanner does not, and the two disagree entirely on a file terminated with lone
`\r`. A single shared splitter serves all three.

## Prompt

The tool notes are rewritten to match the code. The instruction to strip a
`NUMBER|` prefix goes, since there is no prefix. The claim that `edit_file`
"requires an exact, unique old_string" stays, and becomes true. The advice to
re-read before editing stays, because it is sound. `docs/tools.md` is updated
in the same pass.

## Testing

The four cases in the "defect" section above are the acceptance criteria. They
are written as tests that drive the real tool against real files on disk, and
they must fail before the change and pass after.

Beyond those: a verbatim round trip on a file containing tabs, CRLF, a lone
`\r`, and a markdown table, asserting that text copied out of `read_file`
matches with `edit_file` unchanged; the CRLF recovery, asserting both that it
works and that the message discloses it; that `new_string` is written byte for
byte on both the exact and the recovery path; and that `read_file`, `grep` and
`edit_file` report the same line number for the same line across all four line
terminators.
