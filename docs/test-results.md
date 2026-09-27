# Test results

The printed results of the tests, as the tools print them. CI prints the same for every push, on the run's page on GitHub (Actions), and keeps them as files under Artifacts; these are the longer runs, made on a real machine. [Running the tests](../README.md#running-the-tests) has the commands.

- Date: 2026-09-27
- Code: commit `2bb5761` (branch all-systems)
- Machine: Windows 11, AMD Ryzen 9 7900X3D (12 cores, 24 threads), NVMe SSD
- Tools: Go 1.27.1, Node 24, pnpm 12, Python 3.13; Nix 2.35 in the `nixos/nix` container, with KVM
- The compatibility run prints the name of the computer its server runs on; here it reads `this-computer`.

## Unit tests

`go test -count=1 ./...`

```
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	1.223s
ok  	ticket-auction-manager/tam-go/internal/client	8.063s
ok  	ticket-auction-manager/tam-go/internal/config	0.505s
ok  	ticket-auction-manager/tam-go/internal/db	0.578s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	0.563s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/httpx	0.419s
ok  	ticket-auction-manager/tam-go/internal/presence	0.215s
ok  	ticket-auction-manager/tam-go/internal/remote	0.624s
ok  	ticket-auction-manager/tam-go/internal/server	0.998s
ok  	ticket-auction-manager/tam-go/internal/store	1.204s
ok  	ticket-auction-manager/tam-go/internal/sync	0.355s
ok  	ticket-auction-manager/tam-go/internal/tlscert	0.660s
?   	ticket-auction-manager/tam-go/internal/version	[no test files]
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
```

## Compatibility with the original tam

`bash scripts/compat/run.sh`: the original FastAPI server with the Go client's compatibility tests, then `tam-server` with the original SvelteKit client and the Go client, each saving through one client and reading through the other.

