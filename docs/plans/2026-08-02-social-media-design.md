# Social Media Feature Design

> Status: Approved design
> Date: 2026-08-02

## Overview

Agents get their own social media presence. A single Gmail/IMAP mailbox configured
by the user serves as the verification channel. A persistent stealth browser
(cloak-go) holds all social media login sessions. Social account credentials are
encrypted at rest in SQLite. A new `social-media-manager` role drives account
creation, content publishing, and self-directed skill/RAG updates. Autopilot can
be toggled on/off. No approval workflow, no audit, no versioning bureaucracy —
agents update skills and RAG directly.

## Scope

### In scope (Phase 1)

- Master encryption key setup (`~/.antares/secrets.env`) with one-time recovery
  key display.
- SQLite schema for `social_accounts` table (encrypted credentials).
- IMAP/Gmail configuration + connection test API.
- Persistent social browser manager (cloak-go) with start/stop/status/open.
- Social Media Page (frontend): Gmail setup, browser controls, account list,
  autopilot toggle, onboarding reminder.
- Non-blocking migration: old installations see reminder only.

### In scope (Phase 2)

- `social-media-manager` role with seed prompt.
- Platform-specific RAG namespaces (`social/instagram`, `social/facebook`,
  `social/threads`, `social/x`, `social/shared`).
- Dynamic skill creation/update by agent.
- Autopilot scheduling for maintenance/learning/inbox.
- Manual task entry ("create Threads account", "learn IG algorithm change").

### Out of scope (Phase 3+)

- Content scheduling calendar.
- Analytics dashboard.
- Multi-user browser profiles.
- OAuth provider integrations (direct credential entry first).
- Post draft management UI.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  Social Media Page              │
│  ┌──────────┐  ┌──────────────┐  ┌───────────┐ │
│  │ IMAP/Gmail│  │ Browser Ctrl │  │ Autopilot │ │
│  │  Settings │  │ Start/Stop   │  │  Toggle   │ │
│  └──────────┘  └──────────────┘  └───────────┘ │
│  ┌───────────────────────────────────────────┐ │
│  │            Account Grid                    │ │
│  │  [Instagram]  [Facebook]  [Threads]  [X]   │ │
│  └───────────────────────────────────────────┘ │
└─────────────────────────────────────────────────┘
                        │
                        ▼
┌─────────────────────────────────────────────────┐
│                 Backend API                      │
│  GET  /api/social/status                         │
│  POST /api/social/imap/test                      │
│  POST /api/social/imap/save                     │
│  POST /api/social/browser/start                 │
│  POST /api/social/browser/stop                  │
│  POST /api/social/browser/open                  │
│  POST /api/social/autopilot                     │
│  GET  /api/social/accounts                      │
│  POST /api/social/accounts                      │
│  DEL  /api/social/accounts/{id}                 │
└─────────────────────────────────────────────────┘
                        │
            ┌───────────┼───────────┐
            ▼           ▼           ▼
     ┌──────────┐ ┌──────────┐ ┌──────────┐
     │ Secret   │ │ Browser  │ │  Store   │
     │ Manager  │ │ Manager  │ │ (SQLite) │
     │ AES-GCM  │ │ cloak-go │ │ encrypted│
     └──────────┘ └──────────┘ └──────────┘
