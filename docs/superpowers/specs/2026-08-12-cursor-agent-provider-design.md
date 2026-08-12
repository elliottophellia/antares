# Cursor Agent Provider Design

## Summary

Antares will support Cursor as a first-class external agent integration. Users
configure a Cursor API key from the existing Providers page, discover the
models available to that key, and delegate coding work to Cursor Cloud Agents
through `cursor_agent` and `cursor_agent_status` tools.

Cursor is deliberately not implemented as an `llm.Client`. Cursor's public API
creates durable coding agents and runs; it does not expose an OpenAI-compatible
chat-completions endpoint. Treating one cloud-agent run as one model completion
would lose Antares tool calls, duplicate conversation state, increase latency,
and misrepresent the product in the model picker.

## Goals

- Show Cursor in Antares' existing provider-management experience.
- Authenticate with a deployment-owned `CURSOR_API_KEY`.
- Validate credentials and list models through Cursor's official REST API.
- Create no-repo or repository-backed Cursor Cloud Agents.
- Continue an existing Cursor agent with follow-up runs.
- Stream progress, inspect status/results, and cancel runs.
- Preserve Cursor agent/run IDs and return branch/PR information to Antares.
- Never log, return, or commit the API key.

## Non-goals

- Using Cursor as Antares' primary chat-completion model.
- Supporting undocumented Cursor endpoints.
- Running Cursor's local SDK Bridge in the initial release.
- Per-user Cursor credentials in a shared Antares deployment.
- Building a separate dashboard for all Cursor runs.
- Automatically exposing Antares tools to cloud agents over MCP.

## Product Model

Cursor appears on the Providers page because that is where Antares already
manages external AI credentials. It is labelled **Cursor Cloud Agents** and
marked as an **Agent integration**, rather than an LLM provider.

Connecting Cursor enables the Cursor agent tools. Cursor models are shown inside
the Cursor provider modal for task configuration, but they are excluded from
Antares' primary Models picker because they cannot satisfy the `llm.Client`
contract.

A deployment has one Cursor credential. Every user permitted to invoke tools on
that Antares instance consumes the same Cursor account/team quota. Existing
Antares tool approval and platform toolset controls remain the authorization
boundary.

## Configuration

Cursor uses the existing `providers` map:

```yaml
providers:
  cursor:
    kind: cursor-agent
    label: Cursor Cloud Agents
    base_url: https://api.cursor.com
    api_key_env: CURSOR_API_KEY
    enabled: true
    timeout_seconds: 900
```

The dashboard may store a key through the existing credential endpoint for
consistency with other providers, but documentation recommends the environment
variable. Responses continue to expose only `has_key`; the secret is never
serialized to the browser.

Default configuration includes this provider in an enabled-but-disconnected
state, matching the built-in OpenAI/Anthropic entries: `CURSOR_API_KEY` works
without first writing YAML, while the absent key leaves the integration
unavailable. Provider status computes `has_key` from the resolved provider
(stored key or populated key environment variable), rather than checking only
the literal YAML field.

Provider catalogue entries gain a capability marker (`llm` or `agent`). This
prevents generic setup/model code from passing Cursor to `llm.New` or selecting
it as the active chat provider.

## Components

### `internal/cursor` REST client

A focused client owns Cursor protocol details:

- `Me` — `GET /v1/me`, used to validate a key.
- `Models` — `GET /v1/models`, including model parameter definitions.
- `CreateAgent` — `POST /v1/agents`.
- `CreateRun` — `POST /v1/agents/{agentID}/runs`.
- `GetAgent` and `GetRun`.
- `StreamRun` — SSE stream with `Last-Event-ID` reconnect support.
- `CancelRun`.

Authentication uses `Authorization: Bearer <key>`. Request errors are typed so
callers can distinguish rejected credentials, rate limits, invalid requests,
missing agents/runs, transport failures, and cancellation. Error messages are
bounded and sanitized; request headers are never included.

The client accepts an injected base URL and `http.Client`, allowing all tests to
run against `httptest.Server`.

### Provider catalogue and setup

Cursor is added to the web setup catalogue with:

- ID `cursor`
- kind `cursor-agent`
- key hint `crsr_…`
- key URL `https://cursor.com/dashboard/api`
- base URL `https://api.cursor.com`
- capability `agent`
- note explaining shared deployment usage

Credential testing special-cases agent-capability providers:

1. Call `GET /v1/me` to verify authentication.
2. Call `GET /v1/models` to verify model access.
3. Return the available models without activating Cursor as the default LLM.

CLI/TUI provider metadata receives the same capability marker. Connecting an
agent-capability provider stores/enables it but does not mutate
`model.provider` or `model.default`.

### `cursor_agent` and `cursor_agent_status` tools

The mutating `cursor_agent` tool exposes a small action-based API:

- `start` — create an agent and its first run.
- `follow_up` — create a run on an existing active agent.
- `cancel` — cancel an active run.

`start` accepts:

- required `prompt`
- optional `model`
- optional `repository_url`, `starting_ref`, and `pull_request_url`
- optional `mode` (`agent` or `plan`)
- optional `auto_create_pr` and `skip_reviewer_request`
- optional `wait`

Omitting repository fields creates a documented no-repo agent. Repository URLs
must be HTTPS GitHub URLs; refs are passed as data, never interpolated into
shell commands.

`follow_up` requires `agent_id` and `prompt`, and optionally accepts `mode` and
`wait`. `cancel` requires both agent and run IDs.

The read-only `cursor_agent_status` tool accepts `agent_id`, an optional
`run_id`, and optional `wait`. Without a run ID it resolves the agent's latest
run. With `wait=false` it returns one snapshot. With `wait=true` it follows the
run to a terminal state.

