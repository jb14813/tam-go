#!/bin/sh
# Runs before the files of @PROGRAM@ are unpacked: the tam system user that
# owns the data must exist by then. build.sh fills in @PROGRAM@.
set -e
if ! id tam >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/tam-server --no-create-home \
    --shell "$(command -v nologin || echo /bin/false)" tam
fi
