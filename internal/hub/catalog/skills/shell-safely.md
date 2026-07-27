---
name: shell-safely
description: Run terminal commands without destroying anything. Use before any command that deletes, overwrites, or changes system state.
tags: [terminal, safety]
triggers: [rm, delete, drop, overwrite, sudo, migration]
---

# Running commands safely

## Look before you write

Before deleting or overwriting, print what will be affected. `ls` the glob,
`cat` the file, `git status` the tree. A destructive command run against the
wrong path is not recoverable by apologising afterwards.

## Prefer reversible steps

- Move to a temporary directory rather than `rm`.
- `cp file file.bak` before editing in place.
- On a git repository, commit or stash first — then any mistake is one
  `git checkout` away from undone.

## Commands that deserve a pause

`rm -rf`, `git reset --hard`, `git clean`, `git push --force`, `DROP`,
`TRUNCATE`, `chmod -R`, `chown -R`, anything piping a downloaded script into a
shell, and anything with `sudo`.

For each: say what it will do and to what, then do it. If the target is a
glob, expand the glob first and look at the list.

## Long-running work

Run it in the background and watch the log, rather than blocking on a command
that may never return. Always give network commands a timeout.

## Reporting

Show the command and its real output. If it failed, show the error rather than
describing it — the text of the error is usually the whole diagnosis.
