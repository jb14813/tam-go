#!/bin/bash
# Installs, upgrades and removes the .deb or .rpm packages of both programs
# on this machine, and checks what their scripts do to the services. Run it
# as root on a machine or container that is thrown away afterwards:
#
#   deploy/linux/test-packages.sh [--fake-systemd] OLD NEW [RC1]
#
# OLD and NEW are folders with the packages of tam-server and tam-client,
# NEW of a higher version; only those for this machine's architecture are
# used. The test installs OLD, turns the client service off, upgrades to NEW,
# tries the application-menu entry, removes both, installs NEW again and
# removes it. With RC1, a folder with the packages of 1.0.0-rc1 (whose
# scripts turned the services off during an upgrade), it also upgrades from
# those to NEW. With systemd running it checks as well that both services
# answer, that the client listens on this computer only, the permissions in
# the data folders, and that the server takes its password from
# /etc/default/tam-server before and after the upgrade.
#
# --fake-systemd is for a container without systemd: a stand-in systemctl
# keeps each unit's state in files, so the package scripts run as they
# would, but no service does.
#
# It prints the systemctl calls of the package scripts at every step, and
# its checks, "ok" or "FAIL"; it exits 1 when a check failed.
set -uo pipefail

usage() {
  echo "usage: $0 [--fake-systemd] OLD NEW [RC1]  (folders with the packages; run as root)" >&2
  exit 2
}
fake=no
if [ "${1:-}" = --fake-systemd ]; then
  fake=yes
  shift
fi
if [ $# -lt 2 ] || [ $# -gt 3 ] || [ "$(id -u)" -ne 0 ]; then
  usage
fi
old=$1 new=$2 rc1=${3:-}

if command -v apt-get >/dev/null 2>&1; then
  kind=deb
elif command -v rpm >/dev/null 2>&1; then
  kind=rpm
else
  echo "$0: neither apt nor rpm on this machine" >&2
  exit 2
fi

calls=/var/log/tam-test-systemctl.log
units=/var/lib/tam-test-systemd
work="$(mktemp -d)"
: > "$calls"
checks=0
failures=0
mark=0

ok() {
  checks=$((checks + 1))
  echo "ok   - $*"
}
fail() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  echo "FAIL - $*"
}
# check DESCRIPTION COMMAND...: ok when the command succeeds.
check() {
  local what=$1
  shift
  if "$@"; then ok "$what"; else fail "$what"; fi
}

# systemctl for the checks, which the call log leaves out.
sc() { TAM_TEST_CHECK=1 systemctl "$@"; }
enabled() { sc is-enabled --quiet "$1.service" 2>/dev/null; }
disabled() { ! enabled "$1"; }
active() { sc is-active --quiet "$1.service" 2>/dev/null; }
inactive() { ! active "$1"; }
unit_installed() { [ -f "/usr/lib/systemd/system/$1.service" ] || [ -f "/lib/systemd/system/$1.service" ]; }
unit_gone() { ! unit_installed "$1"; }
mainpid() {
  if [ "$fake" = yes ]; then
    cat "$units/$1.service.active" 2>/dev/null || echo 0
  else
    sc show -p MainPID --value "$1.service"
  fi
}
real() { [ "$fake" = no ]; }

# The systemctl calls of the package scripts: a stand-in that keeps the
# state in files, or a wrapper that logs and runs the real one.
if [ "$fake" = yes ]; then
  if [ -d /run/systemd/system ] && [ ! -e "$units" ]; then
    echo "$0: systemd runs here; leave out --fake-systemd" >&2
    exit 2
  fi
  if [ -e /usr/bin/systemctl ] && ! grep -q 'stand-in for deploy/linux/test-packages.sh' /usr/bin/systemctl; then
    echo "$0: /usr/bin/systemctl is here; --fake-systemd is for a container without systemd" >&2
    exit 2
  fi
  mkdir -p /run/systemd/system "$units"
  cat > /usr/bin/systemctl <<'STUB'
