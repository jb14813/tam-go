#!/bin/bash
# Installs tam-server, tam-client or both as systemd services on this
# machine, from the programs next to this script (the release archive) or
# from the build folder of a checkout. Run it again from a newer archive to
# upgrade.
#
#   sudo ./install.sh                  the program(s) found next to this script
#   sudo ./install.sh server|client|all
#
# It copies the programs to /usr/local/bin, creates the tam system user and
# the data folders /var/lib/tam-server and /var/lib/tam-client, puts the
# icons and the client's application-menu entry (and tam-client-open, which
# the entry runs) under /usr/local, installs the units into
# /usr/local/lib/systemd/system, enables them and restarts them, so they run
# the programs just installed. A service disabled after an earlier install
# stays disabled. Nothing in /etc is written: local settings, the server's
# password in /etc/default/tam-server and drop-ins from `systemctl edit`,
# stay as they are.
#
# To undo it:
#   sudo systemctl disable --now tam-server tam-client
#   sudo rm -f /usr/local/lib/systemd/system/tam-server.service \
#     /usr/local/lib/systemd/system/tam-client.service \
#     /usr/local/bin/tam-server /usr/local/bin/tam-client /usr/local/bin/tam-client-open \
#     /usr/local/share/applications/tam-client.desktop \
#     /usr/local/share/icons/hicolor/scalable/apps/tam-server.svg \
#     /usr/local/share/icons/hicolor/scalable/apps/tam-client.svg
#   sudo systemctl daemon-reload
# and, once they are no longer needed, remove the local settings
# (/etc/default/tam-server, /etc/systemd/system/tam-server.service.d,
# /etc/systemd/system/tam-client.service.d), the data (/var/lib/tam-server,
# /var/lib/tam-client) and the tam user (userdel tam).
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
if [ -e /etc/NIXOS ]; then
  # /etc and the units come from the NixOS configuration, which the flake's
  # module fills in; the programs here still run by hand.
  echo "$0: this is NixOS; use the NixOS module of this repository's flake instead (services.tam-server, services.tam-client; see README.md)" >&2
  exit 1
fi
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

# shipped_unit: the unit on standard input is one that earlier versions of
# this script copied into /etc/systemd/system, unchanged.
shipped_unit() {
  case "$(sha256sum | cut -d' ' -f1)" in
    500210583762974700c2b1452024a51454d022f91301657425fc5f3c643bd5bb | \
      41149aaf89f39364fa5ee7c6cefb7af89a6fa9dfdf787ba4a3190ded49a4f927 | \
      652263ba2f674b11d2e4694768aea43e5e3245d979b1cd846a3f87602666c2bf | \
      ad719f84597719261405ae4924263fccf6047540a5cda1f4d8bde628d8382261)
      return 0
      ;;
  esac
  return 1
}

# move_old_unit PROGRAM: earlier versions of this script put the unit into
# /etc/systemd/system, where it hides the one installed now. As it was
# shipped, it goes. With the server password as its only change, it goes
# too, and the password moves to /etc/default/tam-server. With other
# changes it stays in effect, and the script says how to move them.
move_old_unit() {
  local p=$1 old="/etc/systemd/system/$1.service" pw
  if [ ! -f "$old" ] || [ -L "$old" ]; then
    return 0
  fi
  if ! shipped_unit < "$old"; then
    pw="$(sed -n 's/^Environment=TAM_PWD=//p' "$old")"
    case "$pw" in
      *[!A-Za-z0-9._,:@+=/^!*?~-]*) pw="" ;;
    esac
    if [ -z "$pw" ] || [ -e "/etc/default/$p" ] ||
      ! sed 's/^Environment=TAM_PWD=.*$/#Environment=TAM_PWD=change-me/' "$old" | shipped_unit; then
      echo "$0: $old, from an earlier install, has local changes;" >&2
      echo "  it stays, and it still overrides $unitdir/$p.service." >&2
      if [ "$p" = tam-server ]; then
        echo "  Move the password to /etc/default/tam-server (TAM_PWD=...) and other changes" >&2
        echo "  to a drop-in (sudo systemctl edit tam-server), then run" >&2
      else
        echo "  Move the changes to a drop-in (sudo systemctl edit $p), then run" >&2
      fi
      echo "  sudo systemctl disable $p && sudo rm $old && sudo $0 $what" >&2
      return 0
    fi
    install -m 0600 /dev/null "/etc/default/$p"
    printf 'TAM_PWD=%s\n' "$pw" > "/etc/default/$p"
    echo "moved TAM_PWD from $old to /etc/default/$p"
  fi
  # Its links in multi-user.target.wants lead to it; enabling makes new ones.
  systemctl disable "$p.service" >/dev/null 2>&1 || true
  rm -f "$old"
  echo "removed $old, from an earlier install"
}

