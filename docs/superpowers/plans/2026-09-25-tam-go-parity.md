# tam-go Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the Go rewrite of Ticket Auction Manager to feature parity with the original Python/SvelteKit app and close every finding of the 25 September review.

**Architecture:** One Go module with two binaries. `internal/store` owns every SQL query over one shared `*sql.DB`; `internal/server` and `internal/client` are `http.Handler` factories that take their dependencies as parameters; `internal/client` dispatches each request to the local store or to a remote `tam-server` through `internal/remote`, mirroring the original SvelteKit endpoints. The SvelteKit frontend is built in SPA mode with base `/web` and embedded into `tam-client`.

**Tech Stack:** Go 1.27 (stdlib `net/http` with method patterns, `database/sql`), `modernc.org/sqlite`, SvelteKit 2 + Svelte 5 runes + Tailwind 4 via `adapter-static`, `hotkeys-js`, pnpm.

## Global Constraints

- Wire format identical to the original: JSON field names `prefix, color, weight`, `t_id, first_name, last_name, phone_number, pref`, `b_id, description, donors, winning_ticket`, `auth_key, description`, `unique_buyers, total_buys`, `whoami, authenticated, healthy`; errors are `{"detail": "<message>"}`.
- Headers: server data routes read `TAM-KEY`; server key management reads `TAM-PW`; pages send `TAM-PWD` to the client for key management.
- Range endpoints cap `id_to - id_from` at 300 and return one entry per id with placeholders.
- SPA served under `/web`; API under `/api`; client port 3080; server port 8000; `dev` argument binds the server to localhost.
- Data directory from `TAM_DATA_DIR`, default `./data`; client database `tam-local.db`, server database `tam-remote.db`, settings `settings.json`.
- Server password from `TAM_PWD`, default `changeme` with a startup warning.
- Every handler returns after writing an error; every write validates before touching the database; every transaction's commit error is checked.
- No package-level mutable state; dependencies are passed to constructors.
- Frontend sends no custom auth header to the client; the client refuses cross-site writes via `Sec-Fetch-Site`.

---

## File structure

```
cmd/tam-client/main.go          flags, data dir, store, settings path, embed dist, listen
cmd/tam-server/main.go          flags, data dir, store, password, listen
internal/env/env.go             DataDir() (string, error)
internal/db/db.go               Open(path) (*sql.DB, error); Migrate(db) error; schema constant
internal/db/db_test.go
internal/store/models.go        Prefix, Ticket, Basket, DrawingLine, ReportByNameLine, ReportByBasketLine, ReportCountLine, AuthKey, BackupFile
internal/store/store.go         Store, New, prefixes, auth keys, backup
internal/store/tickets.go       tickets, search
internal/store/baskets.go       baskets, drawing
internal/store/reports.go       reports
internal/store/store_test.go
internal/config/config.go       Settings, Defaults, Load, Save, Merge, Validate, RemoteURL
internal/config/config_test.go
internal/httpx/httpx.go         WriteJSON, WriteError, DecodeJSON, SameSite, IntParam
internal/httpx/httpx_test.go
internal/remote/remote.go       Client, New, Get, Post, Delete, Status
internal/server/server.go       NewHandler(st *store.Store, password string) http.Handler + all routes
internal/server/server_test.go
internal/client/client.go       NewHandler(st *store.Store, settingsPath string, dist fs.FS) http.Handler
internal/client/api.go          the /api/* handlers with local-or-remote dispatch
internal/client/client_test.go
frontend/                       SvelteKit SPA (see Task 10)
build.sh, run.sh, Dockerfile.client, Dockerfile.server, compose.yml, rp/Caddyfile, README.md, .github/workflows/ci.yml
```

---

### Task 1: env and db

**Files:** Create `internal/env/env.go` (replace), `internal/db/db.go` (replace), `internal/db/db_test.go`.

**Interfaces produced:**
- `env.DataDir() (string, error)`: reads `TAM_DATA_DIR`, defaults to `./data`, `filepath.Clean`, `os.MkdirAll(0o755)`.
- `db.Open(path string) (*sql.DB, error)`: DSN `file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)`, pings.
- `db.Migrate(sqldb *sql.DB) error`: executes the schema (four tables, four views) with `CREATE ... IF NOT EXISTS`.

- [ ] Test `TestMigrateCreatesSchema`: open a temp-dir DB, migrate twice (idempotent), query `sqlite_master` for names `prefixes, tickets, baskets, auth_keys, drawing, report_by_name, report_by_basket, report_counts`.
- [ ] Run `go test ./internal/db/` → FAIL (package missing) → implement → PASS → commit `feat(db): shared open with pragmas and full schema`.

### Task 2: store

**Files:** Create `internal/store/models.go`, `store.go`, `tickets.go`, `baskets.go`, `reports.go`, `store_test.go`.

