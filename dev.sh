#!/usr/bin/env bash
set -e

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
RED='\033[0;31m'
NC='\033[0m'

cleanup() {
  echo -e "\n${YELLOW}Shutting down...${NC}"
  kill 0 2>/dev/null
  exit 0
}
trap cleanup EXIT INT TERM

# Check air
AIR="$(command -v air 2>/dev/null || echo "$HOME/go/bin/air")"
if [ ! -x "$AIR" ]; then
  echo -e "${RED}air not found. Install with: go install github.com/air-verse/air@latest${NC}"
  exit 1
fi

# Check bun
BUN="$(command -v bun 2>/dev/null || echo "$HOME/.bun/bin/bun")"
if [ ! -x "$BUN" ]; then
  echo -e "${RED}bun not found. Install from https://bun.sh${NC}"
  exit 1
fi

echo -e "${CYAN}Antares Dev${NC}"
echo -e "  Backend: ${GREEN}http://localhost:8787${NC}"
echo -e "  Frontend: ${GREEN}http://localhost:${VITE_PORT:-5174}${NC}"
echo -e "  ${YELLOW}Open http://localhost:5173 in your browser${NC}"
echo ""

# Backend: hot reload via air
(
  cd "$ROOT"
  "$AIR" -c .air.toml
) &
BACKEND_PID=$!

# Frontend: Vite HMR, proxy /api to backend
# Use --port 5174 to avoid conflicts with other projects on 5173.
VITE_PORT="${VITE_PORT:-5174}"
(
  cd "$ROOT/web"
  "$BUN" x vite --host 0.0.0.0 --port "$VITE_PORT"
) &
FRONTEND_PID=$!

echo -e "${GREEN}Backend PID: ${BACKEND_PID}${NC}"
echo -e "${GREEN}Frontend PID: ${FRONTEND_PID}${NC}"
echo -e "${YELLOW}Press Ctrl+C to stop both.${NC}"
echo ""

wait
