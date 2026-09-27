# Test results

The printed results of the tests, as the tools print them. CI prints the same for every push, on the run's page on GitHub (Actions), and keeps them as files under Artifacts; these are the longer runs, made on a real machine. [Running the tests](../README.md#running-the-tests) has the commands.

- Date: 2026-09-27
- Code: commit `ad5a592` (branch parity, the pull request)
- Machine: Windows 11, AMD Ryzen 9 7900X3D (12 cores, 24 threads), NVMe SSD
- Tools: Go 1.27.1, Node 24, pnpm 12, Python 3.13; Nix 2.35 in the `nixos/nix` container, with KVM
- The compatibility run prints the name of the computer its server runs on; here it reads `this-computer`.

## Unit tests

`go test -count=1 ./...`

```
?   	ticket-auction-manager/tam-go/cmd/tam-client	[no test files]
?   	ticket-auction-manager/tam-go/cmd/tam-server	[no test files]
ok  	ticket-auction-manager/tam-go/internal/admin	1.183s
ok  	ticket-auction-manager/tam-go/internal/client	6.094s
ok  	ticket-auction-manager/tam-go/internal/config	0.498s
ok  	ticket-auction-manager/tam-go/internal/db	0.518s
?   	ticket-auction-manager/tam-go/internal/desktop	[no test files]
ok  	ticket-auction-manager/tam-go/internal/discovery	0.549s
?   	ticket-auction-manager/tam-go/internal/env	[no test files]
ok  	ticket-auction-manager/tam-go/internal/httpx	0.394s
ok  	ticket-auction-manager/tam-go/internal/remote	0.581s
ok  	ticket-auction-manager/tam-go/internal/server	1.301s
ok  	ticket-auction-manager/tam-go/internal/store	1.149s
ok  	ticket-auction-manager/tam-go/internal/sync	0.301s
ok  	ticket-auction-manager/tam-go/internal/tlscert	0.607s
?   	ticket-auction-manager/tam-go/scripts/loadtest	[no test files]
```

## Compatibility with the original tam

`bash scripts/compat/run.sh`: the original FastAPI server with the Go client's compatibility tests, then `tam-server` with the original SvelteKit client and the Go client, each saving through one client and reading through the other.

