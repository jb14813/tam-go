# R-TAM local validation

This records the initial approved local delivery. The current optimized
delivery and refreshed checks are in [R-TAM-SPEED.md](R-TAM-SPEED.md).

Snapshot: 2026-10-11. Source, strict builds, browser scenarios, runtime probes
and Go/Rust/mixed load checks pass. **Independent local-delivery review is approved.**
Results describe specific local artifacts and bounded failure tests, rather
than universal Go parity or production/hardware durability.

## Reference and tested artifacts

The Go contract and actual baseline programs are pinned to
`b530cdad8da7be968e8f640a8de8badc1d113be7`. Rust runtime results below use the
local `rust/` Git tree `90948ce7f1c0414ad01344276d123b7c9dae2a97` and delivered
original programs preserved under `build/r-tam-validation/speed-baseline-artifacts/`.
This historical source tree and its raw receipts are retained locally, rather
than included as development commits in the public branch. Current source
identity and rebuilt artifacts are in [R-TAM-NETWORK.md](R-TAM-NETWORK.md).
Original logs identify their then-current `build/r-tam/` paths. The later report commit is documentation
only; the client embeds the retained Svelte interface and the server embeds
its administration assets.

Both platforms used Rust/Cargo 1.98.0. The Windows frontend used Node 26.7.0
and pnpm 11.25.0; Linux used Node 24.21.0 and pnpm 12.11.2. The pinned Go
programs were built with Go 1.27.2. Browser checks used Playwright 1.63.0 and
Chromium headless shell 153.0.8010.12, revision 1243. Runtime probes used Python
3.12.3 on Ubuntu 24.04/WSL2, Linux amd64. Exact version output is retained in
`build/r-tam-validation/final-toolchain-versions.txt`; browser versions also
appear in the frozen suite's `playwright-core/browsers.json`.

| Program | Tested SHA-256 |
| --- | --- |
| Linux Rust client | `de00924748faa14dd5a5e09a870b2d1cbd6fde6ed3bc920590681c2c51786102` |
| Linux Rust server | `2a994f92abbffcdedc897d8080cab368e65663a2d141aa0c671c2a3cc84d61ca` |
| Windows Rust client | `f57a1b413cb44609b06d3257daf4beb8e65a738d48142ba8583fb307b4e09edb` |
| Windows Rust server | `38cfda2f92da5769b0e9e43cfb9a661bed2c4622e71d22264cf717e103e3a6d7` |

Exact commands, probe/binary hashes, all 87 tracked Rust source-file hashes and
cleanup are retained in `final-runtime-a2e68a4-delivery/metadata.json`.
The collector and independent results verifier both passed: all ten actual
invocations exited zero, source/probe/binary identities stayed unchanged, and
no owned processes remained. Earlier runtime snapshots remain historical
evidence against their recorded clients.

## Observed results

| Check | This snapshot | Local evidence under `build/r-tam-validation/` |
| --- | --- | --- |
| Native Windows Cargo all-target tests | PASS, 116 registered, zero failed/ignored | `final-native-tests.log` |
| Linux/WSL Cargo all-target tests | PASS, 153 registered, zero failed/ignored | `final-linux-tests.log` |
| Cargo all-target check, both platforms | PASS, `RUSTFLAGS=-Dwarnings` | `final-{native,linux}-check.log` |
| Format and Clippy, both platforms | PASS; Clippy uses `-D warnings` | `final-{native,linux}-{fmt,clippy}.log` |
| Frozen Windows frontend/release build | PASS, clean | `native-rust-build-script.log` |
| Final release builds, both platforms | PASS, `RUSTFLAGS=-Dwarnings` | `final-{native,linux}-release.log` |
| Unchanged frozen frontend on Linux ext4 | PASS, 1.80 s, no warnings | `final-linux-frontend-ext4.log` |
| Actual pinned Go load with repaired harness | PASS, 26/26 | `go-load-b85vFr/load-report.txt` |
| Original pinned Go browser suite | PASS, 18/18 | `go-runtime-baseline/browser-tests.log` |
| Added print-report Go baseline | PASS, 2/2 | `go-print-baseline-Ku72qM/print-tests.log` |
| Corrected stale-form/report Go scenarios | PASS, 2/2, no retries | `go-browser-PYH54F/browser-tests.log` |
| Final Rust/mixed runtime probes | PASS, 107/107 plus lifecycle | `final-runtime-a2e68a4-delivery/summary-verification.log` |
| Final actual Rust load | PASS, 26/26 | `rust-load-FbBfEJ/load-report.txt` |
| Final mixed-client load | PASS, 26/26, six Go and six Rust clients | `mixed-load-W9Noew/load-report.txt` |
| Final Rust browser suite, including print | PASS, 20/20, no retries, 2.7 min | `rust-browser-baWzbs/browser-tests.log` |
| Independent whole-branch review | APPROVED for the initial local delivery | `reviews/final-branch-review.md` |
| Refreshed delivered Windows/Linux binaries | PASS | Exact hashes above |