`cursor_agent.RequiresApproval()` is always true because every action it
supports either consumes paid resources or mutates remote state.
`cursor_agent_status` does not require approval.

When `wait` is true on either tool, it consumes Cursor's SSE stream and forwards
bounded status, assistant, reasoning, and tool-call updates through
`Input.Emit`. Completion returns the final text, duration, Cursor URL, run
status, and any pushed branches/PRs. A caller context cancellation closes the
stream; it does not automatically cancel the remote run unless the caller
explicitly invokes `cancel`.

When `wait` is false, `cursor_agent` returns `agent_id`, `run_id`, and the
Cursor URL immediately. Its description tells the model not to busy-poll; the
user or a later turn can call `cursor_agent_status`.

### Registration and availability

Both tools are registered with the standard tool registry, but execution checks
that the Cursor provider is enabled and has a resolved key. If not configured,
they return an actionable message directing the user to Providers or
`CURSOR_API_KEY`.

The existing toolset controls can remove both tools. No Cursor credential is
sent to the model or included in tool metadata.

## Data Flow

### Connect

1. Admin opens Providers and selects Cursor Cloud Agents.
2. Dashboard sends the key to the existing protected credential endpoint.
3. Backend validates it with `/v1/me` and fetches `/v1/models`.
4. Backend stores the provider configuration and reloads Antares.
5. Cursor becomes connected, while the current Antares LLM remains unchanged.

### Start and wait

1. Antares calls `cursor_agent(action=start, ...)`.
2. Tool resolves `providers.cursor` and constructs the REST client.
3. Client creates the Cursor agent and initial run.
4. Tool streams the run, emitting progress without exposing secrets.
5. On terminal status, tool returns final text and git metadata.

### Follow-up

1. Antares reuses the returned `agent_id`.
2. Client posts a new run; Cursor retains its conversation and cloud workspace.
3. The result follows the same wait/background behavior as the initial run.

## Error Handling

- `401/403`: “Cursor API key was rejected”; never echo the key.
- `429`: include a bounded retry-after hint; do not blindly create duplicate
  agents.
- `409` on idempotent create: return the existing conflict clearly.
- `404`: distinguish missing agent from missing run.
- SSE disconnect: reconnect with the last Cursor event ID and a capped
  exponential backoff while the caller context remains active.
- Invalid resume event ID: clear the event ID once, reconnect from Cursor's
  retained stream, then fail if the server rejects it again.
- Terminal statuses (`FINISHED`, `ERROR`, `CANCELLED`) are returned as explicit
  structured metadata.
- Context timeout: report that the remote run may still be active and provide
  IDs for a later `cursor_agent_status` or cancel action.

Automatic retries are limited to idempotent GET/stream reconnection. Create
agent/run calls are not retried unless a client-supplied idempotency identifier
can prove they will not duplicate work.

## Security

- No API key literal in source, tests, fixtures, docs, command arguments, or
  logs.
- Prefer `CURSOR_API_KEY`; stored-key behavior remains consistent with current
  Antares providers.
- Redact authorization headers from every error path.
- Enforce existing dashboard-password checks on credential mutation.
- Validate configurable base URLs with the existing provider URL policy.
- Require tool approval for paid or repository-mutating actions.
- Do not pass arbitrary environment variables, MCP servers, or worker targets
  in the initial tool schema.
- Return only bounded Cursor tool-call summaries to avoid leaking cloud
  environment data into the Antares conversation.

## Testing

### Unit tests

- Bearer authorization is sent and never appears in errors.
- `/v1/me` credential validation.
- Model catalogue decoding, including parameter definitions.
- Agent/run request encoding for no-repo, repo, PR, and follow-up cases.
- Terminal run decoding and git metadata.
- SSE parsing for status, assistant, reasoning, tool-call, result, and error
  events.
- `Last-Event-ID` reconnect and context cancellation.
- Typed handling for 400, 401/403, 404, 409, 429, and 5xx.

### Tool tests

- Missing/disabled credential errors.
- Action-specific validation.
- Mutating and read-only tools have the correct static approval classification.
- Progress emission is bounded and secret-free.
- Start/follow-up/cancel and status/wait call the correct client operations.
- A wait timeout returns recoverable agent/run IDs.

### Server/UI tests

- Cursor appears as an agent-capability provider.
- Connecting Cursor does not change the active Antares model/provider.
- Cursor never appears in the primary model picker.
- Credential responses remain redacted.
- Failed auth and model discovery produce actionable UI messages.

### Live test

An opt-in test runs only when `CURSOR_API_KEY` is already present in the test
process environment. It calls `/v1/me` and `/v1/models`; it does not create an
agent by default, avoiding unexpected billing. A separate explicitly enabled
smoke test may create a no-repo agent with a minimal prompt.

## Rollout and Compatibility

Existing configs require no migration. The provider catalogue supplies Cursor
defaults when it is first connected. Unknown/custom providers retain the
current LLM capability by default, preserving compatibility.

The initial release is cloud-only. A future local implementation can add a
`cursor-local-agent` capability through the official SDK Bridge without
changing the REST client or pretending either runtime is a chat-completion
provider.

## Acceptance Criteria

- An admin can connect a valid Cursor key from Providers.
- Invalid keys fail without appearing in logs or responses.
- The provider modal lists models returned for that account.
- Connecting Cursor leaves the active Antares chat model untouched.
- Antares can create, follow up, and cancel a Cursor Cloud Agent through
  `cursor_agent`, and inspect/stream it through `cursor_agent_status`.
- Repo-backed runs return branch/PR metadata.
- No-repo runs work without repository fields.
- Unit and UI tests pass without external network access.
- The live auth/model test passes when an environment-provided key is valid.
