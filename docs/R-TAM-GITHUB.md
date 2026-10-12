# R-TAM branch and releases

The `r-tam` branch provides native Rust server and client programs with the
existing Svelte interface, compatible with Go commit
`b530cdad8da7be968e8f640a8de8badc1d113be7`.

It preserves current Go SQLite/native-backup formats and protocol identities,
with durable queued intents, validated acknowledgments, ordered replay and
preservation of later local edits. Build and trial instructions are in
[README](../README.md). Use fresh data directories or stopped, complete copies
while retaining originals.

## Use the Rust downloads

Choose an R-TAM release from this repository's Releases page. Its Windows ZIP
and Linux tar.gz each contain the native Rust server and client, data-isolating
launch scripts, instructions and license. See
[release instructions](R-TAM-RELEASE.md) for supported systems and existing Go
data. No compiler is required to use the downloads.

## Build the branch

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
all-target checks/tests and Clippy, then tests packaging and executes the
programs from extracted release archives. It retains the binaries and ready-to-run
packages. Matching R-TAM version tags publish those packages and checksums after
both native jobs pass. Consult Actions for each commit's hosted result. Existing
Go release, Docker and installer automation continues to target Go.

The initial R-TAM release is marked as a prerelease for optional use. Release
publication does not merge the Rust branch or deploy it to existing events.
