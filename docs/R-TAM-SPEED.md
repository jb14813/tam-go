# R-TAM speed and safety validation

Local snapshots: optimized delivery on 2026-10-11, followed by the Linux-storage
comparison on 2026-10-12. The delivery's final build, load, runtime and browser
gates passed; independent local-delivery review is approved. This report covers
the performance work following the [initial approved delivery](R-TAM-VALIDATION.md).
The later rebuilt programs, hardware/network checks and GitHub preparation are
documented in [the current network handoff report](R-TAM-NETWORK.md).
No schema migration or protocol change occurred during these measurements.

## Source and artifacts

Measured local `rust/` Git tree: `717d34198f578fdd11b9db5f61b19cda3008ef5b`.
This historical tree and its raw receipts are retained locally; they are not
development commits on the public branch. The original approved
programs, manifest and report are preserved under
`build/r-tam-validation/speed-baseline-artifacts/`. These exact optimized programs
were the optimized local delivery under `build/r-tam/`; `build/r-tam-speed/`
retains those identical measured files. The current `build/r-tam/` manifest
identifies the newer rebuilt programs described in the network handoff report.
The pinned Go reference remains `b530cdad8da7be968e8f640a8de8badc1d113be7`.

| Program | SHA-256 |
| --- | --- |
| Linux client | `8eee83f67c2741bd9690b19de9239519d177886654e77348be42893194e30f15` |
| Linux server | `634950a2f2e34bb868bb9626d884f57414dcdd4c029938ba0baaabcda0457d87` |
| Windows client | `aa3b558def447d8dd3e7b937af0998a8e510bee70fddc08270751206adca1cea` |
| Windows server | `7c059a93e15a203a5391f37aa2048b8ba9fef6ca0835e2482009a2bfab8c71fd` |

The tested tools remain Rust/Cargo 1.98.0, Go 1.27.2, Linux Node 24.21.0/pnpm
12.11.2, Windows Node 26.7.0/pnpm 11.25.0, Python 3.12.3 and Playwright
1.63.0/Chromium 153.0.8010.12. Exact output is retained in the initial
`build/r-tam-validation/final-toolchain-versions.txt` receipt. Frontend source,
embedded assets, dependency locks, settings and public interfaces are unchanged.

## What changed

The save batch resolves event metadata lazily once and reuses revision
statements. Every changed row still reads the database's authoritative integer
counter, checks the exact native JSON limit, and keeps the original order and
transaction boundary. Ticket lookup/upsert statements reuse their preparation
on the existing connection; their SQL, parameters and typed decoding are unchanged.

The store also retains up to four idle read connections. Each operation owns
its checked-out connection; a short mutex covers checkout/return, never query
execution. Additional concurrent readers remain allowed. A connection returns
only when autocommit is active and no statement is busy, so active snapshots or
uncertain transactions are discarded. Reads execute SQLite each time; cached
connections do not cache rows, key validity, event identity or pending counts.
The writer, five-second busy timeout, WAL/FULL durability, generation triggers,
durable intent journaling, replay ownership and shutdown drain are unchanged.

