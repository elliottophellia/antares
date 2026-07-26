<p align="center">
  <img src="antares.png" alt="Antares" width="180">
</p>

<h1 align="center">Antares</h1>

<p align="center">A self-hosted AI agent. Go backend, React dashboard, one binary.</p>

Antares reads and writes files, runs shell commands, searches the web, remembers
what matters across sessions, retrieves from a semantic index, schedules its own
work, and answers from Telegram and Discord — all from a single process you run
on your own machine.

```
antares serve      # API + dashboard on :8787
```

---

## Why it is built this way

| Decision | Reason |
|---|---|
| Go backend, no framework | One static binary, no runtime to install, low idle memory. |
| `net/http` routing | Go 1.22 method+pattern routing covers every route here. |
| Pluggable storage | SQLite for a single node, Postgres when you outgrow it — same code. |
| Dashboard embedded in the binary | `make build` produces one file to copy anywhere. |
| Hand-rolled WebSocket client | The Discord gateway is the only consumer; a small `internal/wsutil` beats a dependency. |

Dependencies are deliberately few: a YAML parser, two database drivers. Everything
else is the standard library.

---

## Quick start

```bash
make install          # Go modules, Air, frontend packages
make dev              # backend with hot reload + Vite dev server
```

- Dashboard: http://localhost:5173
- API: http://localhost:8787

Then open **Settings → Providers** and set an API key, or:

```bash
echo 'OPENROUTER_API_KEY=sk-or-v1-…' >> ~/.antares/.env
```

Production build:

```bash
make build            # → bin/antares, dashboard embedded
./bin/antares serve
```

### Accessing it from another machine

Both dev servers bind `0.0.0.0`, so a private-network address works directly:

```
http://<tailscale-ip>:5173     # dev
http://<tailscale-ip>:8787     # production binary
```

Antares leaves the dashboard open when `server.auth_token` is empty, which is the
right default behind a private network. Set the token to require a bearer token.

---

## Configuration

Everything lives in `~/.antares/config.yaml`, editable from the dashboard, the
CLI, or the file itself. Environment variables override the file; `~/.antares/.env`
is loaded automatically.

```bash
antares config get model.default
antares config set model.default anthropic/claude-sonnet-4.5
antares config path
```

| Key | Meaning |
|---|---|
| `model.default` / `model.provider` | Which model answers |
| `providers.*` | Endpoints and API keys |
| `database.driver` | `sqlite`, `postgres`, or `memory` |
| `tools.toolset` | Which tools the model gets: `minimal`, `coding`, `research`, `default`, `all` |
| `rag.provider` | `builtin` (internal vector store) or `enowx` (enowx-rag daemon) |
| `gateway.telegram` / `gateway.discord` | Messaging bots |

### Providers

Any OpenAI-compatible endpoint works out of the box; Anthropic and Gemini have
native adapters so reasoning, prompt caching, and vision behave correctly.

| Provider | Kind | Notes |
|---|---|---|
| OpenRouter | `openai-compatible` | Default. Model ids stay slash-qualified. |
| OpenAI | `openai` | |
| Anthropic | `anthropic` | Extended thinking, prompt caching |
| Google Gemini | `gemini` | Thinking budgets |
| Ollama / LM Studio / vLLM | `openai-compatible` | Point `base_url` at the local server |
| Anything else | `custom` | Set `base_url` and `api_key` |

### Storage

```yaml
database:
  driver: sqlite
  dsn: ~/.antares/antares.db
```

```yaml
database:
  driver: postgres
  dsn: postgres://user:pass@localhost:5432/antares?sslmode=disable
```

SQLite uses FTS5 for conversation search; Postgres uses `tsvector`. Both are
created automatically on first run.

---

## What it can do

**Tools.** File read/write/edit, directory listing, glob, regex search, a
persistent shell (working directory and environment survive between calls), web
search and fetch, long-term memory, cross-session search, semantic retrieval,
task lists, skill authoring, and sub-agent delegation.

**Memory.** The agent decides what is worth keeping and writes it to durable
storage. Memories are injected into the system prompt on every turn, bounded by
`memory.memory_char_limit`.

**RAG.** Two interchangeable backends:

- `builtin` — embeds with your configured model, stores vectors in the Antares
  database, optional hybrid dense + lexical fusion.
- `enowx` — delegates to an [enowx-rag](https://github.com/enowdev/enowx-rag)
  daemon, which brings reranking and near-duplicate compression.

**Skills.** Markdown files with YAML front matter in `~/.antares/skills`. The
agent writes its own after solving something non-obvious; the catalogue (names
and descriptions only) goes in the prompt, and full bodies are fetched on demand
so the context stays small.

**Scheduling.** A five-field cron parser plus `@daily`/`@every 90m` shorthands.
Jobs are natural-language prompts that run unattended and can deliver their
output to a messaging channel.

**Messaging.** Telegram (long polling, no public domain needed) and Discord
(websocket gateway). Both share the same sessions, memory, and tools, and both
gate access behind an allow list or a pairing approval flow.

**MCP.** External Model Context Protocol servers over stdio or streamable HTTP.
Their tools are namespaced `mcp__<server>__<tool>` and made available to the
model automatically; a server that fails to start is reported, never fatal.

**Context compaction.** As a conversation approaches the model's context window,
older turns are summarised while recent ones stay verbatim — and tool-call turns
are never split from their results.

---

## Layout

```
cmd/antares/          CLI entry point
internal/
  agent/              conversation loop, tool dispatch, compaction, delegation
  llm/                provider adapters (openai, anthropic, gemini, compatible)
  tools/              the callable tool surface and toolsets
  store/              Store interface + SQLite/Postgres implementation
  rag/                retrieval backends
  skills/             the skill library
  cron/               schedule parser and runner
  gateway/            Telegram and Discord adapters
  mcp/                Model Context Protocol client
  server/             HTTP API and dashboard hosting
  wsutil/             minimal RFC 6455 client
  config/             layered configuration and its schema
web/                  React dashboard (Vite, Tailwind, shadcn-style, Phosphor)
```

The dashboard owns its layout centrally: `web/src/lib/routes.ts` declares every
page, and `AppShell` renders the container and header, so pages contain content
only. The interface ships in English, Indonesian, Japanese, Chinese, and Russian.

---

## Development

```bash
make dev         # backend (Air) + frontend (Vite), both hot-reloading
make dev-api     # backend only
make dev-web     # frontend only
make check       # go vet + go test + tsc
make smoke       # load every dashboard route in a real browser
make build       # single binary with the dashboard embedded
make doctor      # diagnose configuration and connectivity
```

`make smoke` exists because two of the worst bugs so far passed every type
check: a hook called from inside an effect, and the server bouncing SPA routes
to `./`. Both blanked the entire dashboard. Loading each route in a real browser
catches that class of failure; nothing static does.

`antares doctor` checks the config file, workspace, database, provider
credentials, and RAG backend in one pass.

---

## License

MIT
