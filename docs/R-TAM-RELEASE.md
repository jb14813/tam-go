# R-TAM Rust release

These downloads contain the native Rust server and client with their web
interfaces embedded. Running them needs no Rust, Go, Node.js or separate web
server. Windows builds include the Visual C++ runtime through static linkage;
no Visual C++ Redistributable installation is needed. This initial release is marked as a prerelease for optional use and
evaluation; it is a complete executable package.

## Choose a download

| Package | Supported system | Contents |
| --- | --- | --- |
| Windows x86-64 ZIP | 64-bit Windows | Rust server, Rust client, launch scripts, instructions, license and build metadata |
| Linux x86-64 tar.gz | Ubuntu 24.04 or another glibc-based distribution with glibc 2.39 or later | Rust server, Rust client, executable launch scripts, instructions, license and build metadata |

Use the ZIP or tar.gz asset, rather than GitHub's automatic source-code archive.
The `SHA256SUMS` asset lists the SHA-256 digest of each package. On Windows,
compare it with `Get-FileHash -Algorithm SHA256 <downloaded-zip>`; on Linux,
download the checksum file and run `sha256sum --check --ignore-missing SHA256SUMS`
from the download directory.

## Start the programs

Extract the complete package. On Windows, double-click `start-server.cmd` and
`start-client.cmd`, or run them from a Command Prompt in the extracted folder.
On Linux, run `./start-server.sh` and `./start-client.sh` in separate terminals.
Use only the client for standalone operation. Default data folders are separate and persist outside the extracted
package:

- Windows: `%LOCALAPPDATA%\R-TAM\server` and `%LOCALAPPDATA%\R-TAM\client`.
- Linux: `$XDG_DATA_HOME/r-tam/server` and `$XDG_DATA_HOME/r-tam/client`, or
  `$HOME/.local/share/r-tam/` when `XDG_DATA_HOME` is unset.

An explicit `TAM_DATA_DIR` overrides these defaults. Give the two programs
different folders. Close each program normally before copying or backing up
its complete data directory.

Open `http://127.0.0.1:8000/admin` to set the server password and
`http://127.0.0.1:3080/web/` for the client. In the client, press Alt+A to show
administration, then use Settings to pair with host `127.0.0.1`, port `8000`
and the server password. For clients on another computer, use the server's LAN
address. The client listens locally by default; the server listens on port 8000.
The server supports `-tls` on port 8443 and creates a retained self-signed
certificate; use HTTPS when pairing to that port.

## Existing Go data

R-TAM targets the current Go protocol and Go-native SQLite/backup formats at
reference commit `b530cdad8da7be968e8f640a8de8badc1d113be7`. For the first trial,
stop the Go program and copy its complete data folder to a separate location,
including any SQLite WAL/SHM files. Set `TAM_DATA_DIR` to that copy. Retain the
original data, and avoid running both implementations against the same folder.

Existing offline queues must finish or be quarantined before restore or
selective push. A native event backup contains event rows and revisions;
settings, authentication keys and waiting saves require the complete data folder.
Upgrading the extracted binaries preserves the launcher data folders.

## Build and release validation

GitHub Actions builds both Rust programs and embedded frontend assets from
locked dependencies on native Windows and Ubuntu 24.04 runners. Publication
requires both platforms to pass formatting, all-target compiler checks, tests,
Clippy with warnings denied, Windows runtime dependency inspection, packaging tests and execution of both programs
from the extracted release archives. The publishing job verifies package
checksums before uploading the archives and `SHA256SUMS`.

Release tags use `r-tam-v<major.minor.patch>` and must match the Cargo workspace
version. They trigger the R-TAM workflow; the existing Go release tags and Go
packages remain separate. Ordinary branch builds also retain ready-to-run Rust
packages as Actions artifacts. Published release assets remain available beyond
the temporary Actions artifact retention period.
