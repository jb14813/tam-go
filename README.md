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
2. It opens http://localhost:3080/ in your default browser as soon as it is listening (start it with `-open=false` to skip that, for example from a script). Keep the console window open while the event runs. To stop it, press `Alt+A` on the main menu and use **Shut Down TAM**, close the console window, or press Ctrl+C in it. Closing the browser tab alone leaves it running.
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

## Configuration

| Setting | Where | Default |
|---|---|---|
| `TAM_DATA_DIR` | environment, both daemons | `./data` |
| `TAM_PWD` | environment, tam-server | `changeme` (a warning is logged; set it before exposing the server) |
| `-addr` | flag, both daemons | `localhost:3080` for the client, `:8000` for the server (`:8443` with `-tls`) |
| `-open` | flag, tam-client | `true`: open the web app in the default browser on start |
| `dev` | positional argument, tam-server | binds localhost instead of every interface |
| `-tls`, `-cert`, `-key` | flags, tam-server | HTTPS with the given PEM files, or a self-signed pair created in the data directory |

`tam-client` keeps `settings.json` in the data directory. It is safe to edit by hand while the daemon runs; the edit is picked up on the next request. A file that fails to parse is logged and the last good settings stay in effect until it is fixed or saved again from the Settings page.

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

1. Run `tam-server` where every laptop can reach it, with `TAM_PWD` set. For HTTPS start it with `-tls`: it listens on port 8443 and, on first start, writes a self-signed certificate (`server.crt`, `server.key`) into its data directory; put your own PEM files there, or point `-cert` and `-key` at them, to use a real certificate. Without `-tls` it speaks plain HTTP on port 8000. Windows asks once whether to allow the program through the firewall.
2. On each client, open Settings and enter the server's host name, the port (8000 for plain HTTP, 8443 for TLS) and the TLS toggle, then Save.
3. Open Auth Keys, log in with `TAM_PWD`, create a key for this laptop and press Use. The key is stored in `settings.json` and sent as the `TAM-KEY` header on every server call.
4. Data entered from now on goes to the server and is mirrored locally. Backup/Restore can push the local prefixes, tickets or baskets to the server and download the server's data.

The client accepts the server's self-signed certificate, matching the original deployment (which ran a Caddy proxy with a self-signed certificate in front of the server); use a real certificate if the server is reachable from outside the venue network.

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
internal/env                     data directory from TAM_DATA_DIR
internal/db                      SQLite open + schema (tables and views of the original)
internal/store                   every query, typed models
internal/config                  settings.json
internal/httpx                   JSON helpers, request guards
internal/remote                  HTTP client for tam-server
internal/server                  tam-server API
internal/client                  tam-client API and web app serving
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
