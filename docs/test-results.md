# Test results

The latest recovery and shared winner-lookup validation is in
[Event recovery test results](recovery-test-results.md) (2026-09-28). The results
below are the archived 2026-09-27 build at `fcbb12e`.

The printed results of the tests, as the tools print them. CI prints the same for every push, on the run's page on GitHub (Actions), and keeps them as files under Artifacts; these are the longer runs, made on a real machine. [Running the tests](../README.md#running-the-tests) has the commands.

- Date: 2026-09-27
- Code: commit `fcbb12e` (branch all-systems)
- Machine: Windows 11, AMD Ryzen 9 7900X3D (12 cores, 24 threads), NVMe SSD
- Tools: Go 1.27.1, Node 26.7, pnpm 12.6, Python 3.13; Nix 2.35 in the `nixos/nix` container, with KVM
- The compatibility run prints the name of the computer its server runs on; here it reads `this-computer`.

## Unit tests

`go test -count=1 ./...`

```
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	1.698s
ok  	ticket-auction-manager/tam-go/internal/client	11.188s
ok  	ticket-auction-manager/tam-go/internal/config	1.066s
ok  	ticket-auction-manager/tam-go/internal/db	1.018s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	0.720s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/guard	0.368s
ok  	ticket-auction-manager/tam-go/internal/httpx	0.510s
ok  	ticket-auction-manager/tam-go/internal/presence	0.269s
ok  	ticket-auction-manager/tam-go/internal/remote	0.753s
ok  	ticket-auction-manager/tam-go/internal/server	1.420s
ok  	ticket-auction-manager/tam-go/internal/store	1.691s
ok  	ticket-auction-manager/tam-go/internal/sync	5.318s
ok  	ticket-auction-manager/tam-go/internal/tlscert	0.828s
?   	ticket-auction-manager/tam-go/internal/version	[no test files]
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
```

## Compatibility with the original tam

`bash scripts/compat/run.sh`: the original FastAPI server with the Go client's compatibility tests, then `tam-server` with the original SvelteKit client and the Go client, each saving through one client and reading through the other.

```
original at 19eab77 (change): Embedding TLS policy
--- Go client tests against the original server
=== RUN   TestCompatEverything
2026/09/27 20:24:37 paired with 127.0.0.1 (127.0.0.1:8010)
2026/09/27 20:24:37 server 127.0.0.1: connected
2026/09/27 20:24:37 mirror refreshed from 127.0.0.1: 0 prefixes, 0 tickets, 0 baskets
--- PASS: TestCompatEverything (0.12s)
PASS
ok  	ticket-auction-manager/tam-go/internal/client	0.193s
--- building the original client
--- driving both clients against the Go server
ok  original client: standalone to begin with
ok  original client: settings saved
ok  original client: key created on the Go server
ok  original client: key stored
ok  original client sees the Go server: {'authenticated': True, 'healthy': True, 'name': 'this-computer', 'version': '0.0.1', 'whoami': 'TAM Server'}
ok  go client: paired ({'message': 'Paired with this-computer.', 'server': '127.0.0.1:8011'})
ok  go client: connected ({'mode': 'remote', 'state': 'connected', 'server': '127.0.0.1:8011', 'server_name': 'this-computer', 'pending': 0, 'failed': 0, 'last_ok': '2026-09-28T00:24:49Z'})
ok  original: prefix saved
ok  original: tickets saved
ok  go client reads the original's tickets: [{'prefix': 'X55089', 't_id': 1, 'first_name': 'Ann', 'last_name': 'Both', 'phone_number': '555-1', 'pref': 'CALL'}, {'prefix': 'X55089', 't_id': 2, 'first_name': 'Ben', 'last_name': 'Both', 'phone_number': '555-2', 'pref': 'TEXT'}]
ok  go client lists the original's prefix
ok  go client: basket saved
ok  original reads the go client's basket: [{'prefix': 'X55089', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 0}]
ok  original: winner saved
ok  go client report shows the winner: [{'prefix': 'X55089', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 2, 'last_name': 'Both', 'first_name': 'Ben', 'phone_number': '555-2', 'pref': 'TEXT'}]
ok  original report: [{'last_name': 'Both', 'first_name': 'Ben', 'phone_number': '555-2', 'pref': 'TEXT', 'prefix': 'X55089', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 2}]
ok  go client counts
ok  original search
ok  original: server backup download (200 ['baskets', 'prefixes', 'tickets'])
ok  go client: server backup download
ok  original: push tickets
ok  go client: push baskets
ok  original: prefix deleted ({'message': 'Deleted successfully.'})
ok  go client: prefix gone
all 24 checks passed
compat run passed
```

