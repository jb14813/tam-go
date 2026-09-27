# All systems: the Go version as the replacement for the original

**Goal.** tam-go runs natively on Windows, Linux and macOS (amd64 and
arm64), carries the original's data unchanged, ships as one archive per
system with install helpers, and its server shows every client's
connection state, last update and queue, so an admin can see when all
clients have synced. Branch `all-systems`, on top of `parity`.

**Standing decisions.** The compensations for the original as published
(the drawing route after a restore, the `TAM_KEY` spelling) stay. The
original repo is not changed further from here; the Go version replaces it.

## Presence on the server admin page

- The client's heartbeat (`GET /api` every 5 s with the key) carries
  `X-TAM-Client: tam-client/<version>` and `X-TAM-Pending: <queued saves>`.
  Every keyed request carries `X-TAM-Client`.
- The server keeps one in-memory presence record per key: last seen (any
  keyed request), last update (last accepted keyed write: POST or DELETE
  under /api), pending (from the last heartbeat), client (from the header,
  else the User-Agent's first word). `last_seen` keeps being persisted as
  today (throttled); `last_update` is persisted the same way in a new
  `auth_keys.last_update` column added by `db.MigrateServer`, so restarts
  keep history. The in-memory values are exact and win when present.
- Admin Status page, table "Clients": Client, Program, State, Last seen,
  Last update, Queued. State is "connected" when seen within 15 s, "away"
  (with how long) otherwise, "never" when unseen. The page reloads itself
  every 5 s. A client that sends no heartbeat (the original) shows what its
  requests reveal; its State follows the same 15 s rule.
- `/admin/status` also answers JSON when asked with `Accept:
  application/json`, for scripts and for the reload.

## Version

`internal/version.Version`, default "0.0.1"; `build.sh` and the release
workflow stamp it with `-X`. Both programs print it in their banner and the
server reports it on `GET /api` and the admin page.

## Builds and packaging

- `./build.sh release` builds `build/<os>-<arch>/` for windows, linux and
  darwin on amd64 and arm64, CGO off, `-trimpath -ldflags "-s -w -X ..."`,
  then one archive per program and target (`tam-server-<version>-<os>-<arch>`
  and `tam-client-...`, `.zip` on Windows, `.tar.gz` elsewhere) containing that
  program, README.md, LICENSE.md and its deploy files for that system.
  On Windows the bare program is published next to its zip, so one
  download that is double-clicked runs without unpacking. On Linux a
  .deb and an .rpm of each program (nfpm, from the same units, program
  in /usr/bin, the tam user and the service set up by the package scripts)
  are published as well.
- CI: cross-compile check for all six targets on every push; a `release`
  workflow on tags `v*` builds the archives and attaches them to the
  GitHub release.
- `deploy/linux/`: `tam-server.service` and `tam-client.service` (system
  units, `-tray=false -open=false`, data under `/var/lib/tam-<name>`, a
  `tam` system user), `install.sh` (copies the binaries to
  `/usr/local/bin`, creates the user and data folders, installs and enables
  the chosen unit), `tam-client.desktop` for a client's application menu.
- `deploy/macos/`: `launchd` plists for both programs and notes on the
  quarantine flag of unsigned downloads.
- `deploy/docker/`: multi-stage `Dockerfile` (build from source, run from a
  minimal image) with a `PROGRAM` build arg, and `compose.yml` running the
  server on 8000 with a `./data` volume, matching the original's compose.
- The tray icon stays Windows-only; elsewhere the Shut Down button, Ctrl+C
  and SIGTERM stop the programs, and `-open` uses `xdg-open` or `open`.

## Switching from the original

Same data files and names: `tam-remote.db` for the server, `tam-local.db`
and `settings.json` for the client. Point `TAM_DATA_DIR` at the old data
folder (or the Docker volume's folder) and start the Go program; the
schema additions are applied on first start. `internal/db/compat_test.go`
proves both original schemas open and migrate. README gets a "Switching
from the original" section with the Docker compose mapping.

## Verification

Unit tests; the compatibility run; a Linux smoke run in WSL (both
binaries, pairing, admin page, the install script and the units); the
archives built by `./build.sh release` extracted and started on Windows.
