# Intercept subsystem — HTTP Toolkit port status

# Antares Intercept Subsystem — Port Implementation Plan

Grounded against the live code: `internal/tools/intercept.go` (shared `InterceptProxy()` singleton, flat `intercept.Rule`, `RequiresApproval()==true`, `ca` action prints a path), `internal/intercept/{proxy.go,ca.go}` (CONNECT-based plain-HTTP MITM, per-host leaf signing, `Status()`, `CACertPEM()`, `Exchanges()`, `Rules()`).

## 1. Component matrix

| Component | go_feasibility | effort | External deps | Verdict |
|---|---|---|---|---|
| **cert-check-and-install** | native | medium | none (install helpers gated on `security`/`certutil`/`adb`) | **PORT NOW** |
| **breakpoints-and-rules** | native | large | none | **PORT NOW** (phase the breakpoint half) |
| **browser-chromium** | native | medium | chrome/edge/brave/opera binary | **PORT NOW** |
| **terminal** | native | medium | terminal emulator (fresh only); curl (existing) | **PORT NOW** |
| **dns-server** | native | small | none (`x/net/dnsmessage`); udp/53 bind is root-gated | **PORT NOW** (lead with Chrome `--host-resolver-rules`) |
| **browser-firefox** | shell-out | medium | firefox binary; **certutil (hard req)**; libnss3 | **DEP-GATE** |
| **electron** | native | large | operator-supplied Electron app; embedded `prepend-electron.js` | **DEP-GATE** (native but heavy; gate on inspector target) |
| **android-adb** | shell-out | large | `adb`; device; root (optional); host reachability | **DEP-GATE** |
| **docker** | partial | large | docker socket; SOCKS-tunnel image; shim assets | **DEP-GATE** (attach-mode only; DEFER daemon-proxy + tunnel/DNS) |
| **jvm** | shell-out | large | java (JRE/JDK); **prebuilt java-agent.jar** (compiled out-of-band) | **DEFER** (Phase-1 `jvm_env` only; runtime-attach + jar deferred) |

## 2. Build order (waves)

**Wave 0 — Shared foundation** (blocks everything; see §3). CA accessors + SPKI/subject-hash math + interceptor registry + client-configurator interface + `Proxy` upstream-dialer hook. Small, native, no new deps.

**Wave 1 — Native, cheap, high-value, zero new deps.** These deliver "point a real client at the proxy with no trust-store fiddling" for the 80% case.
1. **cert-check-and-install** — verification server + `SubjectHashOld`/`Fingerprint`/`InstallLocations`. Underpins every other component's "is trust working" story. (medium)
2. **browser-chromium** (Fresh variant only) — the SPKI-fingerprint flag means *no cert install at all*. Highest value-per-line. (medium)
3. **dns-server** + Chrome `--host-resolver-rules` steering — small; unlocks hostname redirect for the browser launcher and later docker/adb. (small)
4. **terminal** (existing-variant first: script-serving server; then fresh spawn) — env-var core intercepts curl/git/python/node/rust/aws/deno with zero trust changes. (medium)

**Wave 2 — Native but structurally invasive.**
5. **breakpoints-and-rules** — Phase 2a: rich matchers + transform/forward/reset/delay steps (medium). Phase 2b: breakpoint pause/resume plumbing on synchronous `forward()` (large). Touches the proxy core, so do it after Wave 1 has stabilized the shared accessors.

**Wave 3 — Native/shell-out, dependency-gated, one platform target each.**
6. **browser-firefox** — reuses Chromium detection scaffolding + the CA PEM; gated on `certutil`. (medium)
7. **electron** — native CDP-over-WebSocket + embedded `prepend-electron.js`; gated on inspector target. Reuses Wave-0 SPKI. (large)

**Wave 4 — Shell-out / partial, heaviest, most environment-specific. Ship gated stubs first, real impl behind flags.**
8. **android-adb** — `adb` shell-out; app-free `settings put global http_proxy` path; root-gated system-cert injection. (large)
9. **docker** — attach-mode (env+CA-volume inject on recreate) only; daemon-proxy socket + SOCKS-tunnel/DNS **deferred**. Needs the Wave-0 upstream-dialer hook. (large)
10. **jvm** — Phase 1 `jvm_env` (JAVA_TOOL_OPTIONS string) only; runtime-attach + embedded jar deferred to a later milestone. (large)

## 3. Shared foundation to build first (Wave 0)

Build these before any interceptor so every component plugs into one contract:

**A. CA / crypto accessors** — `internal/intercept/ca.go`:
- `(*CA) Cert() *x509.Certificate` and `CertDER() []byte` accessors (needed by SPKI, subject-hash, cert-check TLS config, firefox/electron/android).
- `(*CA) SPKIFingerprint() string` = `base64.StdEncoding(sha256(cert.RawSubjectPublicKeyInfo))` — the linchpin for Chromium/Electron/Android CT-bypass, zero trust-store install.
- Keep the persisted PEM path as the single source: `config.Path("intercept","ca-cert.pem")`.

**B. Trust math** — `internal/intercept/trust.go` (pure stdlib):
- `SubjectHashOld(c)` = `fmt.Sprintf("%08x", binary.LittleEndian.Uint32(md5.Sum(c.RawSubject)[:4]))` (use `RawSubject` directly — no DN re-encoding).
- `Fingerprint(c)` = hex `sha1.Sum(c.Raw)`.
- `(*CA) InstallLocations() []InstallTarget` returning per-OS command strings (macOS `security`, Linux `update-ca-certificates`, Windows `certutil -addstore`, NSS `certutil`, Android `<hash>.0` push), each gated by `exec.LookPath` so it degrades to "here's the command."