#!/bin/sh
# systemctl stand-in for deploy/linux/test-packages.sh.
units=/var/lib/tam-test-systemd
[ -n "${TAM_TEST_CHECK:-}" ] || echo "systemctl $*" >> /var/log/tam-test-systemctl.log
quiet= now= cmd= list=
for a in "$@"; do
  case "$a" in
    -q | --quiet) quiet=1 ;;
    --now) now=1 ;;
    -*) ;;
    *)
      if [ -z "$cmd" ]; then
        cmd=$a
      else
        case "$a" in *.*) list="$list $a" ;; *) list="$list $a.service" ;; esac
      fi
      ;;
  esac
done
exists() {
  for d in /etc/systemd/system /usr/local/lib/systemd/system /usr/lib/systemd/system /lib/systemd/system; do
    [ -f "$d/$1" ] && return 0
  done
  return 1
}
run() {
  exists "$1" || { echo "Failed to start $1: Unit $1 not found." >&2; return 5; }
  n=$(($(cat "$units/pid" 2>/dev/null || echo 1000) + 1))
  echo "$n" > "$units/pid"
  echo "$n" > "$units/$1.active"
}
rc=0
for u in $list; do
  case "$cmd" in
    enable)
      if exists "$u"; then touch "$units/$u.enabled"; else echo "Failed to enable unit: Unit file $u does not exist." >&2; rc=1; fi
      if [ -n "$now" ] && [ ! -e "$units/$u.active" ]; then run "$u" || rc=$?; fi
      ;;
    disable)
      rm -f "$units/$u.enabled"
      if [ -n "$now" ]; then rm -f "$units/$u.active"; fi
      ;;
    start) if [ ! -e "$units/$u.active" ]; then run "$u" || rc=$?; fi ;;
    restart) run "$u" || rc=$? ;;
    try-restart) if [ -e "$units/$u.active" ]; then run "$u" || rc=$?; fi ;;
    stop) rm -f "$units/$u.active" ;;
    is-enabled)
      if exists "$u" && [ -e "$units/$u.enabled" ]; then
        [ -n "$quiet" ] || echo enabled
      else
        [ -n "$quiet" ] || echo disabled
        rc=1
      fi
      ;;
    is-active)
      if exists "$u" && [ -e "$units/$u.active" ]; then
        [ -n "$quiet" ] || echo active
      else
        [ -n "$quiet" ] || echo inactive
        rc=3
      fi
      ;;
  esac
done
exit $rc
STUB
  chmod 755 /usr/bin/systemctl
else
  if [ ! -d /run/systemd/system ]; then
    echo "$0: systemd does not run here; use --fake-systemd" >&2
    exit 2
  fi
  real_systemctl="$(command -v systemctl)"
  mkdir -p /usr/local/sbin
  cat > /usr/local/sbin/systemctl <<WRAP
#!/bin/sh
# Logs the calls for deploy/linux/test-packages.sh, then runs systemctl.
[ -n "\${TAM_TEST_CHECK:-}" ] || echo "systemctl \$*" >> $calls
exec $real_systemctl "\$@"
WRAP
  chmod 755 /usr/local/sbin/systemctl
  hash -r
fi

# A stand-in xdg-open for the menu entry's check: it notes the address.
mkdir -p /usr/local/bin
cat > /usr/local/bin/xdg-open <<'XDG'
#!/bin/sh
echo "$1" >> /var/log/tam-test-xdg-open.log
XDG
chmod 755 /usr/local/bin/xdg-open
: > /var/log/tam-test-xdg-open.log

cleanup() {
  pkill -f "$work/" 2>/dev/null
  if [ "$fake" = no ]; then
    rm -f /usr/local/sbin/systemctl /etc/default/tam-server
    if [ -e "$work/default-tam-server" ]; then
      mv "$work/default-tam-server" /etc/default/tam-server
    fi
  fi
  rm -f /usr/local/bin/xdg-open
  rm -rf "$work"
}
trap cleanup EXIT
# The test writes its own server password there; one that is there already
# comes back afterwards.
if [ "$fake" = no ] && [ -e /etc/default/tam-server ]; then
  mv /etc/default/tam-server "$work/default-tam-server"
