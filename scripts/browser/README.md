# Browser regression tests

The leave-save tests launch a real Linux `tam-client` with a new temporary data folder
and an automatically allocated loopback port. They fill two marked rows in
Tickets, Baskets, Drawing, and Search, then check the saved API data after
visibility changes, Main Menu navigation, and closing the browser tab.
Failures return a nonzero exit status. The client and its temporary data are
cleaned up after the run; failure traces and client logs stay in `test-results`.

The Drawing lookup tests start a real server and two clients with separate
data folders and access keys. A buyer entered on one client is displayed by
the other client's Drawing page without copying that ticket into its local
database. These tests distinguish a missing ticket from a saved blank ticket,
verify the drawing save contains only drawing fields, and disconnect real TCP
links to check local-only feedback and automatic lookup retries after queued
entries arrive. Queue readiness also waits for event recovery to finish.

Build the frontend and both Linux programs first, then run on Linux (Node.js 22+):

```sh
# From the repository root, after pnpm install and pnpm build in frontend:
go build -o /tmp/tam-client ./cmd/tam-client
go build -o /tmp/tam-server ./cmd/tam-server
cd scripts/browser
npm ci
npx playwright install --with-deps chromium
TAM_CLIENT_BIN=/tmp/tam-client TAM_SERVER_BIN=/tmp/tam-server npm test
```

On Windows, run the programs and this suite in a Linux container. The fixtures
refuse Windows execution to avoid starting throwaway listening programs.
The npm dependencies here are separate from the frontend and its Nix inputs.

The hidden-page check synthesizes `visibilitychange`, since headless Chromium
keeps tabs visible; it checks persistence before any close can save the rows.
The navigation and close checks use actual browser actions without request
interception. These cover the application's lifecycle handlers, not every
operating system's tab-discard or browser-termination behavior.
