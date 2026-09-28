#!/bin/sh
# Runs before the files of @PROGRAM@ are removed (build.sh fills in
# @PROGRAM@). Only a removal stops and disables the service: dpkg passes
# "remove", rpm 0. Before an upgrade (dpkg "upgrade", rpm 1) the service is
# left as it is, and the new version's postinstall restarts it.
set -e
case "$1" in
  remove | purge | 0)
    if [ -d /run/systemd/system ]; then
      systemctl disable --now @PROGRAM@.service >/dev/null 2>&1 || true
    elif command -v systemctl >/dev/null 2>&1; then
      systemctl disable @PROGRAM@.service >/dev/null 2>&1 || true
    fi
    ;;
esac
exit 0
