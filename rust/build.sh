#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
profile=release
flags=(--release)
if [[ ${1:-} == --debug ]]; then profile=debug; flags=(); elif [[ $# -ne 0 ]]; then echo 'Usage: rust/build.sh [--debug]' >&2; exit 2; fi
cd "$root/frontend"
pnpm install --frozen-lockfile
pnpm build
cd "$root"
cargo build --manifest-path rust/Cargo.toml --locked --workspace "${flags[@]}"
target=${CARGO_TARGET_DIR:-$root/rust/target}
out="$root/build/r-tam/linux"
mkdir -p "$out"
cp "$target/$profile/r-tam-server" "$target/$profile/r-tam-client" "$out/"
printf 'Built R-TAM programs in %s\n' "$out"
