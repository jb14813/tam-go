#!/bin/sh
# Runs after the files of @PROGRAM@ are gone. The data in /var/lib/@PROGRAM@
# and the tam user stay, so a reinstall finds the data again; remove them by
# hand once they are no longer needed:
#   rm -rf /var/lib/@PROGRAM@   and, when no tam program is left, userdel tam
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
