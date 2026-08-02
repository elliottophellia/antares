# HTTP API

`antares serve` exposes a JSON API on `:8787` and serves the dashboard from the
same port. Everything the dashboard does is available here.

## Authentication

While `server.auth_token` is empty the API is open, which is right behind a
private network and wrong anywhere else.

The mutating onboarding endpoints (`/api/setup/test` and
`/api/setup/complete`) and first dashboard-password bootstrap are additionally
restricted to a loopback client until a bearer token has been configured. This
prevents a remote first writer from claiming or reconfiguring a new instance.

```bash
antares config set server.auth_token "$(openssl rand -hex 24)"
```

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8787/api/status
```

Bearer tokens are accepted in query strings only for the explicitly documented
SSE/media routes that cannot set request headers. Ordinary JSON and mutating
routes require the `Authorization` header.

`/api/health` is always open, so a health check needs no credential.

## Chat

### `POST /api/chat`

Streams a turn as server-sent events.

```bash
curl -N -X POST http://localhost:8787/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"message": "what changed in this repo today?", "session_id": ""}'
```

An empty `session_id` starts a new conversation; the `session` event carries the
id assigned.

| Event | Fields | Meaning |
|---|---|---|
| `session` | `id`, `title` | Which conversation this is |
| `turn` | `turn` | A new model call began |
| `text` | `delta` | Reply text |
| `reasoning` | `delta` | Reasoning, when the model exposes it |
| `tool_call` | `id`, `name`, `arguments` | A tool is about to run |
| `tool_progress` | `id`, `chunk` | Incremental output from it |
| `tool_result` | `id`, `content`, `is_error` | What it returned |
| `usage` | `input_tokens`, `output_tokens` | Running totals |
| `notice` | `message` | Compaction, steering, goal, guard |
| `error` | `error` | The turn failed |
| `done` | | Terminal — always last |

### `POST /api/chat/interrupt`

```json
{"session_id": "sess_…"}
```

## Sessions

| | |
|---|---|
| `GET /api/sessions` | List, paginated |
| `GET /api/sessions/search?q=` | Full-text search |
| `GET /api/sessions/{id}` | One session with its messages |
| `DELETE /api/sessions/{id}` | Delete |
| `POST /api/sessions/{id}/title` | Rename |
| `GET /api/sessions/empty/count` | How many are empty |
| `POST /api/sessions/empty/delete` | Remove those |
| `POST /api/sessions/prune` | Delete older than a cutoff |

## Commands

| | |
|---|---|
| `GET /api/commands?surface=web` | The catalogue for a surface |
| `POST /api/commands/run` | Run one |

```json
{"input": "/status", "session_id": "sess_…", "surface": "web"}
```

Returns `{"ok": true, "output": "…", "action": {...}}`. A command that fails
comes back with `ok: false` and an error — a failed command is a normal outcome,
not a transport error.

## Models and providers

| | |
|---|---|
| `GET /api/model/options` | Configured providers and the active model |
| `GET /api/model/list?provider=` | What a provider offers |
| `POST /api/model/set` | `{"model": "...", "provider": "..."}` |
| `POST /api/providers/{id}/key` | Verify a key against the provider, then save |

## The hub

| | |
|---|---|
| `GET /api/hub/skills?q=` | Search skills |
| `POST /api/hub/skills/install` | `{"id": "builtin/code-review"}` |
| `GET /api/hub/mcp?q=` | Search MCP servers |
| `POST /api/hub/mcp/install` | `{"id": "github"}` |

## Skills, memory, retrieval

| | |
|---|---|
| `GET /api/skills` | List |
| `POST /api/skills` | Create |
| `GET /api/skills/{name}` | Read the body |
| `POST /api/skills/toggle` | Enable or disable |
| `DELETE /api/skills/{name}` | Delete |
| `GET /api/memory` | List |
| `POST /api/memory` | Add |
| `GET /api/memory/search?q=` | Search |
| `DELETE /api/memory/{id}` | Delete |
| `GET /api/rag/status` | Backend state and collections |
| `POST /api/rag/index` | Index a path |
| `POST /api/rag/search` | Query |
| `DELETE /api/rag/collections/{name}` | Drop a collection |

## Scheduling and channels

| | |
|---|---|
| `GET /api/cron/jobs` | List |
| `POST /api/cron/jobs` | Create |
| `DELETE /api/cron/jobs/{id}` | Delete |
| `POST /api/cron/jobs/{id}/toggle` | Pause or resume |
| `POST /api/cron/jobs/{id}/run` | Run now |
| `GET /api/cron/jobs/{id}/runs` | History |
| `GET /api/cron/validate?schedule=` | Check an expression, preview run times |
| `GET /api/channels` | Channels and pairings |
| `POST /api/channels/{id}/toggle` | Enable or disable |
| `POST /api/channels/{id}/token` | Verify a bot token, then save |
| `POST /api/pairing/approve` | Approve a pending user |
| `POST /api/pairing/revoke` | Revoke one |

## Configuration and diagnostics

| | |
|---|---|
| `GET /api/config` | Current settings, secrets redacted |
| `POST /api/config` | Set one path |
| `GET /api/config/raw` | The YAML file |
| `POST /api/config/raw` | Replace it |
| `GET /api/config/schema` | Field metadata that drives the Settings form |
| `GET /api/status` | Version, uptime, model, counts |
| `GET /api/system/stats` | Host CPU, memory, disk |
| `GET /api/analytics` | Token and cost series |
| `GET /api/logs` | Recent log lines |
| `GET /api/logs/stream` | Live tail, SSE |
| `GET /api/tools` | Registered tools and their toolsets |
| `GET /api/mcp` | MCP servers and connection state |
| `GET /api/files` | Browse the workspace |
| `GET /api/files/read` | Read a file from it |

## Setup

| | |
|---|---|
| `GET /api/setup/status` | Whether setup is needed, and the provider catalogue |
| `POST /api/setup/test` | Try a provider and key |
| `POST /api/setup/complete` | Write the configuration |

## Errors

```json
{"error": "no provider named \"nope\" is configured"}
```

`400` for a bad request, `401` for a missing or wrong token, `404` for an
unknown path, `500` for a failure inside. Endpoints where failure is an ordinary
outcome — installing, verifying a key, running a command — return `200` with
`ok: false` instead, so the caller can show the message rather than a banner.
