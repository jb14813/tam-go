// Command loadtest runs a whole event through a real tam-server and many
// real tam-client programs on one machine, the way the laptops at a busy
// event use them, then checks that every ticket, basket and winner ended up
// where it belongs. It reports how long each page action took, how much the
// server took per second, and the CPU and memory the programs used.
//
// Run it from the repository root. tam-client embeds the web app, so build
// that first, as for any build (pnpm install and pnpm build in frontend/):
//
//	go run ./scripts/loadtest
//	go run ./scripts/loadtest -laptops 50 -tickets 9000 -baskets 1000
//
// A run, in order:
//
//  1. starts tam-server with a password and one tam-client per laptop, each
//     with a data folder of its own, and pairs every client with the server
//     through its Settings route, as a volunteer does;
//  2. ticket entry: every laptop opens its sheets (ranges of ticket ids)
//     and saves them, paced to fill -entry, fixing a typo now and then and
//     opening a sheet again to see it was kept. A quarter of the way in the
//     server is killed, like a power cut, and started again after -outage;
//     the laptops keep saving meanwhile, and each goes back to correct the
//     sheets it saved offline as soon as it sees the server again;
//  3. an information desk corrects every 40th ticket from another laptop
//     than the one that entered it;
//  4. baskets, the drawing (looking each winner up, as the page does) and
//     the reports and searches, every laptop as fast as it can;
//  5. a rush: every laptop saves its sheets again as fast as it can for
//     -rush, which measures how much the server takes;
//  6. the checks: the server's data, every laptop's own copy, the admin
//     page's Clients table, and the logs.
//
// The exit status is 1 when a check fails. The data folders and logs are
// kept then (and with -keep) and their location is printed.
//
// With -server the laptops run here and use a tam-server already running
// on another machine, over the real network. That server should start with
// no data. -kill and -restart give the commands that kill it and start it
// again for the outage, for example through ssh; without them there is no
// outage.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"time"
)

type options struct {
	laptops, tickets, baskets, prefixes, page int
	entry, outage, rush, settle               time.Duration
	bin                                       string
	keep                                      bool
	seed                                      uint64
	server, password, kill, restart           string
	wifi                                      float64
	wifiDrop, late, crashDown, storm, soak    time.Duration
	crashes                                   int
	tls                                       bool
}

func main() {
	var o options
	flag.IntVar(&o.laptops, "laptops", 20, "simulated laptops; each runs a tam-client of its own")
	flag.IntVar(&o.tickets, "tickets", 6000, "tickets entered in total")
	flag.IntVar(&o.baskets, "baskets", 1000, "baskets entered in total")
	flag.IntVar(&o.prefixes, "prefixes", 5, "prefixes the tickets and baskets are spread over, 1 to 26")
	flag.IntVar(&o.page, "page", 25, "rows per sheet, what one save of a form holds")
	flag.DurationVar(&o.entry, "entry", 40*time.Second, "how long ticket entry takes; the laptops pace their sheets to fill it")
	flag.DurationVar(&o.outage, "outage", 8*time.Second, "how long the server is down during ticket entry; 0 for no outage")
	flag.DurationVar(&o.rush, "rush", 10*time.Second, "how long every laptop saves sheets as fast as it can at the end; 0 to skip")
	flag.DurationVar(&o.settle, "settle", 2*time.Minute, "how long the laptops get to send what they queued")
	flag.StringVar(&o.bin, "bin", "", "folder with the tam-server and tam-client to test; by default both are built from this checkout")
	flag.BoolVar(&o.keep, "keep", false, "keep the data folders and logs of a passing run")
	flag.Uint64Var(&o.seed, "seed", 1, "seed of the made-up event")
	flag.Float64Var(&o.wifi, "wifi", 0.25, "share of the laptops whose Wi-Fi drops once during ticket entry, silently; 0 for none")
	flag.DurationVar(&o.wifiDrop, "wifi-drop", 12*time.Second, "how long their Wi-Fi is gone")
	flag.DurationVar(&o.late, "late", 10*time.Second, "how late data held during a Wi-Fi drop may still arrive once it is back, as TCP retransmits")
	flag.IntVar(&o.crashes, "crashes", 2, "laptops that crash (the program killed) while the server is down, and start again")
	flag.DurationVar(&o.crashDown, "crash-down", 3*time.Second, "how long a crashed laptop stays off")
	flag.DurationVar(&o.storm, "storm", 5*time.Second, "how long every laptop saves the same few tickets at once; 0 to skip")
	flag.BoolVar(&o.tls, "tls", false, "run the server over HTTPS, with the laptops pinning its certificate")
	flag.DurationVar(&o.soak, "soak", 0, "keep the event going this long after the main run, watching the programs' memory, handles and database for growth; 0 to skip")
	flag.StringVar(&o.server, "server", "", "use the tam-server already running at this address (http://host:port) instead of starting one; it should have no data yet")
	flag.StringVar(&o.password, "password", os.Getenv("TAM_PWD"), "with -server: that server's password (default $TAM_PWD)")
	flag.StringVar(&o.kill, "kill", "", "with -server: a command that kills that server, for the outage (run by cmd on Windows, sh elsewhere)")
	flag.StringVar(&o.restart, "restart", "", "with -server: a command that starts that server again with its data")
	flag.Parse()
	if err := o.valid(); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(2)
	}
	os.Exit(run(o))
}

