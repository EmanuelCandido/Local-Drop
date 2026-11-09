#!/usr/bin/env sh
set -eu
PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
APP="$PROJECT_ROOT/dist/localdrop"
if [ ! -f "$APP" ]; then
  printf 'dist/localdrop não existe. Execute ./scripts/build.sh primeiro.\n' >&2
  exit 1
fi
exec "$APP" "$@"
