#!/bin/sh
# Runs after the files of @PROGRAM@ are gone, on a removal and after an
# upgrade. The data in /var/lib/@PROGRAM@, the tam user and local settings
# (/etc/default/@PROGRAM@, drop-ins in /etc/systemd/system/@PROGRAM@.service.d)
# stay, so a reinstall finds them again; remove them by hand once they are no
# longer needed:
#   rm -rf /var/lib/@PROGRAM@   and, when no tam program is left, userdel tam
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
