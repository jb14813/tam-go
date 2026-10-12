# R-TAM network validation and GitHub handoff

Local validation date: 2026-10-12. Validated `rust/` Git tree:
`61b20fbea837b9880fd4b332a4e779f7855d7b15`. Compatibility reference:
Go `b530cdad8da7be968e8f640a8de8badc1d113be7`.

The public branch imports the validated implementation onto that Go reference.
Its Rust, frontend, load harness and dependency locks match the locally tested
source. The document cleanup changes no program behavior. To verify the Rust
source identity from a clone, run `git rev-parse HEAD:rust`. Development history
and raw local test receipts are not included in the published branch.

The committed [result summary](R-TAM-RESULTS.json) records artifact hashes,
cohort totals, cleanup outcomes and remaining limits. The
[review branch guide](R-TAM-GITHUB.md) supplies portable build and trial
instructions.

This preparation adds Windows/Linux Rust CI and removes redundant trailing
blank lines from the server manifest and templates. Application logic, schema,
protocol, frontend source and dependency locks are unchanged from the optimized
implementation. Both platforms were rebuilt with warnings denied. The rebuilt
frontend and programs were validated again; the earlier performance measurements
remain measurements of the artifacts identified in [the speed report](R-TAM-SPEED.md).

## Fresh validation

| Gate | Result |
| --- | --- |
| Windows Cargo tests | 128 passed, zero failed or ignored |
| Linux Cargo tests | 165 passed, zero failed or ignored |
| Format, all-target check, Clippy and release builds | Passed on both platforms; Rust warnings denied |
| Actual browser and print scenarios | 20/20 passed, one worker, no retries |
| New GitHub workflow review | Approved; consult Actions for hosted execution results |

The hardware/network cohorts use six clients, 900 tickets, 150 baskets, five
prefixes and 25-row sheets, with a 12-second entry, three-second server outage,
three-second rush, three-second concurrent-correction phase, two client crashes,
three simulated dropped links, key deletion/repair, reports and queue drain.
The unchanged load harness exercises actual executables and TCP connections.
Linux processes run at reduced priority; Windows workloads run below normal
priority. Every cohort has fresh disposable data and bounded process lifetimes.

| Cohort | Server | Clients | Transport | Application checks |
| --- | --- | --- | --- | ---: |
| 1 | Linux host A, Rust | Linux host B, three Go and three Rust | LAN HTTP | 26/26 |
| 2 | Linux host A, Go | Linux host B, six Rust | LAN HTTPS | 26/26 |
| 3 | Linux host B, Rust | Linux host A, three Go and three Rust | LAN HTTPS | 26/26 |
| 4 | Linux host B, Go | Linux host A, six Rust | LAN HTTP | 26/26 |
| 5 | Linux host A, Rust | Windows, six native Rust | LAN HTTPS | 26/26 |
| 6 | Windows, native Rust | Windows, six native Rust | Loopback HTTPS | 26/26 |

The native Windows loopback cohort exercises the real Windows server listener
and client processes. The LAN cohorts use manually selected server addresses;
these tests do not establish cross-host multicast discovery or inbound Windows
firewall behavior. No network or firewall settings were changed.

All six cohorts passed exactly 26 application checks with no skips or failed
requests. All 42 stopped databases passed integrity and foreign-key checks;
all 36 client pending/refused queues were empty. The 24 clients in the four
HTTPS cohorts retained fingerprints matching their server's leaf certificate,
which was preserved across the deliberate server restart. Copies of the final
executables and once-built load harness retained their before/after hashes.

## Data and cleanup evidence

Each application workload checks server values, each client's own saved cache
rows, queued replay after outages/crashes, concurrent corrections, revoked-key
recovery, reports, final queue drain and clean client shutdown. External servers
are explicitly stopped by their owning wrappers before database verification.
The stopped-server checks therefore also exercise SQLite recovery after a hard
stop; they are not a claim that every external server exited gracefully.

Supplementary checks require SQLite integrity and foreign-key checks on every
stopped database, exact server row counts, empty client pending/refused queues,
and persisted HTTPS client fingerprints matching the server's DER certificate.
Clients maintain partial caches, as Go does; a connected client is not a full,
continuously updated replica. Local cache counts are observations, while the
load harness verifies the rows that the client wrote. No workload assertion was
removed to accommodate that contract.

Temporary orchestration initially exposed collector assumptions about server
queue tables and client cache cardinality, and an archive-output placement
error. Those diagnostics are retained separately. The actual application
checks passed; only the supplemental collection was corrected and repeated.
No application behavior or load-harness assertion changed.

The successful Linux cohorts' full host A container snapshot comparison recorded
a restart of an unrelated staging container. Its ID remained the same and its
PID changed; every other host A row and the host B snapshot matched. Read-only
inspection confirmed concurrent staging activity stopping/starting that
container. The strict snapshot guard's nonzero result and full snapshots are
retained, rather than relabeled as complete host immutability. R-TAM test
commands never modify Docker services.

Cleanup independently verified the two owned remote roots were absent, no
owned program executables or test-port listeners remained, and the native
Windows temporary data directories and owned processes were gone. The native
LAN cohort's host A container snapshot matched exactly. Host B's snapshots
matched throughout the successful remote cohorts. Final read-only host checks
confirmed the pre-existing service containers remained running, and host B's
existing services remained healthy with no failed systemd units. Compact
local receipts remain for audit; the remote test files and workloads were removed.
Extra local binary staging copies were removed after their hashes were checked.

Raw LAN commands, reports, hashes, database/pin receipts, server/client logs and
cleanup proof are retained locally under
`build/r-tam-validation/lan-20261012-f240fbbb/`. The Windows loopback evidence is
under `build/r-tam-validation/github-prep-native-loopback-92dcc283/`; browser
evidence is under `build/r-tam-validation/github-prep-browser-2T2aLY/`.
These ignored evidence directories are not required to build or run a clone.

## GitHub preparation

The [Rust workflow](../.github/workflows/r-tam.yml) runs on pushes and pull
requests. Its two native jobs build the frontend with the frozen pnpm lock,
build both Rust programs with the Cargo lock, and run format/check/test/Clippy
with warning denial. It retains Windows and Linux binary artifacts for review.
Hosted CI was not part of these local checks. Existing Go CI, Docker,
installer and tag-release packaging continue to target Go.

Build and use instructions are in [README](../README.md). The permanent
compatibility/browser/load probes and their dependency locks are tracked.
Building the programs needs Rust, Node/pnpm and a native C toolchain; running
the compatibility probes additionally needs Go and Python. Raw logs are local
validation evidence and are not required by a clean clone.

The review branch is `r-tam`. No release tag, deployment, automatic merge or
pull request is part of the branch handoff.
