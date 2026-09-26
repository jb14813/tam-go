#!/bin/sh
# Runs before the files of @PROGRAM@ are removed (and before an upgrade
# replaces them): the service stops. On an upgrade postinstall starts it again.
set -e
if [ -d /run/systemd/system ]; then
  systemctl disable --now @PROGRAM@.service >/dev/null 2>&1 || true
fi
