package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

// laptop is one venue laptop: a tam-client and the volunteer in front of
// its browser.
type laptop struct {
	n    int
	prog *program
	rng  *rand.Rand // only the laptop's own goroutines use it, one at a time

	mu          sync.Mutex
	offline     []sheet     // sheets whose save was queued, once entry has moved past them
	queuedAt    []time.Time // when a save was answered as queued
	reconnected time.Time   // when it saw the server again after the outage
	backlog     int         // saves it still had queued at that moment
	caughtUp    time.Time   // when it first showed Connected with nothing queued after the restart
}

// clientStatus is what GET /api/status answers, the status bar's source.
type clientStatus struct {
	Mode    string `json:"mode"`
	State   string `json:"state"`
	Pending int    `json:"pending"`
	Failed  int    `json:"failed"`
}

// call sends one request to the laptop's tam-client, as its pages do, and
// records it in ph under op (not at all when ph is nil). into, when not
// nil, receives the JSON answer. It reports whether a save was queued.
func (l *laptop) call(ph *phase, op, method, path string, body, into any, rows int) (queued bool, err error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return false, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, l.prog.url+path, rd)
	if err != nil {
		return false, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	res, err := web.Do(req)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err == nil && res.StatusCode != http.StatusOK {
			err = fmt.Errorf("%s answered %d to %s %s: %s", l.prog.name, res.StatusCode, method, path, bytes.TrimSpace(data))
		}
		queued = err == nil && res.Header.Get("X-TAM-Queued") != ""
	}
	took := time.Since(start)
	if err == nil && into != nil {
		if jerr := json.Unmarshal(data, into); jerr != nil {
			err = fmt.Errorf("%s: %s %s: %w", l.prog.name, method, path, jerr)
		}
	}
	if queued {
		l.mu.Lock()
		l.queuedAt = append(l.queuedAt, start)
		l.mu.Unlock()
	}
	if ph != nil {
		ph.rec.add(op, took, err, rows, queued)
	}
	return queued, err
}

// peek reads the laptop's status without recording it.
func (l *laptop) peek() (clientStatus, error) {
	var st clientStatus
	_, err := l.call(nil, "", http.MethodGet, "/api/status", nil, &st, 0)
	return st, err
}

// statusBar polls the status every three seconds, as the bar on every page
// does, until stop is closed.
func (l *laptop) statusBar(t *test, stop <-chan struct{}) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			var st clientStatus
			l.call(t.current(), "status bar", http.MethodGet, "/api/status", nil, &st, 0)
		}
	}
}

// enterTickets works through the laptop's ticket sheets, one every pace:
// open the sheet, type it, save it; now and then fix a typo at once or
// open the sheet again to look at it.
func (l *laptop) enterTickets(t *test, ph *phase, sheets []sheet, pace time.Duration, saved *atomic.Int64) {
	next := time.Now()
	for _, s := range sheets {
		var shown []store.Ticket
		if _, err := l.call(ph, "open ticket sheet", http.MethodGet, s.path("tickets"), nil, &shown, 0); err == nil && len(shown) != s.size() {
			t.problems.add("sheet size", 1, "%s: ticket sheet %s %d-%d showed %d rows", l.prog.name, s.prefix, s.from, s.to, len(shown))
		}
		rows := t.ev.stubs(s)
		queued, err := l.call(ph, "save ticket sheet", http.MethodPost, "/api/tickets", rows, nil, len(rows))
		saved.Add(1)
		if err == nil {
			t.ev.savedTickets(l.n, rows)
		}
		if l.rng.IntN(6) == 0 {
			fix := []store.Ticket{corrected(rows[l.rng.IntN(len(rows))], l.rng)}
			if _, err := l.call(ph, "fix a typo", http.MethodPost, "/api/tickets", fix, nil, 1); err == nil {
				t.ev.savedTickets(l.n, fix)
			}
		}
		if l.rng.IntN(4) == 0 {
			l.look(t, ph, "open ticket sheet", s)
		}
		if queued {
			l.mu.Lock()
			l.offline = append(l.offline, s)
			l.mu.Unlock()
		}
		next = next.Add(pace)
		time.Sleep(time.Until(next))
	}
}

