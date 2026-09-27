#!/bin/sh
# Runs before the files of @PROGRAM@ are unpacked, on an install and on an
# upgrade (build.sh fills in @PROGRAM@). dpkg passes "install" or "upgrade",
# rpm 1 on an install and 2 on an upgrade.
set -e

# The tam system user that owns the data must exist by then.
if ! id tam >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/tam-server --no-create-home \
    --shell "$(command -v nologin || echo /bin/false)" tam
fi

# A note for the scripts that run once the files are in place, kept in /run
# for the length of this install.
state=/run/@PROGRAM@.package-state
rm -f "$state"
case "$1" in
  install)
    # dpkg: a first install, or one after a removal that kept the package's
    # configuration. postinstall then gets the version configured before, as
    # on an upgrade, so it needs to be told.
    { echo install > "$state"; } 2>/dev/null || true
    ;;
  2)
    # rpm: an upgrade. The old package's %preun runs after this package's
    # %post, and the one of 1.0.0-rc1 disabled and stopped the service, so
    # note whether it is enabled and running now; posttrans puts that back.
    if [ -d /run/systemd/system ]; then
      if systemctl is-enabled --quiet @PROGRAM@.service; then
        { echo enabled >> "$state"; } 2>/dev/null || true
      fi
      if systemctl is-active --quiet @PROGRAM@.service; then
        { echo active >> "$state"; } 2>/dev/null || true
      fi
    fi
    ;;
esac
exit 0
