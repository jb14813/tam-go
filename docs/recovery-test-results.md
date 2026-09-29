# Event recovery and shared winner lookup: test results

Validated on 2026-09-28 on branch `fix/event-recovery`, based on `all-systems`
at `ed4ef4a`, with upstream `dev` through `20c4cfc` recorded by merge `20557ac`.
These results cover commit `b10a925`, before the subsequent data-integrity audit.
They do not certify the newer changes; the [data-integrity audit report](integrity-test-results.md)
records their separate validation. The earlier
[test results](test-results.md) describe the September 27 build.

The frontend was built on Windows with Node.js 26.7 and pnpm 12.6. Listening
programs and tests ran in Linux containers on the same 24-thread Windows host.
Go tests used Go 1.27.1; browser tests used Playwright 1.63.0 and Chromium on
Ubuntu Noble. Compatibility used Python 3.12 with SQLite 3.45.1, the original
application at `7ff95fa228c23c14f5d86c131b079e83cbe19fde`, and real Go programs.

## Results

| Check | Result |
|---|---|
| Frontend production build | Passed |
| `gofmt` and `go vet ./...` | Passed |
| `go test -count=1 ./...` | All 14 test packages passed |
| `go test -race -count=1 ./...` | All 14 test packages passed |
| Vet and build for all six release targets | Passed |
| Original server with Go client; original and Go clients with Go server | All 24 compatibility checks passed |
| Real Chromium browser suite | All 17 tests passed, 36.4 seconds |
| HTTP load: 50 clients, 9,000 tickets, 1,000 baskets | All 26 checks passed, 66.3 seconds |
| HTTPS load: 20 clients, same data, 45-second server outage | All 26 checks passed, 84.9 seconds |
| Short load smoke test: 4 clients | All 26 checks passed |

The six compilation targets were Windows, Linux and macOS, each on amd64
and arm64. This does not establish native runtime behavior on every platform.
Native Windows/macOS/ARM runs, Nix/NixOS and package installation were not rerun
locally for this change; those remain in CI. No new GitHub CI result is claimed
before this branch is pushed.

## What the regression tests establish

- An empty authenticated server requests each client's own saved entries before
  its pending saves replay. Real database tests wipe and restart the server,
  including two clients sharing one access key and a client reconnecting later.
- Recovery survives interrupted uploads and server restarts. Live saves are not
  replaced by an older snapshot. Queued saves survive endpoint/key changes and
  unpairing; slow requests cannot finish against the previous connection after
  reconfiguration.
- Basket descriptions/donors and drawing results recover independently, in both
  arrival orders. Explicitly cleared winners and empty descriptions remain
  saved components. Backup JSON remains compatible with the original app.
- Reading other clients' tickets, baskets, reports or searches does not import
  those event rows into a client's database. Shared prefix definitions remain
  available for offline forms.
- Entering a positive winning ticket on client B immediately retrieves a buyer
  entered on client A. Missing tickets, blank saved tickets and local-only
  fallbacks have distinct feedback. A queued buyer appears after its owner
  reconnects, and unresolved lookups retry without retyping the winning number.
- Zero remains undrawn/cleared. A standalone client uses its own data without
  misleading remote-sync messages or repeated remote lookup attempts.
- Browser tests also verify marked-row saves on hiding, navigating away from
  and closing Tickets, Baskets, Drawing and Search pages. The hidden-page event
  is synthetic; navigation and tab closure use actual browser actions. See the
  [browser test instructions](../scripts/browser/README.md) for that boundary.
- Legacy prefix identities remain exact, including spaces and punctuation;
  validation of new prefix names does not disconnect existing rows.

The load harness now distinguishes a reachable server from a client that has
finished recovery and drained its queue. It waits for setup saves to arrive
before other clients read the shared prefixes. Queued writes during observed
recovery or behind an existing queue are expected; unexplained queuing still
fails the test.

## Load-test evidence

Selected output from the HTTP run:

```text
PASS  the server holds every ticket as last saved (after the rush): 9000 tickets: 0 missing, 0 different, 0 unexpected
PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
PASS  every client ends connected with nothing waiting or refused: 50 clients: 0 disconnected or recovering, 0 saves waiting, 0 refused by the server
PASS  every request was answered: 8448 requests, 0 failed
PASS  every crashed client came back with its queue: 2 crashed
PASS  no errors in what the programs wrote: 51 programs, 1649 lines, 0 errors
PASSED: all 26 checks
```

Selected output from the HTTPS run:

```text
PASS  the server holds every ticket as last saved (after the rush): 9000 tickets: 0 missing, 0 different, 0 unexpected
PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
PASS  every client ends connected with nothing waiting or refused: 20 clients: 0 disconnected or recovering, 0 saves waiting, 0 refused by the server
PASS  every request was answered: 7083 requests, 0 failed
PASS  every crashed client came back with its queue: 2 crashed
PASS  no errors in what the programs wrote: 21 programs, 1019 lines, 0 errors
PASSED: all 26 checks
```

These outage load runs restart the existing server database. Empty-server
repopulation and split basket/drawing recovery are separately exercised by the
backend integration tests; the load runs do not stand in for those tests.

## Reproduce

Run these commands on Linux, or inside Linux containers from Windows. Build the
frontend first so the binaries embed the current pages:

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

mkdir -p build/validation/bin
CGO_ENABLED=0 go build -o build/validation/bin/ ./cmd/tam-client ./cmd/tam-server
go build -o build/validation/loadtest ./scripts/loadtest
build/validation/loadtest -bin build/validation/bin -clients 50 -tickets 9000 -baskets 1000 -out build/validation/load-50.txt
build/validation/loadtest -bin build/validation/bin -clients 20 -tickets 9000 -baskets 1000 -tls -entry 60s -outage 45s -out build/validation/load-tls-20.txt

repo_root="$PWD"
cd scripts/browser
npm ci
npx playwright install --with-deps chromium
TAM_CLIENT_BIN="$repo_root/build/validation/bin/tam-client" TAM_SERVER_BIN="$repo_root/build/validation/bin/tam-server" npm test
```

The local checkout retains raw logs under ignored `build/validation/`:
`unit.txt`, `race.txt`, `vet.txt`, `cross-build.txt`, `compat.txt`, `browser.txt`,
`load-50.txt` and `load-tls-20.txt`. The browser binaries were byte-identical to
the final binaries used for the load runs. CI has a browser job and keeps its
output and failure traces alongside the existing test artifacts.

See [the recovery contract](recovery.md) for limits: an offline client cannot
contribute yet, and legacy conflicting edits without a shared revision cannot
be ranked automatically. Historical cached rows are preserved because their
original author cannot be reliably inferred.
