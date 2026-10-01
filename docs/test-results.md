# Test results

The printed results of the tests, as the tools print them. CI prints the same for every push, on the run's page on GitHub (Actions), and keeps them as files under Artifacts. [Running the tests](../README.md#running-the-tests) has the commands.

- Date: 2026-10-01
- Code: commit `ea28f60`. The load tests ran on `352fc18`, whose Go code is the same.
- Where: Linux containers (Docker Desktop on Windows 11, AMD Ryzen 9 7900X3D, 24 threads), so that no test program listens on the Windows host
- Tools: go1.27.1 linux/amd64, Node 24.20.0, pnpm 12.6.0, Chromium from Playwright 1.63.0; the race detector with gcc 12
- Not run here: the Nix build and NixOS test, and the release build with its package tests; CI runs both on every push.

## Unit tests

`go test -count=1 ./...`

```
?   	ticket-auction-manager/tam-go/build/validation/ci-report-fix	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	2.145s
ok  	ticket-auction-manager/tam-go/internal/client	16.844s
ok  	ticket-auction-manager/tam-go/internal/config	0.842s
ok  	ticket-auction-manager/tam-go/internal/db	0.455s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	0.012s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/guard	0.102s
ok  	ticket-auction-manager/tam-go/internal/httpx	0.007s
ok  	ticket-auction-manager/tam-go/internal/presence	0.009s
ok  	ticket-auction-manager/tam-go/internal/remote	0.026s
ok  	ticket-auction-manager/tam-go/internal/server	3.073s
ok  	ticket-auction-manager/tam-go/internal/store	3.981s
ok  	ticket-auction-manager/tam-go/internal/sync	5.418s
ok  	ticket-auction-manager/tam-go/internal/tlscert	0.029s
?   	ticket-auction-manager/tam-go/internal/version	[no test files]
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
ok  	ticket-auction-manager/tam-go/scripts/shutdown	8.799s
```

## Unit tests with the race detector

`CGO_ENABLED=1 go test -race -count=1 ./...`: no data race reported.

```
?   	ticket-auction-manager/tam-go/build/validation/ci-report-fix	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	13.594s
ok  	ticket-auction-manager/tam-go/internal/client	33.017s
ok  	ticket-auction-manager/tam-go/internal/config	1.875s
ok  	ticket-auction-manager/tam-go/internal/db	1.715s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	1.032s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/guard	1.140s
ok  	ticket-auction-manager/tam-go/internal/httpx	1.046s
ok  	ticket-auction-manager/tam-go/internal/presence	1.056s
ok  	ticket-auction-manager/tam-go/internal/remote	1.213s
ok  	ticket-auction-manager/tam-go/internal/server	6.069s
ok  	ticket-auction-manager/tam-go/internal/store	7.496s
ok  	ticket-auction-manager/tam-go/internal/sync	7.211s
ok  	ticket-auction-manager/tam-go/internal/tlscert	1.206s
?   	ticket-auction-manager/tam-go/internal/version	[no test files]
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
ok  	ticket-auction-manager/tam-go/scripts/shutdown	10.778s
```

## Every release target

`go vet ./...` and `go build ./...` with `CGO_ENABLED=0` for each target.

```
windows/amd64 ok
windows/arm64 ok
linux/amd64 ok
linux/arm64 ok
darwin/amd64 ok
darwin/arm64 ok
```

## Browser tests

`pnpm test` in `scripts/browser`, against the Linux programs of this commit.

```
Running 18 tests using 1 worker
  ✓   1 counts.spec.js:3:1 › a prefix named Total is a row of its own beside the total (203ms)
  ✓   2 save-on-leave.spec.js:34:1 › A description saved from the Baskets form leaves a drawn winner alone (336ms)
  ✓   3 save-on-leave.spec.js:54:5 › Tickets: marked rows survive hidden (284ms)
  ✓   4 save-on-leave.spec.js:54:5 › Tickets: marked rows survive navigation (321ms)
  ✓   5 save-on-leave.spec.js:54:5 › Tickets: marked rows survive close (327ms)
  ✓   6 save-on-leave.spec.js:54:5 › Baskets: marked rows survive hidden (253ms)
  ✓   7 save-on-leave.spec.js:54:5 › Baskets: marked rows survive navigation (319ms)
  ✓   8 save-on-leave.spec.js:54:5 › Baskets: marked rows survive close (293ms)
  ✓   9 save-on-leave.spec.js:54:5 › Drawing: marked rows survive hidden (259ms)
  ✓  10 save-on-leave.spec.js:54:5 › Drawing: marked rows survive navigation (360ms)
  ✓  11 save-on-leave.spec.js:54:5 › Drawing: marked rows survive close (307ms)
  ✓  12 save-on-leave.spec.js:54:5 › Search: marked rows survive hidden (208ms)
  ✓  13 save-on-leave.spec.js:54:5 › Search: marked rows survive navigation (293ms)
  ✓  14 save-on-leave.spec.js:54:5 › Search: marked rows survive close (265ms)
  ✓  15 stale-saves.spec.js:46:1 › a page saving over a newer value shows the newer value and says so (1.6s)
  ✓  16 stale-saves.spec.js:75:1 › an offline save that arrives after a newer one waits in Settings for Retry (7.4s)
  ✓  17 stale-saves.spec.js:99:1 › a winner from a page loaded before another winner was entered does not replace it (1.4s)
  ✓  18 stale-saves.spec.js:121:1 › reports read from this computer's copy while the server is away say so (7.2s)
  18 passed (27.5s)
```

