# R-TAM Rust release

R-TAM is JZB's independent Rust fork of
[Ticket Auction Manager Go](https://github.com/ticket-auction-manager/tam-go).
The original Ticket Auction Manager project is Dilan Gilluly's project. These
R-TAM downloads are separate from the official project's Go releases.

These downloads contain the native Rust server and client with their web
interfaces embedded. Running them needs no Rust, Go, Node.js or separate web
server. Windows builds include the Visual C++ runtime through static linkage;
no Visual C++ Redistributable installation is needed. These releases are marked as prereleases for optional use and
evaluation; each is a complete executable package.

0.1.2 fixes the Alt keyboard shortcuts of the entry forms. After a message box,
such as "Some changes were not saved" answering Alt+S, Alt+C, Alt+V and every
other Alt shortcut stopped working until the window lost focus; now they keep
working. Alt+V with nothing copied leaves the row unmarked, and Alt+C on a Ticket
Search row's Pref button no longer switches it to CALL. The README lists the
shortcuts.

## Choose a download

| Package | Supported system | Contents |
| --- | --- | --- |
| Windows x86-64 ZIP | Windows 10 or 11, Intel/AMD 64-bit | Rust server, Rust client, launch scripts, instructions, license and build metadata |
| Linux x86-64 tar.gz | Linux x86-64 with glibc 2.39 or later and the standard libgcc runtime, such as Ubuntu 24.04 or later | Rust server, Rust client, executable launch scripts, instructions, license and build metadata |

No native R-TAM macOS, ARM or musl package is published. The repository's
retained Go installers, Docker files and Nix configuration target Go, not these
Rust packages. A browser is required for the interface; Linux launchers do not
open one automatically. In WSL, keep the package and data on the Linux filesystem.

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
- Linux: `${XDG_DATA_HOME:-$HOME/.local/share}/r-tam/server` and the separate
  `${XDG_DATA_HOME:-$HOME/.local/share}/r-tam/client` folder.

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

Ordinary restore overwrites matching supplied rows and retains absent rows; it
does not replace the whole event. A paired client's cache and local event backup
may be partial; use the server's backup for the shared event. JSON event backups
omit operational metadata as well as settings, keys and pending saves. Use a
complete stopped-folder copy when those are needed. The automatic archive
before a client switches server events does not cover ordinary server restores.

Compatibility with later upstream changes is not implied. These prereleases and
bounded validation do not guarantee recovery from arbitrary power loss,
storage failure or corruption. Retain complete stopped-folder copies and backups.

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

## License and support

R-TAM retains the upstream
[MIT license and copyright notice](https://github.com/jb14813/tam-go/blob/r-tam/LICENSE.md).
The Rust fork is maintained by JZB; Ticket Auction Manager's original project
and Dilan Gilluly's credit remain separate from the fork.

Follow the fork's
[R-TAM branch](https://github.com/jb14813/tam-go/tree/r-tam) for source and
maintenance information. This fork currently has no Issues or Discussions
board, so no dedicated R-TAM support tracker is advertised. For the official
Go software, use the
[official repository](https://github.com/ticket-auction-manager/tam-go) and its
[issue tracker](https://github.com/ticket-auction-manager/tam-go/issues).
Do not present a Rust-only failure as an upstream Go bug.