The original manifest is preserved under `speed-baseline-artifacts/`.
`build/r-tam/manifest.json` now identifies the optimized delivery described in
the current speed and safety report.

The final browser run uses the existing scenarios with the synchronized stale-
form precondition, plus two print checks. Its seven JavaScript source hashes
match the corrected Go scenario run, and its exact executable hashes match
the delivered Linux programs. The two retained print screenshots were visually
checked: both report tables, R-TAM titles and the offline-copy notice render
without clipping. Tests assert refreshed online winners before checking the
offline print view.

Counts exclude repeated filtered child-test invocations. No failing test was
disabled; platform differences come from conditional network/lifecycle tests.

The earlier Linux frontend build on `/mnt/c` emitted a `PLUGIN_TIMINGS`
performance advisory. Repeating the unchanged source and frozen dependencies
on Linux ext4 completed in 1.80 seconds without that advisory. This resolves
the observed filesystem build bottleneck; it is not a Rust lint suppression.

The pre-fix Rust load run exposed a race between direct-save journaling and
background replay before the direct request acquired the send boundary.
Symptoms included `this intent was already accepted locally`, incorrect saved
values and queues while the server was reachable. The final artifacts passed
26/26 after causal POST/DELETE regressions and independent repair reviews.
The failed run remains recorded as failed. A second external-SQLite-lock
regression reproduced canceled journal dispatch order `[2,1]`; retaining owned
serialization guards in the blocking worker corrected it to `[1,2]` while
preserving later edits.

The intermediate corrected run `rust-load-yklOLs` passed 25/26, with all rows
correct and zero request failures; the admin page retained stale pending counts.
An exact live-intent marker now distinguishes active direct work from waiting
work, retaining canceled/uncertain/older intent and raw restore/push guards.
Real root/admin/status held-ack regressions cover both save methods.

Another attempted run `rust-load-uhXsE7` exposed a harness startup collision:
reserved client ports were released before relay allocation, and readiness
could accept a relay's server response. The numeric colliding port/owner was
not retained. Permanent held reservations, role-specific readiness, a
post-response child-exit check and safe partial-startup cleanup have focused
regressions and independent review. All fresh cohorts use that same harness;
its 26 workload assertions are unchanged. The original pinned baseline remains.

Whole-branch review also caught settings normalization/validation, port-only
client binding and successful CLI help. All were reproduced and repaired;
invalid settings preserve both primary and recovery bytes, native help avoids
data/log/lock/tray/network initialization, and Linux proves actual `:0` binding.

The first complete Rust browser run (`rust-browser-eEE8BU`) passed 18/20. Its
winner test had not awaited the form read: B's update completed before A's
response, which actually loaded winner 2 and explicitly submitted base 2 before
changing it to 1. That valid edit did not demonstrate a stale overwrite. The
retained scenario now awaits the exact drawing response and rendered winner 0
before B writes 2, preserving every original conflict/server/UI assertion. The
same corrected scenarios passed against pinned Go. Original frozen sources,
failed traces and results remain intact; there are no sleeps or retry changes.

The other failure was a real report-origin bug: an unrelated prefix mirror
update invalidated a successful report download, creating an incorrect local-
copy notice while connected. Reports do not mirror rows, so they now guard event
identity instead of unrelated row-generation changes, retaining configuration
and raw pending-intent guards. Ordinary mirrored reads retain their generation
guard. Four real-router regressions cover all three report types, genuine stale
winners, queued/offline data and held previous-event replies; independent review
approved the repair and synchronized browser premise.

The same workload uses 12 clients, 3,000 tickets, 500 baskets, a server outage,
two client crashes, three dropped links, deleted-key repair, concurrent edits,
drawing/report checks and a five-second rush. Mixed uses the Rust server with
alternating Go and Rust clients.