## Load test: 50 clients

`go run ./scripts/loadtest -clients 50 -tickets 9000 -baskets 1000`: a whole event with the server killed during ticket entry, Wi-Fi drops with late delivery, crashes, a deleted key, everyone on the same tickets and a rush at the end.

```
tam load test: 50 clients, 9000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (windows/amd64, 24 CPUs)
built tam-server and tam-client in 1.4s
tam-server 0.0.1 answering on http://127.0.0.1:52721
50 tam-client programs answering after 0.3s
Pairing...
Setup...
Ticket entry...
  server killed after 100 of 360 sheets; starting it again in 8s
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
  Pairing: 0.2s, 50 requests (264 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                50            100ms    123ms    124ms    124ms      0      0
  Setup: 0.0s, 51 requests (1759 a second), 5 rows saved in 1 saves (172 rows and 34 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    2.5ms    2.5ms    2.5ms    2.5ms      0      0
    list prefixes                       50             25ms     27ms     27ms     27ms      0      0
  Ticket entry: 40.0s, 1734 requests (43 a second), 9173 rows saved in 533 saves (229 rows and 13 saves a second)
    360 sheets, one every 5000ms on each client; server killed at 5.1s, back at 13.1s (2 clients crashed and restarted meanwhile); all 128 queued saves sent 5.0s after that; the Wi-Fi of 12 clients dropped at 20.1s for 17.0s (12 saves hung until queued), all caught up 0.8s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  444            6.5ms     62ms     71ms     72ms      0      0
    save ticket sheet                  360    9000     13ms     50ms   5002ms   5002ms      0    126
    fix a typo                          61      61    6.0ms     21ms     21ms     22ms      0     16
    status bar                         650            1.0ms    2.5ms     19ms     30ms      0      0
    admin status page                    7            0.5ms    1.0ms    1.0ms    1.0ms      0      0
    open a sheet saved offline         100            0.5ms    7.5ms     20ms     21ms      0      0
    correct a sheet saved offline      100     100    1.5ms     11ms     24ms     28ms      0     86
    type a row again after a slow save      12      12    1.0ms    1.5ms    1.5ms    1.5ms      0     12
  Corrections: 0.1s, 50 requests (737 a second), 225 rows saved in 50 saves (3318 rows and 737 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    50     225     46ms     67ms     68ms     68ms      0      0
  A client's key deleted: 0.1s, 4 requests (37 a second), 50 rows saved in 2 saves (457 rows and 18 saves a second)
    client-26's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.0ms    0.0ms    0.0ms    0.0ms      0      0
    save while the key is refused        2      50    1.5ms    1.5ms    1.5ms    1.5ms      0      2
    pair again                           1            4.0ms    4.0ms    4.0ms    4.0ms      0      0
  Everyone on the same tickets: 5.0s, 10193 requests (2030 a second), 9592 rows saved in 9592 saves (1910 rows and 1910 saves a second)
    50 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves      9592    9592     26ms     28ms     32ms     54ms      0      0
    status bar                         100            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    1            6.5ms    6.5ms    6.5ms    6.5ms      0      0
    open a ticket everyone saved       500             22ms     28ms     30ms     31ms      0      0
  Baskets: 0.1s, 80 requests (987 a second), 1000 rows saved in 40 saves (12344 rows and 494 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40             48ms     56ms     73ms     73ms      0      0
    save basket sheet                   40    1000    6.0ms     20ms     31ms     31ms      0      0
  Drawing: 0.2s, 1080 requests (4943 a second), 1000 rows saved in 40 saves (4577 rows and 183 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40             30ms     44ms     48ms     48ms      0      0
    look up the winner                1000            1.0ms     24ms     37ms     58ms      0      0
    save drawing sheet                  40    1000    3.5ms     40ms     50ms     50ms      0      0
  Reports and searches: 1.8s, 950 requests (521 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       50            524ms    542ms    545ms    545ms      0      0
    report by basket                   250             22ms     34ms     44ms     47ms      0      0
    report by name                     250             22ms     53ms     75ms     77ms      0      0
    drawing results                    250             22ms     35ms     46ms     56ms      0      0
    search by last name                150            263ms    289ms    305ms    306ms      0      0
  Rush: 10.0s, 16909 requests (1687 a second), 417675 rows saved in 16707 saves (41663 rows and 1667 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet      16707  417675     30ms     34ms     37ms     77ms      0      0
    status bar                         200            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    2            0.5ms    0.5ms    0.5ms    0.5ms      0      0

Programs
  tam-server 0.0.1: CPU 34.2s over 2 runs, peak memory 105 MB, database 5 MB
  tam-client x50: CPU 50.6s in all (1.0s each on average), peak memory 70 MB for the largest
  this machine: windows/amd64, 24 CPUs; the test ran 60.6s

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
  PASS  every request was answered: 31101 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 242 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: save ticket sheet in Ticket entry, 5002ms
  PASS  every client saw the server again after the restart: 43 of 50 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20 24 28 32 36 40 44 48]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 50 of 50
  PASS  no errors in what the programs wrote: 51 programs, 1672 lines, 0 errors

PASSED: all 26 checks
```