```
original at 19eab77 (change): Embedding TLS policy
--- Go client tests against the original server
=== RUN   TestCompatEverything
2026/09/27 13:03:13 paired with 127.0.0.1 (127.0.0.1:8010)
2026/09/27 13:03:13 server 127.0.0.1: connected
2026/09/27 13:03:13 mirror refreshed from 127.0.0.1: 0 prefixes, 0 tickets, 0 baskets
--- PASS: TestCompatEverything (0.15s)
PASS
ok  	ticket-auction-manager/tam-go/internal/client	0.235s
--- building the original client
--- driving both clients against the Go server
ok  original client: standalone to begin with
ok  original client: settings saved
ok  original client: key created on the Go server
ok  original client: key stored
ok  original client sees the Go server: {'authenticated': True, 'healthy': True, 'name': 'this-computer', 'version': '0.0.1', 'whoami': 'TAM Server'}
ok  go client: paired ({'message': 'Paired with this-computer', 'server': '127.0.0.1:8011'})
ok  go client: connected ({'mode': 'remote', 'state': 'connected', 'server': '127.0.0.1:8011', 'server_name': 'this-computer', 'pending': 0, 'failed': 0, 'last_ok': '2026-09-27T17:03:48Z'})
ok  original: prefix saved
ok  original: tickets saved
ok  go client reads the original's tickets: [{'prefix': 'X28628', 't_id': 1, 'first_name': 'Ann', 'last_name': 'Both', 'phone_number': '555-1', 'pref': 'CALL'}, {'prefix': 'X28628', 't_id': 2, 'first_name': 'Ben', 'last_name': 'Both', 'phone_number': '555-2', 'pref': 'TEXT'}]
ok  go client lists the original's prefix
ok  go client: basket saved
ok  original reads the go client's basket: [{'prefix': 'X28628', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 0}]
ok  original: winner saved
ok  go client report shows the winner: [{'prefix': 'X28628', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 2, 'last_name': 'Both', 'first_name': 'Ben', 'phone_number': '555-2', 'pref': 'TEXT'}]
ok  original report: [{'last_name': 'Both', 'first_name': 'Ben', 'phone_number': '555-2', 'pref': 'TEXT', 'prefix': 'X28628', 'b_id': 1, 'description': 'Wine', 'donors': 'Smiths', 'winning_ticket': 2}]
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
tam-server 0.0.1 answering on http://127.0.0.1:61706
50 tam-client programs answering after 0.3s
Pairing...
Setup...
Ticket entry...
  server killed after 98 of 360 sheets; starting it again in 8s
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
  Pairing: 0.1s, 50 requests (345 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                50             57ms     77ms     79ms     79ms      0      0
  Setup: 0.0s, 51 requests (1672 a second), 5 rows saved in 1 saves (164 rows and 33 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    2.0ms    2.0ms    2.0ms    2.0ms      0      0
    list prefixes                       50             25ms     27ms     28ms     28ms      0      0
  Ticket entry: 44.9s, 1798 requests (40 a second), 9180 rows saved in 540 saves (205 rows and 12 saves a second)
    360 sheets, one every 5000ms on each client; server killed at 5.1s, back at 13.1s (2 clients crashed and restarted meanwhile); all 125 queued saves sent 4.8s after that; the Wi-Fi of 12 clients dropped at 20.1s for 17.0s (12 saves hung until queued), all caught up 7.8s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  444             16ms     61ms     95ms    103ms      0      0
    save ticket sheet                  360    9000    7.1ms     40ms   5002ms   5002ms      0    134
    fix a typo                          61      61    2.0ms     13ms     17ms     31ms      0     20
    status bar                         700            0.5ms    1.0ms    1.5ms    2.0ms      0      0
    admin status page                    7            0.5ms    2.5ms    2.5ms    2.5ms      0      0
    open a sheet saved offline         107            0.5ms     13ms     30ms     40ms      0      0
    correct a sheet saved offline      107     107    1.5ms     13ms     19ms     29ms      0     69
    type a row again after a slow save      12      12    1.5ms    1.5ms    1.5ms    1.5ms      0     12
  Corrections: 0.0s, 50 requests (1351 a second), 225 rows saved in 50 saves (6081 rows and 1351 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    50     225     25ms     35ms     36ms     36ms      0      0
  A client's key deleted: 0.1s, 55 requests (518 a second), 50 rows saved in 2 saves (471 rows and 19 saves a second)
    client-26's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.0ms    0.0ms    0.0ms    0.0ms      0      0
    save while the key is refused        2      50    1.0ms    1.5ms    1.5ms    1.5ms      0      2
    pair again                           1            2.5ms    2.5ms    2.5ms    2.5ms      0      0
    status bar                          50            0.5ms    1.0ms    1.0ms    1.0ms      0      0
    admin status page                    1            1.0ms    1.0ms    1.0ms    1.0ms      0      0
  Everyone on the same tickets: 5.0s, 12298 requests (2450 a second), 11747 rows saved in 11747 saves (2341 rows and 2341 saves a second)
    50 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves     11747   11747     21ms     24ms     27ms     92ms      0      0
    status bar                          50            0.0ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    1            0.0ms    0.0ms    0.0ms    0.0ms      0      0
    open a ticket everyone saved       500             21ms     25ms     26ms     27ms      0      0
  Baskets: 0.1s, 80 requests (1252 a second), 1000 rows saved in 40 saves (15654 rows and 626 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40             20ms     59ms     62ms     62ms      0      0
    save basket sheet                   40    1000   10.0ms     26ms     33ms     33ms      0      0
  Drawing: 0.2s, 1080 requests (5131 a second), 1000 rows saved in 40 saves (4751 rows and 190 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40             27ms     46ms     53ms     53ms      0      0
    look up the winner                1000            1.0ms     22ms     34ms     50ms      0      0
    save drawing sheet                  40    1000     16ms     33ms     43ms     43ms      0      0
  Reports and searches: 2.0s, 1000 requests (510 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       50            586ms    606ms    608ms    608ms      0      0
    report by basket                   250             20ms     30ms     34ms     39ms      0      0
    report by name                     250             21ms     56ms     60ms     63ms      0      0
    drawing results                    250             21ms     34ms     41ms     43ms      0      0
    status bar                          50            0.5ms    0.5ms    0.6ms    0.6ms      0      0
    search by last name                150            307ms    332ms    351ms    385ms      0      0
  Rush: 10.0s, 18953 requests (1891 a second), 470025 rows saved in 18801 saves (46904 rows and 1876 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet      18801  470025     26ms     29ms     33ms     85ms      0      0
    status bar                         150            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    admin status page                    2            0.0ms    0.0ms    0.0ms    0.0ms      0      0

Programs
  tam-server 0.0.1: CPU 33.6s over 2 runs, peak memory 101 MB, database 5 MB
  tam-client x50: CPU 49.9s in all (1.0s each on average), peak memory 29 MB for the largest
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
  SKIP  the admin page's Clients table lists every client as connected and caught up: this server's status page answers HTML only
  SKIP  the admin page counts every prefix, ticket and basket: this server's status page answers HTML only
  PASS  every request was answered: 35415 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 237 saves queued, 0 of them while the client could reach the server
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
  PASS  no errors in what the programs wrote: 51 programs, 1666 lines, 0 errors

PASSED: all 24 checks (2 skipped: this server cannot answer them)
```