| Measurement | Go server + 12 Go clients | Rust server + 12 Rust clients | Rust server + 6 Go/6 Rust clients |
| --- | ---: | ---: | ---: |
| Checks | 26/26 | 26/26 | 26/26 |
| Requests / failed | 3,307 / 0 | 2,312 / 0 | 2,692 / 0 |
| Queued saves / while reachable | 119 / 0 | 108 / 0 | 114 / 0 |
| Rush changed rows/s | 4,207 | 1,826 | 2,705 |
| Rush sheet latency median / p95 | 71 / 80 ms | 160 / 198 ms | 107 / 143 ms |
| Server CPU, across two runs | 7.3 s | 15.6 s | 12.7 s |
| Server peak memory | 37 MB | 27 MB | 26 MB |
| Combined client CPU | 17.9 s | 27.8 s | 22.3 s |
| Largest individual client peak memory | 23 MB | 17 MB | 22 MB |
| Total elapsed | 44.0 s | 52.8 s | 50.1 s |

Every run retained all 3,500 correct rows and client copies, had no request
failures, and ended with connected clients and zero waiting/refused saves.
Rust was slower and used less peak memory here. Rush saves change every sheet
row, rather than repeat no-op writes; fixed-duration phases naturally produce
different request counts. Measurements are single local runs on Linux amd64/WSL,
24 CPUs, with disposable data on `/mnt/c`. They are not universal performance
or aggregate-memory estimates; no profiling established the cause.

## Runtime coverage and safety boundaries

The 107 checks comprise HTTP/TLS pair matrices (48), all four Go/Rust server
and client handoff directions (12), event/preservation checks (25), durable
intent fault checks against Rust and Go servers (10), and both discovery TLS
assignments (12). Server lifecycle records named outcomes separately.

Coverage includes typed Go JSON/digests, permanent Go-readable SQLite layouts,
form ownership and stale edits, reports/search, invalid restore rollback,
copied actual Go databases/settings/outbox, offline queue/crash/replay, and
SQLite integrity. Event probes check archive-before-clear, quarantine of old
event saves, unchanged data/configuration after real archive permission denial,
same-event repair, and Rust's deliberate refusal of restore/selective push with
pending intent. TLS rotation retains the pin and queue until explicit re-pair.

The fault relay forwards to actual servers, then holds or alters their replies.
It checks durable numbered intent before acknowledgment, kill/restart replay
without a new server revision, and retention after HTML/wrong-identity replies.
Lifecycle checks cover real port-zero URLs, duplicate-folder lock rejection,
Go/Rust bcrypt compatibility, password persistence, append logs, retained TLS
files/cookie flags and graceful SIGTERM. Focused passes supplement the actual
Go/Rust/mixed load and browser gates.

Permission-sensitive probes ran as an unprivileged user on Linux ext4. All
event directories were disposable and separate from existing user data; probe
process cleanup was verified. No temporary topology migration utility was
introduced. Go filenames, protocol identities/headers, the MIT license and its
original copyright notices remain. This report records the initial local
validation scope; later network validation is documented separately.

## Reproduce retained checks

From the repository root, ordinary Cargo gates are:

```sh
cargo test --manifest-path rust/Cargo.toml --workspace --all-targets --locked
cargo fmt --manifest-path rust/Cargo.toml --all --check
cargo clippy --manifest-path rust/Cargo.toml --workspace --all-targets --locked -- -D warnings
```

Release-default build scripts are `rust/build.ps1` on Windows and
`bash rust/build.sh` on Linux. They use `pnpm install --frozen-lockfile` and
embed the generated frontend before copying the two programs into
`build/r-tam/{windows,linux}/`.

Run actual network and permission-sensitive probes only in Linux/WSL. Substitute
real absolute binary paths below. Use a fresh persistent Linux output root,
never `/tmp`; `server_lifecycle.py` requires its output directory not to exist.
The compatibility probes additionally need Go and Python 3 available on PATH.

```bash
GO_SERVER=/absolute/path/to/pinned/tam-server
GO_CLIENT=/absolute/path/to/pinned/tam-client
RUST_SERVER=/absolute/path/to/r-tam-server
RUST_CLIENT=/absolute/path/to/r-tam-client
OUT="$HOME/.cache/r-tam-validation/new-run"
COMMON=(--go-server "$GO_SERVER" --go-client "$GO_CLIENT" --rust-server "$RUST_SERVER" --rust-client "$RUST_CLIENT")
PAIRS=(--pair rust-rust --pair rust-server-go-client --pair go-server-rust-client)
PROBES=rust/tests/compatibility

python3 "$PROBES/run.py" "${COMMON[@]}" "${PAIRS[@]}" --output "$OUT/http"
python3 "$PROBES/run.py" "${COMMON[@]}" "${PAIRS[@]}" --tls --output "$OUT/tls"
python3 "$PROBES/handoff.py" "${COMMON[@]}" --output "$OUT/handoff"
python3 "$PROBES/events.py" --server "$RUST_SERVER" --client "$RUST_CLIENT" --rust-safety --output "$OUT/events-http"
python3 "$PROBES/events.py" --server "$RUST_SERVER" --client "$RUST_CLIENT" --rust-safety --tls --output "$OUT/events-tls"
python3 "$PROBES/faults.py" --server "$RUST_SERVER" --rust-client "$RUST_CLIENT" --output "$OUT/faults-rust"
python3 "$PROBES/faults.py" --server "$GO_SERVER" --rust-client "$RUST_CLIENT" --output "$OUT/faults-go"
python3 "$PROBES/server_lifecycle.py" --server "$RUST_SERVER" --output "$OUT/lifecycle"
python3 "$PROBES/discovery.py" "${COMMON[@]}" --output "$OUT/discovery"
python3 "$PROBES/discovery.py" "${COMMON[@]}" --tls --output "$OUT/discovery-inverse"
```

