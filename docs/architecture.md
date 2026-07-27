# Architecture

One process, one binary, no runtime to install.

```
cmd/antares/          the CLI: serve, setup, doctor, config, cron, and the TUI
internal/
  agent/              the conversation loop, tool dispatch, compaction, harness
  commands/           slash commands, shared by every surface
  llm/                provider adapters and streaming
  tools/              the callable tool surface and the toolsets
  browser/            Chrome DevTools Protocol client and page control
  hub/                skill and MCP catalogue, and the installers
  store/              the Store interface and its SQL implementation
  rag/                retrieval backends
  skills/             the skill library
  memory/             durable facts
  cron/               schedule parsing and the runner
  gateway/            Telegram and Discord
  mcp/                Model Context Protocol client
  tui/                the terminal interface
  server/             HTTP API and dashboard hosting
  wsutil/             a minimal RFC 6455 client
  config/             layered configuration and its schema
web/                  the React dashboard
```

## The turn

1. `server` or `tui` or `gateway` calls `agent.Run` with a request.
2. The session is resolved or created; history is loaded.
3. The system prompt is built: instructions, environment, standing goal, memory,
   the skill catalogue, tool notes.
4. If the history is close to the context window, older turns are summarised.
5. The model is called, streaming events out as they arrive.
6. Tool calls are dispatched in parallel, each streaming its own progress.
7. Results go back into the history and the loop repeats.
8. When the model stops calling tools, the [harness](harness.md) decides whether
   it is really finished.
9. `done` is emitted. `Run` owns the terminal events, so no caller double-reports.

## Choices worth explaining

**Go, standard library.** One static binary, nothing to install on the target,
low idle memory. `net/http` method-and-pattern routing covers every route here,
so there is no router dependency. Three dependencies in total: a YAML parser and
two database drivers.

**One `Store` interface.** SQLite and Postgres behind the same methods. Two
things make that work: timestamps stored as unix milliseconds so both dialects
agree, and `?` placeholders rebound to `$n` for Postgres. Search is the only
place they diverge — FTS5 against `tsvector`.

**A hand-rolled WebSocket client.** The Discord gateway is the only consumer.
`internal/wsutil` is a few hundred lines of RFC 6455, which is less than a
dependency costs to carry.

**CDP directly for the browser.** No driver process, no Node, no Playwright
install. The protocol is JSON over a WebSocket, and the WebSocket client already
existed.

**Commands defined once.** The terminal, the web chat, and the gateways all
dispatch through `internal/commands`, so a command means the same thing
everywhere and adding one is a single registration.

**The dashboard embedded.** `make build` copies `web/dist` into the binary. One
file to copy anywhere. In development Vite serves the UI and proxies the API.

## The dashboard

`web/src/lib/routes.ts` is the single source of navigation, routing, and page
chrome. `AppShell` renders the frame; pages contain content only. That is why
every page has the same padding and the same header, and why adding one is a
single entry.

Built with React 19, Vite, Tailwind v4 with oklch tokens, Radix primitives, and
Phosphor icons. Five languages, English by default.

## Streaming

Events flow the same way to every surface:

```
llm.Client.Stream → agent.Emit → SSE / TUI redraw / message edit
```

The terminal folds them into a live frame, the browser renders them into the
transcript, and Telegram edits its own message as text arrives.

## Failure

- A provider error is reported as an event and ends the turn cleanly.
- A tool error goes back to the model, which usually recovers.
- An MCP server that will not start is reported and skipped.
- A gateway that drops reconnects with exponential backoff.
- A panic in a handler is recovered and logged with the request that caused it.

Nothing here takes the process down. A single tool that will not run should not
cost you the session.

## Testing

Go tests cover the cron parser, provider adapters, the MCP client, the store
against both drivers, the WebSocket client, the hub, the harness, and the
browser against a real Chromium.

The dashboard is checked by loading every route in a headless browser, taking
screenshots at desktop and mobile widths, and asserting no horizontal overflow.
That exists because two of the worst bugs so far passed every type check and
blanked the entire page. Nothing static catches that class of failure.
