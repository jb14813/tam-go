# Ticket Auction Manager (Go)

Ticket Auction Manager (TAM) runs in-person penny socials and benefit auctions: sell numbered tickets by prefix, describe the baskets, enter the drawn winning tickets, and print the winners and counts reports. This is the Go rewrite of [ticket-auction-manager/tam](https://github.com/ticket-auction-manager/tam): the same features and the same wire format, in two single-file binaries with no Python or Node at runtime.

- **tam-client** serves the web app and its API on `http://localhost:3080`. In standalone mode it keeps everything in a local SQLite file. In remote mode it sends every change to a **tam-server** and keeps a local mirror.
- **tam-server** is the shared database for large events with several laptops. It speaks the same API, protected by access keys.

## Features

- **Forms** per prefix: Tickets (names, phone numbers, contact preference), Baskets (descriptions and donors), Drawing (winning ticket numbers with instant winner lookup). Range-based paging, keyboard shortcuts, copy/paste and duplicate rows, save-marked-rows.
- **Reports**: Winners by Name, Winners by Basket (both printable, filterable by CALL or TEXT preference), Ticket Counts (unique buyers and total buys per prefix, auto-refresh).
- **Print Sheets** for ticket sales, **Ticket Search** across every prefix.
- **Settings**: remote server, port and TLS, default contact preference, venue name, attribution toggle; **Prefixes** with colours and ordering; **Auth Keys** for the server; **Backup/Restore** with local and remote downloads, uploads, and push-to-server.
- Admin section on the main menu with `Alt+A`.

## Quick start

1. Run `tam-client` (double-click, or `./tam-client` in a terminal). It creates a `data/` folder in the directory it is started from, which is its own folder when double-clicked; set `TAM_DATA_DIR` to keep the data elsewhere. Prebuilt binaries from a release zip are under `build/<os>-<arch>/`; on Linux or macOS run `chmod +x` on them if your unzip tool dropped the executable bit.
2. It opens http://localhost:3080/ in your default browser as soon as it is listening (start it with `-open=false` to skip that, for example from a script). On Windows a TAM icon sits in the notification area next to the clock while it runs (under the `^` overflow unless you pin it): left-click it to open the app again, right-click it for **Open TAM** and **Shut Down TAM**. The console window it started with stays open too, titled "Ticket Auction Manager - client" in the taskbar; closing it or pressing Ctrl+C in it also stops the program cleanly, as does **Shut Down TAM** under `Alt+A` on the main menu. Closing the browser tab alone leaves it running.
3. Press `Alt+A`, open Settings, then Prefixes, and add at least one prefix. Prefixes are the ticket series (for example `CALL`, `A`, `B`) and unlock the forms and reports on the main menu.

## Building from source

Requirements: Go 1.27.1 or newer (the version in `go.mod`), Node 24 or newer with pnpm (the scripts fall back to `npx pnpm` when pnpm is not installed).

```bash
./build.sh all          # web app + tam-client + tam-server into ./build
./build.sh client       # or one of them
GOOS=linux GOARCH=amd64 ./build.sh all     # cross-compile (CGO is not needed)
```

The web app must be built before `go build ./cmd/tam-client`, because the binary embeds `cmd/tam-client/dist`. `build.sh client` does both.

Development:

```bash
./run.sh server         # tam-server on localhost:8000, password "changeme"
./run.sh client         # installs and builds the web app, then tam-client on localhost:3080
go test ./...           # store, server and client tests, including remote mode
```

For work on the pages, `pnpm dev` in `frontend/` serves them on http://localhost:5173/web/ and proxies `/api` to a running `tam-client`.

## Running the tests

The tests need Go and the built web app (`pnpm build` in `frontend/`, as for any build). CI runs all of them on every push.

Unit and integration tests; the client tests drive the real server handler as their server:

```
go test ./...
```

The compatibility run against the original tam, its FastAPI server at a pinned commit and its SvelteKit client next to the Go programs (needs Python 3, Node with pnpm, git and curl):

```
bash scripts/compat/run.sh
```

The load test runs a whole event through one real `tam-server` and many real `tam-client` programs on the machine, each laptop with its own data folder and paired through its Settings route. Ticket entry is paced over 40 seconds, fixing a typo now and then and opening sheets again; a quarter of the way in the server is killed and started again 8 seconds later while the laptops keep saving, and each laptop goes back to correct the sheets it saved meanwhile as soon as it sees the server again. Then another laptop corrects every 40th ticket, the baskets are entered and drawn (each winner looked up as the page does), every report and a few searches are read, and for 10 seconds every laptop saves as fast as it can. It then checks every ticket, basket and winner on the server and in each laptop's own copy, and everything the programs wrote, and prints the time of every page action:

```
go run ./scripts/loadtest
go run ./scripts/loadtest -laptops 50 -tickets 9000 -baskets 1000
go run ./scripts/loadtest -h
```

It exits with status 1 when a check fails and then keeps the data folders and logs for a look. `-bin <folder>` tests programs built elsewhere, such as a release or a build with `-race`.

To test over a real network, start `tam-server` on another machine with an empty data folder and a password, and point the laptops at it; `-kill` and `-restart` take the commands that kill that server and start it again (through ssh, for example) for the outage, and without them the run has no outage:

```
go run ./scripts/loadtest -server http://<that machine>:8000 -password <its password> -laptops 100
```

## Configuration

| Setting | Where | Default |
|---|---|---|
| `TAM_DATA_DIR` | environment, both daemons | `./data` |
| `TAM_PWD` | environment, tam-server | `changeme` (a warning is logged; set it before exposing the server) |
| `-addr` | flag, both daemons | `localhost:3080` for the client, `:8000` for the server (`:8443` with `-tls`) |
| `-open` | flag, tam-client | `true`: open the web app in the default browser on start |
| `-tray` | flag, both daemons | `true` on Windows: a TAM icon in the notification area with Open (client) and Shut Down entries; use `-tray=false` for services and scripts. Other systems have no icon and stop on Ctrl+C or SIGTERM |
| `-announce` | flag, tam-server | `true`: announce the server on the local network (mDNS) so clients can find it in Settings |
| `dev` | positional argument, tam-server | binds localhost instead of every interface |
| `-tls`, `-cert`, `-key` | flags, tam-server | HTTPS with the given PEM files, or a self-signed pair created in the data directory |

`tam-client` keeps `settings.json` in the data directory. It is safe to edit by hand while the daemon runs; the edit is picked up on the next request. A file that fails to parse is logged and the last good settings stay in effect until it is fixed or saved again from the Settings page. Both programs also append everything they print to `tam-client.log` or `tam-server.log` in the data directory, including why they stopped (Ctrl+C, a closed window, the Shut Down button, or the notification-area icon), so the reason is there after the window is gone.

The databases use SQLite's WAL journal, so recent writes may sit in `tam-local.db-wal` next to the main file: copy the whole data folder, or stop the daemon first, when taking a copy by hand. Backup/Restore in the app is the safer route.

A data folder from the original app is a drop-in: the tables and views are the same, and the Go daemons open it as is.

```json
{
  "remote_server": "",
  "remote_key": "",
  "remote_port": "8000",
  "remote_tls": false,
  "default_pref": "CALL",
  "venue_name": "Test Venue",
  "disable_attrib": false
}
```

## Remote mode

Remote mode is for events with several laptops: one `tam-server` holds the data and every client works against it. It is built for laptops that move around and lose wifi: a save never waits for a dead connection and is never lost.

1. Run `tam-server` on the machine that stays put, where every laptop can reach it. On first start it has no password: open `http://<that machine>:8000/admin` and set one (or start it with `TAM_PWD` set, as the original was). For HTTPS start it with `-tls`: it listens on port 8443 and writes a self-signed certificate (`server.crt`, `server.key`) into its data directory on first start; put your own PEM files there, or point `-cert` and `-key` at them, to use a real certificate. Windows asks once whether to allow the program through the firewall. To stop it, right-click its icon in the notification area and choose **Shut Down TAM Server**, close its console window, or press Ctrl+C in it; the clients' **Shut Down TAM** button only stops the client it is pressed on.
2. On each client press `Alt+A`, open Settings and look at the **Server** section. Servers on the venue network appear there by name: they announce themselves (mDNS, `_tam._tcp`), and because some access points drop multicast, the client also asks the standard ports (8000, and 8443 for TLS) on every address of its own /24 networks, which takes a second or two and finds the server wherever plain traffic gets through. A server on another port or another subnet is typed in by hand; its admin status page and its start-up banner list the addresses to type. Pick one, enter the server password once and press **Pair**. The client creates its own access key on the server, named after the laptop, and over TLS it pins the server's certificate. **Unpair** returns the client to standalone mode with its local data intact. The original way still works too: the remote fields and the Auth Keys page are still there.
3. A bar on every page then shows where the client stands: green **Connected to <server>**, amber **Reconnecting** or red **Offline** with the number of saves waiting, or red when the server rejected this laptop's key. The main menu footer keeps the original's three lines.

What happens with the connection:

- **Reads** come from the server while it answers and nothing saved on this laptop is still waiting to reach it, and are copied into the laptop's own database on the way. Otherwise the pages read that copy, so the forms, reports and search keep working, and a sheet saved while the server was away shows what was saved until the server has it too. On pairing and every time the connection comes back, the client pulls the server's whole data set into its copy (0.25 s at 9,000 tickets) so a laptop that goes offline later has everything; rows the laptop saves while that download is on its way keep what was saved.
- **Saves** go to the server first, with a five-second limit. When the server does not answer (or answers 5xx), or this laptop still has saves waiting for it, the rows are stored on the laptop and queued in an outbox behind the ones already there, so the server takes a laptop's saves in the order they were made; the page gets its normal answer plus an `X-TAM-Queued: 1` header. A background worker pings the server every five seconds, replays the outbox in order as soon as it answers, and then pulls the data set again. A save the server rejects as bad data (a 4xx) is not queued: the error goes back to the page. A save the server refuses because the key is wrong stays queued, the bar says so, and pairing again with the same server (at its old address, or by its name at a new one) sends it. Pairing with another server, or unpairing, sets the saves still queued aside in the failed list rather than sending them anywhere by themselves.
- **Conflicts** are settled by arrival at the server: the last save wins, as in the original. A laptop replaying an old edit after another laptop changed the same ticket wins with the older edit.
- **Refused saves** (the server answered 4xx during a replay), and saves set aside when the laptop paired with another server or was unpaired, are kept in a failed list, counted in the bar, and can be retried (sent to the server the laptop is paired with now) or discarded from Settings. Nothing queued is ever dropped without a Discard.

Backup/Restore can still push the local prefixes, tickets or baskets to the server and download the server's data; those two actions are direct and report failure instead of queueing.

### Server admin page

`tam-server` serves its own pages under `/admin`, protected by the server password: status (address, TLS, data directory, counts, and the paired laptops with the time each was last seen), keys (create and delete), backup download and restore, and a password change. The password hash lives in `server.json` in the data directory and wins over `TAM_PWD`; with neither set the server starts in setup mode, logs the address to open, and refuses to hand out keys until a password exists.

### Compatibility with the original

The API is the original's, so the original `tam-client` (Linux/Docker) and the Go client can share one server, and either server works. `scripts/compat/run.sh` proves it: it starts the original FastAPI server at a pinned commit and runs the Go client's `TestCompat*` tests against it, then starts the Go server with both the original SvelteKit client and the Go client and drives every route through each (`scripts/compat/drive.py`). CI runs it on every push. Two quirks of the original as published are handled on this side, so a mixed setup comes out right either way: its server leaves winning tickets alone on a restore, so `tam-client` sends them a second time through the drawing route after every restore or push into a server; and its client sends the key as `TAM_KEY` on its server-backup download, so `tam-server` accepts that spelling too. Fixes for the original itself, including its local restore skipping tickets that already exist, are submitted as [ticket-auction-manager/tam#1](https://github.com/ticket-auction-manager/tam/pull/1). The checks here are strict.

## Deployment

Both programs are single, self-contained executables: copy the one you need to the machine and run it. There is nothing to install and no container runtime involved. The original's Docker, Caddy, portable-Node and NixOS deployment files are therefore not carried over; the server's `-tls` flag replaces the reverse proxy.

| Original | Here |
|---|---|
| `dbob16/tam-client` container on port 3000 | `tam-client` (or `tam-client.exe`) on port 3080 |
| `dbob16/tam-server` container plus a Caddy proxy on 8443 | `tam-server -tls` on 8443, or `tam-server` on 8000 |
| Data volume `/data` | the `data` folder next to the program, or `TAM_DATA_DIR` |

On Windows the executables carry the TAM icons and version information; `go generate ./cmd/...` regenerates the resource files with [go-winres](https://github.com/tc-hib/go-winres) after changing `icon.ico`.

## API

Both daemons answer JSON with the original's field names and `{"detail": "..."}` on errors. The server requires a `TAM-KEY` header on every data route and a `TAM-PW` header on key management.

| Route | Server | Client |
|---|---|---|
| `GET /api` | who am I, authenticated, healthy | who am I; in remote mode the server's answer |
| `GET/POST /api/settings` | | read, or merge a full or partial object |
| `GET/POST/DELETE /api/auth` | list, create `{description}`, delete `?key_to_del=` (password) | proxied with the page's `TAM-PWD` header |
| `GET/POST/DELETE /api/prefixes` | list, upsert, delete `?p=` | same; DELETE answers 404 when nothing matched |
| `GET /api/tickets[/{prefix}[/{id}|/{from}/{to}]]`, `POST /api/tickets` | rows; single and range answer lists | single answers one object or a placeholder; range answers every id with placeholders, capped at 300 |
| `GET /api/baskets…`, `POST /api/baskets` | same shape | same shape |
| `GET /api/drawing…`, `POST /api/drawing` | baskets joined with winners; POST sets winning tickets | same |
| `GET /api/reports/byname/{prefix}`, `/bybasket/{prefix}`, `/counts` | report rows | same |
| `GET /api/search/tickets?first_name&last_name&phone_number`, `POST` | substring search, upsert | same |
| `GET/POST /api/backuprestore` | export, import | `/local`, `/remote`, and `POST /push/{prefixes\|tickets\|baskets}` |
| `GET /api/status` | | `{"mode":"standalone"}` or mode, state (`connected`, `reconnecting`, `offline`, `unauthenticated`), server, server_name, pending, failed, last_ok |
| `GET /api/servers` | | servers announcing themselves on the network: name, host, port, tls, version |
| `POST /api/pair`, `POST /api/unpair` | | `{host, port, tls, password}` pairs and stores the key; unpair takes an optional `{password}` to delete the key on the server |
| `POST /api/outbox/retry`, `POST /api/outbox/discard` | | the failed list back into the queue, or dropped |
| `/admin/...` | login, status, keys, backup, password (HTML) | |

Writes to the client require `Content-Type: application/json`, and a browser request from another site (`Sec-Fetch-Site: cross-site`) is refused, which replaces the original's per-process client id header.

## Differences from the original

- Every handler validates before writing and returns after an error; a rejected batch writes nothing.
- Settings saves merge onto the current file and are validated; a malformed file no longer breaks every request.
- Prefix deletion encodes the name (`A&B`, `50%`, `C+` can be deleted) and reports 404 when nothing matched.
- Restore overwrites existing rows on both daemons (the original overwrote locally but skipped existing tickets, and never updated winning tickets on the server).
- The remote backup download sends the correct `TAM-KEY` header, the single-basket lookup calls the baskets endpoint, and the by-basket report is titled by basket.
- Access keys are generated with a cryptographic random source; the server never accepts an empty key.
- The number input for prefix weight only accepts non-negative integers; prefix names are trimmed, at most 100 characters, and may not contain `/` or `\` (they appear in URLs).
- Ticket search treats `%` and `_` typed by the user as literal characters instead of SQL wildcards.
- A backup written by the original app restores even if a prefix carries a colour outside the palette (it is shown as white); the contact preference stays free text as in the original.
- The Settings page refuses a remote server entered with a scheme or a path (`http://tam.lan`, `tam.lan/api`): enter the host name or address only.
- Every link is a full page load (`data-sveltekit-reload`, as in the original), so pending rows on the forms are saved when you leave the page and each prefix starts with a clean form.
- The client listens on port 3080 instead of the original's 3000.
- Both programs run as plain desktop programs rather than containers: on Windows they show a TAM icon in the notification area with Shut Down (and, for the client, Open) entries, the client opens the browser on start and has a **Shut Down TAM** button under `Alt+A`, and Ctrl+C or a closed console window stops either one cleanly.
- Request bodies are capped at 64 MiB (the original ran Node with no limit); that is far above any realistic backup file. A larger body answers 413.
- Validation errors answer 400 where FastAPI answered 422. Unknown paths and wrong methods under `/api` answer `{"detail": ...}` as the original did. `HEAD` is accepted on every GET route.
- `DELETE /api/prefixes` and `DELETE /api/auth` echo the deleted row; a missing row is 404. In remote mode a prefix the server no longer has is still removed from the local mirror.
- Success messages use the `message` key everywhere (the original used `details` for a local restore and an empty list for a remote one). A reversed range (`/5/1`) is swapped instead of answered empty.
- Ids accept every spelling the original accepted (`4`, `4.0`, `"4"`); a ticket or basket without an id is rejected instead of being stored as id 0.
- Both daemons require `Content-Type: application/json` on POST (the original client always sent it; the original server did not check). The push buttons send an empty JSON object for the same reason.
- 500 and 502 responses carry a generic message; the reason is in the daemon's log.
- `tam-server dev` binds localhost only when `-addr` is not given.
- The counts report labels its last row `Total` on both daemons (the original server said `Totals`); the report views are recreated on every start so an older database picks that up.

## Layout

```
cmd/tam-client, cmd/tam-server   entry points
internal/env                     data directory from TAM_DATA_DIR, log file
internal/db                      SQLite open + schema (tables and views of the original)
internal/store                   every query, typed models
internal/config                  settings.json
internal/httpx                   JSON helpers, request guards
internal/remote                  HTTP client for tam-server
internal/server                  tam-server API
internal/client                  tam-client API and web app serving
internal/desktop                 browser opening, console title, Ctrl+C handling, Windows notification-area icon
internal/sync                    connection state, heartbeat, outbox replay, mirror pull (remote mode)
internal/discovery               mDNS announce (server) and browse (client)
internal/admin                   the server's login-protected admin pages and password file
scripts/compat                   the compatibility run against the original tam
scripts/loadtest                 the load test: a whole event through one server and many laptops
frontend/                        SvelteKit single-page app (built into cmd/tam-client/dist)
```

## License

MIT, see [LICENSE.md](LICENSE.md).

## Verified

On 2026-09-25, on Windows 11 with Go 1.27.1 and pnpm 12:

- `go vet`, `gofmt -l` and `go test ./...` are clean. The client tests drive the real server handler as the remote, so the proxy contract is tested end to end.
- Standalone, through the browser: adding prefixes, selecting a prefix on the main menu, loading a ticket range and saving names, saving basket descriptions, entering a winning ticket with the buyer looked up live, winners by name, winners by basket, ticket counts, ticket search, print sheets, saving settings.
- Remote mode, through the browser: pointing the client at `tam-server dev` with `TAM_PWD=testpw`; the main menu shows Remote, Authenticated and Healthy; a wrong password on Auth Keys is rejected; creating a key and pressing Use; pushing prefixes, tickets and baskets; entering tickets against the server (confirmed on the server with curl and the key); remote backup download.
- Remote mode over HTTPS: `tam-server -tls dev` creating its certificate on first start, and the client with Remote TLS on and port 8443 reporting Authenticated and Healthy.

A second, adversarial pass then reviewed the package against the original with the original FastAPI server and the original SvelteKit client running as oracles, sending identical request suites to both implementations, and mixed the daemons (Go client against the original server, original client against the Go server). It found and led to fixes for: full page loads on navigation (the original's `data-sveltekit-reload`, without which pending rows were lost when leaving a form), a push that the original server rejected because unused lists were sent as `null`, a new connection pool per proxied request, a settings save that could be read half-written, numeric ids sent as strings by the original client, executable bits lost in the zip, and the smaller wire and documentation differences listed above. Attacks that held up: SQL injection through every parameter, path traversal on the web app, cross-site writes, oversized and malformed bodies, hundreds of concurrent and same-row writes, key deletion and server outage in remote mode.

Remote mode v2 (2026-09-26, Windows 11): `scripts/compat/run.sh` passed locally, the Go client against the original FastAPI server (commit `19eab77`) and the original SvelteKit client together with the Go client against `tam-server`, 25 cross-checks in `drive.py`. Live, with the built executables: the server's first start in setup mode and its password set from the admin page; the client finding the server by name on the network, pairing with the password, and the bar reading Connected; the server closed from its window, the bar turning Reconnecting within five seconds and Offline after thirty, a ticket saved meanwhile answered with `X-TAM-Queued` and shown on the tickets page from the laptop's copy; the server started again, the bar back to Connected within five seconds and the queued ticket present on the server. Size checked at 9,000 tickets and 400 baskets: every call about 0.2 s, the localhost floor on Windows.