// look opens a sheet and notes every row that does not show what was last
// saved.
func (l *laptop) look(t *test, ph *phase, op string, s sheet) {
	var shown []store.Ticket
	if _, err := l.call(ph, op, http.MethodGet, s.path("tickets"), nil, &shown, 0); err != nil {
		return
	}
	if n, first := t.ev.stale(shown); n > 0 {
		t.problems.add("stale sheet", n, "%s: %s", l.prog.name, first)
	}
}

// revisit waits for the server to come back, then goes back to the sheets
// this laptop saved while it was away, the moment the laptop shows it is
// connected again: open the sheet, correct a row, save.
func (l *laptop) revisit(t *test, ph *phase, back <-chan struct{}) {
	<-back
	deadline := time.Now().Add(t.o.settle)
	for {
		st, err := l.peek()
		if err == nil && st.State == "connected" {
			l.mu.Lock()
			l.reconnected, l.backlog = time.Now(), st.Pending
			l.mu.Unlock()
			break
		}
		if time.Now().After(deadline) {
			t.problems.add("reconnect", 1, "%s did not show the server as connected within %s of its restart", l.prog.name, t.o.settle)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.mu.Lock()
	sheets := append([]sheet(nil), l.offline...)
	l.mu.Unlock()
	for _, s := range sheets[:min(len(sheets), 5)] {
		l.look(t, ph, "open a sheet saved offline", s)
		k := key{s.prefix, s.from + l.rng.IntN(s.size())}
		cur, _, ok := t.ev.ticketNow(k)
		if !ok {
			continue
		}
		fix := []store.Ticket{corrected(cur, l.rng)}
		if _, err := l.call(ph, "correct a sheet saved offline", http.MethodPost, "/api/tickets", fix, nil, 1); err == nil {
			t.ev.savedTickets(l.n, fix)
		}
	}
}

// saveInBatches saves tickets a sheet at a time.
func (l *laptop) saveInBatches(t *test, ph *phase, op string, ts []store.Ticket) {
	for len(ts) > 0 {
		n := min(len(ts), t.o.page)
		if _, err := l.call(ph, op, http.MethodPost, "/api/tickets", ts[:n], nil, n); err == nil {
			t.ev.savedTickets(l.n, ts[:n])
		}
		ts = ts[n:]
	}
}

// enterBaskets opens and saves the laptop's basket sheets as fast as it can.
func (l *laptop) enterBaskets(t *test, ph *phase, sheets []sheet) {
	for _, s := range sheets {
		var shown []store.Basket
		if _, err := l.call(ph, "open basket sheet", http.MethodGet, s.path("baskets"), nil, &shown, 0); err == nil && len(shown) != s.size() {
			t.problems.add("sheet size", 1, "%s: basket sheet %s %d-%d showed %d rows", l.prog.name, s.prefix, s.from, s.to, len(shown))
		}
		cards := t.ev.cards(s)
		if _, err := l.call(ph, "save basket sheet", http.MethodPost, "/api/baskets", cards, nil, len(cards)); err == nil {
			t.ev.savedCards(l.n, cards)
		}
	}
}

// draw opens the laptop's drawing sheets, types each winning ticket (the
// page looks the ticket up to show who won) and saves the sheet.
func (l *laptop) draw(t *test, ph *phase, sheets []sheet) {
	for _, s := range sheets {
		var lines []store.DrawingLine
		if _, err := l.call(ph, "open drawing sheet", http.MethodGet, s.path("drawing"), nil, &lines, 0); err != nil {
			continue
		}
		if len(lines) != s.size() {
			t.problems.add("sheet size", 1, "%s: drawing sheet %s %d-%d showed %d rows", l.prog.name, s.prefix, s.from, s.to, len(lines))
			continue
		}
		for i := range lines {
			k := key{lines[i].Prefix, lines[i].BID}
			lines[i].WinningTicket = t.ev.winner[k]
			var who store.Ticket
			path := fmt.Sprintf("/api/tickets/%s/%d", url.PathEscape(k.prefix), lines[i].WinningTicket)
			if _, err := l.call(ph, "look up the winner", http.MethodGet, path, nil, &who, 0); err == nil {
				if want := t.ev.buyerOf(k.prefix, lines[i].WinningTicket); who != want {
					t.problems.add("report", 1, "%s: winner lookup %s %d showed %s, saved %s", l.prog.name, k.prefix, lines[i].WinningTicket, describe(who), describe(want))
				}
			}
		}
		if _, err := l.call(ph, "save drawing sheet", http.MethodPost, "/api/drawing", lines, nil, len(lines)); err == nil {
			t.ev.savedWinners(l.n, lines)
		}
	}
}

// readReports reads what the office reads at the end: the counts, both
// winners reports and the drawing results of every prefix, and a few
// searches, and compares each with the data.
func (l *laptop) readReports(t *test, ph *phase) {
	var counts []store.ReportCountLine
	if _, err := l.call(ph, "counts report", http.MethodGet, "/api/reports/counts", nil, &counts, 0); err == nil {
		want := t.ev.counts()
		got := map[string]store.ReportCountLine{}
		for _, c := range counts {
			got[c.Prefix] = c
		}
		if len(got) != len(want) {
			t.problems.add("report", 1, "%s: the counts report has %d lines, the data %d", l.prog.name, len(got), len(want))
		}
		for p, w := range want {
			if got[p] != w {
				t.problems.add("report", 1, "%s: counts for %s are %+v, the data says %+v", l.prog.name, p, got[p], w)
			}
		}
	}
	for _, p := range t.ev.prefixes {
		name := url.PathEscape(p.Prefix)
		var byBasket []store.ReportByBasketLine
		if _, err := l.call(ph, "report by basket", http.MethodGet, "/api/reports/bybasket/"+name, nil, &byBasket, 0); err == nil {
			l.compareWinners(t, "report by basket "+p.Prefix, len(byBasket), t.ev.baskets[p.Prefix], func(i int) (string, int, store.Ticket) {
				r := byBasket[i]
				return r.Prefix, r.WinningTicket, store.Ticket{Prefix: r.Prefix, TID: r.WinningTicket, FirstName: r.FirstName, LastName: r.LastName, PhoneNumber: r.PhoneNumber, Pref: r.Pref}
			})
		}
		var byName []store.ReportByNameLine
		if _, err := l.call(ph, "report by name", http.MethodGet, "/api/reports/byname/"+name, nil, &byName, 0); err == nil {
			l.compareWinners(t, "report by name "+p.Prefix, len(byName), t.ev.baskets[p.Prefix], func(i int) (string, int, store.Ticket) {
				r := byName[i]
				return r.Prefix, r.WinningTicket, store.Ticket{Prefix: r.Prefix, TID: r.WinningTicket, FirstName: r.FirstName, LastName: r.LastName, PhoneNumber: r.PhoneNumber, Pref: r.Pref}
			})
		}
		var results []store.DrawingLine
		if _, err := l.call(ph, "drawing results", http.MethodGet, "/api/drawing/"+name, nil, &results, 0); err == nil {
			l.compareWinners(t, "drawing results "+p.Prefix, len(results), t.ev.baskets[p.Prefix], func(i int) (string, int, store.Ticket) {
				r := results[i]
				want := t.ev.buyerOf(r.Prefix, r.WinningTicket)
				return r.Prefix, r.WinningTicket, store.Ticket{Prefix: r.Prefix, TID: r.WinningTicket, FirstName: r.FirstName, LastName: r.LastName, PhoneNumber: r.PhoneNumber, Pref: want.Pref}
			})
		}
	}
	for i := 0; i < 3; i++ {
		last := pick(l.rng, lastNames)
		var found []store.Ticket
		q := url.Values{"first_name": {""}, "last_name": {last}, "phone_number": {""}}
		if _, err := l.call(ph, "search by last name", http.MethodGet, "/api/search/tickets?"+q.Encode(), nil, &found, 0); err == nil {
			if want := t.ev.searchCount(last); len(found) != want {
				t.problems.add("report", 1, "%s: searching %q found %d tickets, the data has %d", l.prog.name, last, len(found), want)
			}
		}
	}
}

// compareWinners checks the lines of a winners report: one per basket, each
// naming the buyer of its winning ticket as last saved.
func (l *laptop) compareWinners(t *test, what string, n, want int, line func(i int) (prefix string, winning int, shown store.Ticket)) {
	if n != want {
		t.problems.add("report", 1, "%s: %s has %d lines, the prefix %d baskets", l.prog.name, what, n, want)
		return
	}
	for i := 0; i < n; i++ {
		prefix, winning, shown := line(i)
		if buyer := t.ev.buyerOf(prefix, winning); shown != buyer {
			t.problems.add("report", 1, "%s: %s names %s for ticket %d, saved %s", l.prog.name, what, describe(shown), winning, describe(buyer))
		}
	}
}
