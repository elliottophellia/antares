# Plugins

A plugin is an external program Antares runs at points in the agent's life. It
can watch what the agent does, refuse a tool call, rewrite arguments, or replace
what the model is shown.

Adding *tools* is what [MCP](mcp.md) is for, and it does that better than a
bespoke protocol would. Plugins exist for the thing MCP does not cover: getting
in between the agent and its own actions.

## The shape

A directory with a manifest and something to run:

```
~/.antares/plugins/
  audit-log/
    plugin.yaml
    run.sh
```

```yaml
# plugin.yaml
name: audit-log
description: Append every terminal command to a file
version: 1.0.0
command: ./run.sh
hooks: [pre_tool_call]
timeout_ms: 2000
env:
  LOG_PATH: /var/log/antares-audit.log
```

| Field | Meaning |
|---|---|
| `command` | The executable. A bare name is found on PATH; a relative path is resolved inside the plugin directory |
| `args` | Extra arguments |
| `hooks` | Which events to receive. An empty list means none |
| `timeout_ms` | Bound on one call, default 5000 |
| `env` | Extra environment for the process |

## The protocol

One JSON object on stdin, one JSON object on stdout. That is the whole thing.

```bash
#!/bin/sh
PAYLOAD=$(cat)
echo "$PAYLOAD" >> "$LOG_PATH"
echo '{}'
```

**In:**

```json
{
  "event": "pre_tool_call",
  "session_id": "ses_…",
  "platform": "web",
  "tool": "terminal",
  "arguments": "{\"command\":\"rm -rf build\"}"
}
```

**Out** — every field optional; `{}` means "no opinion", which is the common
case:

```json
{
  "deny": false,
  "reason": "",
  "arguments": "",
  "result": "",
  "notice": ""
}
```

| Field | Effect |
|---|---|
| `deny` | Refuse the tool call. `reason` is given to the model |
| `arguments` | Replace the call's arguments |
| `result` | Replace what the model sees from a finished tool |
| `notice` | Show a line in the transcript |

Printing nothing is fine — a plugin that only observes should say nothing.

## Events

| Event | When | Can |
|---|---|---|
| `pre_tool_call` | Before a tool runs | Deny, rewrite arguments |
| `post_tool_call` | After it returns | Replace the result |
| `session_start` | A conversation begins | Observe |
| `session_end` | It ends | Observe |
| `turn_end` | After each completed turn | Observe |

## Ordering

Plugins run in name order, each seeing the previous one's changes — so two that
rewrite arguments compose rather than fight.

A `deny` from any plugin ends the chain immediately. A refusal is not something
a later plugin should be able to quietly undo.

## Failure

A plugin that crashes, times out, or prints something that is not JSON is
logged and skipped. The agent carries on.

This matters more than it sounds: a plugin is arbitrary code on the critical
path of every tool call. If a broken one could stop the agent, nobody could
safely install one.

Broken plugins still appear in the list with the reason, so the failure is
visible rather than a plugin that mysteriously does nothing.

## Examples

**Refuse writes outside a directory:**

```bash
#!/bin/sh
PAYLOAD=$(cat)
case "$PAYLOAD" in
  *'"tool":"write_file"'*)
    case "$PAYLOAD" in
      *'/home/me/project'*) echo '{}' ;;
      *) echo '{"deny":true,"reason":"writes are limited to /home/me/project"}' ;;
    esac ;;
  *) echo '{}' ;;
esac
```

**Redact a secret from tool output:**

```python
#!/usr/bin/env python3
import json, re, sys

payload = json.load(sys.stdin)
result = payload.get("result", "")
cleaned = re.sub(r"(sk-[A-Za-z0-9]{8})[A-Za-z0-9]+", r"\1…", result)
print(json.dumps({"result": cleaned} if cleaned != result else {}))
```

**Send every turn to your own metrics:**

```yaml
name: metrics
command: /usr/local/bin/antares-metrics
hooks: [turn_end]
timeout_ms: 1000
```

## Configuration

```yaml
plugins:
  enabled: true
  dirs: [~/.antares/plugins]
```

Directories are scanned at startup and on reload. Adding a plugin needs a
restart or a config reload, not a rebuild.

## Security

A plugin runs as you, with your environment. It is code you are choosing to
run, exactly like a shell script — Antares does not sandbox it and does not
pretend to.

Read one before installing it, particularly one that subscribes to
`pre_tool_call`: that hook sees every argument the agent passes to every tool,
which includes anything sensitive it is working with.
