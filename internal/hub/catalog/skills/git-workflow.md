---
name: git-workflow
description: Branch, commit, and open pull requests cleanly. Use for any work that will be reviewed or merged.
tags: [git, engineering]
triggers: [commit, branch, pull request, PR, merge, rebase]
---

# Working with git

## Before starting

Check `git status` and `git log --oneline -5`. Know what branch you are on and
whether the tree is clean. If you are on the default branch, make a branch
before the first commit.

## Commits

One commit per idea. A commit that fixes a bug and reformats a file is two
commits.

The message says why, not what — the diff already says what:

    Reject expired tokens at the edge

    Validation ran after the session was created, so an expired token
    still produced a usable session for the rest of the request.

Write in the imperative, no trailing period on the subject, wrap the body at
72 columns.

## Before pushing

- `git diff --staged` and read it. Debug prints, stray files, secrets.
- Run the tests.
- `git log` on your branch: does the history tell a story someone could
  follow? Squash the "fix typo" commits.

## Pull requests

The description says what changed, why, and how it was verified. Link the
issue. Call out anything a reviewer should look at hardest, and anything you
were unsure about — a PR that admits its weak spot gets a better review.

## What not to do

- Force-push a shared branch.
- Commit generated files or dependencies.
- Amend a commit that has already been pushed and reviewed.