fi

# The package files of both programs in folder $1, for this machine.
packages() {
  local arch f
  if [ "$kind" = deb ]; then
    arch="$(dpkg --print-architecture)"
    for f in "$1"/tam-server_*_"$arch".deb "$1"/tam-client_*_"$arch".deb; do echo "$f"; done
  else
    arch="$(uname -m)"
    for f in "$1"/tam-server-*."$arch".rpm "$1"/tam-client-*."$arch".rpm; do echo "$f"; done
  fi
}

# pm ACTION [FOLDER]: installs (or upgrades to) the packages in FOLDER, or
# removes or purges both, printing what the package manager and the
# package scripts say about them (all of it when it fails).
pm() {
  local out="$work/pm.txt" status f
  if [ "$1" = install ]; then
    mapfile -t f < <(packages "$2")
    echo "   packages: ${f[*]##*/}"
  fi
  # apt runs dpkg with a PATH of its own, which leaves out the logging
  # systemctl in /usr/local/sbin unless told otherwise.
  case "$kind/$1" in
    deb/install) DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Path="$PATH" install -y "${f[@]}" > "$out" 2>&1 ;;
    deb/remove) DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Path="$PATH" remove -y tam-server tam-client > "$out" 2>&1 ;;
    deb/purge) DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Path="$PATH" purge -y tam-server tam-client > "$out" 2>&1 ;;
    rpm/install) dnf install -y --disablerepo='*' "${f[@]}" > "$out" 2>&1 ;;
    rpm/remove | rpm/purge) dnf remove -y tam-server tam-client > "$out" 2>&1 ;;
  esac
  status=$?
  if [ $status -ne 0 ]; then
    sed 's/^/   | /' "$out"
    fail "$kind: $1 exited with $status"
  else
    # What the package scripts print, and a line per package; apt's and
    # dnf's other chatter is left out.
    grep -v -E '^\s*$|^>>>\s*$|^(Reading |Building dependency|Note, selecting|The following|[0-9]+ upgraded|Need to get|After this operation|Get:[0-9]|\(Reading database|debconf: |Selecting previously|Preparing to unpack|Updating and loading|Repositories loaded|Package +Arch|Installing:|Upgrading:|Removing:|Replacing |Transaction Summary|Total size|Running transaction|Complete!|\[[0-9]+/[0-9]+\] (Verify|Prepare)|  *(tam-client|tam-server)[ *]|  *(Installing|Upgrading|Removing|Replacing|Freed space|After this)|>>> (Running|Finished|Scriptlet output:)|Package "|Nothing to do|Warning: skipped OpenPGP| +replacing )' "$out" |
      sed 's/ *$//' | sed 's/^>>> /   | /; t; s/^/   | /'
  fi
}

step() {
  echo
  echo "== $*"
  mark=$(wc -l < "$calls")
}
# The systemctl calls the package scripts made since the step began.
show_calls() {
  echo "   systemctl calls of the package scripts:"
  if [ "$(wc -l < "$calls")" -gt "$mark" ]; then
    tail -n +$((mark + 1)) "$calls" | sed 's/^/     /'
  else
    echo "     (none)"
  fi
}
calls_have() { tail -n +$((mark + 1)) "$calls" | grep -q -E "$1"; }