## Load test: 20 clients over HTTPS, the server down for 45 s

`go run ./scripts/loadtest -clients 20 -tickets 6000 -baskets 1000 -tls -outage 45s`

```
tam load test: 20 clients, 6000 tickets and 1000 baskets in 5 prefixes, sheets of 25 rows (windows/amd64, 24 CPUs)
tam-server 0.0.1 answering on https://127.0.0.1:59045
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
  Pairing: 0.1s, 20 requests (260 a second)
    action                           count    rows   median      p95      p99      max errors queued
    pair with the server                20             45ms     61ms     62ms     62ms      0      0
  Setup: 0.0s, 21 requests (1909 a second), 5 rows saved in 1 saves (455 rows and 91 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save prefixes                        1       5    2.0ms    2.0ms    2.0ms    2.0ms      0      0
    list prefixes                       20            2.5ms    9.0ms    9.0ms    9.0ms      0      0
  Ticket entry: 57.0s, 1138 requests (20 a second), 6135 rows saved in 375 saves (108 rows and 7 saves a second)
    240 sheets, one every 3333ms on each client; server killed at 6.7s, back at 51.8s (2 clients crashed and restarted meanwhile); all 209 queued saves sent 5.2s after that; the Wi-Fi of 5 clients dropped at 23.3s for 15.3s (0 saves hung until queued), all caught up 17.3s after it was back
    action                           count    rows   median      p95      p99      max errors queued
    open ticket sheet                  301            0.5ms     44ms     45ms     51ms      0      0
    save ticket sheet                  240    6000    2.5ms     12ms     16ms     22ms      0    180
    fix a typo                          35      35    1.5ms    8.0ms     12ms     12ms      0     29
    status bar                         360            0.5ms    1.0ms    1.0ms    1.0ms      0      0
    admin status page                    2            0.5ms    1.5ms    1.5ms    1.5ms      0      0
    open a sheet saved offline         100            0.5ms    2.0ms     12ms     13ms      0      0
    correct a sheet saved offline      100     100    1.5ms    3.0ms     14ms     19ms      0     75
  Corrections: 0.0s, 40 requests (939 a second), 150 rows saved in 20 saves (3522 rows and 470 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    save corrections                    20     150     13ms     40ms     43ms     43ms      0      0
    status bar                          20            0.0ms    0.5ms    0.5ms    0.5ms      0      0
  A client's key deleted: 0.1s, 4 requests (37 a second), 50 rows saved in 2 saves (460 rows and 18 saves a second)
    client-11's key deleted on the server; 50 tickets corrected meanwhile, then paired again
    action                           count    rows   median      p95      p99      max errors queued
    open Settings                        1            0.0ms    0.0ms    0.0ms    0.0ms      0      0
    save while the key is refused        2      50    1.0ms    1.0ms    1.0ms    1.0ms      0      2
    pair again                           1            4.5ms    4.5ms    4.5ms    4.5ms      0      0
  Everyone on the same tickets: 5.0s, 10778 requests (2152 a second), 10557 rows saved in 10557 saves (2108 rows and 2108 saves a second)
    20 clients saving the same 10 tickets for 5.0s
    action                           count    rows   median      p95      p99      max errors queued
    save a ticket everyone saves     10557   10557    9.5ms     11ms     14ms     20ms      0      0
    admin status page                    1            0.0ms    0.0ms    0.0ms    0.0ms      0      0
    status bar                          20            0.5ms    0.5ms    0.5ms    0.5ms      0      0
    open a ticket everyone saved       200            7.5ms     12ms     13ms     13ms      0      0
  Baskets: 0.1s, 80 requests (1212 a second), 1000 rows saved in 40 saves (15152 rows and 606 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open basket sheet                   40            1.0ms     45ms     50ms     50ms      0      0
    save basket sheet                   40    1000    2.5ms     11ms     12ms     12ms      0      0
  Drawing: 0.2s, 1080 requests (5653 a second), 1000 rows saved in 40 saves (5234 rows and 209 saves a second)
    action                           count    rows   median      p95      p99      max errors queued
    open drawing sheet                  40            2.0ms     15ms     20ms     20ms      0      0
    look up the winner                1000            1.0ms     12ms     17ms     29ms      0      0
    save drawing sheet                  40    1000    9.0ms     24ms     30ms     30ms      0      0
  Reports and searches: 0.4s, 400 requests (936 a second)
    action                           count    rows   median      p95      p99      max errors queued
    counts report                       20            110ms    114ms    114ms    114ms      0      0
    report by basket                   100            3.5ms   10.0ms     12ms     15ms      0      0
    report by name                     100            5.0ms     16ms     17ms     17ms      0      0
    drawing results                    100            5.5ms     10ms     17ms     18ms      0      0
    search by last name                 60             63ms     72ms     73ms     80ms      0      0
    status bar                          20            0.5ms    0.5ms    1.5ms    1.5ms      0      0
  Rush: 10.0s, 17982 requests (1797 a second), 448000 rows saved in 17920 saves (44758 rows and 1790 saves a second)
    every save changes every row of its sheet
    action                           count    rows   median      p95      p99      max errors queued
    save a changed ticket sheet      17920  448000     11ms     13ms     15ms     54ms      0      0
    admin status page                    2            0.0ms    0.0ms    0.0ms    0.0ms      0      0
    status bar                          60            0.0ms    0.5ms    0.5ms    0.5ms      0      0

Programs
  tam-server 0.0.1: CPU 22.8s over 2 runs, peak memory 73 MB, database 4 MB
  tam-client x20: CPU 40.2s in all (2.0s each on average), peak memory 30 MB for the largest
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
  SKIP  the admin page's Clients table lists every client as connected and caught up: this server's status page answers HTML only
  SKIP  the admin page counts every prefix, ticket and basket: this server's status page answers HTML only
  PASS  every request was answered: 31543 requests, 0 failed
  PASS  the server started again after being killed
  PASS  saves were queued only while the server was out of reach: 286 saves queued, 0 of them while the client could reach the server
  PASS  no page action waited more than 6 s: the slowest: counts report in Reports and searches, 114ms
  PASS  every client saw the server again after the restart: 16 of 20 clients went back to their offline sheets while saves were still queued
  PASS  every sheet showed all its rows
  PASS  a sheet opened again showed what was saved
  PASS  reports, searches and winner lookups match the data
  PASS  the clients sent everything they queued
  PASS  every crashed client came back with its queue: 2 crashed
  PASS  every client whose Wi-Fi dropped sent what it queued: clients [4 8 12 16 20]
  PASS  a client whose key was deleted said so, and pairing again sent its queue
  PASS  tickets everyone saved at once end whole, and every client shows them
  PASS  every client shut down cleanly when asked: 20 of 20
  PASS  no errors in what the programs wrote: 21 programs, 906 lines, 0 errors

PASSED: all 24 checks (2 skipped: this server cannot answer them)
```


