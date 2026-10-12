#!/bin/sh
set -eu
if [ -z "${TAM_DATA_DIR:-}" ]; then
    TAM_DATA_DIR="${XDG_DATA_HOME:-${HOME:?HOME is unset; set TAM_DATA_DIR to a dedicated client folder}/.local/share}/r-tam/client"
fi
export TAM_DATA_DIR
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$script_dir/r-tam-client" -tray=false -open=false "$@"
