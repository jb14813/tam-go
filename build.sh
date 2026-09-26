#!/bin/bash
# Builds the programs into ./build.
#
#   ./build.sh client     build the web app, then tam-client
#   ./build.sh server     build tam-server
#   ./build.sh all        both
#   ./build.sh release    the web app once, then both programs for windows,
#                         linux and darwin on amd64 and arm64 into
#                         build/<os>-<arch>/, and one archive per target in
#                         build/: tam-go-<version>-<os>-<arch>.zip for
#                         windows, .tar.gz for the others
#
# Cross-compile one target by setting GOOS and GOARCH, for example:
#   GOOS=linux GOARCH=amd64 ./build.sh all
#
# VERSION stamps internal/version.Version (both programs print it, the
# server reports it on GET /api and its admin page); it defaults to
# `git describe --tags --always --dirty`. SKIP_WEB=1 keeps an existing
# cmd/tam-client/dist instead of building the web app again.
set -euo pipefail
cd "$(dirname "$0")"

target="${1:-}"
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.1)}"
ldflags="-s -w -X ticket-auction-manager/tam-go/internal/version.Version=${version}"
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
  if [ "${SKIP_WEB:-}" = "1" ] && [ -f cmd/tam-client/dist/index.html ]; then
    echo "SKIP_WEB=1: keeping the web app in cmd/tam-client/dist"
    return
  fi
  (cd frontend && $pnpm_cmd install --frozen-lockfile && $pnpm_cmd build)
}

build_client() {
  CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "build/tam-client${ext}" ./cmd/tam-client/
}

build_server() {
  CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "build/tam-server${ext}" ./cmd/tam-server/
}

# The release targets. Only windows-amd64 has the icon and version resources
# (cmd/*/rsrc_windows_amd64.syso); windows-arm64 builds without them.
release_targets="windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

# python_cmd prints a Python 3 interpreter, which writes the archives when
# zip or tar is not around.
python_cmd() {
  local p
  for p in python3 python; do
    if "$p" -c 'import sys; sys.exit(sys.version_info[0] < 3)' >/dev/null 2>&1; then
      echo "$p"
      return 0
    fi
  done
  echo "build.sh: neither zip/tar nor python found to write the archives" >&2
  return 1
}

# on_windows_shell reports whether this is Git Bash, MSYS or Cygwin, where
# tar only sees an executable bit on files it recognises by their content
# (an ELF program yes, a macOS one no), so the archives are written by
# Python there, with the bits set explicitly.
on_windows_shell() {
  case "$(uname -s)" in
    MINGW* | MSYS* | CYGWIN*) return 0 ;;
  esac
  return 1
}

# package_target OS ARCH: one archive in build/ with a single top-level
# folder holding both programs, README.md, LICENSE.md and the deploy files
# for that system.
package_target() {
  local os=$1 arch=$2 ext="" name stage
  if [ "$os" = "windows" ]; then
    ext=".exe"
  fi
  name="tam-go-${version}-${os}-${arch}"
  stage="build/$name"
  rm -rf "$stage"
  mkdir -p "$stage"
  cp "build/$os-$arch/tam-server$ext" "build/$os-$arch/tam-client$ext" README.md LICENSE.md "$stage/"
  case "$os" in
    linux)
      cp deploy/linux/tam-server.service deploy/linux/tam-client.service deploy/linux/install.sh deploy/linux/tam-client.desktop "$stage/"
      cp cmd/tam-server/icon.svg "$stage/tam-server.svg"
      cp cmd/tam-client/icon.svg "$stage/tam-client.svg"
      ;;
    darwin)
      cp deploy/macos/*.plist "$stage/"
      cp deploy/macos/README.md "$stage/INSTALL.md"
      ;;
  esac
  # The programs and scripts are executable, the rest is not, whatever the
  # file system here says.
  find "$stage" -type f -exec chmod 644 {} +
  chmod 755 "$stage/tam-server$ext" "$stage/tam-client$ext"
  if [ -f "$stage/install.sh" ]; then
    chmod 755 "$stage/install.sh"
  fi

  if [ "$os" = "windows" ]; then
    rm -f "build/$name.zip"
    if command -v zip >/dev/null 2>&1; then
      (cd build && zip -qr "$name.zip" "$name")
    else
      "$(python_cmd)" -c 'import shutil, sys; shutil.make_archive(sys.argv[1] + "/" + sys.argv[2], "zip", root_dir=sys.argv[1], base_dir=sys.argv[2])' build "$name"
    fi
    echo "wrote build/$name.zip"
  else
    rm -f "build/$name.tar.gz"
    if command -v tar >/dev/null 2>&1 && ! on_windows_shell; then
      tar -czf "build/$name.tar.gz" -C build "$name"
    else
      "$(python_cmd)" - build "$name" <<'PY'
import os, sys, tarfile
build, name = sys.argv[1], sys.argv[2]
def modes(info):
    base = os.path.basename(info.name)
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mode = 0o755 if info.isdir() or base in ("tam-server", "tam-client") or base.endswith(".sh") else 0o644
    return info
with tarfile.open(os.path.join(build, name + ".tar.gz"), "w:gz") as tar:
    tar.add(os.path.join(build, name), arcname=name, filter=modes)
PY
    fi
    echo "wrote build/$name.tar.gz"
  fi
  rm -rf "$stage"
}

build_release() {
  local t os arch out ext
  echo "version $version"
  build_web
  rm -rf build/tam-go-*
  for t in $release_targets; do
    os="${t%/*}"
    arch="${t#*/}"
    ext=""
    if [ "$os" = "windows" ]; then
      ext=".exe"
    fi
    out="build/$os-$arch"
    rm -rf "$out"
    mkdir -p "$out"
    echo "building $os/$arch"
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$out/tam-server$ext" ./cmd/tam-server/
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$out/tam-client$ext" ./cmd/tam-client/
    package_target "$os" "$arch"
  done
  ls -la build/tam-go-*
}

case "$target" in
  client) build_web; build_client ;;
  server) build_server ;;
  all) build_web; build_client; build_server ;;
  release) build_release; exit 0 ;;
  *) echo "Usage: $0 client|server|all|release"; exit 1 ;;
esac

if command -v upx >/dev/null 2>&1; then
  upx -q build/tam-*"${ext}" || true
fi
ls -la build/
