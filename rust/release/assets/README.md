# R-TAM @VERSION@ for @PLATFORM@

This package contains the native R-TAM server and client with their web interface
embedded. Go, Rust, Python and Node.js are not needed to run the programs.

@REQUIREMENTS@

## Start

Extract the entire archive into a folder you can write to. Start the server and
client in separate terminals from that folder:

```text
@SERVER_COMMAND@
```

```text
@CLIENT_COMMAND@
```

The client can also run alone. Open `http://localhost:3080/web/` for its interface.
On Windows, the client opens a browser automatically. Open
`http://localhost:8000/admin/` to set the server password. In the client, press
Alt+A to show administration, then use Settings to pair with the server host,
port `8000` and that password. Use `localhost` when both run on the same computer;
other clients use the server computer's network address. Add a prefix to use its
entry forms and reports. Leave the server unset for a standalone client.

The server listens on all network interfaces and announces itself with mDNS by
default. Use `@SERVER_COMMAND@ -addr 127.0.0.1:8000 -announce=false` to restrict it
to the local computer. Firewall permissions may be needed for other computers
to connect. `@SERVER_COMMAND@ -tls` uses HTTPS on port `8443`, creates a persistent
self-signed certificate and retains it in the server's data folder. Pairing
records the certificate fingerprint; certificate changes require pairing again.

Use `@SERVER_COMMAND@ --help` or `@CLIENT_COMMAND@ --help` for supported arguments.
Arguments passed to a launcher are forwarded to its program. Press Ctrl+C for a
graceful shutdown, or use Shut Down in the Windows tray menu.

## Data

Launchers use separate persistent folders for the server and client:
@DEFAULT_ROOT@. These folders survive replacing or deleting the extracted
package. An explicit nonempty `TAM_DATA_DIR` overrides the launcher's default.

@OVERRIDE@

Keep each running program's folder separate. Launchers start with fresh R-TAM
folders and do not locate, import or change existing Go data. If Go is running,
stop it or choose different listen ports before starting R-TAM on the same
computer. Upgrade R-TAM by stopping it, extracting the new package and running
the new launchers with the same data folders.

For an existing Go event, retain its original data and first test with a copy of
its complete folder while Go is stopped, including any SQLite WAL/SHM files. Set
`TAM_DATA_DIR` to that copy. R-TAM targets the Go protocol and native backup
format at commit `b530cdad8da7be968e8f640a8de8badc1d113be7`. Its client cache is
partial; the server holds the shared event. A JSON event backup contains event
rows and revisions, but omits settings, TLS keys and waiting saves. Retain the
complete data folder for those. Do not restore or change events while saves are
pending; resolve them through Settings first. Never run Go and R-TAM against
the same live data folder.

## Verify the download

Download the release's `SHA256SUMS` file beside the archive. From the download
folder:

```text
@CHECKSUM_COMMAND@
```

Compare the Windows hash with the corresponding archive line in `SHA256SUMS`.
The Linux command verifies the downloaded archives and skips any absent archive.
`VERSION.json` records this package's version, source commit,
build target and SHA-256 hashes of both binaries. The upstream MIT license and
required copyright notice are retained in `LICENSE.md`.

Source: `@SOURCE@`. Target: `@TARGET@`.
