package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

// soakSample is what the programs used after one soak round.
type soakSample struct {
	round         int
	at            time.Duration // since the soak began
	serverMem     uint64        // working set (resident memory)
	serverHandles int           // open handles (file descriptors)
	clientMem     uint64        // the largest working set of a client
	clientHandles int           // the clients' open handles together
	db, wal       int64         // the server's database and its write-ahead log
}

// soak keeps the event going for -soak after the main run, in rounds of
// about half a minute: every client corrects a few of its tickets, reads
// the counts, a report and a search, and a quarter of the clients, in turn,
// lose their Wi-Fi for a while. After each round the server's data is
// compared with what was saved, and the memory and open handles of every
// program and the size of the database are noted; a leak shows as growth
// from the first rounds to the last. The server runs through the whole
// soak without a restart, so its numbers add up.
func (t *test) soak() {
	ph := t.newPhase("Soak")
	deals := deal(t.ev.ticketSheets, len(t.clients), 0)
	began := time.Now()
	stop := began.Add(t.o.soak)
	for round := 1; time.Now().Before(stop); round++ {
		var group []*client
		for _, l := range t.clients {
			if l.n%4 == round%4 {
				group = append(group, l)
			}
		}
		var wifi sync.WaitGroup
		wifi.Add(1)
		go func() {
			defer wifi.Done()
			time.Sleep(4 * time.Second)
			windows := make([]int, len(group))
			for i, l := range group {
				windows[i] = l.openAway(time.Now().Add(-5 * time.Second))
				l.relay.setDown(true)
			}
			time.Sleep(8 * time.Second)
			var back sync.WaitGroup
			for i, l := range group {
				l.relay.setDown(false)
				back.Add(1)
				go func() {
					defer back.Done()
					if !t.untilCaughtUp(l) {
						t.problems.add("soak", 1, "round %d: %s did not send what it queued within %s of its Wi-Fi coming back", round, l.prog.name, t.o.settle)
					}
					l.closeAway(windows[i], time.Now())
				}()
			}
			back.Wait()
		}()
		t.each(func(l *client) { l.soakRound(t, ph, deals[l.n-1]) })
		wifi.Wait()
		t.settle(fmt.Sprintf("after soak round %d", round))

		if d, err := t.diffServer(); err != nil {
			t.problems.add("soak", 1, "round %d: the server's data could not be read: %v", round, err)
		} else if !d.clean() {
			t.problems.add("soak", 1, "round %d: %s", round, d.summary())
		}
		t.sample(round, time.Since(began))
		s := t.soakSamples[len(t.soakSamples)-1]
		fmt.Printf("  round %d at %s: server %s and %d handles, largest client %s, database %s + %s log\n",
			round, secs(s.at), megabytes(s.serverMem), s.serverHandles, megabytes(s.clientMem), megabytes(uint64(s.db)), megabytes(uint64(s.wal)))
	}
	ph.end = time.Now()
	ph.note = fmt.Sprintf("%d rounds; each client corrected three tickets, read reports and searched per round, a quarter of them lost their Wi-Fi for 8 s", len(t.soakSamples))
}

// soakRound is a client's share of a soak round: three of its sheets opened
// and a row of each corrected, a few seconds apart, then the counts, a
// report and a search.
func (l *client) soakRound(t *test, ph *phase, sheets []sheet) {
	for i := 0; i < 3 && len(sheets) > 0; i++ {
		s := sheets[l.intN(len(sheets))]
		l.lookOwn(t, ph, s)
		k := key{s.prefix, s.from + l.intN(s.size())}
		if cur, _, ok := t.ev.ticketNow(k); ok {
			fix := []store.Ticket{l.fix(cur)}
			if _, err := l.call(ph, "correct a ticket", http.MethodPost, "/api/tickets", fix, nil, 1); err == nil {
				t.ev.savedTickets(l.n, fix)
			}
		}
		time.Sleep(4 * time.Second)
	}
	var counts []store.ReportCountLine
	l.call(ph, "counts report", http.MethodGet, "/api/reports/counts", nil, &counts, 0)
	p := t.ev.prefixes[l.intN(len(t.ev.prefixes))].Prefix
	var byBasket []store.ReportByBasketLine
	l.call(ph, "report by basket", http.MethodGet, "/api/reports/bybasket/"+url.PathEscape(p), nil, &byBasket, 0)
	var found []store.Ticket
	q := url.Values{"first_name": {""}, "last_name": {l.lastName()}, "phone_number": {""}}
	l.call(ph, "search by last name", http.MethodGet, "/api/search/tickets?"+q.Encode(), nil, &found, 0)
}

