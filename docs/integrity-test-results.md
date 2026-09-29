# Data-integrity audit and verification

This is the historical report for `c7b85a7`. The later
[adversarial follow-up](adversarial-test-results.md) covers additional failures
and corrections found after its successful CI run.

This report covers the follow-up to `b10a925` on `fix/event-recovery`, tested
September 28-29, 2026. Upstream `dev` through `20c4cfc` is included through
merge `20557ac`. The [previous report](recovery-test-results.md) describes the
earlier implementation, not this audit.

## Event workflows checked

- A ticket-entry client retains its own buyers. A different drawing client
  immediately looks up the shared buyer when a positive winner is entered,
  without importing that buyer into its local database. An open Drawing page
  refreshes missing and already-found buyers when another client uploads or
  corrects contact information.
- An empty replacement server with the existing access keys requests the
  clients' saved entries automatically. HTTP integration tests replace the
  server database twice, copying only authentication keys, and recover both
  client arrival orders. Accepted corrections survive, including saves whose
  acknowledgment was lost and saves made before the first heartbeat.
- Pending edits replay in order after recovery. They do not masquerade as
  previously accepted competing recovery copies. Explicitly retrying a failed
  older value retains that chosen value locally as well as sending it again.
- Basket descriptions/donors and winning numbers retain independent ownership.
  Native backup/restore and Push preserve that separation, explicit blank
  values, cleared winners, and prefix deletion history.
- Same-record corrections use recorded operation history. When the available
  evidence cannot establish which value supersedes another, the server keeps
  the alternatives and blocks event reads/reports until an administrator
  reviews them. Tests cover reused operation numbers, repeated values,
  incomplete ancestry, stale review pages and reviewed copies arriving late.
- Delayed saves, responses and row loads do not discard newer browser edits.
  Hidden-page saves carry their ordering information through the client.
  Interrupted online saves remain in a durable local journal.

These checks use real SQLite stores, HTTP handlers and executable processes;
the browser suite uses Chromium. Fault injection controls dropped replies,
TCP outages and request timing. The browser visibility test synthesizes the
hidden event; navigation and tab closure use actual browser actions.

## Results

| Check | Result |
|---|---|
| Frontend production build | Passed |
| `go test -count=1 ./...` | All 14 test packages passed |
| `go test -race -count=1 ./...` | All 14 test packages passed |
| Formatting, vet and six release target builds | Passed: Windows, Linux and macOS on amd64 and arm64 |
| Original/Go interoperability | All 24 compatibility checks passed |
| Real Chromium browser suite | All 30 tests passed, 1.8 minutes |
| HTTP: 50 clients, 9,000 tickets, 1,000 baskets | All 26 checks passed |
| HTTPS: 20 clients, same data, 45-second server outage | All 26 checks passed |

The final HTTP run reported 9,336 requests with zero failures, zero missing,
different or unexpected tickets/baskets, and no disconnected/recovering
clients, queued saves or refused saves at completion. Both crashed clients
recovered their queues, and all 12 clients whose Wi-Fi dropped sent their saves.

The HTTPS run reported 8,168 requests with zero failures and the same zero
missing/different/unexpected record checks. All 382 saves queued during its
45-second server outage were delivered after reconnection. All 20 clients
finished connected with recovery complete and nothing queued or refused.

## Issues found during validation

The audit reproduced stale recovery overwrites, replay of an old accepted save
after server loss, lost newer form edits, incomplete local retention of online
saves, and partial backup/Push operations touching an unowned basket component.
The implementation now retains operation history, journals saves before
delivery, keeps component ownership, and exposes unresolved differences for
explicit review. The regression tests exercise those failure paths.

The first final load run then exposed pending corrections being uploaded as
historical recovery copies. Recovery exports now omit the current operations
that belong to pending/failed requests; their normal queue remains responsible
for delivery. Repeated-server-loss regressions also check that correction
ordering survives when a server has only part of the earlier history.

A subsequent load startup found a harness port collision: a relay claimed a
client's released port reservation, and the startup probe accepted the relay's
server response as client readiness. Relay allocation and client-specific
readiness probes were corrected. Old retry test fixtures also used bodies and
routes that no real editor produces; those fixtures now exercise actual save
requests while retaining the ordering/refusal assertions.

## Reproduce

Use Go 1.27.1, Node.js with pnpm 12, and Linux or Linux containers. The
compatibility runner additionally needs Python with a recent SQLite and uses
the original application pinned at `7ff95fa228c23c14f5d86c131b079e83cbe19fde`.
Build the frontend before building the programs so they embed current pages.

```sh
(cd frontend && pnpm install --frozen-lockfile && pnpm build)
test -z "$(gofmt -l cmd internal scripts)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
for target in windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 go vet ./...
  GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 go build ./...
done
bash scripts/compat/run.sh
mkdir -p build/validation/integrity/bin
CGO_ENABLED=0 go build -o build/validation/integrity/bin/ ./cmd/tam-client ./cmd/tam-server
go build -o build/validation/integrity/loadtest ./scripts/loadtest
build/validation/integrity/loadtest -bin build/validation/integrity/bin -clients 50 -tickets 9000 -baskets 1000
build/validation/integrity/loadtest -bin build/validation/integrity/bin -clients 20 -tickets 9000 -baskets 1000 -tls -entry 60s -outage 45s
repo_root="$PWD"
cd scripts/browser
npm ci
npx playwright install --with-deps chromium
TAM_CLIENT_BIN="$repo_root/build/validation/integrity/bin/tam-client" TAM_SERVER_BIN="$repo_root/build/validation/integrity/bin/tam-server" npm test
```

Local raw logs are under ignored `build/validation/integrity/`; failed earlier
attempts are retained in `attempt-1/` and `attempt-2/`. The browser test
[instructions](../scripts/browser/README.md) explain the lifecycle-test limits.

## Boundaries

The load tests interrupt and restart an existing server database. Empty-server
reconstruction and repeated replacement are separately covered by the HTTP
integration tests; passing load tests alone would not establish that behavior.

Cross-compilation does not establish native runtime behavior. After this local
audit, [CI run 36542173244](https://github.com/jb14813/tam-go/actions/runs/36542173244)
passed all nine jobs for `c7b85a73598f7b7895ec3fc2b52d243537e3d81a`, including
native Windows, macOS and ARM execution, Nix/NixOS and package installation.
Those results do not cover the later follow-up changes.

Clients cannot contribute while offline, and do not retain other clients'
records merely by reading them. A reachable server cannot certify that an
offline workstation has finished uploading. Backups and paper records remain
necessary; tests cannot guarantee zero faults or recovery from destroyed
client storage. See the [recovery contract](recovery.md) for operational details.
