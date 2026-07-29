# Installation

Antares is a single binary. The installer downloads the prebuilt binary for your
platform from the project's **GitHub Releases** — no build tools required.

Works on **Linux**, **macOS**, and **Windows** (amd64 and arm64).

> **Early release, private repo.** While the repository is private, release
> assets are not publicly downloadable. Install the
> [GitHub CLI](https://cli.github.com) and run `gh auth login` once — the
> installer uses it automatically. When the repo goes public this step goes
> away and the plain URL download just works.

## One-line install

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/antares/main/scripts/install.sh | bash
```

Installs to `~/.local/bin/antares`. Knobs:

```bash
# specific version, and a system-wide location
curl -fsSL .../install.sh | ANTARES_VERSION=v0.1.0 PREFIX=/usr/local bash
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/enowdev/antares/main/scripts/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Antares\bin\antares.exe` and adds it to your user
PATH. Open a **new** terminal afterwards. Pin a version with
`$env:ANTARES_VERSION='v0.1.0'` before running.

> Piping a script to your shell runs code from the internet. To read it first,
> download `scripts/install.sh` / `install.ps1` and run it locally.

## First run

```bash
antares setup      # configure a provider (browser or terminal wizard)
antares            # terminal UI
antares serve      # API + dashboard on http://localhost:8787
```

`antares setup` writes `~/.antares/config.yaml` (Windows:
`%USERPROFILE%\.antares\config.yaml`). See [Getting started](getting-started.md)
for what setup asks.

## Upgrading

Re-run the installer — it overwrites the binary in place. Pass
`ANTARES_VERSION` to move to a specific release.

## Build from source

You only need this to develop Antares or to run an unreleased commit. It needs
**Go 1.21+** (the Go toolchain auto-fetches the exact version the project pins),
**Bun or npm**, and **git**.

```bash
git clone https://github.com/enowdev/antares.git
cd antares
make install       # toolchain deps (Go modules, Air, dashboard packages)
make build         # → ./bin/antares (dashboard embedded)
make install-cli   # build and install to ~/.local/bin (PREFIX overrides)
```

## Cutting a release (maintainers)

Until a CI workflow does this on tag push (added when the repo is public),
releases are built locally and uploaded by hand:

```bash
VERSION=v0.1.0 ./scripts/release-build.sh      # cross-compiles all 6 targets
gh release create v0.1.0 dist/release/* --title v0.1.0 --notes "…"
```

`release-build.sh` builds the dashboard once, embeds it, and cross-compiles the
binary for linux/macOS/windows on amd64 and arm64 into `dist/release/`, with a
`checksums.txt`. The asset names are what `install.sh`/`install.ps1` expect:
`antares_<version>_<os>_<arch>[.exe]`.

## Uninstall

```bash
# Linux / macOS
rm ~/.local/bin/antares
rm -rf ~/.antares            # optional: config, sessions, memory

# Windows (PowerShell)
Remove-Item "$env:LOCALAPPDATA\Antares\bin\antares.exe"
Remove-Item -Recurse "$env:USERPROFILE\.antares"   # optional
```

## Troubleshooting

- **`no asset … in release` / download fails** — the repo is likely still
  private. Install the [GitHub CLI](https://cli.github.com), run
  `gh auth login`, and re-run the installer.
- **`antares: command not found` after install** — the install dir is not on
  your PATH. The installer prints the line to add; on Windows, open a new
  terminal.
- **macOS: "cannot be opened because the developer cannot be verified"** — the
  installer clears the quarantine flag; if you moved the binary yourself, run
  `xattr -d com.apple.quarantine <path>`.
- **Windows: "running scripts is disabled"** — allow the current session:
  `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass`, then re-run.

Found a rough edge? This is an early release — please
[open an issue](https://github.com/enowdev/antares/issues) or send a PR.