The frozen load harness uses a directory containing executable names
`tam-server` and `tam-client`; retained launchers use symlink aliases and fresh
data folders. Its workload is `-clients 12 -tickets 3000 -baskets 500 -entry 20s
-outage 5s -rush 5s -keep -out <report>`. The local Rust/mixed launchers are
`build/r-tam-validation/run-rust-load.sh rust` and the same script with `mixed`.
The browser suite accepts `TAM_SERVER_BIN`/`TAM_CLIENT_BIN`; the retained
`run-rust-browser.sh` runs the pinned 18 scenarios plus the two print scenarios.
Exact final runtime invocations and artifact hashes are retained with the
results. The ignored local helpers/toolchain directories are validation aids,
not a product runtime dependency or topology migration utility.

## Platform and durability limits

Actual product/network peers ran inside WSL; no native Windows TCP/mDNS
listeners or firewall changes were used. Native Windows Cargo tests cover
in-process APIs and platform lifecycle behavior, while Linux selects additional
network tests; zero ignored tests does not imply Windows network validation.

Discovery uses real `_tam._tcp.local.` announcements at nonstandard random
ports, negative controls and exact metadata/reachable server identities. A
process-local Rust hostname avoids collision with Go's kernel-hostname instance.
WSL can advertise its loopback-interface address as well as eth0; both were
reachable within WSL. This does not establish remote LAN/native Windows
multicast or address selection, nor behavior for colliding service names.

One recorded minor timing difference remains: reqwest applies its ten-second
timeout to body reads as well as headers, while Go's ten-second limit is for
response headers. Both retain five-second connect and thirty-second whole
request limits; a stalled body can fail earlier in Rust.

No macOS/ARM, physical power-loss, long soak, deployment or native Windows
network parity claim is made. Process-kill and injected filesystem/SQLite
failures are bounded tests of specific recovery paths. Complete-folder handoff
requires stopping the source; Rust's instance lock is not represented as a lock
understood by a simultaneously running Go process. Python/legacy backup
conversion is excluded.

## Implementation rulings

The complete decision log is retained with the review evidence. All rulings,
in their original order, are listed here with their practical cost if wrong:

1. Allow platform/server work in parallel after interfaces stabilize; a wrong
   interface assumption requires integration repairs and repeated checks.
2. Allow platform work alongside Store after typed-core acceptance; queue
   consumers still wait for both accepted components before integration.
3. Start server routes against declared Store interfaces; acceptance waits for
   Store/transport reviews, so wrong assumptions cost rework rather than release.
4. Scope concurrent task reviews to assigned/shared files and retain an
   unfiltered final review; cross-task gaps require the whole-branch gate.
5. Use WSL after Docker startup failure; native Windows/LAN behavior still
   requires separate validation before relying on it.
6. Scaffold the empty Cargo/test crate before behavioral RED; no functionality
   is accepted without meaningful failing and passing checks.
7. Digest normalized Go JSON bytes; typed validation/canonical encoding and
   actual Go vectors guard callers, whose misuse would break numbered replay.
8. Hold parity acceptance until save-ownership repairs are reviewed and actual
   workloads pass; the cost is delayed acceptance, not a hidden failed gate.
9. Repair harness reservations/readiness for every cohort while preserving the
   frozen baseline; harness changes could affect comparisons, so all 26 workload
   assertions remain unchanged and fresh Go results are recorded.
10. Accept the final review's six limits: hardware persistence, native Windows/
    LAN, untested platforms, legacy backups, simultaneous original-folder sharing,
    and deployment/publication need additional scope or validation.
11. Synchronize the winner test's actual prior GET/form before the other client
    writes, preserving every outcome assertion and frozen evidence; incorrect
    synchronization could hide an ordering issue, so both actual Go/Rust runs
    and a deterministic real-router stale regression establish the premise.
