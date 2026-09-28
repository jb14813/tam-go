#!/bin/bash
# Compatibility run against the original Ticket Auction Manager.
#
#   1. The original FastAPI server (ticket-auction-manager/tam at a pinned
#      commit) is started and the Go client's compat tests run against it.
#   2. The Go server is started; the original SvelteKit client and the Go
#      client are both pointed at it and driven by scripts/compat/drive.py,
#      which saves through one client and reads through the other.
#
# Needs: go, python3 (venv + pip), node with pnpm (or npx), git, curl.
# Variables: TAM_COMPAT_WORK (scratch directory, default a temp dir),
# TAM_ORIGINAL (an existing clone of the original, otherwise it is cloned),
# PYTHON (default python3, or python when python3 is missing).
set -euo pipefail
cd "$(dirname "$0")/../.."
ROOT=$(pwd)

PIN=7ff95fa228c23c14f5d86c131b079e83cbe19fde
WORK=${TAM_COMPAT_WORK:-$(mktemp -d)}
mkdir -p "$WORK"
ORIG=${TAM_ORIGINAL:-$WORK/tam}
PY=${PYTHON:-}
if [ -z "$PY" ]; then
  if command -v python3 >/dev/null 2>&1; then PY=python3; else PY=python; fi
fi
# The original server's reports use SQLite's concat() function. Fail here
# with the actual runtime problem instead of letting its 500 response make
# the Go client fall back to its local data during a compatibility check.
"$PY" - <<'PY'
import sqlite3
import sys
try:
    sqlite3.connect(":memory:").execute("SELECT concat('T', 'AM')").fetchone()
except sqlite3.OperationalError:
    sys.exit("Compatibility tests need a Python SQLite runtime with concat(); use a newer Python/SQLite runtime.")
PY
pnpm_cmd="pnpm"
command -v pnpm >/dev/null 2>&1 || pnpm_cmd="npx --yes pnpm@12"
PASSWORD=compat-secret
# Every run starts from empty data folders; a client left paired by an
# earlier run would otherwise answer as its server.
rm -rf "$WORK/orig-server-data" "$WORK/go-server-data" "$WORK/go-client-data" "$WORK/orig-client-data"

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
      # Git Bash: make sure the native programs behind the wrappers are gone.
      powershell.exe -NoProfile -Command "foreach (\$port in 8010, 8011, 3010, 3011) { Get-NetTCPConnection -LocalPort \$port -State Listen -ErrorAction SilentlyContinue | ForEach-Object { \$p = Get-Process -Id \$_.OwningProcess -ErrorAction SilentlyContinue; if (\$p -and \$p.ProcessName -in 'python','node','tam-server','tam-client') { Stop-Process -Id \$p.Id } } }" 2>/dev/null || true
      ;;
  esac
}
trap cleanup EXIT

wait_http() { # url, header, want
  for _ in $(seq 1 120); do
    if curl -s -m 3 -H "$2" "$1" 2>/dev/null | grep -q "$3"; then return 0; fi
    sleep 0.5
  done
  echo "timed out waiting for $1" >&2
  return 1
}

if [ ! -d "$ORIG/.git" ]; then
  git clone -q https://github.com/ticket-auction-manager/tam.git "$ORIG"
fi
git -C "$ORIG" checkout -q "$PIN"
echo "original at $(git -C "$ORIG" log --oneline -1)"

# --- 1. Go client against the original server ---------------------------
VENV="$WORK/venv"
if [ ! -d "$VENV" ]; then
  "$PY" -m venv "$VENV"
fi
if [ -x "$VENV/bin/python" ]; then VPY="$VENV/bin/python"; else VPY="$VENV/Scripts/python.exe"; fi
"$VPY" -m pip install -q -r "$ORIG/api/app/requirements.txt"
mkdir -p "$WORK/orig-server-data"
(cd "$ORIG/api/app" && TAM_PWD=$PASSWORD TAM_DATA_DIR="$WORK/orig-server-data" \
  exec "$VPY" -m uvicorn main:app --host 127.0.0.1 --port 8010 >"$WORK/orig-server.log" 2>&1) &
pids+=($!)
wait_http http://127.0.0.1:8010/api "X-None: 1" "TAM Server"
echo "--- Go client tests against the original server"
TAM_COMPAT_SERVER=http://127.0.0.1:8010 TAM_COMPAT_PASSWORD=$PASSWORD go test ./internal/client -run Compat -count=1 -v

# --- 2. Original client and Go client against the Go server -------------
ext=""
case "$(go env GOOS)" in windows) ext=".exe" ;; esac
go build -o "$WORK/tam-server$ext" ./cmd/tam-server/
go build -o "$WORK/tam-client$ext" ./cmd/tam-client/
mkdir -p "$WORK/go-server-data" "$WORK/go-client-data" "$WORK/orig-client-data"
(TAM_PWD=$PASSWORD TAM_DATA_DIR="$WORK/go-server-data" exec "$WORK/tam-server$ext" -addr 127.0.0.1:8011 -tray=false -announce=false >"$WORK/go-server.log" 2>&1) &
pids+=($!)
(TAM_DATA_DIR="$WORK/go-client-data" exec "$WORK/tam-client$ext" -addr 127.0.0.1:3011 -open=false -tray=false >"$WORK/go-client.log" 2>&1) &
pids+=($!)

echo "--- building the original client"
# Its build opens the database while analysing the routes, so the data
# directory must exist before the build and be the same one it runs with.
(cd "$ORIG/client" && $pnpm_cmd install --frozen-lockfile >"$WORK/orig-client-install.log" 2>&1 \
  && TAM_DATA_DIR="$WORK/orig-client-data" $pnpm_cmd build >"$WORK/orig-client-build.log" 2>&1)
# The original client invents its request id at startup and hands it to its
# pages; drive.py reads it from the rendered main menu.
(cd "$ORIG/client" && PORT=3010 HOST=127.0.0.1 ORIGIN=http://127.0.0.1:3010 TAM_DATA_DIR="$WORK/orig-client-data" \
  exec node build >"$WORK/orig-client.log" 2>&1) &
pids+=($!)
wait_http http://127.0.0.1:8011/api "X-None: 1" "TAM Server"
wait_http http://127.0.0.1:3011/api "X-None: 1" "whoami"
wait_http http://127.0.0.1:3010/ "X-None: 1" "tamClientID"

echo "--- driving both clients against the Go server"
"$PY" scripts/compat/drive.py --original http://127.0.0.1:3010 \
  --go-client http://127.0.0.1:3011 --server-host 127.0.0.1 --server-port 8011 --password $PASSWORD
echo "compat run passed"