// lookOwn opens a sheet and notes every row this client saved last that
// does not show what it saved. Rows other clients changed may rightly be
// older on a client that lost its Wi-Fi.
func (l *client) lookOwn(t *test, ph *phase, s sheet) {
	var shown []store.Ticket
	if _, err := l.call(ph, "open ticket sheet", http.MethodGet, s.path("tickets"), nil, &shown, 0); err != nil {
		return
	}
	if n, first := t.ev.staleFor(shown, l.n); n > 0 {
		t.problems.add("stale sheet", n, "%s: %s", l.prog.name, first)
	}
}

// sample notes what the programs use after a soak round.
func (t *test) sample(round int, at time.Duration) {
	s := soakSample{round: round, at: at}
	if !t.server.external {
		if cmd, _ := t.server.current(); cmd != nil {
			s.serverMem, s.serverHandles, _ = usageNow(cmd.Process.Pid)
		}
		for name, size := range map[string]*int64{"tam-remote.db": &s.db, "tam-remote.db-wal": &s.wal} {
			if fi, err := os.Stat(filepath.Join(t.server.dir, name)); err == nil {
				*size = fi.Size()
			}
		}
	}
	for _, l := range t.clients {
		if cmd, _ := l.prog.current(); cmd != nil {
			mem, handles, _ := usageNow(cmd.Process.Pid)
			s.clientMem = max(s.clientMem, mem)
			s.clientHandles += handles
		}
	}
	t.mu.Lock()
	t.soakSamples = append(t.soakSamples, s)
	t.mu.Unlock()
}

// checkSoak turns the soak's rounds into verdicts. What a program uses may
// wander; it must not keep growing: the median of the last third of the
// rounds is compared with that of the first third.
func (t *test) checkSoak() {
	samples := t.soakSamples
	t.record("the data was right after every soak round", t.problemFree("soak"), "%d rounds%s", len(samples), t.problemText("soak"))
	if len(samples) < 6 {
		t.record("the soak ran long enough to judge growth", false, "%d rounds; give -soak at least 5m", len(samples))
		return
	}
	third := len(samples) / 3
	median := func(part []soakSample, v func(soakSample) float64) float64 {
		vals := make([]float64, len(part))
		for i, s := range part {
			vals[i] = v(s)
		}
		slices.Sort(vals)
		return vals[len(vals)/2]
	}
	level := func(name string, v func(soakSample) float64, slack float64, unit string) {
		first, last := median(samples[:third], v), median(samples[len(samples)-third:], v)
		t.record(name, last <= first*1.5 || last-first <= slack,
			"median %.0f%s over the first %d rounds, %.0f%s over the last %d", first, unit, third, last, unit, third)
	}
	mb := func(n uint64) float64 { return float64(n) / (1 << 20) }
	if !t.server.external {
		level("the server's memory stayed level over the soak", func(s soakSample) float64 { return mb(s.serverMem) }, 32, " MB")
		level("the server's open handles stayed level over the soak", func(s soakSample) float64 { return float64(s.serverHandles) }, 64, "")
		var wal int64
		for _, s := range samples {
			wal = max(wal, s.wal)
		}
		t.record("the server's write-ahead log stayed bounded", wal <= 64<<20, "at most %s", megabytes(uint64(wal)))
	}
	level("the clients' memory stayed level over the soak", func(s soakSample) float64 { return mb(s.clientMem) }, 32, " MB")
	level("the clients' open handles stayed level over the soak", func(s soakSample) float64 { return float64(s.clientHandles) }, 64*float64(len(t.clients)), "")
}

// soakTable writes the samples of the soak for the report.
func (t *test) soakTable(b *strings.Builder) {
	if len(t.soakSamples) == 0 {
		return
	}
	fmt.Fprintf(b, "\nSoak\n    %5s %8s %10s %8s %10s %9s %9s %9s\n", "round", "at", "server", "handles", "client", "handles", "database", "log")
	for _, s := range t.soakSamples {
		fmt.Fprintf(b, "    %5d %8s %10s %8d %10s %9d %9s %9s\n", s.round, secs(s.at), megabytes(s.serverMem), s.serverHandles,
			megabytes(s.clientMem), s.clientHandles, megabytes(uint64(s.db)), megabytes(uint64(s.wal)))
	}
}
