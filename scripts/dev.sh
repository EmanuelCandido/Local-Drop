#!/usr/bin/env sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$PROJECT_ROOT/logs"

cd "$PROJECT_ROOT/go-backend"
LOCALDROP_AUTO_OPEN=false go run . --headless >"$PROJECT_ROOT/logs/backend-dev.log" 2>&1 &
BACKEND_PID=$!
trap 'kill "$BACKEND_PID" 2>/dev/null || true' EXIT INT TERM

cd "$PROJECT_ROOT/frontend"
printf 'Frontend: http://localhost:5173\nBackend:  http://localhost:8088\n'
npm run dev
