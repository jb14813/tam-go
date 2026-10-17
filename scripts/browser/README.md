# Browser tests

Playwright tests that drive the web app in Chromium against real programs:
a `tam-client` with its own temporary data folder, or a `tam-server` with two
paired clients whose network link to it can be cut. Nothing is mocked.

- `save-on-leave.spec.js`: marked rows on Tickets, Baskets, Drawing and Search
  are saved when the page is hidden, when a link is followed and when the tab
  is closed; a description saved from Baskets leaves a drawn winner alone.
- `stale-saves.spec.js`: a change from a page or a queued offline save never
  replaces a newer value another client saved; the volunteer is told, the page
  shows the current value, and Settings lists a queued change kept out, which
  Retry applies. Reports read from a client's copy while the server is away
  say so.
- `counts.spec.js`: a prefix named Total is a row of its own on the counts page.
- `shortcuts.spec.js`: every Alt shortcut of Tickets, Baskets, Drawing, Search
  and the main menu does what its tooltip says, by the letter on the key on
  other layouts too. After a message box opened by Alt+S while its keys were
  held, Alt+C, Alt+V and Alt+L still work. Alt+V with nothing copied leaves the
  row alone, Alt+C on Search's Pref button leaves the preference, and Ctrl+Alt
  (AltGr) and Alt+Shift are not shortcuts.

Build the web app and both programs, then run the tests (Node.js 24, pnpm 12):

```sh
cd frontend && pnpm install && pnpm build && cd ..
go build -o build/ ./cmd/tam-client ./cmd/tam-server
cd scripts/browser
pnpm install
pnpm exec playwright install --with-deps chromium
TAM_CLIENT_BIN=../../build/tam-client TAM_SERVER_BIN=../../build/tam-server pnpm test
```

On Windows use the `.exe` paths. The programs listen on 127.0.0.1 only and
stop when the tests end; failure traces and program logs are kept in
`test-results`.
