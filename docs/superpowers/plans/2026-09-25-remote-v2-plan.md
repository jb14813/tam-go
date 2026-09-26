# Remote mode v2 implementation plan

Spec: `docs/superpowers/specs/2026-09-25-tam-go-remote-v2-design.md`. Base: branch `parity` at the commit that adds this plan.

**Goal:** a client that keeps working through wifi drops and never loses a save, one-step pairing with a server it can find on the network, a status bar on every page, a server admin page with login, and a CI job that proves compatibility with the original tam.

**Architecture:** the client's local database becomes a mirror plus an outbox; `internal/sync` owns the connection state, the heartbeat, the replay worker and the full pull. `internal/discovery` wraps mDNS. `internal/admin` is the server's login page. The web app gets a status bar, a Server section in Settings and a CALL/TEXT choice on the tickets form. Nothing in `/api` changes shape.

**Tech stack:** Go 1.27, modernc.org/sqlite, `github.com/grandcat/zeroconf` (mDNS), `golang.org/x/crypto/bcrypt`, `html/template`; SvelteKit 2 with Svelte 5 runes.

## Global constraints

- Every existing `/api` route keeps its path, method, JSON field names, status codes and `{"detail": ...}` errors on both daemons.
- New routes are additive. The original client and server must keep working against the Go daemons unchanged.
- `gofmt -l .` empty, `go vet ./...` clean for `GOOS=windows` and `GOOS=linux`, `go test ./...` green, `pnpm build` in `frontend/` clean.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.

## Contracts shared by the work packages

### Client routes (new, all JSON, writes need `Content-Type: application/json` and pass the same-site guard)

| Route | Body | Answer |
|---|---|---|
| `GET /api/status` | | `{"mode":"standalone"}` or `{"mode":"remote","state":"connected"\|"reconnecting"\|"offline"\|"unauthenticated","server":"host:port","server_name":"main-laptop","pending":0,"failed":0,"last_ok":"2026-09-25T18:00:00Z"}` (`last_ok` is `""` before the first success) |
| `GET /api/servers` | | `[{"name":"main-laptop","host":"192.168.1.10","port":"8443","tls":true,"version":"0.0.1"}]`, discovered in the last browse (about 1.5 s), possibly empty |
| `POST /api/pair` | `{"host":"main-laptop","port":"8443","tls":true,"password":"..."}` | 200 `{"message":"Paired with main-laptop","server":"main-laptop:8443"}`; 401 `{"detail":"The server rejected the password"}`; 502 `{"detail":"Remote server unreachable"}`; 503 `{"detail":"The server has no password yet; open its admin page first"}` |
| `POST /api/unpair` | `{}` or `{"password":"..."}` | 200 `{"message":"Standalone again. 3 saves that had not reached the server were dropped; they are still on this laptop."}`; with a password the laptop's key is deleted on the server when reachable |
| `POST /api/outbox/retry` | `{}` | 200 `{"message":"Retrying 2 saves","pending":2}` |
| `POST /api/outbox/discard` | `{}` | 200 `{"message":"Discarded 2 saves"}` |

Existing write routes answer 200 with the rows and the header `X-TAM-Queued: 1` when the save went to the outbox.

### Settings file

`settings.json` gains `remote_name` (display name of the paired server) and `remote_fingerprint` (SHA-256 of the server certificate, lowercase hex, `""` when not pinned). Both default to `""`; `POST /api/settings` accepts them.

### Server root

`GET /api` on the server answers `{"whoami":"TAM Server","authenticated":bool,"healthy":true,"name":"<host name>","version":"0.0.1"}`. The two new fields are additive.

### Server password

`internal/admin.Password`: `Load(dataDir, envValue string) (*Password, error)`; `IsSet() bool`; `Check(plain string) bool` (constant time; bcrypt for the stored hash, `subtle.ConstantTimeCompare` for the env value); `Set(plain string) error` writes `<dataDir>/server.json` as `{"password_hash":"$2a$..."}`. A stored hash wins over the env value. `POST /api/auth` answers 503 `{"detail":"server password not set"}` while `IsSet()` is false.

### Databases

- Client: tables `outbox(id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL, body BLOB, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '')` and `outbox_failed` (same columns plus `failed_at TEXT NOT NULL`), created by `db.MigrateClient`.
- Server: `auth_keys.last_seen TEXT` added by `db.MigrateServer` when missing (`PRAGMA table_info`).

