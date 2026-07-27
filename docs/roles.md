# Roles

One general assistant is a generalist by necessity, and a generalist is
mediocre at everything. A role is a named specialist: its own instructions, its
own tools, and often its own model. A reviewer that only reads. A researcher
that only browses. A report writer that only writes.

Roles are how one conversation becomes a team.

## Using one

**Run a conversation as a role:**

```
/roles                 list them
/role coder            switch to one, remembered across turns
/role                  show the current role
/role assistant        back to the general assistant
```

**Delegate a piece of work to one.** The agent calls `delegate_task` with a
`role`, and the sub-agent runs as that specialist — with exactly the reach that
work needs and no more. `list_roles` lets the model discover them.

## What ships

| Role | For |
|---|---|
| `assistant` | The general-purpose agent |
| `planner` | Breaking a goal into an ordered plan |
| `coder` | Reading, writing, and editing code; running the tests |
| `reviewer` | Reading a change for defects — reads only, never writes |
| `data-analyst` | Exploring data, running queries, reporting the numbers |
| `researcher` | Investigating with sources |
| `writer` | Clear prose |
| `security-recon` | Mapping an authorized target's attack surface |
| `security-webapp` | Testing an authorized web app against OWASP |
| `security-api` | Testing an authorized API |
| `security-report` | Turning findings into a report |
| `threat-modeler` | Reasoning about how a system could be attacked |
| `incident-responder` | Investigating logs and evidence after an incident |

## Writing your own

A role is a Markdown file with front matter, dropped into `~/.antares/roles`. A
file with the same name as a bundled role overrides it.

```markdown
---
name: sre
title: Site Reliability Engineer
summary: Diagnoses production incidents and proposes durable fixes.
category: engineering
toolset: coding
tags: [ops, reliability]
---

You are a site reliability engineer. When something is broken, establish the
timeline from evidence before proposing a cause. Prefer the smallest change
that restores service, then the durable fix afterward. Never guess at a metric
you can query.
```

| Field | Meaning |
|---|---|
| `name` | How the role is addressed |
| `summary` | One line, shown in the picker |
| `category` | Groups it: general, engineering, research, writing, security |
| `toolset` | Which tools it runs with |
| `model` | Optional model override |
| `danger` | Marks a role that needs authorization (security testing) |

The body is the role's standing instructions, appended to the system prompt
while the role is active.

## The team learns

Every delegated task is a small trial. Antares records how each specialist did
— whether it finished cleanly, whether its work was kept, how many turns it took
— and scores each role 0–100 from that history.

```
/team          how the specialists have performed
```

The Roles page shows the leaderboard as bars, and — while sub-agents are running
— a live panel of who is working on what. An untried role starts neutral; an
untried specialist is not a bad one.

## Security roles

The security roles are for **authorized** testing — a penetration test on a
target you own or have written permission to assess. They are gated on scope.

A security test is only lawful against a target its owner authorized. The
difference between a penetration test and an attack is that list, so the list is
checked in code, not left to a prompt to remember.

```
/scope add example.com          authorize a target (domain, IP, or CIDR)
/scope add *.staging.example.com
/scope list                     what is authorized
/scope check api.example.com    is this in scope?
/scope remove example.com
```

The `scope_check` tool consults it, and it fails closed: an empty scope
authorizes nothing. Set `security.require_scope` to refuse out-of-scope targets
outright rather than warn.

```yaml
security:
  scope:
    - example.com
    - "*.staging.example.com"
    - 10.0.0.0/24
  require_scope: true
```

The security roles find, and `report_finding` records — a confirmed issue with
its severity, reproduction, impact, and remediation goes into a per-session
ledger:

```
/findings              what the engagement recorded
/report                compile them into a Markdown report, worst first
```

### The skill library

Over seven thousand security testing procedures ship inside the binary — web,
API, cloud, infrastructure, Active Directory, Kerberos, the OWASP testing guide,
the MITRE techniques, the CIS and NIST benchmarks. They unpack on first run into
their own directory.

They are not in the prompt — seven thousand names would bury the conversation.
They work the way a reference works: the security roles search for the technique
they need and read it.

```
skill tool: action=search name="jwt"     find the technique
skill tool: action=read name="attack-jwt"  load it
```

The everyday catalogue stays the handful of general skills; the library is
behind search.

Antares does not ship exploit payloads, offensive tooling, or evasion
techniques. It gives the security roles the same tools every role has — a
browser, a shell, web access — under authorization and scope, and the structure
to turn what they find into a report.

## Running an assessment

A penetration test is a sequence, not one action: map the surface, enumerate
what is on it, test each thing, then report. Antares tracks that sequence so an
agent does not find one bug and write the report while three services sit
untested.

As the security roles work, they record what they find with `add_intel` — a
host, a subdomain, a service, an endpoint, a technology. The methodology reads
that ledger to decide which of five phases actually has the evidence to be
called complete:

```
/engagement            the phases, their evidence, and what to do next
/engagement intel      the facts recorded so far
```

`methodology_status` shows the same to the agent mid-run, so it orients itself
before deciding what to test next. A phase with findings but skipped
prerequisites — a vulnerability recorded before the surface was enumerated — is
flagged as built on sand.

The phases:

| Phase | Complete when it has |
|---|---|
| Scope & Authorization | an authorized scope |
| Reconnaissance | hosts or subdomains |
| Enumeration | services, endpoints, or technologies |
| Vulnerability Testing | confirmed findings |
| Reporting | a compiled report |

## Delegation

The primary agent hands a self-contained piece of work to a specialist with
`delegate_task`, naming a `role`. The sub-agent runs as that specialist, with
its own instructions and tools, and returns only its final answer — research
that would flood the main conversation happens elsewhere.

For work on a git repository, pass `isolate`: the sub-agent gets its own git
worktree off the current HEAD. Several sub-agents can then edit the same
repository in parallel without conflicting — each has a private working
directory sharing the one object store. When a sub-agent finishes, an unchanged
worktree is removed and one with work in it is left on its branch for review.

This needs git. Without it, or outside a repository, the sub-agent shares the
workspace instead — reported, not fatal.
