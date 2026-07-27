# The hub

The hub is where new capabilities come from. Two catalogues — skills and MCP
servers — each browsable and installable in one click.

A curated set ships inside the binary, so the hub is useful with no network and
no account. Beyond that it reads plain GitHub repositories and any URL serving a
`SKILL.md`, which means publishing to it needs nothing more than a public repo.

## Browsing

**In the dashboard.** Skills and MCP both have a **Browse** button.

**From a command.**

```
/skills search              everything bundled
/skills search debugging    narrowed
/skills search owner/repo   a GitHub repository
/mcp search                 the MCP catalogue
/mcp search database        narrowed
```

**Over HTTP.**

```
GET  /api/hub/skills?q=debug
POST /api/hub/skills/install   {"id": "builtin/systematic-debugging"}
GET  /api/hub/mcp?q=git
POST /api/hub/mcp/install      {"id": "github"}
```

## Skills

### What ships

Fourteen skills are written into `~/.antares/skills` the first time Antares
runs — eight general, and six security methodology procedures for the security
roles (authorized testing only):

| Skill | For |
|---|---|
| `systematic-debugging` | Finding the cause of a bug instead of guessing at fixes |
| `code-review` | Reviewing a diff for defects that matter |
| `test-driven-development` | Building a feature test-first |
| `writing-clearly` | Prose a busy reader can act on |
| `research-a-topic` | Investigating properly, with sources |
| `browser-automation` | Driving a website reliably |
| `shell-safely` | Commands that delete or overwrite |
| `git-workflow` | Branching, committing, and pull requests |
| `pentest-recon` | Mapping an authorized target's attack surface |
| `owasp-access-control` | Testing for broken access control |
| `owasp-injection` | Testing for injection flaws |
| `owasp-authentication` | Testing authentication and sessions |
| `owasp-ssrf` | Testing for server-side request forgery |
| `pentest-reporting` | Writing an assessment report |

Seeding happens once. A `.seeded` marker records that it did, so a skill you
delete on purpose stays deleted. Delete the marker to have them restored.

### Installing from elsewhere

```
/skills install builtin/code-review
/skills install owner/repo
/skills install owner/repo/skills/deploy
/skills install https://github.com/owner/repo/tree/main/skills/deploy
/skills install https://example.com/my-skill.md
```

A repository reference is looked up directly rather than searched. Antares looks
for a `SKILL.md` at the path, then one directory down — which covers both a repo
that *is* one skill and a repo that *holds* many. It does not crawl deeper; a
deep search of a large repository is a lot of requests for a guess.

Unauthenticated GitHub allows sixty requests an hour. Set `GITHUB_TOKEN` in the
environment to raise that; Antares uses it automatically if it is there.

### Installed skills are scanned

A skill is prompt text the model follows. A file that tells it to pipe a
download into a shell, or to read credentials and post them somewhere, is the
whole attack — there is no sandbox to catch it later, because the skill is not
code being run, it is an instruction being obeyed.

Installation refuses a skill that:

- pipes a download straight into a shell
- deletes a home or root directory
- reads credentials and sends them somewhere
- tries to override the system prompt
- tries to disable safety rules
- makes the filesystem world-writable

The refusal names which one. If you have read the file and trust it, put it in
the skills directory by hand — that path is deliberately manual.

Ordinary skills that merely mention `rm` or an API key are not affected; the
patterns look for the combination that constitutes an attack.

### Publishing one

Put a `SKILL.md` in a public repository:

```markdown
---
name: deploy-homeserver
description: Deploy this project to the home server. Use when asked to deploy or ship.
tags: [deployment, ops]
triggers: [deploy, ship, release]
---

# Deploying

1. …
```

Then anyone can install it:

```
/skills install yourname/yourrepo
```

No account, no registry, no review queue.

## MCP servers

Seventeen servers, each with the command, arguments, and credentials it needs.

| | |
|---|---|
| **Files and code** | Filesystem, Git, GitHub |
| **Data** | PostgreSQL, SQLite |
| **Web** | Fetch, Brave Search, Puppeteer, Playwright |
| **Work** | Linear, Notion, Slack, Sentry |
| **Thinking** | Sequential thinking, knowledge-graph memory |
| **Utility** | Time and timezones, the protocol reference server |

Installing writes the server into `~/.antares/config.yaml`, enables MCP, and
connects without a restart.

### Credentials

A server needing a key takes it from the environment when it is already there —
a machine that exports `GITHUB_PERSONAL_ACCESS_TOKEN` needs no further setup.
When it is not, the install still happens and the response names exactly what is
missing:

> Added, but it still needs `GITHUB_PERSONAL_ACCESS_TOKEN`. Set it under
> Settings, then restart.

That is deliberately louder than leaving a server that fails silently the first
time the model reaches for it.

### Prerequisites

Most stdio servers run through `npx` or `uvx`, so they need Node or
[uv](https://docs.astral.sh/uv/) on the machine. The catalogue says which. A
hosted server needs neither — it is an HTTPS endpoint that asks you to authorise
on first use.

### Adding one by hand

The catalogue is a convenience, not a limit. Any MCP server works:

```yaml
mcp:
  enabled: true
  servers:
    my-server:
      enabled: true
      transport: stdio
      command: /usr/local/bin/my-mcp-server
      args: ["--flag"]
      env:
        MY_TOKEN: xxx
```

See [MCP](mcp.md) for the full shape.

## Extending the catalogue

The bundled catalogue lives in `internal/hub/catalog/` — Markdown files for
skills, one JSON file for MCP servers. Both are embedded at build time, so
adding an entry is a file and a rebuild.
