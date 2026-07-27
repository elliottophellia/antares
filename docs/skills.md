# Skills

A skill is a written procedure the agent follows: a Markdown file with YAML
front matter. Not code, not a plugin — instructions.

The point is that the agent gets better at *your* work. Solve something
non-obvious once, keep the procedure, and the next time it takes minutes.

## The shape

```markdown
---
name: deploy-homeserver
description: Deploy this project to the home server. Use when asked to deploy, ship, or release.
tags: [deployment, ops]
triggers: [deploy, ship, release]
---

# Deploying to the home server

1. Run the tests. Do not deploy on red.
2. `make build` — the binary lands in `bin/`.
3. `rsync bin/antares homeserver:/opt/antares/`
4. `ssh homeserver systemctl restart antares`
5. Check `curl homeserver:8787/api/health` before saying it worked.

## When it goes wrong

Port 8787 already in use usually means the old process did not exit. Check
`systemctl status antares` before restarting again.
```

| Field | Meaning |
|---|---|
| `name` | Unique, kebab-case. How it is referred to |
| `description` | **The most important line.** How the agent decides whether this is relevant |
| `tags` | For your own browsing |
| `triggers` | Words that make it more likely to surface |
| `enabled` | `false` keeps it on disk but out of the prompt |

The description does the work. "Deployment stuff" will not get picked; "Deploy
this project to the home server. Use when asked to deploy, ship, or release."
will.

## How they reach the model

Only names and descriptions go into the system prompt — the catalogue. Bodies
are fetched on demand with the `skill` tool.

Twenty skills therefore cost a few hundred tokens per turn rather than tens of
thousands, and adding more does not degrade the conversation.

## Where they live

```yaml
skills:
  enabled: true
  dirs: [~/.antares/skills]
```

Several directories are searched in order and later ones win, so a personal copy
overrides a shared one — useful for a team directory in a repository plus your
own adjustments.

## Getting them

**Bundled.** Eight are written on first run. See [the hub](hub.md).

**From the hub.**

```
/skills search debugging
/skills install owner/repo
```

**Written by you.** Drop a file in the skills directory, or use the Skills page.

**Written by the agent.** With `skills.auto_create` on, it writes one after
solving something non-obvious.

**Learned from a session.**

```
/learn
/learn the deploy sequence
```

The transcript goes to a model with one instruction: write the procedure someone
would want next time, with the commands, paths, and gotchas that actually came
up, and skip everything particular to this conversation. If nothing general was
learned it says so and writes nothing.

## Managing them

```
/skills                 what is installed
/skills deploy          filter
```

The dashboard's Skills page lists them with a switch each, shows the body
inline, and has a Browse button for the hub.

```yaml
skills:
  auto_create: true
  creation_nudge_interval: 20   # turns between suggestions
```

## Writing a good one

**Be specific.** The actual command, the actual path, the actual flag. A skill
that says "build the project" is worth nothing; one that says `make build -j2`
because `-j` alone runs the machine out of memory is worth a lot.

**Write the failure modes.** What went wrong the first time, and what it meant.
That is the part that is expensive to rediscover.

**One procedure per skill.** "Deploy" and "roll back" are two skills. A skill
that covers four unrelated things will surface for the wrong one.

**Say when to use it.** The description is a matching problem. Include the words
someone would actually use.

**Leave out this-time details.** No session ids, no timestamps, no "the user
asked me to". A skill is for next time.

## Safety

Skills installed from the hub are scanned first. A skill is prompt text the
model follows, so a file telling it to pipe a download into a shell or read
credentials and post them somewhere is the whole attack — there is no sandbox to
catch it later.

Files you write yourself are not scanned. You wrote them.

See [the hub](hub.md) for what is refused and why.