```

## Components

### 1. Master Encryption Key

**File:** `~/.antares/secrets.env`

```
ANTARES_MASTER_KEY=<base64-encoded-32-byte-key>
```

- Generated on first Social Media setup (onboarding or post-update).
- File permission `0600`; parent dir `0700`.
- Read at startup; never logged, never sent to API/Frontend/RAG/prompt.
- One-time recovery key shown after generation; downloadable as text file.
- If key + file lost: all social credentials unrecoverable (by design).
- Existing `internal/secret/secret.go` (`.secretkey`) remains for VPS; social
  uses the new managed env file.
- `ANTARES_MASTER_KEY` environment variable overrides the file.

**Migration:**

- Old installations: dashboard shows "Set up Social Media encryption" reminder.
- Non-blocking: chat, tools, projects, all existing features keep working.
- Social Media Page opens but credential storage is disabled until key exists.
- After key creation: restart required.

### 2. SQLite Schema

```sql
CREATE TABLE IF NOT EXISTS social_accounts (
    id                  TEXT PRIMARY KEY,
    platform            TEXT NOT NULL,
    display_name        TEXT NOT NULL DEFAULT '',
    username            TEXT NOT NULL DEFAULT '',
    encrypted_password  TEXT NOT NULL DEFAULT '',
    encrypted_recovery  TEXT NOT NULL DEFAULT '',
    profile_url         TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'not_created',
    rag_namespace       TEXT NOT NULL DEFAULT '',
    skill_name          TEXT NOT NULL DEFAULT '',
    last_checked_at     BIGINT,
    created_at          BIGINT NOT NULL,
    updated_at          BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_social_accounts_platform
ON social_accounts(platform);
```

- `encrypted_password` and `encrypted_recovery` store AES-256-GCM ciphertext
  (nonce + ciphertext, base64).
- Username is plaintext (needed for display and login).
- Statuses: `not_created`, `pending`, `verification_required`, `connected`,
  `suspended`, `error`.
- Cookies/sessions live in browser profile, not in this table.

### 3. IMAP/Gmail Configuration

- Stored in `social_config` table or KV:
  - `social.imap.host` (default: `imap.gmail.com`)
  - `social.imap.port` (default: `993`)
  - `social.imap.username` (plaintext, it's just an email)
  - `social.imap.encrypted_password` (AES-GCM)
  - `social.imap.enabled`
- API:
  - `POST /api/social/imap/test` — validate connection, return inbox count.
  - `POST /api/social/imap/save` — encrypt and persist.
- Agent reads email via a `email_read` tool that wraps IMAP fetch.

### 4. Persistent Social Browser (cloak-go)

**Package:** `internal/socialbrowser/`

- One persistent browser profile: `~/.antares/social-browser/profile/`
- Fingerprint seed generated once, stored in `~/.antares/social-browser/seed`.
- Browser managed as singleton with mutex lock (one controller at a time).
- `cloak.Launch()` with `UserDataDir` and stable fingerprint.
- Headless=false (shared live browser; user can see/take over).
- API:
  - `POST /api/social/browser/start` — launch if not running.
  - `POST /api/social/browser/stop` — close browser.
  - `POST /api/social/browser/open` — open visible window / focus.
  - `GET /api/social/status` — includes browser state.
- State: `disabled | unavailable | stopped | starting | running | error`.
- Start is async (can take up to 45s for binary download/verify).
- All mutations require `requireDashboardPassword`.
- Browser debug URL, cookies, profile internals never exposed in API responses.

### 5. Social Media Page (Frontend)

**Route:** `/social-media`

- Not `primary` (sidebar only, not mobile bottom nav).
- Uses `PageLayout`, `Card`, `Badge`, `Switch`, `Button`, `EmptyState`.
- Sections:
  1. Onboarding reminder (page-local, dismissible).
  2. Gmail/IMAP settings card.
  3. Browser status + controls card.
  4. Autopilot toggle card.
  5. Account grid (responsive: 1 col mobile, 2 col tablet, 3 col desktop).
- Sensitive operations behind `SensitiveGate`.
- Poll `/api/social/status` every 5s when browser running.
- No optimistic updates; mutate then reload.

### 6. Social Media Role (Phase 2)

**Directory:** `~/.antares/roles/social-media-manager/`

- Seed prompt defines: create accounts, learn platforms, publish content,
  manage inbox, update skills/RAG autonomously.
- Toolset includes: `terminal`, `browser` (social), `email_read`, `read_file`,
  `write_file`, `edit_file`, plus social-specific tools.
- Model: default or user-configured.
- Autopilot schedule: check inbox, learn platform changes, create content,
  publish, monitor accounts.

### 7. Platform RAG (Phase 2)

- Per-platform namespace: `social/instagram`, `social/facebook`, etc.
- Shared namespace: `social/shared`.
- Agent creates new namespace when discovering a new platform.
- RAG indexed from: signup flow observations, API docs, scraping results,
  error messages, successful workflows.
- No secrets in RAG. No passwords, cookies, OTP codes, or recovery codes.
- Skills live in `~/.antares/skills/social-*` and are created/updated by agent
  directly. No approval, no versioning, no rollback UI.

### 8. Autopilot (Phase 2)

- Global toggle: `social.autopilot.enabled`.
- When on: agent runs on schedule (cron-based) for:
  - Inbox checking (verification emails, OTP).
  - Platform learning (what changed, new features, algorithm updates).
  - Account health monitoring.
  - Content planning and publishing.
- When off: agent only runs on manual task.
- Hybrid: autopilot handles routine; manual tasks take priority.
- Schedule configurable in Social Media Page.

## API Endpoints

| Method | Path                          | Gate               | Description                    |
|--------|-------------------------------|--------------------|--------------------------------|
| GET    | /api/social/status            | none               | Aggregate status               |
| POST   | /api/social/imap/test         | dashboard password | Test IMAP connection           |
| POST   | /api/social/imap/save         | dashboard password | Save IMAP credentials          |
| POST   | /api/social/browser/start     | dashboard password | Launch persistent browser      |
| POST   | /api/social/browser/stop      | dashboard password | Stop browser                   |
| POST   | /api/social/browser/open      | dashboard password | Open/focus browser window      |
| POST   | /api/social/autopilot         | dashboard password | Toggle autopilot               |
| GET    | /api/social/accounts          | none               | List accounts (redacted)       |
| POST   | /api/social/accounts          | dashboard password | Add account                    |
| DELETE | /api/social/accounts/{id}     | dashboard password | Remove account                 |
| POST   | /api/social/encryption/setup  | loopback/bearer    | Generate master key             |

## Acceptance Criteria

### Phase 1

- [ ] Master key generated to `~/.antares/secrets.env` with `0600` permissions.
- [ ] One-time recovery key displayed and downloadable.
- [ ] Old installations show non-blocking reminder; existing features work.
- [ ] IMAP credentials encrypted with AES-256-GCM before SQLite write.
- [ ] IMAP connection test returns success/failure with inbox count.
- [ ] Persistent browser launches with stable fingerprint and user-data-dir.
- [ ] Browser start/stop/status/open APIs work.
- [ ] Only one browser instance runs at a time (mutex).
- [ ] Browser state never exposes debug URL or cookies in API.
- [ ] Social Media Page renders responsively on mobile/tablet/desktop.
- [ ] Account grid shows platform, username, status badge, last checked.
- [ ] Autopilot toggle persists and reflects in status.
- [ ] All sensitive mutations return 428 without dashboard password.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./internal/social...` passes.
- [ ] `bun test` passes.
- [ ] `bun x tsc -b --noEmit` passes.
- [ ] `bun run build` passes.

### Phase 2

- [ ] `social-media-manager` role loads from seed prompt.
- [ ] Agent can read IMAP inbox via `email_read` tool.
- [ ] Agent can operate persistent browser via `browser` tool.
- [ ] Agent creates platform RAG namespace for new platforms.
- [ ] Agent creates/updates skill files directly.
- [ ] Autopilot runs agent on schedule.
- [ ] Manual tasks interrupt/override autopilot.
- [ ] Agent handles CAPTCHA/phone verification by requesting human help.

## Implementation Order (Phase 1)

1. `internal/secret/social.go` — master key load/generate/validate.
2. `internal/store/migrations.go` — append social_accounts + social_config.
3. `internal/store/social.go` — CRUD with encrypt/decrypt.
4. `internal/socialbrowser/manager.go` — cloak-go singleton wrapper.
5. `internal/server/handlers_social.go` — API endpoints.
6. `internal/server/routes.go` — route registration.
7. `internal/config/config.go` — Social config struct.
8. `internal/config/defaults.go` — defaults.
9. `web/src/pages/SocialMediaPage.tsx` — frontend page.
10. `web/src/lib/routes.ts` — route + nav.
11. `web/src/lib/i18n.tsx` — translations.
12. `scripts/smoke.mjs` — smoke route.
13. Tests: backend + frontend + race + build.
