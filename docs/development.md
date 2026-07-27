# Development

```bash
make install     # Go modules, Air, dashboard packages
make dev         # backend and frontend, both hot-reloading
make check       # go vet, go test, tsc
make build       # one binary with the dashboard embedded
```

`make dev` runs the API on `:8787` with [Air](https://github.com/air-verse/air)
watching Go files, and Vite on `:5173` proxying `/api` to it. Both bind
`0.0.0.0`, so another machine on the network can use them.

## Targets

| | |
|---|---|
| `make dev` | Both servers |
| `make dev-api` | Backend only |
| `make dev-web` | Frontend only |
| `make check` | Vet, test, typecheck |
| `make test` | Go tests |
| `make smoke` | Load every dashboard route in a real browser |
| `make build` | Production binary |
| `make doctor` | Diagnose the configuration |

## Layout

Go code is in `internal/`, one package per concern, described in
[Architecture](architecture.md). The dashboard is in `web/`.

## Adding things

**A tool** — implement the four-method interface in `internal/tools`, register
it in `register.go`, add it to the toolsets that should have it. See
[Tools](tools.md).

**A command** — one `register(Spec{...}, handler)` in `internal/commands`. It
appears in the terminal, the web chat, and the gateways at once. See
[Commands](commands.md).

**A provider** — implement `llm.Client` in `internal/llm`. Most endpoints are
OpenAI-shaped and need no new adapter, only configuration.

**A dashboard page** — add an entry to `web/src/lib/routes.ts` and the component.
Navigation, routing, and chrome follow from the entry; the page itself contains
content only.

**A store method** — add it to the `Store` interface and implement it once in
`internal/store/sql.go`. SQLite and Postgres share the implementation; only
search differs.

## Testing

```bash
make test
go test ./internal/store/ -run TestMemory -v
```

The browser tests drive a real Chromium and skip when none is installed. The
store tests run against SQLite in a temporary directory; set `TEST_POSTGRES_DSN`
to run them against Postgres too.

The dashboard is checked by loading every route in a headless browser, which is
what `make smoke` does. That exists because two of the worst bugs so far passed
`tsc` and `go vet` cleanly and blanked the entire page: a hook called from inside
an effect, and the server bouncing SPA routes to `./`. Static analysis cannot see
either.

## Style

**Go.** Standard library first. `gofmt`. Errors wrapped with context, not
swallowed. Comments explain why, not what — a comment restating the code is
noise, and one explaining a non-obvious decision is worth several lines.

**TypeScript.** No semicolons, single quotes, 100 columns. Function components
with hooks. Tailwind utilities, no CSS files beyond the token definitions.

**Both.** Match the code around you. If a file does something one way, do it that
way, or change the whole file.

## Commits

The subject says what changed, in the imperative, under about 70 characters. The
body says why — the diff already says what. Where a decision was not obvious,
say what the alternative was and why it lost.
