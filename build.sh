#!/bin/bash
# Builds the daemons into ./build.
#
#   ./build.sh client     build the web app, then tam-client
#   ./build.sh server     build tam-server
#   ./build.sh all        both
#
# Cross-compile by setting GOOS and GOARCH, for example:
#   GOOS=linux GOARCH=amd64 ./build.sh all
set -euo pipefail
cd "$(dirname "$0")"

target="${1:-}"
goos="${GOOS:-$(go env GOOS)}"
ext=""
if [ "$goos" = "windows" ]; then
  ext=".exe"
fi
mkdir -p build

pnpm_cmd="pnpm"
if ! command -v pnpm >/dev/null 2>&1; then
  pnpm_cmd="npx --yes pnpm@latest"
fi

build_web() {
  (cd frontend && $pnpm_cmd install --frozen-lockfile && $pnpm_cmd build)
}

build_client() {
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "build/tam-client${ext}" ./cmd/tam-client/
}

build_server() {
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "build/tam-server${ext}" ./cmd/tam-server/
}

case "$target" in
  client) build_web; build_client ;;
  server) build_server ;;
  all) build_web; build_client; build_server ;;
  *) echo "Usage: $0 client|server|all"; exit 1 ;;
esac

if command -v upx >/dev/null 2>&1; then
  upx -q build/tam-*"${ext}" || true
fi
ls -la build/