```
original at 19eab77 (change): Embedding TLS policy
--- Go client tests against the original server
=== RUN   TestCompatEverything
2026/09/27 13:10:58 paired with 127.0.0.1 (127.0.0.1:8010)
2026/09/27 13:10:58 server 127.0.0.1: connected
2026/09/27 13:10:58 mirror refreshed from 127.0.0.1: 0 prefixes, 0 tickets, 0 baskets
--- PASS: TestCompatEverything (0.14s)
PASS
ok  	ticket-auction-manager/tam-go/internal/client	0.221s
--- building the original client
--- driving both clients against the Go server
ok  original client: standalone to begin with
ok  original client: settings saved
ok  original client: key created on the Go server
ok  original client: key stored
ok  original client sees the Go server: {'authenticated': True, 'healthy': True, 'name': 'this-computer', 'version': '0.0.1', 'whoami': 'TAM Server'}
ok  go client: paired ({'message': 'Paired with this-computer', 'server': '127.0.0.1:8011'})
ok  go client: connected ({'mode': 'remote', 'state': 'connected', 'server': '127.0.0.1:8011', 'server_name': 'this-computer', 'pending': 0, 'failed': 0, 'last_ok': '2026-09-27T17:11:12Z'})
ok  original: prefix saved
ok  original: tickets saved
ok  go client reads the original's tickets: [{'prefix': 'X29071', 't_id': 1, 'first_name': 'Ann', 'last_name': 'Both', 'phone_number': '555-1', 'pref': 'CALL'}, {'prefix': 'X29071', 't_id': 2, 'first_name': 'Ben', 'last_name': 'Both', 'phone_number': '555-2', 'pref': 'TEXT'}]
ok  go client lists the original's prefix
ok  go client: basket saved
ok  original reads the go client's basket: [{'prefix': 'X29071', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 0}]
ok  original: winner saved
ok  go client report shows the winner: [{'prefix': 'X29071', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 2, 'last_name': 'Both', 'first_name': 'Ben', 'phone_number': '555-2', 'pref': 'TEXT'}]
ok  original report: [{'last_name': 'Both', 'first_name': 'Ben', 'phone_number': '555-2', 'pref': 'TEXT', 'prefix': 'X29071', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 2}]
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
tam-server 0.0.1 answering on http://127.0.0.1:56786
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
  Pairing: 0.1s, 50 requests (403 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                50             46ms     59ms     61ms     61ms      0      0
  Setup: 0.0s, 51 requests (1700 a second), 5 rows saved in 1 saves (167 rows and 33 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    2.5ms    2.5ms    2.5ms    2.5ms      0      0
    list prefixes                       50             26ms     27ms     27ms     27ms      0      0
  Ticket entry: 44.9s, 1799 requests (40 a second), 9180 rows saved in 540 saves (204 rows and 12 saves a second)
    360 sheets, one every 5000ms on each client; server killed at 5.1s, back at 13.1s (2 clients crashed and restarted meanwhile); all 127 queued saves sent 5.0s after that; the Wi-Fi of 12 clients dropped at 20.1s for 17.0s (12 saves hung until queued), all caught up 7.9s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  442             16ms     57ms     70ms     88ms      0      0
    save ticket sheet                  360    9000    8.0ms     41ms   5002ms   5002ms      0    136
    fix a typo                          58      58    2.0ms     14ms     17ms     21ms      0     19
    status bar                         700            0.6ms    2.0ms     13ms     18ms      0      0
    admin status page                    7            0.5ms    1.0ms    1.0ms    1.0ms      0      0
    open a sheet saved offline         110            0.5ms     15ms     18ms     19ms      0      0
    correct a sheet saved offline      110     110    1.5ms     13ms     47ms     52ms      0     80
    type a row again after a slow save      12      12    1.5ms    1.5ms    1.5ms    1.5ms      0     12
  Corrections: 0.1s, 101 requests (1517 a second), 225 rows saved in 50 saves (3379 rows and 751 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    50     225     51ms     66ms     66ms     66ms      0      0
    status bar                          50            0.0ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
  A client's key deleted: 0.1s, 4 requests (37 a second), 50 rows saved in 2 saves (464 rows and 19 saves a second)
    client-26's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    save while the key is refused        2      50    1.5ms    2.0ms    2.0ms    2.0ms      0      2
    pair again                           1            3.0ms    3.0ms    3.0ms    3.0ms      0      0
  Everyone on the same tickets: 5.0s, 10925 requests (2176 a second), 10374 rows saved in 10374 saves (2066 rows and 2066 saves a second)
    50 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves     10374   10374     24ms     27ms     28ms     77ms      0      0
    status bar                          50            0.0ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a ticket everyone saved       500             22ms     26ms     27ms     27ms      0      0
  Baskets: 0.1s, 80 requests (1203 a second), 1000 rows saved in 40 saves (15033 rows and 601 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40             48ms     53ms     58ms     58ms      0      0
    save basket sheet                   40    1000    5.0ms     14ms     54ms     54ms      0      0
  Drawing: 0.2s, 1080 requests (4838 a second), 1000 rows saved in 40 saves (4480 rows and 179 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40             27ms     32ms     46ms     46ms      0      0
    look up the winner                1000            1.0ms     21ms     39ms     49ms      0      0
    save drawing sheet                  40    1000    5.5ms     29ms     40ms     40ms      0      0
  Reports and searches: 1.8s, 1000 requests (553 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       50            548ms    563ms    566ms    566ms      0      0
    status bar                          50            0.5ms    0.5ms    0.6ms    0.6ms      0      0
    report by basket                   250             21ms     32ms     46ms     53ms      0      0
    report by name                     250             22ms     57ms     62ms     63ms      0      0
    drawing results                    250             22ms     39ms     53ms     59ms      0      0
    search by last name                150            254ms    286ms    300ms    301ms      0      0
  Rush: 10.0s, 17310 requests (1727 a second), 428950 rows saved in 17158 saves (42787 rows and 1711 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet      17158  428950     29ms     32ms     38ms     90ms      0      0
    status bar                         150            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    2            0.0ms    0.5ms    0.5ms    0.5ms      0      0

Programs
  tam-server 0.0.1: CPU 33.9s over 2 runs, peak memory 103 MB, database 5 MB
  tam-client x50: CPU 51.3s in all (1.0s each on average), peak memory 30 MB for the largest
  this machine: windows/amd64, 24 CPUs; the test ran 64.0s

Checks
  PASS  every client paired and showed Connected: 50 clients, in 0.1s
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
  PASS  every request was answered: 32400 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 249 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: save ticket sheet in Ticket entry, 5002ms
  PASS  every client saw the server again after the restart: 40 of 50 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20 24 28 32 36 40 44 48]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 50 of 50
  PASS  no errors in what the programs wrote: 51 programs, 1679 lines, 0 errors

PASSED: all 26 checks
```

