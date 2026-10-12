# R-TAM local Rust port

## Approved intent

Create Rust implementations of the TAM server and client that work with the
current Go programs and behave the same. Name the product R-TAM. The initial
scope was local implementation and validation, without publication or deployment.
The approved design comprised two native Rust binaries, reuse of the Svelte
interface, compatibility with the current Go checkout, and validation using
disposable data copies on October 11, 2026. Later network validation and the
review branch handoff are recorded in [the network report](../../R-TAM-NETWORK.md).

This is a retained implementation, with clean warning-free builds and meaningful
data-integrity validation. It is not a throwaway prototype. Avoid temporary
migration code for a one-time topology change.

## Compatibility reference

The reference is Go commit `b530cdad8da7be968e8f640a8de8badc1d113be7`.
The implementation branch is `r-tam`. The Go source and tests remain available
in this branch as the pinned interoperability reference.

The current Go implementation supersedes the older September 29 recovery
architecture. Its contract is an offline event copy, numbered saves, field-level
base-value checks, revision-aware event merges, and a durable outbox. Do not
restore the superseded native-backup/recovery-vector design.

Authoritative sources:

- `internal/server/server.go`: public server routes, auth, ordered-save digest.
- `internal/client/{client,api,pair}.go`: client routes, pairing and UI behavior.
- `internal/sync/sync.go`: heartbeat, replay, mirror pull and event changes.
- `internal/store/{models,edits,order,event,outbox,store}.go`: wire data,
  transactions, optimistic edits, revisions, merge and backups.
- `internal/db/{db,client,server}.go`: exact SQLite schemas and views.
- `internal/{config,remote,admin,guard,discovery,tlscert,desktop}`: settings,
  pinning, admin, password guard, discovery, TLS and desktop lifecycle.
- `scripts/browser` and `scripts/loadtest`: existing behavior probes.

## Architecture

Add a Rust Cargo workspace under `rust/`, with a shared `r-tam-core` crate and
the `r-tam-server` and `r-tam-client` binaries. Shared code owns typed models,
Go-compatible JSON encoding, validation, SQLite persistence, configuration,
transport, discovery and platform lifecycle. Each binary owns its HTTP routes
and startup behavior. Keep modules focused and avoid an abstraction layer that
merely wraps every library operation.

Use Tokio and Axum for HTTP, rusqlite with bundled SQLite for persistence, reqwest
with rustls for remote calls, rustls/rcgen for server TLS, serde for typed data,
bcrypt for Go-compatible password files, and cryptographic randomness and
SHA-256 for identifiers/digests. Choose and lock dependency versions only after
checking their official documentation and the available Rust toolchain.

Reuse the static SvelteKit frontend and server admin visual layout. The client
binary embeds its built assets and serves the same `/web` SPA fallback. Change
user-facing product names to R-TAM while retaining attribution and licenses.
Do not rewrite the UI, replace forms, or introduce new settings.

Product identifiers may say R-TAM; protocol identities remain exactly
`TAM Server` and `TAM Client`, since Go discovery, pairing and the frontend
compare them. Preserve `TAM-*` and `X-TAM-*` header names and Go JSON field names.

## Required functional parity

1. Server data routes cover prefixes, tickets, baskets, drawing, reports,
   search, key management, backup/restore and the `/api` root aliases.
2. Client routes cover the same data operations plus settings, status,
   discovery, pair/unpair, failed saves and retry/discard, selective push,
   local/remote backup/restore and shutdown.
3. Standalone client works without a server. Remote client retains its local
   event copy, permits offline edits, persists pending saves, and replays them
   in order after reconnect and process restart.
4. Preserve response shapes: server single-item GETs are arrays; client
   single-item GETs are objects or placeholders; client ranges contain
   placeholders, reverse endpoints if needed and reject ranges above the Go
   limit. Preserve ordering, default contact preference and report contents.
5. Preserve field-level base merge: only changed fields are considered; an edit
   is accepted while the current field still equals its starting value. Return
   current rows. Preserve browser retention of online refused edits and durable
   failed-outbox retention of replayed refusals. Basket description and winner
   revisions are independent.
6. Preserve auth, server setup/login, keys, password changes, status/presence,
   backups, restore confirmation, CSRF, cookie attributes and shared password
   attempt limiting. Read existing Go bcrypt hashes and write hashes Go accepts.
7. Preserve settings field names, defaults, partial update validation,
   normalization, serialized updates, hand-edit reload, last-good retention and
   primary/backup recovery. Read existing Go `settings.json` and `server.json`.
8. Preserve self-signed TLS, explicit PEM pairs and SHA-256 leaf-certificate
   pinning. A changed pinned certificate requires pairing again. Do not silently
   discard a pin or treat an HTML login page as a successful save.
9. Preserve `_tam._tcp.local.` announcement and discovery TXT fields, cached
   subnet discovery, duplicate collapse and interface filtering.
10. Preserve client connection states and timing, queued/failed counts,
    `X-TAM-Copy` offline report marking, server presence and event-switch safety.
11. Preserve browser opening after bind, Windows tray actions and console-close
    handling, command-line options, `TAM_DATA_DIR`, append logs and graceful
    shutdown. Keep the Go executables available for mixed-language validation.

## Data compatibility and safety

### Database and backup compatibility

Use the exact current Go table/column names, primary keys and report views. Open
copied Go server/client databases directly. Keep unknown pre-existing tables
intact. Implement permanent idempotent schema initialization and existing Go
column upgrades where needed; do not create a one-time conversion utility.