SQLite documents automatic statement recompilation after schema changes.
The new behavioral check warms the ticket statements, adds a rejecting trigger
from another connection, verifies complete ordered-write rollback, then removes
the trigger and successfully retries the same number. The exact installed
rusqlite 0.38 implementation was also inspected. [SQLite prepare contract](https://www.sqlite.org/c3ref/prepare.html).

## Measurement and unsuccessful trials

The first two SQL-only trials passed the full workload's 26 correctness checks
but did not improve application throughput: 1,803 and 1,791 changed rows/sec,
against a fresh original run's 1,850. Neither was promoted on that evidence.
Their isolated store gains are kept separate from application measurements.

The standalone store benchmark changes all 25 tickets in each of 5,000 real
ordered WAL/FULL transactions. Three-run mean time fell from 6.068 seconds to
5.399 with the batch stamper, then 3.262 with ticket-statement reuse. This timing
includes returned revision/order checks; it excludes networking and client work.

A separate benchmark executes 1,500 real key-existence/count pairs, 3,000 reads,
against 3,000 tickets and 500 baskets. Three-run mean elapsed time on `/mnt/c`
fell from 18.073 to 0.624 seconds with reader reuse; ext4 fell from 0.714 to
0.0139 seconds. All runs checked unchanged counts, immediate key revocation and
SQLite integrity. This measures whole reads, not connection opening alone.

The actual application workload is unchanged: 12 clients, 3,000 tickets, 500
baskets, five prefixes, a server outage/restart, two client crashes, three
dropped links, deleted-key repair, concurrent corrections, drawing/reports,
queue drain and a five-second rush in which every sheet save changes every row.
Timing cohorts run sequentially without compilation/browser/runtime tests.

| Actual workload | Original Rust, run 1 / 2 | Optimized Rust, run 1 / 2 | Optimized server, 6 Go / 6 Rust clients |
| --- | ---: | ---: | ---: |
| Checks | 26/26 each | 26/26 each | 26/26 |
| Requests / failed | 2,334 / 0; 2,326 / 0 | 3,240 / 0; 3,239 / 0 | 3,403 / 0 |
| Offline-only queued saves | 112 / 107 | 110 / 109 | 112 |
| Rush changed rows/sec | 1,850 / 1,862 | 4,111 / 4,123 | 4,476 |
| Sheet latency median/p95 | 159/185; 158/186 ms | 70/95; 70/94 ms | 65/87 ms |
| Server CPU across two runs | 15.5 / 15.2 s | 5.2 / 5.1 s | 4.5 s |
| Combined client CPU | 27.7 / 26.5 s | 12.5 / 12.4 s | 13.0 s |
| Server peak memory | 26 / 26 MB | 26 / 25 MB | 25 MB |
| Largest client peak memory | 18 / 18 MB | 18 / 18 MB | 22 MB |
| Total elapsed | 52.3 / 52.1 s | 43.0 / 43.1 s | 43.4 s |

The mean rush rate increased from 1,856 to 4,117 changed rows/sec, about 2.22x.
Median sheet latency fell from about 159 to 70 ms. Every run verified the
3,500 server rows and each client's own saved cache rows, had zero failed
requests or saves queued while reachable, and ended with zero waiting/refused
saves. Evidence folders under
`build/r-tam-validation/` are `speed-baseline-load-Fq6C9G`,
`speed-baseline-load-aAyVJm`, `speed-candidate-load-KCRlUK`,
`speed-candidate-load-oq6qRb` and `speed-mixed-load-1jo77X`. Baseline metadata
capture initially used Linux Git,
which cannot resolve the Windows worktree path; its separate source receipt and
matching immutable executable hashes establish the tested original source.
Later runs use Windows Git for that metadata.

The earlier matched pinned-Go run recorded 4,207 changed rows/sec and 71/80 ms
median/p95. Optimized Rust is close to that recorded throughput and median,
with a higher p95. That Go figure was recorded earlier, not rerun as part of
this optimization, so it is a reference rather than a universal parity claim.

Results are bounded local Linux amd64/WSL measurements on the same 24-CPU
machine, with application data on `/mnt/c`. They are not native Windows or
universal throughput claims. Repeated-read measurements and the application
change support reader setup as a major cost here, without attributing every
part of the Go/Rust difference to it.

## Linux-storage comparison, 2026-10-12

The full application workload was repeated with all program executables,
temporary databases, logs and timed reports on Linux ext4. The four existing
Go/Rust release programs were copied byte-for-byte, not rebuilt or linked back
to `/mnt/c`. The unchanged load harness was built once before timing. Six fresh
cohorts ran sequentially in Go, Rust, Rust, Go, Go, Rust order, with no concurrent
compilation or other validation workloads. Each used the same 12 clients,
3,000 tickets, 500 baskets, 20-second entry, five-second outage and five-second
rush, and unchanged defaults for seed, prefixes, sheet size, crashes, simulated
link drops and simultaneous corrections. These are local HTTP measurements.

| Full workload on ext4 | Go, three runs | Optimized R-TAM, three runs |
| --- | ---: | ---: |
| Rush changed rows/sec, mean | 12,787 | 23,002 |
| Rush changed rows/sec, range | 12,445-13,341 | 22,317-23,791 |
| Median sheet-save latency, range of run medians | 22-24 ms | 12-13 ms |
| Sheet-save p95, range of run p95 values | 26-28 ms | 16-18 ms |
| Server peak memory, range | 37-38 MB | 29-32 MB |
| Largest client peak memory, range | 24-25 MB | 20 MB |
| Correctness checks | 26/26 in every run | 26/26 in every run |

R-TAM delivered about 1.80x Go's mean rush throughput here, with lower observed
median and p95 latency. Compared with the earlier `/mnt/c` measurements, R-TAM
throughput was about 5.59x higher and Go about 3.04x higher. Those storage ratios
compare separate measurement sessions; the ext4 Go/Rust comparison is the fresh
interleaved batch. This does not imply a 45x application gain from the isolated
read benchmark, or establish parity on native Windows, physical LANs or other
machines. The fixed outage/entry timings also bound total run duration, which
was approximately 40-41 seconds for both implementations.

Every run exited zero with exactly 26 passing checks, no skipped checks, zero
failed requests, correct server/client row copies, zero final waiting/refused
saves and clean client shutdowns. After all programs stopped, all 78 databases
(13 per cohort) passed SQLite integrity and foreign-key checks. Original and
copied binary hashes, the once-built harness, tracked Rust/harness sources and
both repository states were identical before/after timing. No benchmark-owned
process remained before any next cohort or after the batch. Product code,
durability settings and original user data were unchanged.

Exact per-run results, aggregate JSON, metadata, hashes and database/process
proof are retained in `build/r-tam-validation/speed-ext4-UPxVKk/`. Complete
disposable data and child logs were retained in a dedicated directory under
`$HOME/.cache/r-tam-validation/` on Ubuntu 24.04.
The retained `speed-ext4.sh` protocol and `speed-ext4-collect.ps1` collector
reproduce and verify this comparison without moving any existing data.

## Correctness gates and data-safety scope

| Gate | Result | Evidence under `build/r-tam-validation/` |
| --- | --- | --- |
| Native Windows all-target tests | PASS, 128 registered, zero failed/ignored | `speed-native-tests.log` |
| Linux/WSL all-target tests | PASS, 165 registered, zero failed/ignored | `speed-linux-tests.log` |
| Format, Cargo check, Clippy and release | PASS on both platforms, warnings denied | `speed-{native,linux}-{fmt,check,clippy,release}.log` |
| Independent source review | APPROVED, no actionable findings | `speed-review.md` |
| Actual Rust load | PASS, 26/26 in both repeats | `speed-candidate-load-{KCRlUK,oq6qRb}/load-report.txt` |
| Actual mixed-client load | PASS, 26/26 | `speed-mixed-load-1jo77X/load-report.txt` |
| HTTP/TLS, handoff, event/fault/discovery and lifecycle | PASS, 107/107 plus lifecycle | `speed-runtime-core-batch/summary-verification.log` |
| Actual browser and print scenarios | PASS, 20/20, no retries, 2.4 min | `speed-browser-Aa7oK4/browser-tests.log` |
| Final independent delivery review | APPROVED for the measured local delivery | `speed-review.md` |

Seven focused stamping/statement cases and five reader cases cover duplicate
row order, no-op/client revisions, authoritative counters after row triggers,
invalid counter/exact-limit rollback, prefix tombstones, schema invalidation,
fresh writer/external commits, immediate key revocation, snapshot/error cleanup,
WAL checkpoint release and parallel readers during writer contention. Five
stamping cases predate optimization one; all seven passed before optimization
two, and all five reader cases passed before reader reuse. No outcome assertion
or workload was weakened to get a passing result.

The runtime collector and independent recount each exited zero. All ten actual
probe commands passed; exact binaries, all 89 tracked Rust files and probe
sources stayed unchanged, and no owned processes remained. The browser suite's
seven JavaScript hashes match the previous approved suite and corrected Go
scenarios. No retry/sleep/assertion adjustment was made for the optimization.

The workup is complete for the approved local scope. Its tested
safety paths establish application behavior under specific outages, crashes,
malformed acknowledgments, permission failures and SQLite errors. They cannot
guarantee zero defects or survival of arbitrary hardware corruption/power loss.
At this measured delivery, the six accepted boundaries were physical storage
failure, native Windows/LAN networking, untested platforms, Python/legacy
conversion, simultaneous Go/Rust ownership of one original folder, and external
deployment. The later network handoff report records the added Windows/LAN coverage.
The documented body-versus-header timeout difference also remains unchanged.
For a real-data trial, stop the source and copy its complete data folder,
including WAL/SHM/settings/keys, while retaining the original.

All diagnostic source, immutable microbenchmark binaries, commands, timings
and behavioral evidence are retained under `speed-stamper/`; these ignored
measurement aids are not product dependencies or migration utilities.

Run the normal build commands and fresh explicit `TAM_DATA_DIR` examples in
the [README](../README.md). The retained local measurement helpers are
`build/r-tam-validation/speed-load.sh` (`baseline`, `candidate`, `mixed`),
`speed-browser.sh`, and the runtime collector in `speed-runtime-core-batch/`.
Ordinary Cargo validation and the permanent compatibility probes remain as
documented in the initial report. No runtime Go/Node dependency is introduced.
