#!/usr/bin/env bash
# Hot-reload the antares TUI preview with a real terminal.
#
#   ./scripts/tui-dev.sh
#
# Edit any file under internal/tui or cmd/tuidemo and save — the running TUI is
# killed and relaunched with your changes. Press Ctrl+C inside the TUI to quit.
set -u
cd "$(dirname "$0")/.."

GO="${GO:-$HOME/.local/sdk/go/bin/go}"
command -v "$GO" >/dev/null 2>&1 || GO="go"
BIN="./.air/tuidemo"
WATCH_DIRS=(internal/tui cmd/tuidemo)

mkdir -p .air

fingerprint() { find "${WATCH_DIRS[@]}" -name '*.go' -printf '%T@ ' 2>/dev/null; }

# Background watcher: when a watched .go file changes, kill the running TUI so
# the main loop rebuilds and reruns it.
(
  last="$(fingerprint)"
  while true; do
    sleep 1
    now="$(fingerprint)"
    if [ "$now" != "$last" ]; then
      last="$now"
      pkill -TERM -f "$BIN" 2>/dev/null
    fi
  done
) &
WATCHER=$!
trap 'kill "$WATCHER" 2>/dev/null; exit 0' INT TERM EXIT

while true; do
  printf '\033[2J\033[H'   # clear screen
  echo "building TUI preview…"
  if GOTOOLCHAIN=local "$GO" build -o "$BIN" ./cmd/tuidemo; then
    "$BIN"; code=$?
    # Exit 0 = the user quit cleanly (Ctrl+C in the TUI). Anything else means the
    # watcher killed it (reload) or it crashed — rebuild and rerun.
    if [ "$code" -eq 0 ]; then
      break
    fi
  else
    echo "build failed — fix the error and save to retry."
    # wait for a change before retrying
    fp="$(fingerprint)"
    while [ "$(fingerprint)" = "$fp" ]; do sleep 1; done
  fi
done