## Load test: 50 clients

`go run ./scripts/loadtest -clients 50 -tickets 9000 -baskets 1000`

```
tam load test: 50 clients, 9000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (linux/amd64, 24 CPUs)
built tam-server and tam-client in 1.1s
tam-server 0.0.1 answering on http://127.0.0.1:39003
50 tam-client programs answering after 0.2s
Pairing...
Setup...
Ticket entry...
  server killed after 91 of 360 sheets; starting it again in 8s
  2 clients crashed and started again
  server started again
  Wi-Fi of 12 clients dropping for 12s
  Wi-Fi back everywhere, queues sent
Corrections...
A client's key deleted...
Everyone on the same tickets...
Baskets...
Drawing...
Reports and searches...
Rush...

Phases
  Pairing: 0.2s, 50 requests (212 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                50             71ms    164ms    168ms    168ms      0      0
  Setup: 0.0s, 51 requests (1549 a second), 5 rows saved in 1 saves (152 rows and 30 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    5.5ms    5.5ms    5.5ms    5.5ms      0      0
    list prefixes                       50             24ms     27ms     27ms     27ms      0      0
  Ticket entry: 40.0s, 1748 requests (44 a second), 9177 rows saved in 537 saves (229 rows and 13 saves a second)
    360 sheets, one every 5000ms on each client; server killed at 5.1s, back at 13.2s (2 clients crashed and restarted meanwhile); all 144 queued saves sent 4.8s after that; the Wi-Fi of 12 clients dropped at 20.1s for 17.0s (12 saves hung until queued), all caught up 0.8s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  445            3.6ms     28ms     33ms     35ms      0      0
    save ticket sheet                  360    9000     45ms    164ms   5007ms   5009ms      0    135
    fix a typo                          56      56     27ms     70ms     88ms     99ms      0     25
    status bar                         650            0.5ms    1.5ms    1.7ms    2.6ms      0      0
    admin status page                    7            0.8ms    1.1ms    1.1ms    1.1ms      0      0
    open a sheet saved offline         109            0.4ms    0.9ms    1.1ms    1.2ms      0      0
    correct a sheet saved offline      109     109    4.3ms    6.3ms    7.3ms    7.4ms      0    109
    type a row again after a slow save      12      12    4.3ms    4.6ms    6.5ms    6.5ms      0     12
  Corrections: 0.1s, 50 requests (758 a second), 225 rows saved in 50 saves (3412 rows and 758 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    50     225     41ms     64ms     66ms     66ms      0      0
  A client's key deleted: 0.1s, 4 requests (35 a second), 50 rows saved in 2 saves (436 rows and 17 saves a second)
    client-26's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.2ms    0.2ms    0.2ms    0.2ms      0      0
    save while the key is refused        2      50    3.0ms    4.2ms    4.2ms    4.2ms      0      2
    pair again                           1            4.4ms    4.4ms    4.4ms    4.4ms      0      0
  Everyone on the same tickets: 5.1s, 4982 requests (986 a second), 4381 rows saved in 4381 saves (867 rows and 867 saves a second)
    50 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves      4381    4381     57ms     66ms     73ms     77ms      0      0
    status bar                         100            0.3ms    0.4ms    0.5ms    0.5ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a ticket everyone saved       500             23ms     30ms     31ms     32ms      0      0
  Baskets: 0.1s, 80 requests (682 a second), 1000 rows saved in 40 saves (8523 rows and 341 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40             20ms     25ms     25ms     25ms      0      0
    save basket sheet                   40    1000     53ms     89ms     93ms     93ms      0      0
  Drawing: 0.4s, 1080 requests (2717 a second), 1000 rows saved in 40 saves (2516 rows and 101 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40             26ms     48ms     51ms     51ms      0      0
    look up the winner                1000            4.9ms     28ms     41ms     54ms      0      0
    save drawing sheet                  40    1000     70ms     82ms     82ms     82ms      0      0
  Reports and searches: 1.9s, 1000 requests (521 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       50            185ms    213ms    225ms    225ms      0      0
    report by basket                   250             35ms     55ms     62ms     77ms      0      0
    report by name                     250             31ms     69ms     87ms     91ms      0      0
    drawing results                    250             37ms     60ms     71ms     87ms      0      0
    search by last name                150            357ms    425ms    433ms    434ms      0      0
    status bar                          50            0.3ms    0.6ms    0.7ms    0.7ms      0      0
  Rush: 10.1s, 5464 requests (542 a second), 132800 rows saved in 5312 saves (13165 rows and 527 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet       5312  132800     95ms    101ms    104ms    191ms      0      0
    admin status page                    2            0.6ms    1.0ms    1.0ms    1.0ms      0      0
    status bar                         150            0.3ms    0.3ms    0.4ms    0.4ms      0      0

Programs
  tam-server 0.0.1: CPU 29.5s over 2 runs, peak memory 123 MB, database 5 MB
  tam-client x50: CPU 34.0s in all (0.7s each on average), peak memory 25 MB for the largest
  this machine: linux/amd64, 24 CPUs; the test ran 60.5s

Checks
  PASS  every client paired and showed Connected: 50 clients, in 0.2s
  PASS  the server holds every ticket as last saved (after the drawing): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the drawing): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the drawing): 5 saved, 5 on the server
  PASS  every client's own copy shows what it saved: 50 clients; 0 rows differ on 0 of them
  PASS  the server holds every ticket as last saved (after the rush): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the rush): 5 saved, 5 on the server
  PASS  every client ends connected with nothing waiting or refused: 50 clients: 0 not connected, 0 saves waiting, 0 refused by the server
  PASS  the admin page's Clients table lists every client as connected and caught up: 50 rows for 50 clients: 50 connected, 50 with a last update, 50 with nothing queued
  PASS  the admin page counts every prefix, ticket and basket: 5 prefixes, 9000 tickets, 1000 baskets (saved: 5, 9000, 1000)
  PASS  every request was answered: 14509 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 283 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: save ticket sheet in Ticket entry, 5009ms
  PASS  every client saw the server again after the restart: 50 of 50 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20 24 28 32 36 40 44 48]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 50 of 50
  PASS  no errors in what the programs wrote: 51 programs, 1714 lines, 0 errors

PASSED: all 26 checks
```