## Nix package and NixOS test

`nix flake check -L`: the package build runs the unit tests in the Nix sandbox, then the NixOS test starts three machines: a server, a client that finds it by its announcement and pairs with it, and a server with its own HTTPS certificate.

```
Unit tests in the Nix build sandbox:
ok         ticket-auction-manager/tam-go/internal/admin    1.177s
ok         ticket-auction-manager/tam-go/internal/client   9.384s
ok         ticket-auction-manager/tam-go/internal/config   0.037s
ok         ticket-auction-manager/tam-go/internal/db       0.358s
ok         ticket-auction-manager/tam-go/internal/discovery        0.009s
ok         ticket-auction-manager/tam-go/internal/httpx    0.004s
ok         ticket-auction-manager/tam-go/internal/remote   0.012s
ok         ticket-auction-manager/tam-go/internal/server   2.097s
ok         ticket-auction-manager/tam-go/internal/store    2.631s
ok         ticket-auction-manager/tam-go/internal/sync     0.320s
ok         ticket-auction-manager/tam-go/internal/tlscert  0.012s

NixOS test:
subtest: the client serves the web app
(finished: subtest: the client serves the web app, in 0.05 seconds)
subtest: the server answers through its open port
(finished: subtest: the server answers through its open port, in 0.03 seconds)
subtest: the client finds the server by its announcement
(finished: subtest: the client finds the server by its announcement, in 1.54 seconds)
subtest: the client pairs and saves through the server
(finished: subtest: the client pairs and saves through the server, in 0.22 seconds)
subtest: the ticket is on the server
(finished: subtest: the ticket is on the server, in 0.06 seconds)
subtest: both keep their data over a restart
(finished: subtest: both keep their data over a restart, in 2.72 seconds)
subtest: a server with its own certificate serves it
(finished: subtest: a server with its own certificate serves it, in 0.11 seconds)
subtest: Shut Down TAM stops the client until it is started again
(finished: subtest: Shut Down TAM stops the client until it is started again, in 2.18 seconds)
test script finished in 25.69s
```
