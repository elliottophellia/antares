---
name: systematic-debugging
description: Find the cause of a bug instead of guessing at fixes. Use when something fails, misbehaves, or works only sometimes.
tags: [debugging, engineering]
triggers: [bug, broken, fails, error, crash, flaky, regression]
---

# Systematic debugging

Guessing is the slowest way to fix a bug. Work down this list and stop at the
first step that explains the failure.

## 1. Reproduce it

Get a command that fails every time. Write it down. If it only fails
sometimes, run it in a loop until you have a rate — "3 of 20" is a fact you
can test against later, "flaky" is not.

If you cannot reproduce it, the next job is collecting evidence, not fixing:
logs, the exact input, the version that broke, and the last version that
worked.

## 2. Read the actual error

Read the whole stack trace, bottom to top, not just the last line. Note the
first frame that belongs to the code being worked on — that is usually where
the wrong value was introduced, even when the crash is elsewhere.

## 3. Bisect

Halve the search space, do not scan it. Options, roughly in order of speed:

- Comment out half the input, or half the config.
- `git bisect` between a good and a bad commit.
- Put one print at the midpoint of the pipeline. Is the value already wrong
  there? Move earlier. Still right? Move later.

Two or three rounds of this usually locates the line.

## 4. Confirm before fixing

State the cause in one sentence: "X happens because Y is Z when it should be
W." If that sentence needs a "probably" in it, keep bisecting.

Then prove it: make the smallest change that would fix that specific cause and
watch the reproduction go green. If the fix works but the sentence was wrong,
something else is going on — do not stop there.

## 5. Leave a test behind

The reproduction from step 1 becomes a test. A bug fixed without a test is a
bug scheduled to come back.

## Anti-patterns

- Changing several things at once, then not knowing which one worked.
- "Fixing" by adding a retry, a sleep, or a try/except around the symptom.
- Reasoning about what the code should do instead of reading what it does.