## Load test: 20 clients over HTTPS, the server down for 45 s

`go run ./scripts/loadtest -clients 20 -tickets 6000 -baskets 1000 -tls -outage 45s`

```
tam load test: 20 clients, 6000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (windows/amd64, 24 CPUs)
built tam-server and tam-client in 1.4s
tam-server 0.0.1 answering on https://127.0.0.1:61742
20 tam-client programs answering after 0.2s
Pairing...
Setup...
Ticket entry...
  server killed after 60 of 240 sheets; starting it again in 45s
  2 clients crashed and started again
  Wi-Fi of 5 clients dropping for 12s
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
  Pairing: 0.1s, 20 requests (189 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                20             41ms     47ms     48ms     48ms      0      0
  Setup: 0.0s, 21 requests (1355 a second), 5 rows saved in 1 saves (323 rows and 65 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    2.5ms    2.5ms    2.5ms    2.5ms      0      0
    list prefixes                       20             12ms     13ms     13ms     13ms      0      0
  Ticket entry: 57.0s, 1158 requests (20 a second), 6135 rows saved in 375 saves (108 rows and 7 saves a second)
    240 sheets, one every 3333ms on each client; server killed at 6.7s, back at 51.8s (2 clients crashed and restarted meanwhile); all 209 queued saves sent 5.2s after that; the Wi-Fi of 5 clients dropped at 23.3s for 15.3s (0 saves hung until queued), all caught up 17.3s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  301            0.5ms     44ms     48ms     50ms      0      0
    save ticket sheet                  240    6000    2.5ms    8.5ms     15ms     18ms      0    180
    fix a typo                          35      35    1.5ms    2.5ms    2.5ms    2.5ms      0     29
    status bar                         380            0.5ms    0.5ms    0.5ms    1.0ms      0      0
    admin status page                    2            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a sheet saved offline         100            0.5ms   10.0ms     11ms     12ms      0      0
    correct a sheet saved offline      100     100    1.5ms     12ms     12ms     13ms      0     90
  Corrections: 0.0s, 20 requests (434 a second), 150 rows saved in 20 saves (3259 rows and 434 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    20     150     18ms     43ms     46ms     46ms      0      0
  A client's key deleted: 0.1s, 4 requests (36 a second), 50 rows saved in 2 saves (452 rows and 18 saves a second)
    client-11's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    save while the key is refused        2      50    1.0ms    1.5ms    1.5ms    1.5ms      0      2
    pair again                           1            5.5ms    5.5ms    5.5ms    5.5ms      0      0
  Everyone on the same tickets: 5.0s, 10127 requests (2022 a second), 9906 rows saved in 9906 saves (1978 rows and 1978 saves a second)
    20 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves      9906    9906     10ms     11ms     13ms     43ms      0      0
    status bar                          20            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a ticket everyone saved       200            7.5ms     15ms     16ms     16ms      0      0
  Baskets: 0.1s, 80 requests (1231 a second), 1000 rows saved in 40 saves (15384 rows and 615 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40            1.0ms     39ms     40ms     40ms      0      0
    save basket sheet                   40    1000    2.5ms     15ms     22ms     22ms      0      0
  Drawing: 0.2s, 1080 requests (5523 a second), 1000 rows saved in 40 saves (5114 rows and 205 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40            2.5ms     15ms     20ms     20ms      0      0
    look up the winner                1000            1.0ms     13ms     23ms     30ms      0      0
    save drawing sheet                  40    1000    3.5ms     11ms     16ms     16ms      0      0
  Reports and searches: 0.4s, 400 requests (943 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       20            115ms    118ms    119ms    119ms      0      0
    report by basket                   100            4.0ms    9.5ms     13ms     14ms      0      0
    report by name                     100            6.0ms     16ms     19ms     20ms      0      0
    drawing results                    100            5.5ms     12ms     14ms     14ms      0      0
    search by last name                 60             55ms     66ms     68ms     68ms      0      0
    status bar                          20            0.5ms    0.5ms    0.5ms    0.5ms      0      0
  Rush: 10.0s, 17392 requests (1737 a second), 433250 rows saved in 17330 saves (43268 rows and 1731 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet      17330  433250     11ms     13ms     16ms     52ms      0      0
    admin status page                    2            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    status bar                          60            0.5ms    0.5ms    0.5ms    0.5ms      0      0

Programs
  tam-server 0.0.1: CPU 24.3s over 2 runs, peak memory 75 MB, database 4 MB
  tam-client x20: CPU 38.7s in all (1.9s each on average), peak memory 70 MB for the largest
  this machine: windows/amd64, 24 CPUs; the test ran 75.4s

Checks
  PASS  every client paired and showed Connected: 20 clients, in 0.1s
  PASS  the server holds every ticket as last saved (after the drawing): 6000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the drawing): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the drawing): 5 saved, 5 on the server
  PASS  every client's own copy shows what it saved: 20 clients; 0 rows differ on 0 of them
  PASS  the server holds every ticket as last saved (after the rush): 6000 tickets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every basket and winner as saved (after the rush): 1000 baskets: 0 missing, 0 different, 0 unexpected
  PASS  the server holds every prefix (after the rush): 5 saved, 5 on the server
  PASS  every client ends connected with nothing waiting or refused: 20 clients: 0 not connected, 0 saves waiting, 0 refused by the server
  PASS  the admin page's Clients table lists every client as connected and caught up: 20 rows for 20 clients: 20 connected, 20 with a last update, 20 with nothing queued
  PASS  the admin page counts every prefix, ticket and basket: 5 prefixes, 6000 tickets, 1000 baskets (saved: 5, 6000, 1000)
  PASS  every request was answered: 30302 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 301 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: counts report in Reports and searches, 119ms
  PASS  every client saw the server again after the restart: 18 of 20 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 20 of 20
  PASS  no errors in what the programs wrote: 21 programs, 922 lines, 0 errors

PASSED: all 26 checks
```

