# R-TAM

R-TAM is a native Rust server and client for Ticket Auction Manager. It manages
ticket buyers, baskets, drawing results, searches and printed reports for
in-person benefit auctions. The client uses the existing Svelte interface and
can work alone or pair with a server, including offline entry and queued replay.

The compatibility reference is Go commit
`b530cdad8da7be968e8f640a8de8badc1d113be7`. Rust and Go use the same current
HTTP protocol, SQLite data and native backup document. Protocol identities and
headers retain their TAM names. The Go source and its [reference README](GO-REFERENCE.md)
remain available here.

Ready-to-run Windows and Linux downloads are published as R-TAM releases on
this repository. Choose the ZIP or tar.gz package from the Releases page;
each includes both Rust programs, launchers, instructions and its license.
[Release instructions](docs/R-TAM-RELEASE.md) explain supported systems,
checksums, data folders and pairing. Current validation and cleanup details
are in [the network validation report](docs/R-TAM-NETWORK.md).
Measured performance is recorded in [the speed and safety report](docs/R-TAM-SPEED.md).
The [initial validation report](docs/R-TAM-VALIDATION.md) retains the original
baseline, implementation decisions and earlier compatibility evidence.

## Build

Use Rust 1.98 or later, Node.js 24 and pnpm 12. Windows needs the MSVC C++ build
tools; Linux needs a C compiler. SQLite is bundled. Dependencies are locked and
the build scripts build and embed the Svelte assets before compiling both Rust
programs.

From PowerShell in the checkout:

```powershell
./rust/build.ps1
```

From Linux or WSL:

```sh
bash rust/build.sh
```

Release programs are placed in `build/r-tam/windows/` or `build/r-tam/linux/`.
They run without Go, Node.js or a separate web asset directory.

## Run locally

Give each program its own fresh data directory. In separate PowerShell windows:

```powershell
$env:TAM_DATA_DIR = Join-Path $PWD 'data/r-tam-server'
./build/r-tam/windows/r-tam-server.exe -addr 127.0.0.1:8000 -announce=false
```

```powershell
$env:TAM_DATA_DIR = Join-Path $PWD 'data/r-tam-client'
./build/r-tam/windows/r-tam-client.exe -addr 127.0.0.1:3080
```

For the same loopback test in Linux/WSL, run these in separate terminals and
open the addresses in a browser. These examples keep the data under your Linux
home directory; in WSL, this avoids database access through `/mnt/c`:

```sh
TAM_DATA_DIR="$HOME/.local/share/r-tam/server" ./build/r-tam/linux/r-tam-server -addr 127.0.0.1:8000 -announce=false -tray=false
```

```sh
TAM_DATA_DIR="$HOME/.local/share/r-tam/client" ./build/r-tam/linux/r-tam-client -addr 127.0.0.1:3080 -open=false -tray=false
```

Open `http://127.0.0.1:8000/admin` to set the server password. Open
`http://127.0.0.1:3080/web/` for the client. Press Alt+A to show administration,
then use Settings to pair with host `127.0.0.1`, port `8000` and the server
password. Add a prefix to access its entry forms and reports. Leave the server
unset to use the client on its own.

Without `TAM_DATA_DIR`, data is stored in `./data` relative to the working
directory. Normal defaults are server port 8000, TLS server port 8443 and client
`localhost:3080`. Go-style flags include `-addr`, `-tls`, `-cert`, `-key`,
`-tray`, client `-open` and server `-announce`; boolean flags accept `=false`.
TLS generates and retains a self-signed certificate when both default PEM files
are absent. Pairing records its SHA-256 leaf fingerprint; certificate changes
require pairing again.

## Data and compatibility

For a database handoff, stop the source program and copy its complete data
folder into a separate test folder, including any SQLite WAL/SHM files. Open
that copy with the other implementation. Retain the original folder. R-TAM
reads the current Go tables, settings, password hashes, save counters and queued
saves directly, preserving unknown tables. Current Go-native backups contain
`event`, `prefixes`, `baskets` and `tickets`.

An event backup contains event rows and revisions. It does not contain keys,
settings or waiting saves; retain the complete data folder for those. Ordinary
restore upserts supplied rows and keeps absent rows. A newer-only push respects
event identity, revisions and deleted-prefix records. Changing events preserves
the old copy in an exclusive, flushed JSON archive before clearing or
quarantining anything.

R-TAM adds several safeguards: remote intents are durable before dispatch;
online rows are accepted after a validated acknowledgment; invalid answers
retain their intents; later local edits survive older answers; and explicit
restore or selective push refuses while saves are pending. Finish or quarantine
waiting saves through the existing Settings controls before restoring. Revision
allocation stops at the native JSON integer limit. These are documented data
safety differences from Go. Each Rust process locks its data folder and drains
active work before shutdown.

## Validation

```sh
cargo fmt --manifest-path rust/Cargo.toml --all --check
cargo check --locked --manifest-path rust/Cargo.toml --workspace --all-targets
cargo test --locked --manifest-path rust/Cargo.toml --workspace --all-targets
cargo clippy --locked --manifest-path rust/Cargo.toml --workspace --all-targets -- -D warnings
```

Real-program compatibility probes are retained in `rust/tests/compatibility/`.
Browser scenarios remain in `scripts/browser/`, with extra print checks in
`rust/tests/browser/`. The load harness supports `-other-client` for alternating
Go and Rust clients in one event. The validation report records exact runtime
commands and results. Network validation covers two Linux peers, native
Windows clients over the LAN, and native Windows server/client HTTPS on loopback.
The Windows tray and console-close tests additionally exercise platform callbacks.

The R-TAM GitHub workflow builds, validates and packages native Windows and Linux
Rust programs with warnings denied. Matching `r-tam-v<version>` tags publish the
tested packages and checksums as an R-TAM release. Existing Go workflows and Go
release packaging remain separate.

## Attribution

Ticket Auction Manager and its Svelte interface retain their existing license
and copyright notices. This fork is distributed under the existing
[MIT license](LICENSE.md).
