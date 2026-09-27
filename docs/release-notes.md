Ticket Auction Manager is the ticket, basket and drawing bookkeeping for an in-person benefit auction. This is the Go version: one self-contained program per machine, no runtime to install and no container needed. Each laptop at the event runs **tam-client**, the web app it opens in the browser. One machine runs **tam-server**, the shared database the laptops pair with; a laptop keeps working when the server is out of reach and catches up when it is back, and the server's admin page lists every client, whether it is connected, and when it last saved. The full manual is the [README at this version](https://github.com/{REPO}/blob/{VERSION}/README.md).

## Which file to download

| Machine | File |
|---|---|
| Windows laptop | `tam-client-{VERSION}-windows-amd64.exe` (the `.zip` of the same name adds the README) |
| Windows machine hosting the server | `tam-server-{VERSION}-windows-amd64.exe` (`-windows-arm64` for a Snapdragon machine) |
| Debian, Ubuntu, Mint and their relatives | the `.deb` of the program, `_amd64.deb` or `_arm64.deb` |
| Fedora, RHEL, Rocky, Alma and their relatives | the `.rpm` of the program, `.x86_64.rpm` or `.aarch64.rpm` |
| Any other Linux | the `-linux-amd64.tar.gz` (or `-arm64`) of the program: it holds the program, an installer script for systemd, and the menu entry for the client |
| macOS | the `-darwin-arm64.tar.gz` (Apple silicon) or `-darwin-amd64.tar.gz` (Intel) of the program, with a launchd file |
| NixOS | nothing: the repository's flake builds both programs, and its NixOS module runs them as services (see the README) |
| Docker | build from source with `deploy/docker` in the repository; see the README |

## First start

1. **The server.** Windows: put the exe in a folder of its own and double-click it. Debian or Ubuntu: `sudo apt install ./tam-server_*.deb`; Fedora or RHEL: `sudo dnf install ./tam-server-*.rpm`; the service starts at once. Tarball: `sudo ./install.sh` in the extracted folder, or just run `./tam-server`. Then open `http://<that machine>:8000/admin` and set the server password; that page is also where you see the laptops and take backups.
2. **The laptops.** Run tam-client the same way. It opens the browser. Press `Alt+A`, open Settings, pick the server from the list (it announces itself on the network), enter the password once and press **Pair**. The bar at the top says Connected from then on. A laptop with no server at all works on its own with its data on the laptop.

## Upgrading

Replace the file with the new one, or install the new `.deb` or `.rpm`; the data stays where it is. The server and the laptops do not need to be upgraded at the same time.

## Switching from the original (Linux, Docker) version

The data files are the same: `tam-remote.db` for the server, `tam-local.db` and `settings.json` for the client. Point `TAM_DATA_DIR` at the old data folder (the `/data` of the old containers) or copy those files into the program's `data` folder, and start it; nothing else changes. The original's client can keep talking to this server while laptops are switched one at a time.

## Good to know

- The programs are not signed. Windows SmartScreen asks once (More info, then Run anyway); on macOS clear the quarantine flag once with `xattr -dr com.apple.quarantine tam-client` or `tam-server`.
- GitHub shows a package with a pre-release version as `1.0.0.rc1` in the file name; the package inside is `1.0.0~rc1` and installs under any file name.
- The Windows programs keep a small icon in the notification area while they run; right-click it to shut them down. On Linux and macOS, Ctrl+C or the Shut Down button on the main menu does that.
