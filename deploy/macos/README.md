# Ticket Auction Manager on macOS

`tam-go-<version>-darwin-arm64.tar.gz` is for Apple silicon (M1 and later), `tam-go-<version>-darwin-amd64.tar.gz` for Intel Macs. Both programs are plain command-line programs; nothing is installed unless you want them started at login (the launchd section below). In the release archive this file is `INSTALL.md`.

## Unsigned downloads

The programs are not signed with an Apple developer certificate, so macOS refuses them the first time ("cannot be opened because the developer cannot be verified", or "Apple could not verify ... is free of malware"). Clear the quarantine flag once, and make sure they are executable (some unzip tools drop the bit):

```sh
cd tam-go-<version>-darwin-arm64
xattr -dr com.apple.quarantine tam-server tam-client
chmod +x tam-server tam-client
```

## Running by hand

```sh
./tam-client            # opens http://localhost:3080/ in your browser
./tam-server            # the shared database for several clients, on port 8000
```

Each keeps its data in a `data` folder in the directory it is started from, or in `TAM_DATA_DIR`. Ctrl+C stops either; the client also stops from **Shut Down TAM** under `Alt+A` (Option+A) on its main menu. macOS asks once whether tam-server may accept incoming connections; allow it so the clients can reach it. There is no notification-area icon on macOS.

## Starting at login with launchd

The two plists next to this file are launch agents: they start the program when you log in, restart it after a crash, and keep its data under `~/Library/Application Support/tam-server` or `~/Library/Application Support/tam-client`, with the program's log (`tam-server.log`, `tam-client.log`) next to the database.

1. Put the programs where the plists expect them:

   ```sh
   sudo install -m 755 tam-server tam-client /usr/local/bin/
   ```

2. Copy the plist of each program that should run at login (one or both):

   ```sh
   mkdir -p ~/Library/LaunchAgents
   cp com.ticket-auction-manager.tam-server.plist ~/Library/LaunchAgents/
   ```

3. Start it now, and at every login from here on:

   ```sh
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.ticket-auction-manager.tam-server.plist
   launchctl print gui/$(id -u)/com.ticket-auction-manager.tam-server | head   # state and pid
   curl http://localhost:8000/api
   ```

   On macOS before 10.11 the commands are `launchctl load -w` and `launchctl unload`.

4. To stop it and take it out of the login items:

   ```sh
   launchctl bootout gui/$(id -u)/com.ticket-auction-manager.tam-server
   ```

   `launchctl kickstart -k gui/$(id -u)/com.ticket-auction-manager.tam-server` restarts a running one. After editing a plist, bootout and bootstrap it again so launchd reads the new file.

The server's password is set on its admin page, http://localhost:8000/admin, on the first visit; to set it in the plist instead, uncomment the `EnvironmentVariables` block there, fill in `TAM_PWD`, and load the plist again. `KeepAlive` is limited to failures, so the client's Shut Down button and `bootout` stop a program until the next login, while a crash restarts it; `RunAtLoad` starts it at login.

A launch agent runs while you are logged in. For a server that should run with nobody logged in, put the plist in `/Library/LaunchDaemons` instead (owned by root), replace `$HOME/Library/Application Support/tam-server` in it by a fixed folder such as `/Library/Application Support/tam-server`, and load it with `sudo launchctl bootstrap system /Library/LaunchDaemons/com.ticket-auction-manager.tam-server.plist`.
