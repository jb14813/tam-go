#!/bin/sh
# Runs after the files of @PROGRAM@ are in place, on install and on upgrade:
# the data folder, then the service, started (or restarted with the new
# program) when systemd is running. In a container image being built there
# is no systemd, and the service is simply left for whoever runs the image.
set -e
install -d -m 0750 -o tam -g tam /var/lib/@PROGRAM@
if [ "@PROGRAM@" = tam-client ] && command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable @PROGRAM@.service >/dev/null 2>&1 || true
  if ! systemctl restart @PROGRAM@.service; then
    echo "@PROGRAM@ did not start; see: journalctl -u @PROGRAM@ -e" >&2
    exit 0
  fi
  case "@PROGRAM@" in
    tam-server)
      echo "tam-server is running on port 8000; its admin page, http://<this machine>:8000/admin,"
      echo "asks you to set the server password on the first visit. Data: /var/lib/tam-server."
      ;;
    tam-client)
      echo "tam-client is running on http://localhost:3080/ (data: /var/lib/tam-client)."
      echo "On a laptop used by one person, 'systemctl disable --now tam-client' and the"
      echo "Ticket Auction Manager entry in the application menu may suit better."
      ;;
  esac
fi