Backups use the current Go `event`, `prefixes`, `baskets`, `tickets` document.
All three lists are present; nonzero `rev` and `win_rev` are preserved. Match
ordinary restore versus `X-TAM-Merge: newer` behavior from the actual source,
including retention of absent rows where Go uses upserts. Validate the entire
document before changing anything. Preserve unknown historical prefix names
and normalize backup colors as Go does.

Use the same event identity, revision counter and prefix tombstones. For
`X-TAM-Merge: newer`, accept different events only where Go permits an empty
server to adopt the offered event; otherwise reject without changing rows.
Compare basket component revisions independently. A newer-only merge must not
revive a prefix from a backup older than its deletion. An explicit ordinary
restore retains Go's deliberate overwrite behavior: supplied prefixes can be
revived and the file's event can replace the populated server's event.

### Ordered writes

Preserve stable client names, monotonic positive save numbers, repeat/stale
responses and `X-TAM-Last-Save` recovery. Commit accepted row edits and
`client_saves` number/hash together. Resume copied Go outboxes and counters.

Compute the exact Go digest, including method, path, sorted encoded query,
typed field order, omitted zero revisions, normalization and Go JSON HTML
escaping. Byte-level golden vectors and copied Go database replay tests must
prove a Rust/Go handoff does not misclassify a legitimate repeated save.

Go numeric behavior matters: reject string numerals, fractions and values
outside browser-exact integer bounds; required IDs reject missing/null; optional
integer/revision null becomes zero; negative revisions and invalid IDs are
rejected. Unknown ordinary model fields are ignored as in Go. Match duplicate
row behavior and per-form field ownership.

### Stronger internal durability

Rust may improve internal persistence while preserving the external protocol:

- Persist each remote intent, its identity and number before network dispatch.
  Journal online intent before dispatch without treating its values as accepted
  local data. Commit local changes and queued intent together when accepting
  an offline save. A definitive server rejection leaves local data unchanged,
  removes the unaccepted intent and is not automatically replayed. Preserve
  later edits while applying accepted answers. Use Go-readable outbox rows;
  any required Rust-specific intent metadata uses permanent ancillary tables
  that Go can ignore without losing pending operations.
- A transport error, malformed success, auth failure or process death must not
  drop an unconfirmed save. Queue reconciliation must not replace a newer local
  edit with an older returned row.
- Reject explicit restore while pending saves remain, so an old queued edit
  cannot later undo the restored data. Use the existing queue controls to
  complete or quarantine those saves; document this intentional safety
  difference from Go's unrestricted restore.
- Export backups from one consistent read transaction. Configure SQLite WAL,
  busy timeout and full synchronous durability. Serialize writers and keep
  reads concurrent where safe.
- Settings use exclusive temporary creation, flush, atomic replacement and
  appropriate directory durability. Preserve a valid backup before replacing
  configuration. Event-preservation archives are created exclusively at their
  final path and never replace an existing archive; flush them before mutation.
- Preserve the old event copy in a flushed file before clearing/quarantining
  anything during pairing or an event change. Failure to preserve means the
  operation fails and the local copy remains.
- Do not allow two local instances to accidentally operate on the same data
  folder. Close/drain active requests and sync before SQLite shutdown.

If a stronger safeguard changes externally visible Go behavior, document it
and justify it against the user's data-safety requirement; do not silently
claim exact equivalence. Passing tests is evidence for the tested failure modes,
not a guarantee against every form of physical storage loss.

## Local validation and delivery

Keep test databases, settings, certificates and keys separate from event data.
Use disposable directories and the reference Go source at the pinned commit.
Start network test processes in Linux containers or the existing Linux WSL
distribution, reachable through Windows loopback, to avoid the user's previously
rejected Windows Firewall prompts. Docker Desktop may be started locally for
these tests. Build native Windows Rust executables as well. Validate Windows
tray and console-close behavior with a native lifecycle harness that does not
open network/discovery listeners; do not claim runtime parity from a build alone.

Required validation:

1. Formatting, warning-free `cargo check` and build, release build, complete
   meaningful Rust tests, and `cargo clippy --workspace --all-targets -- -D warnings`.
   Do not hide warnings with blanket lint allowances or exclude failing targets.
2. Bidirectional Go client/Rust server and Rust client/Go server tests, plus
   Rust client/Rust server. Exercise forms, reports, searches, auth, pairing,
   TLS, backup/restore and event-aware merge.
3. Open copied Go databases and queued saves in Rust, then reopen Rust-written
   copies in Go. Validate SQLite integrity and semantic contents after each
   direction, including save-number/digest replay. Digest vectors cover repeated
   query values in their original order, sorted query keys, URL escaping, HTML
   characters, Unicode separators, normalization, optional base fields, duplicate
   rows and nonzero revisions.
4. Fault tests: offline edits/reconnect; lost acknowledgment; restart before
   acknowledgment; delayed responses; duplicate/older save; concurrent edits;
   newer queued edits; event changes; corrupt/missing settings; invalid backup;
   backup-preservation failure; database contention; graceful shutdown;
   definitive server rejection after online intent journaling; ordinary restore
   reviving a deleted prefix versus newer-only merge refusing its older copy.
5. Reuse the real browser suite against Rust binaries and verify ticket,
   basket, drawing, settings, search and printed reports. Extend only for
   meaningful uncovered compatibility/safety cases.
6. Run the existing Go load scenario against Rust endpoints and mixed clients,
   with outage/restart and final row/queue/report checks. Compare behavior and
   record timing and resource observations without claiming universal speedups.
7. Fresh independent review of the Rust implementation and safety invariants;
   fix actionable findings and rerun affected checks.

Deliver local source, two runnable executables, concise build/run instructions
and a report containing exact tested versions, commands, results, compatibility
coverage and any outstanding limits. Keep the goal incomplete if any required
parity or validation item remains. No GitHub publication is authorized.