## Load test: 20 clients over HTTPS, the server down for 45 s

`go run ./scripts/loadtest -clients 20 -tickets 6000 -baskets 1000 -tls -outage 45s`

```
tam load test: 20 clients, 6000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (windows/amd64, 24 CPUs)
tam-server 0.0.1 answering on https://127.0.0.1:53927
20 tam-client programs answering after 0.1s
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
  Pairing: 0.1s, 20 requests (204 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                20             34ms     41ms     42ms     42ms      0      0
  Setup: 0.0s, 21 requests (1273 a second), 5 rows saved in 1 saves (303 rows and 61 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    2.5ms    2.5ms    2.5ms    2.5ms      0      0
    list prefixes                       20             13ms     14ms     14ms     14ms      0      0
  Ticket entry: 56.9s, 1138 requests (20 a second), 6135 rows saved in 375 saves (108 rows and 7 saves a second)
    240 sheets, one every 3333ms on each client; server killed at 6.7s, back at 51.8s (2 clients crashed and restarted meanwhile); all 209 queued saves sent 5.2s after that; the Wi-Fi of 5 clients dropped at 23.3s for 15.3s (0 saves hung until queued), all caught up 17.3s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  301            0.7ms     43ms     45ms     47ms      0      0
    save ticket sheet                  240    6000    2.5ms    6.0ms     16ms     24ms      0    180
    fix a typo                          35      35    1.5ms    2.0ms    3.5ms    3.5ms      0     29
    status bar                         360            0.5ms    0.5ms    1.0ms    1.0ms      0      0
    admin status page                    2            0.0ms    0.5ms    0.5ms    0.5ms      0      0
    open a sheet saved offline         100            0.5ms    1.0ms   10.0ms     12ms      0      0
    correct a sheet saved offline      100     100    1.5ms     11ms     14ms     16ms      0     85
  Corrections: 0.0s, 20 requests (507 a second), 150 rows saved in 20 saves (3803 rows and 507 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    20     150    7.5ms     35ms     39ms     39ms      0      0
  A client's key deleted: 0.1s, 24 requests (218 a second), 50 rows saved in 2 saves (454 rows and 18 saves a second)
    client-11's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    save while the key is refused        2      50    1.0ms    1.5ms    1.5ms    1.5ms      0      2
    pair again                           1            5.0ms    5.0ms    5.0ms    5.0ms      0      0
    status bar                          20            0.0ms    0.0ms    0.0ms    0.0ms      0      0
  Everyone on the same tickets: 5.0s, 10596 requests (2116 a second), 10375 rows saved in 10375 saves (2072 rows and 2072 saves a second)
    20 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves     10375   10375    9.5ms     11ms     14ms     21ms      0      0
    admin status page                    1            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    status bar                          20            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a ticket everyone saved       200            8.0ms     13ms     13ms     14ms      0      0
  Baskets: 0.1s, 80 requests (1168 a second), 1000 rows saved in 40 saves (14599 rows and 584 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40            1.0ms     51ms     64ms     64ms      0      0
    save basket sheet                   40    1000    2.5ms    3.5ms     12ms     12ms      0      0
  Drawing: 0.2s, 1080 requests (5118 a second), 1000 rows saved in 40 saves (4739 rows and 190 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40            2.0ms     21ms     24ms     24ms      0      0
    look up the winner                1000            1.0ms     15ms     24ms     36ms      0      0
    save drawing sheet                  40    1000    2.5ms    7.0ms     17ms     17ms      0      0
  Reports and searches: 0.4s, 400 requests (930 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       20            113ms    117ms    117ms    117ms      0      0
    report by basket                   100            4.5ms     10ms     12ms     13ms      0      0
    report by name                     100            5.0ms     15ms     16ms     16ms      0      0
    drawing results                    100            6.0ms     10ms     11ms     15ms      0      0
    search by last name                 60             60ms     69ms     70ms     71ms      0      0
    status bar                          20            0.5ms    1.0ms    1.0ms    1.0ms      0      0
  Rush: 10.0s, 17501 requests (1748 a second), 435975 rows saved in 17439 saves (43557 rows and 1742 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet      17439  435975     11ms     13ms     15ms     21ms      0      0
    admin status page                    2            0.0ms    0.5ms    0.5ms    0.5ms      0      0
    status bar                          60            0.5ms    0.5ms    0.5ms    0.5ms      0      0

Programs
  tam-server 0.0.1: CPU 22.6s over 2 runs, peak memory 73 MB, database 4 MB
  tam-client x20: CPU 40.2s in all (2.0s each on average), peak memory 31 MB for the largest
  this machine: windows/amd64, 24 CPUs; the test ran 73.8s

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
  PASS  every request was answered: 30880 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 296 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: counts report in Reports and searches, 117ms
  PASS  every client saw the server again after the restart: 17 of 20 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 20 of 20
  PASS  no errors in what the programs wrote: 21 programs, 917 lines, 0 errors

PASSED: all 26 checks
```


