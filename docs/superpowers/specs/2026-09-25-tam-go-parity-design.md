# tam-go parity design

Date: 2026-09-25. Target: bring `tam-go` (the Go rewrite) to feature parity with `ticket-auction-manager/tam` (Python FastAPI server + SvelteKit SSR client) and fix every verified finding from the 25 September review.

## Scope and assumptions

In scope: every user-facing feature and API of the original, on both daemons.

- Client (`tam-client`): main menu with mode/auth/health footer, tickets, baskets, drawing, winners-by-name and winners-by-basket reports, ticket counts, print sheets, ticket search, settings, prefixes, auth keys, backup/restore (local download, remote download, upload, push to server), remote mode (every data call proxied to the server with `TAM-KEY`, and mirrored into the local database as the original does).
- Server (`tam-server`): every `/api/*` route of the FastAPI app, `TAM-KEY` auth on data routes, `TAM-PW` password on key management, backup/restore.
- Packaging: fixed `build.sh`, Dockerfiles for both daemons, a compose file with the Caddy reverse proxy from the original, a root README, a GitHub Actions workflow.

Out of scope, stated so nobody looks for them: the NixOS module (untestable here), the portable Node bundle (meaningless for a Go binary), Drizzle migrations (schema is applied by `InitDB` as in the Python server).

Assumptions made without asking, because the request was unambiguous about the goal and this session runs unattended:

- Keep the author's technology choices: Go standard library `net/http`, `modernc.org/sqlite`, SvelteKit static build with Tailwind, embedded into the binary, served under `/web`.
- Keep the original's wire format exactly (field names, list-or-placeholder semantics, `{"detail": ...}` errors) so the two implementations can be swapped.
- Keep the original's TLS policy for remote mode (the client accepts the server's self-signed Caddy certificate), and keep the original's server password model (`TAM_PWD` environment variable).
- Bugs in the original are not copied: the remote backup download sent a `TAM_KEY` header the server never reads; the single-basket lookup in remote mode called the tickets endpoint; the by-basket report page was titled "by Name".

## Architecture

Two binaries share one module. Everything below `internal/` is a package with one job.

| Package | Job | Depends on |
|---|---|---|
| `internal/env` | data directory from `TAM_DATA_DIR` (default `./data`), created on demand, with `path/filepath` | stdlib |
| `internal/db` | `Open(path)` with `busy_timeout` and WAL pragmas; `Migrate(db)` applies the original schema: `prefixes`, `tickets`, `baskets`, `auth_keys`, and the `drawing`, `report_by_name`, `report_by_basket`, `report_counts` views | modernc sqlite |
| `internal/store` | `Store` around one `*sql.DB`: typed models with the original JSON names and every query the two daemons need | `db` |
| `internal/config` | the `settings.json` file: `Load` (defaults written when missing, defaults returned with a logged warning when unreadable or malformed), `Save`, `Merge` onto current values, `Validate`, `RemoteURL()` | stdlib |
| `internal/httpx` | `WriteJSON`, `WriteError` (`{"detail": msg}`), `DecodeJSON` (limits body size, rejects wrong `Content-Type`), `SameSite` check for cross-site writes, integer path params | stdlib |
| `internal/remote` | HTTP client for a remote `tam-server`: base URL, key, optional insecure TLS; `GetJSON`, `PostJSON`, `Delete` returning status and decoded body | stdlib |
| `internal/server` | `NewHandler(store, password)`: the server mux with `TAM-KEY` middleware on data routes and `TAM-PW` checks on `/api/auth` | `store`, `httpx` |
| `internal/client` | `NewHandler(store, settingsPath, dist)`: SPA serving with fallback, `/api/*` with local-or-remote dispatch mirroring the original SvelteKit endpoints | `store`, `config`, `remote`, `httpx` |
| `cmd/tam-server`, `cmd/tam-client` | wire the above, parse flags, `log.Fatal` on listen errors | everything |

Handlers receive their dependencies explicitly. No package-level state, no `os.Setenv` between packages.

## Data model

Copied from the original schema (SQLite dialect is identical):

```sql
CREATE TABLE IF NOT EXISTS prefixes (prefix TEXT PRIMARY KEY, color TEXT, weight INTEGER);
CREATE TABLE IF NOT EXISTS tickets (prefix TEXT, t_id INTEGER, first_name TEXT, last_name TEXT, phone_number TEXT, pref TEXT, PRIMARY KEY (prefix, t_id));
CREATE TABLE IF NOT EXISTS baskets (prefix TEXT, b_id INTEGER, description TEXT, donors TEXT, winning_ticket INTEGER, PRIMARY KEY (prefix, b_id));
CREATE TABLE IF NOT EXISTS auth_keys (auth_key TEXT PRIMARY KEY, description TEXT);
CREATE VIEW IF NOT EXISTS drawing AS ...;          -- baskets LEFT JOIN tickets on winning ticket
CREATE VIEW IF NOT EXISTS report_by_name AS ...;   -- ordered by last, first, phone, prefix, basket
CREATE VIEW IF NOT EXISTS report_by_basket AS ...; -- ordered by prefix, basket
CREATE VIEW IF NOT EXISTS report_counts AS ...;    -- per-prefix unique buyers and total buys, plus a 'Total' row
```

JSON models: `Prefix{prefix,color,weight}`, `Ticket{prefix,t_id,first_name,last_name,phone_number,pref}`, `Basket{prefix,b_id,description,donors,winning_ticket}`, `DrawingLine{prefix,b_id,description,winning_ticket,last_name,first_name,phone_number}`, `ReportByNameLine`, `ReportByBasketLine`, `ReportCountLine{prefix,unique_buyers,total_buys}`, `AuthKey{auth_key,description}`, `BackupFile{prefixes,baskets,tickets}`. Nullable text columns decode to empty strings.

