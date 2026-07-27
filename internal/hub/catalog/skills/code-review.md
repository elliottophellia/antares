---
name: code-review
description: Review a diff for defects that matter. Use when asked to review code, a pull request, or a change before merging.
tags: [engineering, review]
triggers: [review, pull request, PR, diff, merge]
---

# Reviewing a change

Read the whole diff first, then go back and comment. A review written while
reading the first file is a review of the first file.

## What to look for, in order

1. **Correctness** — does it do what it claims? Trace one realistic input all
   the way through. Check the boundaries: empty, one, many, and the error path.
2. **Concurrency and lifetime** — shared state written from two places, a
   context that is never cancelled, a resource that is never closed.
3. **Security** — untrusted input reaching a query, a shell, a path, or a
   template. Secrets in logs or in the repo.
4. **Interface** — is the public shape one a caller can use wrongly? An easily
   misused API costs more than an ugly one.
5. **Tests** — do they test behaviour, or do they restate the implementation?
   A test that passes when the code is wrong is worse than no test.
6. **Style** — last, and only when it hurts readability.

## Writing the comment

Say what is wrong, what happens because of it, and what to do instead. A
finding with no failure scenario attached is usually a preference in disguise.

Bad: "This is inefficient."
Good: "This re-reads the file inside the loop, so a 10k-row import does 10k
opens. Hoist it above the loop."

## What not to do

- Do not rewrite the author's approach because you would have done it
  differently. Comment on the code that exists.
- Do not pile on trivia. Three real findings land; twenty nitpicks do not.
- Do not approve something you did not understand — say which part you could
  not follow and ask.