## Load test: 20 clients over HTTPS, the server down for 45 s

`go run ./scripts/loadtest -clients 20 -tickets 9000 -baskets 1000 -tls -outage 45s`

```
tam load test: 20 clients, 9000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (linux/amd64, 24 CPUs)
built tam-server and tam-client in 1.1s
tam-server 0.0.1 answering on https://127.0.0.1:41285
20 tam-client programs answering after 0.1s
Pairing...
Setup...
Ticket entry...
  server killed after 94 of 360 sheets; starting it again in 45s
  Wi-Fi of 5 clients dropping for 12s
  2 clients crashed and started again
  server started again
  Wi-Fi back everywhere, queues sent
Corrections...
A client's key deleted...
Everyone on the same tickets...
Baskets...
Drawing...
Reports and searches...
Rush...

Phases
  Pairing: 0.1s, 20 requests (148 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                20             53ms     76ms     77ms     77ms      0      0
  Setup: 0.0s, 21 requests (686 a second), 5 rows saved in 1 saves (163 rows and 33 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    6.4ms    6.4ms    6.4ms    6.4ms      0      0
    list prefixes                       20             24ms     24ms     24ms     24ms      0      0
  Ticket entry: 60.0s, 1473 requests (25 a second), 9155 rows saved in 515 saves (153 rows and 9 saves a second)
    360 sheets, one every 2222ms on each client; server killed at 8.9s, back at 54.0s (2 clients crashed and restarted meanwhile); all 321 queued saves sent 6.0s after that; the Wi-Fi of 5 clients dropped at 22.2s for 14.2s (0 saves hung until queued), all caught up 23.5s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  455            0.8ms    9.0ms    9.9ms     11ms      0      0
    save ticket sheet                  360    9000    8.0ms     52ms     67ms     73ms      0    266
    fix a typo                          55      55    4.1ms     32ms     48ms     49ms      0     47
    status bar                         400            0.5ms    0.8ms    1.1ms    1.4ms      0      0
    admin status page                    3            0.4ms    0.9ms    0.9ms    0.9ms      0      0
    open a sheet saved offline         100            0.4ms    0.9ms    1.2ms    1.3ms      0      0
    correct a sheet saved offline      100     100    4.5ms    6.7ms    8.6ms    8.6ms      0    100
  Corrections: 0.0s, 20 requests (539 a second), 225 rows saved in 20 saves (6064 rows and 539 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    20     225     19ms     35ms     37ms     37ms      0      0
  A client's key deleted: 0.1s, 4 requests (34 a second), 50 rows saved in 2 saves (422 rows and 17 saves a second)
    client-11's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.2ms    0.2ms    0.2ms    0.2ms      0      0
    save while the key is refused        2      50    3.8ms    4.6ms    4.6ms    4.6ms      0      2
    pair again                           1            6.5ms    6.5ms    6.5ms    6.5ms      0      0
  Everyone on the same tickets: 5.0s, 4431 requests (883 a second), 4210 rows saved in 4210 saves (839 rows and 839 saves a second)
    20 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves      4210    4210     24ms     27ms     30ms     40ms      0      0
    status bar                          20            0.3ms    0.3ms    0.3ms    0.3ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a ticket everyone saved       200            6.4ms    9.1ms    9.3ms    9.4ms      0      0
  Baskets: 0.1s, 80 requests (941 a second), 1000 rows saved in 40 saves (11758 rows and 470 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40            0.8ms    7.9ms    8.9ms    8.9ms      0      0
    save basket sheet                   40    1000     34ms     40ms     45ms     45ms      0      0
  Drawing: 0.3s, 1080 requests (4143 a second), 1000 rows saved in 40 saves (3836 rows and 153 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40            3.8ms     12ms     16ms     16ms      0      0
    look up the winner                1000            1.3ms    6.5ms     10ms     13ms      0      0
    save drawing sheet                  40    1000     39ms     77ms     88ms     88ms      0      0
  Reports and searches: 0.6s, 400 requests (711 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       20             64ms     72ms     72ms     72ms      0      0
    report by basket                   100            6.0ms     11ms     13ms     14ms      0      0
    report by name                     100            6.5ms     13ms     16ms     17ms      0      0
    drawing results                    100            9.3ms     14ms     16ms     17ms      0      0
    search by last name                 60            110ms    125ms    129ms    131ms      0      0
    status bar                          20            0.3ms    0.5ms    0.6ms    0.6ms      0      0
  Rush: 10.0s, 5172 requests (515 a second), 127750 rows saved in 5110 saves (12728 rows and 509 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet       5110  127750     39ms     43ms     46ms     75ms      0      0
    status bar                          60            0.3ms    0.3ms    0.4ms    0.4ms      0      0
    admin status page                    2            0.5ms    0.5ms    0.5ms    0.5ms      0      0

Programs
  tam-server 0.0.1: CPU 16.9s over 2 runs, peak memory 103 MB, database 5 MB
  tam-client x20: CPU 24.5s in all (1.2s each on average), peak memory 31 MB for the largest
  this machine: linux/amd64, 24 CPUs; the test ran 78.2s

Checks
  PASS  every client paired and showed Connected: 20 clients, in 0.1s
  PASS  the server holds every ticket as last saved (after the drawing): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the drawing): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the drawing): 5 saved, 5 on the server
  PASS  every client's own copy shows what it saved: 20 clients; 0 rows differ on 0 of them
  PASS  the server holds every ticket as last saved (after the rush): 9000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the rush): 5 saved, 5 on the server
  PASS  every client ends connected with nothing waiting or refused: 20 clients: 0 not connected, 0 saves waiting, 0 refused by the server
  PASS  the admin page's Clients table lists every client as connected and caught up: 20 rows for 20 clients: 20 connected, 20 with a last update, 20 with nothing queued
  PASS  the admin page counts every prefix, ticket and basket: 5 prefixes, 9000 tickets, 1000 baskets (saved: 5, 9000, 1000)
  PASS  every request was answered: 12701 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 415 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: search by last name in Reports and searches, 131ms
  PASS  every client saw the server again after the restart: 20 of 20 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 20 of 20
  PASS  no errors in what the programs wrote: 21 programs, 1038 lines, 0 errors

PASSED: all 26 checks
```
