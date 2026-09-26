# Remote mode v2: moving laptops, one server

Design for the next step of tam-go after the parity branch. Status: proposed, waiting for approval.

## Goal

A client laptop that walks in and out of wifi keeps working and never loses a save. A server on a fixed laptop is set up in one step. Everything stays wire-compatible with the original tam, so the original (Linux/Docker) client and the Go client can share one server, and either server works.

## What is settled

From Dilan's answers on 2026-09-25:

- The reference is ticket-auction-manager/tam. A pull request against tam-go's `dev` branch is welcome.
- Same ticket entered on two laptops: the last save wins. Blocking caused stuck rows before; no review queue.
- Contact preference is CALL or TEXT with a per-workstation default. The hard-coded CALL was the one complaint volunteers had.
- Restore and push skipping existing rows was not deliberate; the parity build overwrites, and that stays.
- Deleting a prefix keeps its tickets and baskets. No foreign keys: a drawing row may name a ticket that does not exist yet and shows blank.
- Event size: 8,000 to 9,000 tickets and about 400 baskets.

From the user's direction:

- Open project; no assumptions about who runs it where. Windows and Linux executables, setup that just works.
- Standalone mode stays as it is.
- Best practice for a server setup: client laptops that lose wifi, server on a laptop that does not move.

Measured on the parity build with 9,000 tickets and 400 baskets, standalone and remote mode: every list, report, search, backup and save answers in about 0.2 s on localhost, which is the connection floor on Windows; restoring all 9,400 rows takes 0.25 s. Size is not a design constraint.

## Architecture

Five pieces, each usable on its own:

1. **Compatibility contract.** The `/api` routes, field names and file formats do not change. Everything new is an additional route or file that the original programs ignore. A CI job proves every client and server pairing.
2. **Pairing.** The server announces itself on the venue network; the client lists what it finds, asks for the server password once, and is paired. Manual address entry stays.
3. **Local-first client.** In remote mode the client's own database becomes the copy the pages read while offline and the place every save lands first. An outbox holds saves the server has not taken yet and a worker replays them when the connection is back.
4. **Status everywhere.** One status endpoint on the client and a bar in the page layout: Connected, Reconnecting, Offline, with the number of saves waiting.
5. **Server admin page.** A login page on the server for status, paired laptops and keys, backup and restore, and the password.

### 1. Compatibility contract

- `/api` on both daemons keeps the original's routes, JSON field names, `{"detail": ...}` errors and the `TAM-KEY` / `TAM-PW` headers.
- New client routes: `GET /api/status`, `GET /api/servers` (discovered servers), `POST /api/pair`, `POST /api/unpair`, `POST /api/outbox/retry`, `POST /api/outbox/discard`. New server routes: `/admin/...` pages. New server DB column: `auth_keys.last_seen`. New client DB tables: `outbox`, `outbox_failed`. New server file: `server.json` (password hash). The original programs never read any of these.
- CI job `compat`: check out ticket-auction-manager/tam at a pinned commit; start its FastAPI server and run the Go client's remote-mode test suite against it; start the original SvelteKit client against the Go server and exercise every route through it; then both clients against one Go server at once. Each pairing covers keys, prefixes, tickets, baskets, drawing, the three reports, search, backup, restore and push.

### 2. Pairing

- The server registers `_tam._tcp` over mDNS (library: `github.com/grandcat/zeroconf`, pure Go) with TXT records `port`, `tls`, `name` (host name) and `v` (version).
- Settings gets a **Server** section. It lists discovered servers with name, address and TLS, refreshed while the section is open, plus fields to type host, port and TLS by hand. Multicast can be blocked by some access points; manual entry is always there.
- **Pair** asks for the server password, calls the existing `POST /api/auth` with `TAM-PW`, using this laptop's host name as the key description, and stores host, port, TLS and key in `settings.json`. This is the same call the Auth Keys page makes today, so it works against the original server unchanged; the Auth Keys page stays for people who want it.
- **Unpair** clears the four settings (the client is standalone again and keeps its local data) and deletes the key on the server when it is reachable.
- TLS: pairing records the server certificate's SHA-256 fingerprint and later connections require the same certificate. This replaces today's accept-anything setting with trust-on-first-use; a changed certificate shows "the server's certificate changed, pair again".

### 3. Local-first client

Today every request in remote mode goes to the server and the local mirror is written after a successful save. The new behaviour:

**Reads.** Connected: fetch from the server as now, upsert the rows into the mirror, answer with the server's rows (prefix lists replace the mirror's set, because prefixes are the only rows that can be deleted). Not connected: answer from the mirror. On startup in remote mode, and on every reconnect, the client pulls the server's full backup (`/api/backuprestore/remote`, 0.25 s at event size) into the mirror so a laptop that goes offline later has everything.