## HTTP surface

Server (`tam-server`, port 8000). Every route except `/api` and `/api/auth` requires a `TAM-KEY` header that exists in `auth_keys`; `/api/auth` requires `TAM-PW` equal to `TAM_PWD`.

| Route | Methods | Notes |
|---|---|---|
| `/api` | GET | `{whoami:"TAM Server", authenticated, healthy:true}`; `authenticated` reflects the key if one is sent |
| `/api/auth` | GET, POST `{description}`, DELETE `?key_to_del=` | list, create (32-char uppercase+digits), delete |
| `/api/prefixes` | GET, POST `[Prefix]`, DELETE `?p=` | upsert; DELETE answers 404 when nothing matched |
| `/api/tickets` `/{prefix}` `/{prefix}/{t_id}` `/{prefix}/{id_from}/{id_to}` | GET; POST on the root | single and range return lists, as the original |
| `/api/baskets` same shape | GET; POST | POST upserts description and donors only |
| `/api/drawing` same shape | GET; POST | POST upserts `winning_ticket` only |
| `/api/reports/byname/{prefix}`, `/bybasket/{prefix}`, `/counts` | GET | |
| `/api/search/tickets` | GET `?first_name&last_name&phone_number` (LIKE, wildcards), POST `[Ticket]` | |
| `/api/backuprestore` | GET, POST `BackupFile` | import upserts in chunks of 300 |

Client (`tam-client`, port 3080). `/` redirects to `/web/`; `/web/` serves the SPA with `index.html` as the fallback for client-side routes. Every write (`POST`, `DELETE`) is refused with 403 unless `Sec-Fetch-Site` is `same-origin` or `none` (curl and other non-browser clients send no header and are allowed; browsers always send it). Routes mirror the original SvelteKit endpoints:

| Route | Standalone | Remote mode |
|---|---|---|
| `/api` | `{whoami:"TAM Client"}` | proxies the server's `/api`; on failure `{whoami:"TAM Server", authenticated:false, healthy:false}` |
| `/api/settings` | GET current; POST merges onto current, validates, saves, returns the result | same |
| `/api/auth` GET/POST/DELETE | 500 "Not configured." | proxied with the page's `TAM-PWD` header sent as `TAM-PW`; 502 on failure |
| `/api/prefixes` | local | GET proxied (`[]` on failure); POST/DELETE proxied then mirrored locally |
| `/api/tickets…`, `/api/baskets…`, `/api/drawing…` | local | GET proxied (`[]` or placeholder on failure); POST proxied then mirrored |
| single `/{prefix}/{id}` | the row, or a placeholder (`pref` = `default_pref` for tickets) | same via proxy |
| range `/{prefix}/{from}/{to}` | full range with placeholders, existing rows merged in; `to - from` capped at 300 | same via proxy; `[]` on failure |
| `/api/reports/*` | local views | proxied, `[]` on failure |
| `/api/search/tickets` | local | proxied; POST proxied then mirrored |
| `/api/backuprestore/local` | GET export, POST import | same (local only) |
| `/api/backuprestore/remote` | `{}` | GET proxied export; POST proxied import |
| `/api/backuprestore/push/{prefixes\|tickets\|baskets}` | 500 "Server not set." | POST pushes that local table to the server |

## Frontend

SvelteKit static build in SPA mode: `ssr = false`, `prerender = false`, `adapter-static({ fallback: 'index.html' })`, `paths.base = '/web'`, `paths.relative = false`. Each page gets a `+page.js` universal `load` that fetches what the original's `+page.server.js` fetched, so page components stay close to the originals and `data` keeps its shape. Components ported: `HeaderBar`, `CommandBar`, `PagerBar`, `TicketSearchBar`; hotkeys are registered in `$effect` blocks with cleanup so they never accumulate. The main menu wires `disable_attrib`, shows mode, authenticated and healthy from `/api`, and shows a status line when a load fails. The prefixes page encodes the delete parameter, trims names, and constrains weight to non-negative integers. The `TAM-CLIENT-ID` header of the original is replaced by the `Sec-Fetch-Site` check on the Go side, so pages send no custom header.

## Error handling

Every handler decodes and validates before touching the database and returns after writing an error. Store methods return errors; handlers map them to 500 with the message in `detail`. Transactions are committed explicitly and their errors checked. Startup fails loudly (`log.Fatal`) when the data directory, database or listener cannot be set up. A malformed `settings.json` is logged and replaced by defaults in memory, never written back until a valid save happens.

## Testing

Go: `go test ./...` with `t.TempDir()` databases. `store` tests cover every query. `server` tests cover auth (missing key, bad key, good key; password checks), every route's happy path, upsert semantics, range placeholders, counts view. `client` tests cover standalone mode, remote mode against an `httptest.Server` playing the real server handler (so the proxy contract is tested end to end), the cross-site refusal, settings merge and malformed-file recovery, SPA fallback. Frontend: `pnpm build` must pass; the flows are exercised in a browser against both daemons before delivery (standalone entry of tickets, baskets, drawing lookup, both reports, counts, search, sheets, backup download and upload, settings, prefixes; then remote mode with keys created through the auth-keys page and data pushed to the server).

## Delivery

Work happens on branch `parity` with logical commits. The deliverable is a zip of the repository (history included, `node_modules` and caches excluded) plus prebuilt `tam-client` and `tam-server` binaries for Windows and Linux (amd64).
