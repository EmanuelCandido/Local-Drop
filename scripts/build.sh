#!/usr/bin/env sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

if [ "${SKIP_FRONTEND:-0}" != "1" ]; then
  cd "$PROJECT_ROOT/frontend"
  if [ ! -d node_modules ]; then
    npm ci
  fi
  npm run build
fi

if [ ! -f "$PROJECT_ROOT/frontend/dist/index.html" ]; then
  printf 'frontend/dist/index.html não existe; gere o frontend antes do build.\n' >&2
  exit 1
fi

mkdir -p "$PROJECT_ROOT/dist"
find "$PROJECT_ROOT/go-backend/web" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
cp -R "$PROJECT_ROOT/frontend/dist/." "$PROJECT_ROOT/go-backend/web/"

cd "$PROJECT_ROOT/go-backend"
go test ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$PROJECT_ROOT/dist/localdrop" .
printf 'LocalDrop pronto: %s\n' "$PROJECT_ROOT/dist/localdrop"