**Interfaces produced (all methods on `*Store`, `New(db *sql.DB) *Store`):**
- Prefixes: `ListPrefixes() ([]Prefix, error)`, `UpsertPrefixes([]Prefix) error`, `DeletePrefix(name string) (int64, error)` (rows affected).
- Tickets: `AllTickets()`, `TicketsByPrefix(prefix)`, `Ticket(prefix, id) (*Ticket, error)` (nil when missing), `TicketRange(prefix, from, to) ([]Ticket, error)`, `UpsertTickets([]Ticket) error`, `SearchTickets(first, last, phone string) ([]Ticket, error)`.
- Baskets: `AllBaskets()`, `BasketsByPrefix`, `Basket(prefix, id) (*Basket, error)`, `BasketRange`, `UpsertBaskets([]Basket) error` (description, donors), `UpsertWinning([]Basket) error` (winning_ticket only, inserts when missing).
- Drawing: `AllDrawing()`, `DrawingByPrefix`, `DrawingLine(prefix, id) (*DrawingLine, error)`, `DrawingRange`.
- Reports: `ReportByName(prefix) ([]ReportByNameLine, error)`, `ReportByBasket(prefix) ([]ReportByBasketLine, error)`, `ReportCounts() ([]ReportCountLine, error)`.
- Auth: `ListKeys()`, `CreateKey(description) (AuthKey, error)`, `DeleteKey(key) (int64, error)`, `KeyExists(key) (bool, error)`.
- Backup: `Export() (BackupFile, error)`, `Import(BackupFile) error` (chunks of 300, upsert semantics of each table).
- Nullable columns scan through `sql.NullString`/`sql.NullInt64` into plain fields.

- [ ] Tests (temp DB per test via helper `newTestStore(t)`): upsert-then-list for each table; single returns nil when missing; range ordering; drawing view joins the winner; counts view has a `Total` row; search uses substring match on all three fields; auth key create is 32 chars of `[A-Z0-9]` and unique; export/import round trip.
- [ ] Run → FAIL → implement → PASS → commit `feat(store): typed data access for every table and view`.

### Task 3: config and httpx

**Files:** Create `internal/config/config.go` (replace), `config_test.go`, `internal/httpx/httpx.go`, `httpx_test.go`.

**Interfaces produced:**
- `config.Settings{RemoteServer, RemoteKey, RemotePort string; RemoteTLS bool; DefaultPref, VenueName string; DisableAttrib bool}` with the original JSON tags.
- `config.Defaults() Settings` (`remote_port "8000"`, `default_pref "CALL"`, `venue_name "Test Venue"`).
- `config.Load(path string) (Settings, error)`: missing file → write defaults and return them; unreadable or malformed → return defaults and a non-nil `*LoadError` the caller logs (never panics).
- `config.Save(path string, s Settings) error` (0644, indented).
- `config.Merge(current Settings, patch map[string]json.RawMessage) (Settings, error)`: only keys present in the patch change; unknown keys rejected.
- `config.Validate(s Settings) error`: port numeric 1..65535 when set, `default_pref` in `{CALL, TEXT}`.
- `s.RemoteURL() string`: `""` when no server, else `http(s)://server:port`.
- `httpx.WriteJSON(w, status, v)`, `httpx.WriteError(w, status, msg)`, `httpx.DecodeJSON(r *http.Request, v any) error` (1 MiB... use 64 MiB limit for backup files, error when `Content-Type` is not `application/json` on non-empty bodies), `httpx.SameSite(r) bool` (`Sec-Fetch-Site` absent, `same-origin` or `none` → true), `httpx.IntParam(r, name) (int, error)`.

- [ ] Tests: load writes defaults; malformed returns defaults + LoadError; merge keeps unspecified fields; validate rejects port `abc` and pref `PHONE`; SameSite table; DecodeJSON rejects `text/plain`.
- [ ] Commit `feat(config,httpx): settings merge and validation, handler helpers`.

### Task 4: server handler

**Files:** Create `internal/server/server.go`, `server_test.go`; delete `internal/prefixes`.

**Interfaces produced:** `server.NewHandler(st *store.Store, password string) http.Handler`.

Routes per the spec table. Middleware `requireKey` reads `TAM-KEY` and answers 401 `{"detail":"Invalid Key"}`; `requirePassword` reads `TAM-PW` and answers 401 `{"detail":"Invalid Password"}`. `GET /api` never fails. `DELETE /api/prefixes` answers 404 `{"detail":"Prefix not found"}` when no row matched. POST bodies are decoded fully and validated (`prefix` non-blank after trim, `color` in palette, `weight >= 0`; ticket/basket ids `>= 0`; `pref` in `{CALL, TEXT, ""}`) before any write.

- [ ] Tests with `httptest.NewServer(NewHandler(...))`: 401 without key, 401 with bad key, 200 with key; password checks on `/api/auth`; create key then use it; prefixes CRUD incl. 404; tickets upsert then single/range/all; baskets upsert; drawing POST sets winning ticket and GET joins winner; reports; search; backup export/import; `/api` authenticated flag; wrong-type payload answers 400 and writes nothing.
- [ ] Commit `feat(server): full API with key and password auth`.

### Task 5: remote client and client handler

**Files:** Create `internal/remote/remote.go`, `internal/client/client.go`, `internal/client/api.go`, `internal/client/client_test.go`.