**Writes.** Validate, write the mirror, then send to the server with a 5 s timeout.

| Server answer | Result |
|---|---|
| 2xx | answered as today |
| unreachable, timeout, 5xx | row appended to `outbox`; answered 200 with the rows and header `X-TAM-Queued: 1` |
| 401 or 403 | appended to `outbox`; connection state becomes "not authenticated"; nothing is dropped |
| other 4xx | answered with the server's error, not queued (the data is wrong, not the network) |

**Outbox.** `outbox(id, created_at, method, path, body, attempts, last_error)`, replayed in order by one worker: on reconnect immediately, otherwise with backoff of 1, 2, 5, 10 and then 30 s. An item that the server rejects with a 4xx other than 401 or 403 moves to `outbox_failed`, is counted in the status bar as "could not be sent", and can be retried or discarded from Settings. Prefix deletes and the three list saves go through the outbox; backup push and restore stay direct and report failure instead of queueing.

**Conflicts.** Last arrival at the server wins, which is what Dilan asked for. A laptop that replays an hour-old edit after another laptop changed the same ticket wins with the older edit; against the Go server a later option is a `saved_at` field so the newer edit wins, but it is not part of this design.

**Heartbeat.** While remote mode is configured the client calls the server's `GET /api` every 5 s with a 2 s timeout. States: `connected`, `reconnecting` (first failure, up to 30 s), `offline`, `unauthenticated`. Reaching `connected` from any other state triggers the full pull and the outbox drain.

### 4. Status everywhere

- `GET /api/status` on the client: `{"mode":"standalone|remote","state":"connected|reconnecting|offline|unauthenticated","server":"host:port","pending":N,"failed":N,"last_ok":"time"}`.
- The page layout polls it every 3 s in remote mode and shows a slim bar: green "Connected to <name>", amber "Reconnecting, N saves waiting", red "Offline, N saves waiting", red "The server rejected this laptop's key, open Settings", and "N saves could not be sent, open Settings" when `failed` is non-zero. In standalone mode the bar is not shown. The main menu footer keeps its three lines.

### 5. Server admin page

- `GET /admin` is a login form. A session is a random 32-byte cookie, HttpOnly, SameSite=Strict, Secure over TLS, kept in memory, 12 hours. Forms carry a session-bound token. Five failed logins from one address wait 30 s.
- Pages: **Status** (address, TLS, data directory, version, uptime, counts of prefixes, tickets and baskets, paired laptops with last-seen time), **Laptops and keys** (create, delete, the key shown once), **Backup** (download the same JSON the API produces; restore an uploaded file after a confirmation), **Password** (change, current password required).
- Password: `server.json` holds a bcrypt hash and wins over `TAM_PWD`. With neither set the server still starts, logs "no password set: open http://<address>/admin to set one", and `/admin` shows a one-time setup form; until then key creation answers 503 "server password not set". `TAM_PWD` keeps working for scripted setups and for the original deployment style.
- `last_seen` on a key is updated at most once a minute per key from any authenticated request; the client's heartbeat keeps it fresh.
- Plain `html/template` pages embedded in the binary; no JavaScript build.

## Data rule changes from the answers

- The Pref cell on the tickets form becomes a CALL / TEXT choice pre-filled with the workstation default. The API accepts any case and stores uppercase; other values already in a database stay as they are.
- Restore and push overwrite existing rows (unchanged). Prefix deletion keeps tickets and baskets (unchanged). A drawing row may name a missing ticket (unchanged).

## Files

- `internal/sync` (client): outbox tables and migration, worker, heartbeat, state.
- `internal/discovery`: announce (server) and browse (client).
- `internal/admin` (server): handlers, sessions, templates, `server.json`.
- `internal/client`: status, pair, unpair and outbox routes; reads and writes go through `internal/sync`.
- `frontend/src/routes/+layout.svelte`: status bar. `frontend/src/routes/settings/+page.svelte`: Server section. Tickets form: Pref choice.
- `.github/workflows/ci.yml`: the `compat` job.

## Testing

- Unit: the outbox worker against an `httptest` server that flips between up, down and 401; ordering, backoff, the failed bucket, and the state machine.
- Integration: Go client and Go server with the server stopped and restarted mid-run; every save made while it was down arrives after it returns, in order.
- Compatibility: the CI `compat` job above.
- Manual: one laptop on wifi walked out of range and back during data entry.

## Delivery

Three pull requests against `dev`, each complete on its own: the parity branch as it stands; remote mode v2 (pairing, discovery, local-first, status bar, Pref choice); the server admin page.

## Out of scope

- Per-volunteer logins on the client.
- Real-time push between laptops; polling is enough at this size.
- Merging two standalone laptops' data after an event (backup and push already cover it).
