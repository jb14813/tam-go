# Adversarial follow-up review

This September 29, 2026 review follows the published `c7b85a7` audit. It starts
with six independently reproduced failures and examines recovery, save queues,
backups, browser editing, reports, settings, authentication and process lifetime.
The earlier [integrity report](integrity-test-results.md) remains the record of
the previous commit and its test run.

## Six reported failures

| Failure | Correction and regression coverage |
|---|---|
| Read-only prefix caches create recovery conflicts | Shared menu cache is separate from authored rows/history. Tests cover update/delete in both recovery orders, offline edits, native backups, Push and legacy rows. |
| Automatic pairing backup loses ownership/history | Pairing snapshots the native format while holding the save locks, after retaining interrupted intents. Tests restore descriptions without clearing another client's winner, retain deleted prefixes/conflicts and wait for active saves. |
| Navigation discards valid edits when another row is invalid | Normal navigation waits for successful saves. Browser drafts retain unconfirmed input for explicit comparison and use. Tests reject saves on all four editors and exercise hidden/closed tabs, unavailable storage and separate tabs. |
| Offline reports silently show partial local data | Remote reports require a usable shared-server response and no local recovery, pending or refused saves. Tests cover outages, recovery, refusals, malformed responses, printable errors and standalone reports. Counts remove stale figures and continue interval retries. |
| Editing during Next Page skips a range | Rows and pager commit together only after a load is accepted. Real-browser tests delay next-page loads and edit the current rows on Tickets, Baskets and Drawing. |
| Missing settings primary overwrites its backup | Load the surviving valid backup with a visible warning. Tests preserve invalid/unreadable backup bytes, recover connection fields and keep newer in-memory settings. |

## Additional defects established during the review

- **Native conflict backups:** JSON formatting and equivalent character escapes
  changed raw conflict payload bytes and caused hash-validation failures. The
  browser now preserves the downloaded document, and the store normalizes typed
  conflict values to their original native encoding before validation and use.
  Tests exercise all record components, deletion, reviewed identities and every
  import boundary; real value changes still fail atomically.
- **Graceful shutdown:** both entry points returned when the listener stopped,
  before active HTTP handlers finished. They now wait through the existing
  bounded shutdown. Real-process tests hold a ticket request body open, stop
  the program, release it after 500 ms and require both acknowledgement and a
  persisted record. Separate cases withhold the body and verify bounded exit.
- **Discarded refused saves:** removing failed-queue evidence allowed a rejected
  local value to become an automatic recovery contribution. The review also
  established that an offline edit could overwrite its only full accepted
  predecessor. The correction retains per-component recovery eligibility and
  accepted predecessor values independently of the visible queue.
  Tests cover repeated server replacement, native backup restore, legacy failed
  journals, mixed basket ownership, overlapping acknowledgements and intent
  renumbering. An explicit legacy restore creates an independent new operation;
  retrying an existing operation must preserve its original identity.
- **Recovery versus pending replay:** integrated tests caught an older local
  prefix being offered before its lost-acknowledgement Push replay, creating a
  false conflict. Pending components now wait for ordered replay. If replay is
  refused, a durable recovery contribution offers the retained accepted value
  to the same replacement server. Tests interrupt this follow-up before upload
  and after a lost reply, restart the client and replace the server again.
  Generation tokens and remote identity scope retries; snapshot epochs prevent
  late acknowledgements from clearing newer recovery work.
- **Draft review identity and keyboard edits:** removing one draft row shifted
  another row's comparison to the wrong record, and keyboard-only changes could
  evade draft capture. Comparisons now use record identity and request guards;
  all four editors observe marked values reactively. Tests include row removal,
  overlapping review requests and an actual Chromium renderer crash.
- **Settings save acknowledgement:** a delayed reply and timed page reload
  discarded newer typing. Replies now update only unchanged submitted fields,
  Cancel uses the latest accepted settings and no delayed reload erases input.
  Pairing and saving cannot overlap, and a pairing refresh preserves unrelated
  settings typed while its response is delayed.
- **Stale printed winners:** Print used the page's previously loaded rows even
  after another client corrected the winner. Print now fetches a fresh report,
  displays failures on paper and adds a snapshot timestamp. Browser-menu
  printing still prints the displayed snapshot, which is explicitly dated.

## Verification

Final local verification passed on September 29, 2026:

| Check | Result |
|---|---|
| Embedded frontend production build | Passed |
| Full Go tests, uncached | All 15 test packages passed |
| Full Go race detector | All 15 test packages passed |
| Go formatting and vet | Passed |
| Windows, Linux and macOS, each amd64 and arm64 | All six targets compiled and passed vet |
| Original application compatibility | All 24 checks passed |
| Real Chromium regressions | All 56 tests passed |
| 50-client HTTP outage run, 9,000 tickets and 1,000 baskets | All 26 checks passed |
| 20-client HTTPS run with a 45-second outage, same dataset | All 26 checks passed |

Both load runs verified expected server and client records, cross-client
corrections, winner lookups, drained queues and clean shutdown. Separate HTTP
tests rebuild empty server databases twice with retained access keys and vary
client arrival order, refusal, missing acknowledgements and restart timing.
Independent reviewers checked the final browser and recovery changes.

Regression outputs, including the original failures, are retained locally under
ignored `build/validation/remaining-review/`, `build/adversarial-lifecycle/` and
`build/validation/six-fixes/`. The GitHub workflow additionally runs native
Windows, macOS and Linux ARM tests, Nix/NixOS and release/package checks; its
result belongs to the exact pushed commit and is recorded on the Actions run.

To reproduce from a checkout, build the embedded frontend before the Go
programs. The compatibility runner also needs the original application's
dependencies; see its script and the browser instructions for prerequisites.

```sh
(cd frontend && pnpm install --frozen-lockfile && pnpm build)
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
CGO_ENABLED=0 go build -o build/ ./cmd/tam-client ./cmd/tam-server
(cd scripts/browser && npm ci && npx playwright install --with-deps chromium && \
  TAM_CLIENT_BIN="$PWD/../../build/tam-client" TAM_SERVER_BIN="$PWD/../../build/tam-server" npm test)
bash scripts/compat/run.sh
go run ./scripts/loadtest -bin build -clients 50 -tickets 9000 -baskets 1000
go run ./scripts/loadtest -bin build -clients 20 -tickets 9000 -baskets 1000 -tls -entry 60s -outage 45s
```

## Scope and limits

Tests use real SQLite databases, HTTP handlers, executable programs and Chromium.
Fault injection controls dropped responses, TCP outages, invalid inputs and
request timing. Browser visibility events are synthesized where headless
Chromium does not reproduce operating-system tab suspension. Graceful-shutdown
process tests run on Unix; they do not simulate Windows console/tray events.

The review does not make a zero-defect claim. Offline clients can contribute only
after reconnecting; inaccessible or destroyed client storage still requires
backups. A report cannot certify that every offline workstation has uploaded.
Genuinely incomparable histories remain an explicit Data review decision.
Older versions did not always retain a full accepted value before overwriting
it with an offline edit. The new retention protects subsequent edits; it cannot
reconstruct an already missing payload from its hash.
The [recovery contract](recovery.md) describes those operational boundaries.