**Interfaces produced:**
- `remote.New(baseURL, key string, insecureTLS bool) *Client`; `(*Client).Get(path string, into any) (int, error)`, `Post(path string, body any, into any) (int, error)`, `Delete(path string, into any) (int, error)`, `Do(method, path string, headers map[string]string, body any, into any) (int, error)`. A transport error returns `(0, err)`. 5 s timeout.
- `client.NewHandler(st *store.Store, settingsPath string, dist fs.FS) http.Handler`; settings are loaded per request through `config.Load` (a malformed file logs once per request and uses defaults).

Behaviour per the spec table, including: cross-site write refusal (403 `{"detail":"Cross-site request refused"}`), `/api/settings` POST merge+validate+save, `/api/auth` proxy with `TAM-PWD` → `TAM-PW`, single/range placeholders, mirror-after-remote writes, backup local/remote/push, SPA fallback (`/web/anything` without an extension serves `index.html`; assets served from `dist`), `/` → 302 `/web/`.

- [ ] Tests: standalone CRUD through the client; remote mode using `httptest.NewServer(server.NewHandler(realStore, "pw"))` as the remote with a created key, asserting proxying and local mirroring; remote down → `[]`/placeholder/`healthy:false`; settings partial POST keeps other fields; garbage POST → 400 and file unchanged; malformed settings file → GET returns defaults; cross-site POST → 403; `/web/tickets/CALL/` → index.html; `/web/_app/x.js` → 404 when missing.
- [ ] Commit `feat(client): local-or-remote API mirroring the original endpoints, SPA fallback`.

### Task 6: binaries

**Files:** Replace `cmd/tam-client/main.go`, `cmd/tam-server/main.go`.

- `tam-client`: flag `-addr` (default `localhost:3080`), env `TAM_DATA_DIR`; opens `tam-local.db`, migrates, `http.Server{ReadHeaderTimeout: 10s}`, `log.Fatal` on error.
- `tam-server`: flag `-addr` (default `:8000`), positional `dev` keeps binding `localhost:8000`; env `TAM_PWD` (default `changeme` + warning); `tam-remote.db`.
- [ ] `go build ./...` (frontend must be built first for the embed), `go vet ./...`, commit `feat(cmd): explicit wiring, fatal on startup errors`.

### Task 7: frontend port (delegated)

**Files:** `frontend/vite.config.js`, `frontend/src/routes/+layout.js`, `+layout.svelte`, `+page.js`, `+page.svelte`, `lib/client/components/{HeaderBar,CommandBar,PagerBar,TicketSearchBar}.svelte`, `lib/client/api.js`, routes `tickets/[prefix]`, `baskets/[prefix]`, `drawing/[prefix]`, `reports/byname/[prefix]`, `reports/bybasket/[prefix]`, `reports/counts`, `search/tickets`, `sheets`, `settings`, `settings/prefixes`, `settings/auth-keys`, `settings/backuprestore` (each with `+page.js` load and `+page.svelte`), `static/manifest.json`.

Rules: SPA mode (`ssr=false`, `prerender=false`, fallback `index.html`, base `/web`, `relative:false`); universal `load` in `+page.js` replaces every `+page.server.js`; no `TAM-CLIENT-ID`; hotkeys registered in `$effect` with `hotkeys.unbind` cleanup; main menu wires `disable_attrib`, shows mode/authenticated/healthy, shows a status line on load failure; prefixes page encodes the delete parameter, trims, weight `step="1" min="0"`, shows API errors; by-basket report titled "Winners by Basket"; backup push uses POST.

- [ ] `npx --yes pnpm@latest build` passes and writes `cmd/tam-client/dist/index.html` plus `_app/`. Commit `feat(frontend): port every page from the original in SPA mode`.

### Task 8: end-to-end verification

- [ ] Build both binaries; run `tam-client` (standalone) and drive in a browser: prefixes, tickets entry + save, baskets, drawing lookup, both reports, counts, search, sheets, settings save, backup download/upload.
- [ ] Run `tam-server dev` with `TAM_PWD=testpw`; in the client set remote server `localhost`, port `8000`, TLS off; auth-keys page: login, create key, Use; enter tickets in remote mode; confirm rows on the server via curl; push baskets; remote backup download.
- [ ] Record results in the README's "Verified" section.

### Task 9: packaging and docs

**Files:** `build.sh`, `run.sh`, `Dockerfile.client`, `Dockerfile.server`, `compose.yml`, `rp/Caddyfile`, `README.md`, `.github/workflows/ci.yml`, `go.mod` (tidy), `.gitignore` (`/build/`).

- [ ] `build.sh client|server [GOOS=... GOARCH=...]` works; `docker build -f Dockerfile.server .` and `-f Dockerfile.client .` succeed; `go vet`, `gofmt -l`, `go test ./...` clean; commit `chore: packaging, CI, README`.
- [ ] Produce `tam-go-parity.zip`: repository with history, without `node_modules`, `.svelte-kit`, caches; plus `build/` with windows-amd64 and linux-amd64 binaries for both daemons.