**C. Interceptor registry + lifecycle** — new `internal/intercept/interceptor.go`:
```
type Interceptor interface {
    ID() string
    Available(ctx) (ok bool, reason string)   // dependency gate; never errors hard
    Activate(ctx, ActivateOpts) (Session, error)
    // Deactivate lives on Session
}
type Session interface { ID() string; Stop() error; Info() map[string]any }
type ActivateOpts struct { ProxyAddr string; CACertPath string; SPKIFingerprint string; Extra map[string]any }
```
A package-level `Registry` holds interceptors; the tool layer resolves `ProxyAddr` from `Proxy.Status()` and `CACertPath` from config, auto-starting the proxy if down. Sessions stored in a mutexed map hung off `interceptState` so `*_stop` and `status` can find them. This unifies the ad-hoc "activeBrowsers"/pid-map patterns from every HTTP Toolkit interceptor into one contract.

**D. Dependency-gate pattern** — extend `internal/depcheck` with entries for `adb`, `certutil`, `java`, `docker` (Bins, Purpose, per-OS InstallHint). Every gated interceptor's `Available()` calls into this; the tool no-ops cleanly with a copy-pasteable install hint instead of crashing. Preserves the single-static-binary reality.

**E. Proxy upstream-dialer hook** — `internal/intercept/proxy.go`: replace the fixed `http.Transport` dialer in `forward()`/`handleConnect()` with an optional `DialContext` selector: `func(host string) (dialer, ok)`. Needed by docker's SOCKS tunnel (route container-alias hosts through `socks5://127.0.0.1:<tunnelPort>`) and useful for DNS-steered targets. This is the *only* structural change the MITM core needs; build the seam now even if the first consumer is Wave 4.

**F. Breakpoint model seam** — reserve the pause/resume shape now even though impl is Wave 2: `Proxy.pending map[int64]*PausedExchange`, `ListPaused()`, `Resume(id, Edit)`, per-breakpoint `context` timeout so a stalled pause can't wedge a client connection. Interactive resume maps onto the existing `Input.AskUser` turn-pause.

**Tool-surface convention:** extend the single `interceptTool` with new actions (`browser_launch`, `browser_firefox`, `electron_launch`, `terminal_env`/`terminal_serve`/`terminal_spawn`, `dns_*`, `cert_check`, `cert_install`, `docker_*`, `android_*`, `jvm_env`, `bp_list`/`bp_resume`, richer `rule_add`) rather than spawning new tools — keeps `RequiresApproval()==true` and one status surface. Mirror each with a `/intercept/*` handler in `internal/server/handlers_intercept.go`.

## 4. Deferred / impractical items — one-line rationale + gated-stub message

- **jvm (runtime-attach + agent jar)** — the actual proxy/TrustManager rewrite lives in a **compiled java-agent.jar that cannot be authored in Go** and needs a full JDK with working Attach APIs. Stub (`jvm` action when jar/JDK absent): *"JVM runtime-attach needs a prebuilt java-agent.jar and a JDK with attach support. Not bundled in this build. Use `action:jvm_env` to get a JAVA_TOOL_OPTIONS string, or install a JDK and supply an agent jar."*
- **docker daemon-proxy socket + SOCKS-tunnel/DNS** — transparent `docker run`/`build`/compose interception needs a fake-daemon unix socket (Dockerfile splice + tar repack + build-output rewrite) and a prebuilt SOCKS-tunnel image for custom-bridge reachability — large surface, external image dep. Stub: *"Auto-intercept of `docker run`/`build` is not enabled; use `action:docker_attach {container_id}` to intercept a named running container, or set HTTP_PROXY/CA env in your Dockerfile."*
- **android-adb companion APK + APEX/nsenter system-cert injection** — the HTTP Toolkit VPN APK is an external GitHub artifact and root system-cert injection is device/version-specific. Stub when `adb` missing: *"Android interception needs `adb` (Android platform-tools): `brew install android-platform-tools`."* Stub when device present but unrooted: *"Device is not rooted; using app-free `settings put global http_proxy` — only user-cert-trusting apps will be intercepted. System-cert injection (all apps) requires root."*
- **electron (non-inspector targets)** — injection only lands in a Node **main process that is an `--inspect-brk` target**; packaged/fused apps that reject the inspector can't be hooked. Stub: *"Target did not expose a Node inspector on --inspect-brk; only standard/unpackaged Electron builds are interceptable. Renderer/<webview> traffic is not captured — expect main-process Node coverage only."*
- **browser-firefox (no certutil)** — Firefox ignores the OS trust store; without `certutil` writing the CA into the profile's cert9.db every HTTPS site fails with SEC_ERROR_UNKNOWN_ISSUER. Stub: *"Firefox interception requires `certutil` (NSS): `brew install nss` / `apt install libnss3-tools`. Without it HTTPS cannot be trusted in Firefox."*
- **terminal advanced runtime shims** (PATH bin-shims, NODE_OPTIONS prepend, `-javaagent`, Ruby/Python/PHP dirs, DOCKER_HOST hijack) — need `go:embed`'d assets / a JVM agent / a docker-proxy. Deferred behind the env-var core. Stub: *"Advanced runtime hooks (node --require, java-agent, docker) are not enabled; the env-var interception covers curl/git/python/node/rust/aws/deno HTTPS."*
- **Existing-browser variant (Chromium/Firefox), Windows registry detection, snap paths** — process-kill/enumeration + `--restore-last-session` + Windows `WM_CLOSE`; deferred after Fresh. Stub: *"Only the Fresh (throwaway-profile) variant is available; the Existing-profile variant is not yet supported on this platform."*
