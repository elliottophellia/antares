---
name: test-driven-development
description: Build a feature test-first so the tests are real. Use when adding behaviour to code that has, or should have, tests.
tags: [engineering, testing]
triggers: [tdd, test first, write tests, add tests]
---

# Test-driven development

The point is not ceremony. It is that a test written after the fact tends to
test what the code happens to do, and a test written first tests what the code
is supposed to do.

## The loop

1. **Red** — write one test for one behaviour. Run it. Watch it fail, and read
   the failure: if it fails for the wrong reason, the test is wrong.
2. **Green** — write the least code that passes. Ugly is fine here.
3. **Refactor** — clean up with the test holding the behaviour still.

Then repeat for the next behaviour. Keep the loop small; if a step takes more
than a few minutes, the behaviour under test is too big.

## Naming tests

Name the behaviour, not the function: `TestRejectsExpiredToken`, not
`TestValidate3`. When it fails in CI six months from now, the name is the
whole bug report.

## What to test

- The contract: what a caller is promised.
- Boundaries: empty, one, many, maximum, and just past it.
- Failure: what happens on bad input, and does the error say something useful.

Do not test private helpers directly. If a helper needs its own test, it
probably wants to be its own unit with its own public surface.

## When not to do it

Exploratory work where the design is unknown — spike first, throw the spike
away, then build it test-first. Keeping a spike is how untested code enters a
codebase.
