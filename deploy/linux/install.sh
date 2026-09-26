#!/bin/bash
# Installs tam-server, tam-client or both as systemd services on this
# machine, from the programs next to this script (the release archive) or
# from the build folder of a checkout.
#
#   sudo ./install.sh                  the program(s) found next to this script
#   sudo ./install.sh server|client|all
#
# It copies the programs to /usr/local/bin, creates the tam system user and
# the data folders /var/lib/tam-server and /var/lib/tam-client, puts the
# icons and the application-menu entry under /usr/local/share, and installs,
# enables and starts the chosen units.
#
# To undo it:
#   sudo systemctl disable --now tam-server tam-client
#   sudo rm -f /etc/systemd/system/tam-server.service /etc/systemd/system/tam-client.service \
#     /usr/local/bin/tam-server /usr/local/bin/tam-client \
#     /usr/local/share/applications/tam-client.desktop \
#     /usr/local/share/icons/hicolor/scalable/apps/tam-server.svg \
#     /usr/local/share/icons/hicolor/scalable/apps/tam-client.svg
#   sudo systemctl daemon-reload
# and, once the data is no longer needed, remove /var/lib/tam-server,
# /var/lib/tam-client and the tam user (userdel tam).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
what="${1:-}"
if [ -z "$what" ]; then
  # No argument: the one-program archives put the program next to this
  # script, so install what is here.
  if [ -f "$here/tam-server" ] && [ -f "$here/tam-client" ]; then
    what=all
  elif [ -f "$here/tam-server" ]; then
    what=server
  elif [ -f "$here/tam-client" ]; then
    what=client
  fi
fi
case "$what" in
  server) programs=(tam-server) ;;
  client) programs=(tam-client) ;;
  all) programs=(tam-server tam-client) ;;
  *) echo "usage: sudo $0 [server|client|all]  (no argument: the program found next to the script)" >&2; exit 1 ;;
esac
if [ "$(id -u)" -ne 0 ]; then
  echo "$0: run it as root: sudo $0 $what" >&2
  exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
  echo "$0: this machine has no systemd; run the programs by hand instead (see README.md)" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) goarch=amd64 ;;
  aarch64 | arm64) goarch=arm64 ;;
  *) goarch="$(uname -m)" ;;
esac

# find_file NAME [ALTERNATIVE...]: NAME next to this script (the archive
# layout), else in the build folder of a checkout, else one of the
# alternatives relative to this script (the icon files of a checkout).
find_file() {
  local name=$1 f
  shift
  for f in "$here/$name" "$here/../../build/linux-$goarch/$name" "$here/../../build/$name"; do
    if [ -f "$f" ]; then
      echo "$f"
      return 0
    fi
  done
  for f in "$@"; do
    if [ -f "$here/$f" ]; then
      echo "$here/$f"
      return 0
    fi
  done
  echo "$0: $name not found next to this script (extract the linux archive and run the script from its folder)" >&2
  return 1
}

# The tam user owns the data and nothing else.
if ! id tam >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/tam-server --no-create-home \
    --shell "$(command -v nologin || echo /bin/false)" tam
  echo "created the tam system user"
fi

for p in "${programs[@]}"; do
  install -m 0755 "$(find_file "$p")" "/usr/local/bin/$p"
  install -d -m 0750 -o tam -g tam "/var/lib/$p"
  chown -R tam:tam "/var/lib/$p"
  install -m 0644 "$(find_file "$p.service")" "/etc/systemd/system/$p.service"
  echo "installed /usr/local/bin/$p, /var/lib/$p and /etc/systemd/system/$p.service"
done

# The icons, and the entry of the client in the application menu.
for p in tam-server tam-client; do
  if icon="$(find_file "$p.svg" "../../cmd/$p/icon.svg" 2>/dev/null)"; then
    install -D -m 0644 "$icon" "/usr/local/share/icons/hicolor/scalable/apps/$p.svg"
  fi
done
if [ "$what" != server ]; then
  install -D -m 0644 "$(find_file tam-client.desktop)" /usr/local/share/applications/tam-client.desktop
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database /usr/local/share/applications || true
  fi
fi

systemctl daemon-reload
for p in "${programs[@]}"; do
  if ! systemctl enable --now "$p.service"; then
    echo "$0: $p did not start; see: journalctl -u $p -e" >&2
    exit 1
  fi
  sleep 1
  if systemctl is-active --quiet "$p"; then
    echo "$p is running"
  else
    echo "$0: $p is not running; see: journalctl -u $p -e" >&2
    exit 1
  fi
done

addr="$(hostname -I 2>/dev/null | awk '{print $1}')"
if [ -z "$addr" ]; then
  addr=localhost
fi
echo
case "$what" in server | all)
  echo "tam-server: http://$addr:8000/  (the laptops pair with this address)"
  echo "  admin page: http://$addr:8000/admin - it asks you to set the server password on the"
  echo "  first visit, unless TAM_PWD is set in /etc/systemd/system/tam-server.service."
  echo "  data: /var/lib/tam-server   log: journalctl -u tam-server"
  ;;
esac
case "$what" in client | all)
  echo "tam-client: http://localhost:3080/  (http://$addr:3080/ from other machines)"
  echo "  data: /var/lib/tam-client   log: journalctl -u tam-client"
  echo "  On a laptop used by one person you may prefer not to run the client as a service:"
  echo "  'sudo systemctl disable --now tam-client' turns it off, and ./tam-client (or the"
  echo "  Ticket Auction Manager entry in the application menu) runs it by hand: it opens"
  echo "  the browser itself and stops from Alt+A, Shut Down TAM."
  ;;
esac