## Nix package and NixOS test

`nix flake check -L`: the package build runs the unit tests in the Nix sandbox, then the NixOS test starts three machines: a server, a client that finds it by its announcement and pairs with it, and a server with its own HTTPS certificate.

```
Unit tests in the Nix build sandbox:
ok         ticket-auction-manager/tam-go/internal/admin    1.358s
ok         ticket-auction-manager/tam-go/internal/client   10.409s
ok         ticket-auction-manager/tam-go/internal/config   0.045s
ok         ticket-auction-manager/tam-go/internal/db       0.466s
ok         ticket-auction-manager/tam-go/internal/discovery        0.010s
ok         ticket-auction-manager/tam-go/internal/httpx    0.005s
ok         ticket-auction-manager/tam-go/internal/presence 0.006s
ok         ticket-auction-manager/tam-go/internal/remote   0.014s
ok         ticket-auction-manager/tam-go/internal/server   2.073s
ok         ticket-auction-manager/tam-go/internal/store    2.693s
ok         ticket-auction-manager/tam-go/internal/sync     0.460s
ok         ticket-auction-manager/tam-go/internal/tlscert  0.014s

NixOS test:
subtest: the client serves the web app
(finished: subtest: the client serves the web app, in 0.06 seconds)
subtest: the server answers through its open port
(finished: subtest: the server answers through its open port, in 0.03 seconds)
subtest: the client finds the server by its announcement
(finished: subtest: the client finds the server by its announcement, in 4.08 seconds)
subtest: the client pairs and saves through the server
(finished: subtest: the client pairs and saves through the server, in 0.29 seconds)
subtest: the ticket is on the server
(finished: subtest: the ticket is on the server, in 0.72 seconds)
subtest: both keep their data over a restart
(finished: subtest: both keep their data over a restart, in 2.80 seconds)
subtest: a server with its own certificate serves it
(finished: subtest: a server with its own certificate serves it, in 0.14 seconds)
subtest: Shut Down TAM stops the client until it is started again
(finished: subtest: Shut Down TAM stops the client until it is started again, in 2.18 seconds)
test script finished in 30.60s
```