# status URL: the HTTP status of a GET of URL on this machine, from curl or,
# where there is none, bash's /dev/tcp.
status() {
  local hostport path
  if command -v curl >/dev/null 2>&1; then
    curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$1"
    return
  fi
  hostport=${1#http://}
  path=/${hostport#*/}
  hostport=${hostport%%/*}
  (
    exec 3<> "/dev/tcp/127.0.0.1/${hostport#*:}" || exit 1
    printf 'GET %s HTTP/1.0\r\nHost: %s\r\n\r\n' "$path" "$hostport" >&3
    read -r -t 5 _ code _ <&3 && echo "$code"
  ) 2>/dev/null
}
# up URL: the URL answers 200 within 15 seconds.
up() {
  local _
  for _ in $(seq 30); do
    [ "$(status "$1")" = 200 ] && return 0
    sleep 0.5
  done
  return 1
}
# answers URL PATTERN: the URL answers 200 within 15 seconds, with PATTERN
# in the body (with curl, which a machine running systemd has).
answers() {
  up "$1" && curl -fsS --max-time 2 "$1" | grep -q "$2"
}
password_is() { [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -H "TAM-PW: $1" http://localhost:8000/api/auth)" = "$2" ]; }
# Every address the client listens on (port 3080, 0C08 in /proc) is
# 127.0.0.1 or ::1, and there is one.
client_on_localhost_only() {
  local a n=0
  while read -r a; do
    case "$a" in
      0100007F:0C08 | 00000000000000000000000001000000:0C08) n=$((n + 1)) ;;
      *) return 1 ;;
    esac
  done < <(awk '$4 == "0A" && $2 ~ /:0C08$/ { print $2 }' /proc/net/tcp /proc/net/tcp6)
  [ $n -gt 0 ]
}
data_folder_private() { [ "$(stat -c '%a %U:%G' "/var/lib/$1")" = "750 tam:tam" ]; }
# The files the program wrote into its data folder are for tam only.
data_files_private() {
  local n
  n=$(find "/var/lib/$1" -type f | wc -l)
  [ "$n" -gt 0 ] && [ -z "$(find "/var/lib/$1" -type f ! -perm 600)" ]
}
# The data folder keeps a file the test put there, and the database once
# the program has run.
data_kept() { [ -f "/var/lib/$1/pkg-test-data" ] && { [ "$fake" = yes ] || [ -f "/var/lib/$1/$2" ]; }; }

# The state after an install: both services enabled and running.
installed_and_running() {
  local p
  for p in tam-server tam-client; do
    check "$p: unit installed" unit_installed "$p"
    check "$p: enabled" enabled "$p"
    check "$p: running" active "$p"
  done
}
removed() {
  local p
  for p in tam-server tam-client; do
    check "$p: unit removed" unit_gone "$p"
    check "$p: not running" inactive "$p"
  done
}

if real; then
  install -m 600 /dev/null /etc/default/tam-server
  echo "TAM_PWD=pkg-test-password" > /etc/default/tam-server
fi

step "1. Install $old (systemd: $([ "$fake" = yes ] && echo stand-in || echo real))"
pm install "$old"
show_calls
installed_and_running
check "tam-server: data folder 750 tam:tam" data_folder_private tam-server
check "tam-client: data folder 750 tam:tam" data_folder_private tam-client
install -m 600 /dev/null /var/lib/tam-server/pkg-test-data
install -m 600 /dev/null /var/lib/tam-client/pkg-test-data
if real; then
  check "tam-server answers on http://localhost:8000/api" answers http://localhost:8000/api 'TAM Server'
  check "tam-client answers on http://localhost:3080/web/" up http://localhost:3080/web/
  check "tam-client listens on localhost only" client_on_localhost_only
  check "tam-server takes TAM_PWD from /etc/default/tam-server" password_is pkg-test-password 200
  check "tam-server refuses another password" password_is wrong-password 401
  check "tam-server: its files are for tam only (600)" data_files_private tam-server
  check "tam-client: its files are for tam only (600)" data_files_private tam-client
fi

step "2. Turn the client service off: systemctl disable --now tam-client"
sc disable --now tam-client.service >/dev/null 2>&1
check "tam-client: disabled" disabled tam-client
check "tam-client: not running" inactive tam-client

server_pid="$(mainpid tam-server)"
step "3. Upgrade to $new"
pm install "$new"
show_calls
check "tam-server: still enabled" enabled tam-server
check "tam-server: running" active tam-server
check "tam-server: restarted with the new program" test "$(mainpid tam-server)" != "$server_pid"
check "tam-client: stays disabled" disabled tam-client
check "tam-client: stays off" inactive tam-client
check "the package scripts disabled or stopped nothing" eval '! calls_have "disable|stop"'
check "tam-server: data folder 750 tam:tam" data_folder_private tam-server
if real; then
  check "tam-server answers on http://localhost:8000/api" answers http://localhost:8000/api 'TAM Server'
  check "tam-server still takes TAM_PWD from /etc/default/tam-server" password_is pkg-test-password 200
  check "tam-server: its files are for tam only (600)" data_files_private tam-server
fi

step "4. The application-menu entry"
check "it runs /usr/bin/tam-client-open" grep -q '^Exec=/usr/bin/tam-client-open$' /usr/share/applications/tam-client.desktop
if [ -x /usr/bin/tam-client-open ]; then
  # A client that answers already (started by hand here): only the browser opens.
  mkdir -p "$work/running"
  TAM_DATA_DIR="$work/running" /usr/bin/tam-client -open=false -tray=false > "$work/running.log" 2>&1 &
  hand=$!
  up http://localhost:3080/web/
  : > /var/log/tam-test-xdg-open.log
  HOME="$work/home-a" /usr/bin/tam-client-open
  check "with a client running, it opens http://localhost:3080/" grep -qx 'http://localhost:3080/' /var/log/tam-test-xdg-open.log
  check "and starts no second client" test ! -e "$work/home-a/.local/share/tam-client"
  kill "$hand"
  wait "$hand" 2>/dev/null
  # None running: it starts one for the user, which opens the browser.
  : > /var/log/tam-test-xdg-open.log
  HOME="$work/home-b" /usr/bin/tam-client-open > "$work/menu.log" 2>&1 &
  menu=$!
  up http://localhost:3080/web/
  sleep 1
  check "with none running, it starts tam-client with the data in ~/.local/share/tam-client" \
    test -f "$work/home-b/.local/share/tam-client/tam-local.db"
  check "which opens the browser itself" grep -q 'http://.*:3080/' /var/log/tam-test-xdg-open.log
  kill "$menu" 2>/dev/null
  wait "$menu" 2>/dev/null
else
  fail "no /usr/bin/tam-client-open"
fi

step "5. Remove both"
pm remove
show_calls
removed
check "tam-server: disabled" disabled tam-server
check "tam-client: disabled" disabled tam-client
check "tam-server: data kept" data_kept tam-server tam-remote.db
check "tam-client: data kept" data_kept tam-client tam-local.db

step "6. Install $new again"
pm install "$new"
show_calls
installed_and_running

step "7. Remove both$([ "$kind" = deb ] && echo ", with their configuration (purge)")"
pm purge
show_calls
removed
check "tam-server: data kept" data_kept tam-server tam-remote.db
check "tam-client: data kept" data_kept tam-client tam-local.db

if [ -n "$rc1" ]; then
  step "8. Install $rc1"
  pm install "$rc1"
  show_calls
  installed_and_running

  step "9. Upgrade from $rc1 to $new"
  pm install "$new"
  show_calls
  check "tam-server: enabled after the upgrade" enabled tam-server
  check "tam-server: running after the upgrade" active tam-server
  check "tam-client: enabled after the upgrade" enabled tam-client
  check "tam-client: running after the upgrade" active tam-client
  if real; then
    check "tam-server answers on http://localhost:8000/api" answers http://localhost:8000/api 'TAM Server'
  fi

  step "10. Remove both$([ "$kind" = deb ] && echo " (purge)")"
  pm purge
  show_calls
  removed
fi

echo
if [ $failures -eq 0 ]; then
  echo "all $checks checks passed ($kind, systemd: $([ "$fake" = yes ] && echo stand-in || echo real))"
else
  echo "$failures of $checks checks failed ($kind, systemd: $([ "$fake" = yes ] && echo stand-in || echo real))"
  exit 1
fi