# The tam user owns the data and nothing else.
if ! id tam >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/tam-server --no-create-home \
    --shell "$(command -v nologin || echo /bin/false)" tam
  echo "created the tam system user"
fi

unitdir=/usr/local/lib/systemd/system
declare -A before
for p in "${programs[@]}"; do
  # enabled, disabled, or nothing when it is not installed yet.
  before[$p]="$(systemctl is-enabled "$p.service" 2>/dev/null || true)"
  install -m 0755 "$(find_file "$p")" "/usr/local/bin/$p"
  install -d -m 0750 -o tam -g tam "/var/lib/$p"
  chown -R tam:tam "/var/lib/$p"
  install -D -m 0644 "$(find_file "$p.service")" "$unitdir/$p.service"
  echo "installed /usr/local/bin/$p, /var/lib/$p and $unitdir/$p.service"
  move_old_unit "$p"
done

# The icons, and the entry of the client in the application menu.
for p in tam-server tam-client; do
  if icon="$(find_file "$p.svg" "../../cmd/$p/icon.svg" 2>/dev/null)"; then
    install -D -m 0644 "$icon" "/usr/local/share/icons/hicolor/scalable/apps/$p.svg"
  fi
done
if [ "$what" != server ]; then
  install -m 0755 "$(find_file tam-client-open)" /usr/local/bin/tam-client-open
  install -D -m 0644 "$(find_file tam-client.desktop)" /usr/local/share/applications/tam-client.desktop
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database /usr/local/share/applications || true
  fi
fi

systemctl daemon-reload
for p in "${programs[@]}"; do
  if [ "${before[$p]}" = disabled ]; then
    # Turned off after an earlier install, for example the client on a
    # computer that uses the menu entry instead: it stays off, and one
    # running anyway gets the new program.
    systemctl try-restart "$p.service" || true
    echo "$p stays disabled, as it was; 'sudo systemctl enable --now $p' turns it on"
    continue
  fi
  systemctl enable "$p.service"
  if ! systemctl restart "$p.service"; then
    echo "$0: $p did not start; see: journalctl -u $p -e" >&2
    exit 1
  fi
  sleep 1
  if systemctl is-active --quiet "$p"; then
    echo "$p is running the program just installed"
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
  echo "tam-server: http://$addr:8000/  (the clients pair with this address)"
  echo "  admin page: http://$addr:8000/admin - it asks you to set the server password on the"
  echo "  first visit, unless TAM_PWD is set in /etc/default/tam-server (systemctl cat tam-server"
  echo "  shows how). data: /var/lib/tam-server   log: journalctl -u tam-server"
  ;;
esac
case "$what" in client | all)
  echo "tam-client: http://localhost:3080/, for this computer only (systemctl cat tam-client shows"
  echo "  how to open it to other machines). data: /var/lib/tam-client   log: journalctl -u tam-client"
  echo "  On a computer used by one person you may prefer not to run the client as a service:"
  echo "  'sudo systemctl disable --now tam-client' turns it off, and the Ticket Auction Manager"
  echo "  entry in the application menu (or ./tam-client) then runs it by hand: it opens the"
  echo "  browser itself and stops from Alt+A, Shut Down TAM."
  ;;
esac
