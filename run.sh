#!/bin/bash
# Runs a daemon from source for development.
#
#   ./run.sh client     build the web app, then run tam-client on localhost:3080
#   ./run.sh server     run tam-server on localhost:8000
set -euo pipefail
cd "$(dirname "$0")"

pnpm_cmd="pnpm"
if ! command -v pnpm >/dev/null 2>&1; then
  pnpm_cmd="npx --yes pnpm@latest"
fi

case "${1:-}" in
  client)
    (cd frontend && $pnpm_cmd build)
    go run ./cmd/tam-client/
    ;;
  server)
    go run ./cmd/tam-server/ dev
    ;;
  *)
    echo "Usage: $0 client|server"
    exit 1
    ;;
esac