## Nix package and NixOS test

`nix flake check -L`: the package build runs the unit tests in the Nix sandbox, then the NixOS test starts three machines: a server, a client that finds it by its announcement and pairs with it, and a server with its own HTTPS certificate.

```
Unit tests in the Nix build sandbox:
ok         ticket-auction-manager/tam-go/internal/admin    1.828s
ok         ticket-auction-manager/tam-go/internal/client   12.231s
ok         ticket-auction-manager/tam-go/internal/config   0.811s
ok         ticket-auction-manager/tam-go/internal/db       1.097s
ok         ticket-auction-manager/tam-go/internal/discovery        0.009s
ok         ticket-auction-manager/tam-go/internal/guard    0.097s
ok         ticket-auction-manager/tam-go/internal/httpx    0.004s
ok         ticket-auction-manager/tam-go/internal/presence 0.005s
ok         ticket-auction-manager/tam-go/internal/remote   0.013s
ok         ticket-auction-manager/tam-go/internal/server   2.368s
ok         ticket-auction-manager/tam-go/internal/store    3.013s
ok         ticket-auction-manager/tam-go/internal/sync     5.567s
ok         ticket-auction-manager/tam-go/internal/tlscert  0.016s

NixOS test:
subtest: the client serves the web app
(finished: subtest: the client serves the web app, in 0.06 seconds)
subtest: the server answers through its open port
(finished: subtest: the server answers through its open port, in 0.03 seconds)
subtest: the client finds the server by its announcement
(finished: subtest: the client finds the server by its announcement, in 1.54 seconds)
subtest: the client pairs and saves through the server
(finished: subtest: the client pairs and saves through the server, in 0.23 seconds)
subtest: the ticket is on the server
(finished: subtest: the ticket is on the server, in 0.07 seconds)
subtest: both keep their data over a restart
(finished: subtest: both keep their data over a restart, in 2.70 seconds)
subtest: a server with its own certificate serves it
(finished: subtest: a server with its own certificate serves it, in 0.11 seconds)
subtest: Shut Down TAM stops the client until it is started again
(finished: subtest: Shut Down TAM stops the client until it is started again, in 2.18 seconds)
test script finished in 26.64s
```
