#!/bin/sh
# Runs after the files of @PROGRAM@ are in place, on an install and on an
# upgrade (build.sh fills in @PROGRAM@): the data folder, then the service.
# dpkg passes "configure" and the version configured before (none on a first
# install), rpm 1 on an install and 2 on an upgrade.
#
# An install enables the service and starts it. An upgrade restarts it with
# the new program when it is running, and leaves it enabled or disabled as it
# was: a service the admin turned off stays off. Without systemd running (a
# container, or a system being installed) the service is only enabled, and
# starts at the next boot.
# shellcheck disable=SC2050,SC2194 # @PROGRAM@ is a program's name by then
set -e
state=/run/@PROGRAM@.package-state

install -d -m 0750 -o tam -g tam /var/lib/@PROGRAM@
if [ "@PROGRAM@" = tam-client ] && command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi

case "$1" in
  configure)
    if [ -z "$2" ] || grep -qx install "$state" 2>/dev/null; then
      action=install
    elif [ "$2" = 1.0.0~rc1-1 ]; then
      # The prerm of 1.0.0-rc1 disabled and stopped the service before this
      # upgrade, and its postinst had enabled it on every install: turn it
      # on again.
      action=repair
    else
      action=upgrade
    fi
    rm -f "$state"
    ;;
  1) action=install ;;
  2) action=upgrade ;;
  *) exit 0 ;;
esac

if [ ! -d /run/systemd/system ]; then
  if [ "$action" != upgrade ] && command -v systemctl >/dev/null 2>&1; then
    systemctl enable @PROGRAM@.service >/dev/null 2>&1 || true
  fi
  exit 0
fi
systemctl daemon-reload >/dev/null 2>&1 || true

# The messages keep their lines short: dnf cuts scriptlet output at 76
# columns.
if [ "$action" = upgrade ]; then
  if ! systemctl try-restart @PROGRAM@.service; then
    echo "@PROGRAM@ did not restart; see: journalctl -u @PROGRAM@ -e" >&2
  fi
  if ! systemctl is-enabled --quiet @PROGRAM@.service; then
    echo "@PROGRAM@ stays disabled, as it was;"
    echo "'systemctl enable --now @PROGRAM@' turns it on."
  fi
  exit 0
fi

systemctl enable @PROGRAM@.service >/dev/null 2>&1 || true
if ! systemctl restart @PROGRAM@.service; then
  echo "@PROGRAM@ did not start; see: journalctl -u @PROGRAM@ -e" >&2
  exit 0
fi
if [ "$action" = repair ]; then
  echo "1.0.0-rc1 turned @PROGRAM@ off for this upgrade; it is enabled"
  echo "and running again ('systemctl disable --now @PROGRAM@' turns it off)."
  exit 0
fi
case "@PROGRAM@" in
  tam-server)
    echo "tam-server is running on port 8000. Its admin page,"
    echo "http://<this machine>:8000/admin, asks you to set the server password"
    echo "on the first visit, unless TAM_PWD is set in /etc/default/tam-server."
    echo "Data: /var/lib/tam-server."
    ;;
  tam-client)
    echo "tam-client is running on http://localhost:3080/, for this computer"
    echo "only (data: /var/lib/tam-client). On a computer used by one person,"
    echo "'systemctl disable --now tam-client' and the Ticket Auction Manager"
    echo "entry in the application menu may suit better. To open the service"
    echo "to other machines, see /usr/share/doc/tam-client/README.md."
    ;;
esac
