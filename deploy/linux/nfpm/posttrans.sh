#!/bin/sh
# rpm only: runs when the whole install or upgrade of @PROGRAM@ is done,
# after the old package's scripts (build.sh fills in @PROGRAM@). The %preun
# of 1.0.0-rc1 disabled and stopped the service during an upgrade;
# preinstall noted before the upgrade whether it was enabled and running,
# and this puts that back.
state=/run/@PROGRAM@.package-state
[ -f "$state" ] || exit 0
if [ -d /run/systemd/system ]; then
  if grep -qx enabled "$state" && ! systemctl is-enabled --quiet @PROGRAM@.service; then
    systemctl enable @PROGRAM@.service >/dev/null 2>&1 || true
  fi
  if grep -qx active "$state" && ! systemctl is-active --quiet @PROGRAM@.service; then
    systemctl start @PROGRAM@.service || echo "@PROGRAM@ did not start; see: journalctl -u @PROGRAM@ -e" >&2
  fi
fi
rm -f "$state"
exit 0