func (o options) valid() error {
	switch {
	case o.laptops < 1:
		return errors.New("-laptops must be at least 1")
	case o.prefixes < 1 || o.prefixes > 26:
		return errors.New("-prefixes must be between 1 and 26")
	case o.tickets < o.prefixes:
		return errors.New("-tickets must be at least -prefixes")
	case o.baskets < o.prefixes:
		return errors.New("-baskets must be at least -prefixes")
	case o.page < 1 || o.page > 300:
		return errors.New("-page must be between 1 and 300, the most a form shows")
	case o.wifi < 0 || o.wifi > 1:
		return errors.New("-wifi must be between 0 and 1")
	case o.crashes < 0:
		return errors.New("-crashes must not be negative")
	case o.server != "" && o.tls:
		return errors.New("with -server, give an https:// address instead of -tls")
	case o.entry < 0 || o.outage < 0 || o.rush < 0 || o.settle <= 0 || o.wifiDrop < 0 || o.late < 0 || o.crashDown < 0 || o.storm < 0 || o.soak < 0:
		return errors.New("durations must not be negative, and -settle must be more than 0")
	case o.server == "" && (o.kill != "" || o.restart != ""):
		return errors.New("-kill and -restart go with -server")
	case (o.kill == "") != (o.restart == ""):
		return errors.New("give both -kill and -restart, or neither")
	case o.server != "" && o.password == "":
		return errors.New("-server needs the server's password: -password or TAM_PWD")
	}
	if o.server != "" {
		u, err := url.Parse(o.server)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Port() == "" {
			return errors.New("-server must look like http://host:port")
		}
	}
	return nil
}

func run(o options) int {
	work, err := os.MkdirTemp("", "tam-loadtest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		return 2
	}
	t := newTest(o, work)

	// Ctrl+C stops every program before leaving.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	go func() {
		<-interrupted
		fmt.Println("\ninterrupted; stopping the programs")
		t.stopAll()
		fmt.Println("data folders and logs:", work)
		os.Exit(130)
	}()

	fmt.Printf("tam load test: %d laptops, %d tickets and %d baskets in %d prefixes, sheets of %d rows (%s/%s, %d CPUs)\n",
		o.laptops, o.tickets, o.baskets, o.prefixes, o.page, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	began := time.Now()
	err = t.run()
	t.stopAll()
	if err == nil {
		t.checkShutdown()
		t.checkLogs()
	}
	t.report(err, time.Since(began))

	passed := err == nil && t.passed()
	if passed && !o.keep {
		os.RemoveAll(work)
	} else {
		fmt.Println("data folders and logs:", work)
	}
	if !passed {
		return 1
	}
	return 0
}