## Work package A: sync core (internal/sync, internal/client, internal/remote, internal/db, internal/store, internal/config)

Files: create `internal/sync/sync.go`, `internal/sync/sync_test.go`, `internal/store/outbox.go`, `internal/store/outbox_test.go`; modify `internal/db/db.go`, `internal/config/config.go`, `internal/remote/remote.go`, `internal/client/client.go`, `internal/client/api.go`, `internal/client/client_test.go`, `cmd/tam-client/main.go`.

- [ ] `store`: `EnqueueOutbox(method, path string, body []byte) (int64, error)`, `NextOutbox() (*Outbox, error)`, `DeleteOutbox(id int64) error`, `NoteOutboxAttempt(id int64, errText string) error`, `FailOutbox(id int64, errText string) error` (moves the row), `OutboxCounts() (pending, failed int, err error)`, `RetryFailed() (int, error)`, `DiscardFailed() (int, error)`, `ClearOutbox() (int, error)`, `ReplacePrefixes([]Prefix) error` (delete all, insert), tests for each.
- [ ] `remote`: `WithTimeout(d time.Duration) *Client`, `WithFingerprint(hex string) *Client` (pins the leaf certificate; `""` keeps today's accept-any behaviour), `Fingerprint(hostPort string) (string, error)`; `Response.Retryable()` is true for 5xx.
- [ ] `config`: fields `RemoteName`, `RemoteFingerprint`; `RemoteURL` unchanged.
- [ ] `sync.Syncer`: `New(st *store.Store, cfg *config.File, client func(config.Settings) *remote.Client) *Syncer`; `Run(ctx)` runs the heartbeat (every 5 s, `GET /api`, 2 s timeout) and the worker; `Status() Status`; `Online() bool`; `Enqueue(method, path string, body []byte)`; `Kick()` wakes the worker; `NoteFailure(err)` and `NoteSuccess()` let request handlers feed the state. Transition to `connected` runs: drain outbox (in order; 2xx delete, transport or 5xx stop and back off 1, 2, 5, 10, 30 s; 401/403 stop and go `unauthenticated`; other 4xx move to failed), then full pull (`GET /api/backuprestore` on the server → `ReplacePrefixes` + `UpsertTickets` + `UpsertBaskets`). Tests use `httptest` servers that flip between up, down and 401.
- [ ] `client`: reads (`listOr`, `singleOr`, `rangeOr`, reports, search, drawing) go to the server when `Online()`, upsert the answer into the mirror, and answer from the mirror when not online or when the call fails; writes go to the server first when online (5 s timeout), then the mirror; on transport/5xx/401/403 write the mirror and enqueue, answer 200 with `X-TAM-Queued: 1`; on other 4xx forward the error. When not online, write the mirror and enqueue without trying. Prefix delete: same pattern with `DELETE /api/prefixes?p=`. New routes from the contract. `GET /api` (client root) keeps its shape and uses the syncer state so it does not block on a dead server.
- [ ] `cmd/tam-client`: `db.MigrateClient`, start the syncer, log state changes ("server main-laptop: connected", "offline, 3 saves waiting").
- [ ] Tests: client tests for queued saves, replay order after the server returns, reads from the mirror while offline, pair/unpair, status.

## Work package B: discovery (internal/discovery, cmd/tam-server, internal/client)

- [ ] `discovery.Announce(ctx, name string, port int, useTLS bool, version string) error` (server) and `discovery.Browse(ctx, wait time.Duration) ([]Server, error)` with `Server{Name, Host, Port string, TLS bool, Version string}` (client), service `_tam._tcp`, TXT `port`, `tls`, `name`, `v`.
- [ ] Server: announce after listening; `-announce=false` flag turns it off.
- [ ] Client: `GET /api/servers` browses for 1.5 s.

## Work package C: server admin page (internal/admin, internal/server, cmd/tam-server, internal/db, internal/store)

Files: create `internal/admin/admin.go`, `internal/admin/password.go`, `internal/admin/session.go`, `internal/admin/templates/*.html`, tests; modify `internal/server/server.go` (password check through `admin.Password`, 503 while unset, `last_seen` touch, root fields), `internal/store/store.go` (`Counts()`, `TouchKey(key string) error`, `last_seen` in `AuthKey`), `internal/db/db.go` (`MigrateServer`), `cmd/tam-server/main.go` (mount `/admin/`, load the password, log the setup hint).

- [ ] `Password` as in the contract, with tests (env only, hash wins, unset, wrong password).
- [ ] Sessions: 32 random bytes, cookie `tam_admin`, HttpOnly, SameSite=Strict, Secure when the request is TLS, 12 h, in memory; a per-session CSRF token in every form; 5 failed logins from one remote address wait 30 s.
- [ ] Pages: `/admin/` (login, or setup when no password), `/admin/status`, `/admin/keys` (list with description, created, last seen; create shows the key once; delete), `/admin/backup` (download `tam-backup.json`; restore upload with a confirmation checkbox), `/admin/password` (current + new twice), `/admin/logout`. `html/template`, embedded, no JavaScript needed; plain readable CSS inline.
- [ ] Server `GET /api` gains `name` and `version`; `requirePassword` uses `Password.Check`; key routes touch `last_seen` at most once a minute per key.
- [ ] Tests: login flow, CSRF refusal, setup mode, keys create/delete, backup download and restore, password change, API 503 while unset.

## Work package D: web app (frontend/)

Files: create `frontend/src/lib/components/StatusBar.svelte`; modify `frontend/src/routes/+layout.svelte`, `frontend/src/routes/settings/+page.svelte` (and its `+page.js`), the tickets form row rendering under `frontend/src/routes/tickets/[prefix]/` and the shared row component under `frontend/src/lib/components/`, `frontend/src/lib/client/api.js` if a helper is needed.

- [ ] Status bar: polls `GET /api/status` every 3 s; hidden in standalone mode and when the route answers 404; green "Connected to <server_name>", amber "Reconnecting, N saves waiting", red "Offline, N saves waiting", red "The server rejected this laptop's key, open Settings", plus "N saves could not be sent, open Settings" whenever `failed` > 0. Never blocks the page.
- [ ] Settings, new **Server** section above the existing form: discovered servers from `GET /api/servers` (poll every 5 s while the page is open; "Looking for servers on this network..." while empty), fields host / port / TLS for manual entry, a password field, **Pair** (POST `/api/pair`, shows the message or the error), current pairing shown with **Unpair** (POST `/api/unpair` with an optional password), and when `failed` > 0 a line with **Retry** and **Discard** buttons. The existing remote fields stay editable for parity.
- [ ] Tickets form: the Pref cell is a `<select>` with CALL and TEXT; a new row defaults to the workstation's `default_pref` from settings; existing values that are neither are shown as a third option so they are not silently changed.
- [ ] `pnpm build` clean; check the pages in `pnpm dev` against a running client.

## Work package E: compatibility job (.github/workflows/ci.yml, scripts/compat/)

- [ ] `scripts/compat/run.sh`: checks out `ticket-auction-manager/tam` at commit `19eab77`, starts its FastAPI server (`api/`) with `TAM_PWD`, runs `go test ./internal/client -run Compat` with `TAM_COMPAT_SERVER=http://127.0.0.1:8000`; then starts the Go server and the original client (`client/`, `pnpm install && pnpm build && node build`) pointed at it and exercises keys, prefixes, tickets, baskets, drawing, reports, search, backup, restore and push through the original client's `/api`; then both clients on one Go server.
- [ ] Go tests `TestCompat*` in `internal/client` skip unless `TAM_COMPAT_SERVER` is set.
- [ ] CI job `compat` on ubuntu with Python 3.12 and Node 24.

## Integration order

1. A and C and D are built in parallel on branches `remote-v2-sync` (A, with B), `remote-v2-admin` (C), `remote-v2-web` (D).
2. Merge C, then A, then D into `parity`; rebuild the web app; run the whole suite; run E locally against the original clone; commit.
3. Manual end-to-end on Windows: pair a client with a server, stop the server mid-entry, save rows, restart the server, watch the outbox drain and the status bar move through the states; the admin page setup, keys, backup, password.
4. README: Remote mode section rewritten around pairing and the status bar; Deployment mentions the admin page; configuration table gains `-announce`; layout lists the new packages.
