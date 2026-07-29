#!/usr/bin/env bash
# Antares installer for Linux and macOS.
#
# Downloads the prebuilt `antares` binary for your platform from the project's
# GitHub Releases and installs it. No build tools required.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/enowdev/antares/main/scripts/install.sh | bash
#
# Env knobs:
#   PREFIX=/usr/local     install dir root (binary lands in $PREFIX/bin); default ~/.local
#   ANTARES_VERSION=v0.1.0  install a specific release; default: latest
#   ANTARES_REPO=owner/name GitHub repo; default enowdev/antares
#
# While the repository is private, releases are not publicly downloadable. Have
# the GitHub CLI installed and authenticated (`gh auth login`); this script uses
# it automatically when present. Once the repo is public, no auth is needed.

set -euo pipefail

REPO="${ANTARES_REPO:-enowdev/antares}"
VERSION="${ANTARES_VERSION:-latest}"
PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# ---- detect platform --------------------------------------------------------
os="$(uname -s)"; arch="$(uname -m)"
case "$os" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) die "unsupported OS '$os' — on Windows use scripts/install.ps1" ;;
esac
case "$arch" in
  x86_64|amd64)  goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) die "unsupported architecture '$arch'" ;;
esac
info "platform: $goos/$goarch"

# The asset name matches scripts/release-build.sh output.
asset_for() { echo "antares_${1}_${goos}_${goarch}"; }

mkdir -p "$BINDIR"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
dest="$tmp/antares"

# ---- fetch ------------------------------------------------------------------
# Prefer the gh CLI (works for a private repo); otherwise use public release URLs.
if have gh; then
  info "downloading via GitHub CLI ($REPO, $VERSION)"
  ver="$VERSION"
  if [ "$ver" = "latest" ]; then
    ver="$(gh release view --repo "$REPO" --json tagName -q .tagName)" \
      || die "could not read the latest release. Is 'gh' authenticated? Try: gh auth login"
  fi
  asset="$(asset_for "$ver")"
  gh release download "$ver" --repo "$REPO" --pattern "$asset" --output "$dest" --clobber \
    || die "no asset '$asset' in release $ver. Available assets: $(gh release view "$ver" --repo "$REPO" --json assets -q '.assets[].name' | tr '\n' ' ')"
else
  have curl || die "need curl (or the gh CLI) to download."
  if [ "$VERSION" = "latest" ]; then
    base="https://github.com/$REPO/releases/latest/download"
    # The 'latest' redirect does not know the version, so try the unversioned
    # name first; release-build.sh embeds the version, so also expose a stable
    # alias if you upload one. Otherwise pass ANTARES_VERSION explicitly.
    ver=""
  else
    ver="$VERSION"
    base="https://github.com/$REPO/releases/download/$ver"
  fi
  [ -n "$ver" ] || die "without the gh CLI you must pass a version, e.g. ANTARES_VERSION=v0.1.0 (the repo may still be private, in which case install the gh CLI and run 'gh auth login')"
  asset="$(asset_for "$ver")"
  url="$base/$asset"
  info "downloading $url"
  curl -fsSL "$url" -o "$dest" \
    || die "download failed. If the repo is still private, install the GitHub CLI (gh) and run 'gh auth login', then re-run."
fi

[ -s "$dest" ] || die "downloaded file is empty."

# ---- install ----------------------------------------------------------------
chmod +x "$dest"
mv -f "$dest" "$BINDIR/antares"
# macOS Gatekeeper quarantines downloaded binaries; clear it so it runs.
if [ "$goos" = "darwin" ] && have xattr; then
  xattr -d com.apple.quarantine "$BINDIR/antares" 2>/dev/null || true
fi
info "installed $BINDIR/antares"
"$BINDIR/antares" --version 2>/dev/null || true

# ---- PATH --------------------------------------------------------------------
# Add $BINDIR to PATH automatically (matching the Windows installer). We append
# an export to the shell rc files that exist, guarded by a marker so re-running
# never duplicates it. Set ANTARES_NO_MODIFY_PATH=1 to skip and just be told.
add_path_line() {
  local rc="$1" marker="# added by antares installer"
  [ -e "$rc" ] || return 0
  grep -qF "$marker" "$rc" 2>/dev/null && return 0
  {
    printf '\n%s\n' "$marker"
    printf 'export PATH="%s:$PATH"\n' "$BINDIR"
  } >> "$rc"
  info "added $BINDIR to PATH in $rc"
}

case ":$PATH:" in
  *":$BINDIR:"*)
    info "run 'antares' from anywhere (open a new shell if not found yet)"
    ;;
  *)
    if [ "${ANTARES_NO_MODIFY_PATH:-0}" = "1" ]; then
      warn "$BINDIR is not on your PATH. Add it:"
      echo "    echo 'export PATH=\"$BINDIR:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
    else
      touched=0
      # bash and zsh: both .profile-style and shell-specific rc files.
      for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
        [ -e "$rc" ] && { add_path_line "$rc"; touched=1; }
      done
      # zsh with no .zshrc yet (common on macOS): create one so it takes effect.
      if [ -n "${ZSH_VERSION:-}" ] || [ "${SHELL##*/}" = "zsh" ]; then
        [ -e "$HOME/.zshrc" ] || { add_path_line "$HOME/.zshrc"; touched=1; }
      fi
      # fish keeps PATH differently.
      if [ -d "$HOME/.config/fish" ]; then
        fish_cfg="$HOME/.config/fish/config.fish"
        if ! grep -qF "# added by antares installer" "$fish_cfg" 2>/dev/null; then
          { printf '\n# added by antares installer\n'; printf 'set -gx PATH %s $PATH\n' "$BINDIR"; } >> "$fish_cfg"
          info "added $BINDIR to PATH in $fish_cfg"
          touched=1
        fi
      fi
      if [ "$touched" = "1" ]; then
        warn "PATH updated — open a NEW terminal (or 'source' your shell rc) for 'antares' to be found."
      else
        warn "$BINDIR is not on your PATH and no shell rc file was found. Add it:"
        echo "    echo 'export PATH=\"$BINDIR:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
      fi
    fi
    ;;
esac

echo
info "next: run 'antares setup' to configure a provider, then 'antares' or 'antares serve'"
