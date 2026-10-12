# R-TAM review branch

The `r-tam` branch provides native Rust server and client programs with the
existing Svelte interface, compatible with Go commit
`b530cdad8da7be968e8f640a8de8badc1d113be7`.

It preserves current Go SQLite/native-backup formats and protocol identities,
with durable queued intents, validated acknowledgments, ordered replay and
preservation of later local edits. Build and trial instructions are in
[README](../README.md). Use fresh data directories or stopped, complete copies
while retaining originals.

## Try the branch

From a clone of the repository containing this branch:

```sh
git fetch origin r-tam
git switch --track origin/r-tam
bash rust/build.sh
```

On Windows, use `./rust/build.ps1` in PowerShell for the build step. The README
provides separate client/server startup commands and pairing instructions.
Existing local branches can use `git switch r-tam`.

## Validation and review scope

Validation includes 128 Windows and 165 Linux Cargo tests with warnings denied,
20 browser/print scenarios, and six real-program cohorts across two Linux hosts
and native Windows. Those cohorts passed 26 checks each; all 42 stopped databases
passed integrity/foreign-key checks, client queues drained and 24 HTTPS pins
matched their server certificates. Temporary workloads were removed and cleanup
was verified. Detailed scope and remaining limits are in
[R-TAM-NETWORK.md](R-TAM-NETWORK.md); measured performance is in
[R-TAM-SPEED.md](R-TAM-SPEED.md).

The Windows/Linux Rust CI workflow validates locked builds, formatting,
all-target checks/tests and Clippy, and retains the two program binaries per
platform. Consult the branch's Actions results for hosted CI status; local
validation does not establish a hosted CI result. Existing Go release, Docker
and installer automation continues to target Go.

This is a review and trial branch. No release tag, deployment, automatic merge
or pull request is implied by the branch handoff.
