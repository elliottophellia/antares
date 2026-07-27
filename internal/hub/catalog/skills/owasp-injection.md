---
name: owasp-injection
description: Test an authorized application for injection flaws — SQL, command, and template. Use during authorized web/API testing.
tags: [security, owasp, injection, sqli]
triggers: [injection, sqli, sql injection, command injection, ssti]
---

# Injection

Injection happens when input crosses from data into code — a query, a command,
a template — because it was concatenated rather than parameterised. **Test only
targets in your authorized scope, and never run destructive payloads.**

## Finding the surface

Every input is a candidate: query parameters, form fields, headers, cookies,
JSON bodies, file names. Map them first.

## SQL injection

1. Send a single quote. An error, a 500, or a changed response is a signal, not
   a confirmation.
2. Test the logic: `' OR '1'='1` versus `' AND '1'='2` — if the responses
   differ predictably, the input reaches the query.
3. Confirm with a benign, provable payload — a boolean or time-based test that
   demonstrates control without touching data. `' AND SLEEP(3)--` that delays
   the response by three seconds proves it.

**Never** run `DROP`, `DELETE`, `INTO OUTFILE`, `xp_cmdshell`, or an automated
tool's `--os-shell`. You are proving a flaw, not exploiting production.

## Command injection

Where input reaches a shell: try a separator with a harmless command
(`; id`, `| whoami`, backticks). A time delay (`; sleep 3`) proves it without
output.

## Template injection

Where input is rendered by a template engine: `{{7*7}}`, `${7*7}`, `#{7*7}`. A
response containing `49` means the input is evaluated.

## Recording

The exact input, where it goes, the proof (the differing responses or the
delay), the impact, and the fix: parameterised queries, safe APIs, never string
concatenation into an interpreter.
