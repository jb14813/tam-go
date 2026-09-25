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
2. Open http://localhost:3080/.
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
| `-addr` | flag, both daemons | `localhost:3080` for the client, `:8000` for the server |
| `dev` | positional argument, tam-server | binds `localhost:8000` instead of every interface |

`tam-client` keeps `settings.json` in the data directory. It is safe to edit by hand; a file that fails to parse is logged and defaults are used until it is fixed or saved again from the Settings page.

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

1. Run `tam-server` where every laptop can reach it, with `TAM_PWD` set. With Docker: `TAM_PWD=change-this docker compose up -d` starts the server on port 8000 and a Caddy reverse proxy with a self-signed certificate on port 8443.
2. On each client, open Settings and enter the server's host name, the port (8000 for plain HTTP, 8443 for TLS) and the TLS toggle, then Save.
3. Open Auth Keys, log in with `TAM_PWD`, create a key for this laptop and press Use. The key is stored in `settings.json` and sent as the `TAM-KEY` header on every server call.
4. Data entered from now on goes to the server and is mirrored locally. Backup/Restore can push the local prefixes, tickets or baskets to the server and download the server's data.

The client accepts the server's self-signed certificate, matching the original deployment; put a real certificate on the proxy if the server is reachable from outside the venue network.

## Docker

```bash
TAM_PWD=change-this docker compose up -d           # server + reverse proxy
docker compose -f compose.client.yml up -d         # client on 127.0.0.1:3080
```

Data lives in `./data` (server) and `./data-client` (client). The bind mounts carry the `:Z` label the original used, so they work with SELinux and podman.

Not carried over from the original's deployment folder: the versioned image build scripts and the generated `dist/` bundle with its podman fallback scripts, the portable Node bundle (meaningless for a static binary) and the NixOS kiosk module. To reuse the NixOS module, point its container at an image built from `Dockerfile.client` and change its port from 3000 to 3080.

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
- Request bodies are capped at 64 MiB (the original ran Node with no limit); that is far above any realistic backup file.

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
- Not verified here: the Docker images and the Caddy proxy, because the Docker daemon was not running on the build machine. The files mirror the original's deployment.
