# Undone

Items from the source repos has not not implemented in Antares.

## CyberStrike — tools

- attack-script          ✅ ported → `internal/tools/hooks.go` (`attack_script`)
- awshook                ✅ ported → `internal/tools/hooks.go` (`awshook`)
- azurehook              ✅ ported → `internal/tools/hooks.go` (`azurehook`)
- kubehook               ✅ ported → `internal/tools/hooks.go` (`kubehook`)
- winhook                ✅ ported → `internal/tools/hooks.go` (`winhook`, Windows-only, .ps1 + .py)
- machook                ✅ ported → `internal/tools/hooks.go` (`machook`, macOS-only)
- cipipe                 ✅ ported → `internal/tools/hooks.go` (`cipipe`)
- ebpf                   ✅ ported → `internal/tools/hooks.go` (`ebpf`, Linux-only)
- hackbrowser            ✅ ported (v1) → `internal/hackbrowser/` + `internal/tools/hackbrowser.go`
- hackbrowser-launcher   ✅ collapsed into `internal/hackbrowser/api.go` (no subprocess needed in Go)

### hackbrowser v1 status

The full BFS crawl engine is ported and working:
- `agent.go`     — page queue, auth-phase transitions, post-login re-discovery
- `scanner.go`   — embedded `data/scanner.js` (550 lines of DOM collection logic) runs via CDP
- `navigator.go` — LLM planner using `internal/llm` (any provider antares supports)
- `executor.go`  — resolves role+label → CSS selector → CDP click/fill
- `capture.go`   — UI snapshot (embedded `data/ui_snapshot.js`) + raw HTTP wire format + UI/request correlation
- `auth.go`      — session save/load, auto-login, 2FA detect, manual-login wait
- `state.go`     — credential-scoped intelligence, empty-state queue, fingerprinting
- `api.go`       — `RunCrawl(ctx, opts, model) (CrawlResult, error)` — single entry point
- `scope.go`     — host matching via `golang.org/x/net/publicsuffix`

`internal/browser/` extended with: `ClickSelector`, `FillSelector`, `WaitForSelector`,
`PressSelector`, `Cookies`, `SetCookies`, `DrainNetwork`, `ResponseBody`.

Tool registered as `hackbrowser` in `internal/tools/register.go`, in the `security` toolset,
requires approval.

### Deferred to v2 (marked in code, not blocking use)

- Multi-credential page-diff (single-cred only in v1)
- Combobox → option mechanical expansion
- Re-plan when new elements appear mid-task (v1 plans once per page)
- Occlusion probe + dismiss-overlay retry
- In-page tactical-HUD login button (v1 uses terminal/dashboard prompt)
- Response body capture (`Network.getResponseBody` plumbing is in place; capture.go leaves Body empty)

## End-to-end verification (needs live third-party credentials)

- Azure OpenAI — live request
- AWS Bedrock — live request
- Google Vertex AI — live request
- GitHub Copilot — live request
- OpenAI Responses (codex) — live request
- Voice (TTS → STT round trip) — live request
- WhatsApp gateway — live message round trip
- Feishu gateway — live message round trip
- Signal gateway — live message round trip
