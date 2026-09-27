package main

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

// test is one load test run.
type test struct {
	o        options
	work     string
	password string
	server   *program
	laptops  []*laptop
	ev       *event
	admin    *adminPage
	version  string // what the server reports as its version

	problems problems
	outage   struct {
		stoppedAt, backAt, caughtUpAt time.Time
		restartErr                    error
		peakBacklog                   int // saves queued on all laptops together, at most
	}

	mu     sync.Mutex
	phases []*phase
	cur    *phase
	checks []check

	stopPollers chan struct{}
	pollers     sync.WaitGroup
	stopOnce    sync.Once
}

func newTest(o options, work string) *test {
	return &test{o: o, work: work, ev: newEvent(o), stopPollers: make(chan struct{})}
}

// newPhase starts a phase. The status bars and the admin page record their
// requests in whichever phase is running.
func (t *test) newPhase(name string) *phase {
	ph := &phase{name: name, start: time.Now()}
	t.mu.Lock()
	t.phases = append(t.phases, ph)
	t.cur = ph
	t.mu.Unlock()
	fmt.Printf("%s...\n", name)
	return ph
}

func (t *test) current() *phase {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur
}

// each runs fn for every laptop at the same time and waits for all.
func (t *test) each(fn func(l *laptop)) {
	var wg sync.WaitGroup
	for _, l := range t.laptops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(l)
		}()
	}
	wg.Wait()
}

func (t *test) run() error {
	if err := t.start(); err != nil {
		return err
	}
	if err := t.pair(); err != nil {
		return err
	}
	// From here on every laptop's status bar polls, and so does an open
	// admin page.
	for _, l := range t.laptops {
		t.pollers.Add(1)
		go func() {
			defer t.pollers.Done()
			l.statusBar(t, t.stopPollers)
		}()
	}
	t.pollers.Add(1)
	go func() {
		defer t.pollers.Done()
		t.admin.watch(t, t.stopPollers)
	}()

	if err := t.setup(); err != nil {
		return err
	}
	t.enterTickets()
	t.settle("after ticket entry")
	t.correct()
	t.flatOut("Baskets", 0, (*laptop).enterBaskets, t.ev.basketSheets)
	t.settle("after the baskets")
	// The drawing sheets go to other laptops than the ones that entered the
	// baskets.
	t.flatOut("Drawing", 1, (*laptop).draw, t.ev.basketSheets)
	t.settle("after the drawing")
	ph := t.newPhase("Reports and searches")
	t.each(func(l *laptop) { l.readReports(t, ph) })
	ph.end = time.Now()

	t.checkData("after the drawing")
	if t.o.rush > 0 {
		t.rush()
		t.settle("after the rush")
		t.checkServer("after the rush")
	}
	t.checkLaptops()
	t.checkPresence()
	t.checkRequests()
	return nil
}

// start builds or finds the programs, starts the server and the laptops'
// clients.
func (t *test) start() error {
	var serverPath, clientPath string
	var err error
	if t.o.bin != "" {
		serverPath, clientPath, err = programsIn(t.o.bin)
	} else {
		began := time.Now()
		serverPath, clientPath, err = build(filepath.Join(t.work, "bin"))
		if err == nil {
			fmt.Printf("built tam-server and tam-client in %s\n", secs(time.Since(began)))
		}
	}
	if err != nil {
		return err
	}
	ports, err := freePorts(1 + t.o.laptops)
	if err != nil {
		return err
	}
	if t.o.server != "" {
		t.password = t.o.password
		t.server, err = externalServer(t.o)
	} else {
		t.password = randomPassword()
		t.server, err = newProgram("tam-server", serverPath, filepath.Join(t.work, "server"), ports[0],
			[]string{"-tray=false", "-announce=false"}, []string{"TAM_PWD=" + t.password})
	}
	if err != nil {
		return err
	}
	if err := t.server.start(); err != nil {
		return err
	}
	var root struct {
		Version string `json:"version"`
	}
	if res, err := web.Get(t.server.url + "/api"); err == nil {
		decodeJSON(res, &root)
	}
	t.version = root.Version
	t.admin = &adminPage{url: t.server.url, password: t.password}
	fmt.Printf("tam-server %s answering on %s\n", t.version, t.server.url)

	began := time.Now()
	t.laptops = make([]*laptop, t.o.laptops)
	for i := range t.laptops {
		name := fmt.Sprintf("laptop-%02d", i+1)
		p, err := newProgram(name, clientPath, filepath.Join(t.work, name), ports[1+i], []string{"-open=false", "-tray=false"}, nil)
		if err != nil {
			return err
		}
		t.laptops[i] = &laptop{n: i + 1, prog: p, rng: rand.New(rand.NewPCG(t.o.seed, uint64(i+2)))}
	}
	var failed atomic.Value
	t.each(func(l *laptop) {
		if err := l.prog.start(); err != nil {
			failed.Store(err)
		}
	})
	if err, _ := failed.Load().(error); err != nil {
		return err
	}
	fmt.Printf("%d tam-client programs answering after %s\n", len(t.laptops), secs(time.Since(began)))
	return nil
}

// pair pairs every laptop with the server through its Settings route, all
// at once, and waits until each shows Connected.
func (t *test) pair() error {
	ph := t.newPhase("Pairing")
	defer func() { ph.end = time.Now() }()
	body := map[string]any{"host": t.server.host, "port": t.server.port, "password": t.password}
	t.each(func(l *laptop) {
		l.call(ph, "pair with the server", http.MethodPost, "/api/pair", body, nil, 0)
	})
	if _, errs, _, _, first := ph.rec.totals(); errs > 0 {
		return fmt.Errorf("%d laptops could not pair: %v", errs, first)
	}
	deadline := time.Now().Add(30 * time.Second)
	for _, l := range t.laptops {
		for {
			st, err := l.peek()
			if err == nil && st.State == "connected" {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s did not show Connected within 30s of pairing (%+v, %v)", l.prog.name, st, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.record("every laptop paired and showed Connected", true, "%d laptops, in %s", len(t.laptops), secs(time.Since(ph.start)))
	return nil
}

// setup saves the prefixes from the first laptop; every laptop then opens a
// form, which lists them.
func (t *test) setup() error {
	ph := t.newPhase("Setup")
	defer func() { ph.end = time.Now() }()
	if _, err := t.laptops[0].call(ph, "save prefixes", http.MethodPost, "/api/prefixes", t.ev.prefixes, nil, len(t.ev.prefixes)); err != nil {
		return err
	}
	t.each(func(l *laptop) {
		var ps []store.Prefix
		if _, err := l.call(ph, "list prefixes", http.MethodGet, "/api/prefixes", nil, &ps, 0); err == nil && len(ps) != len(t.ev.prefixes) {
			t.problems.add("report", 1, "%s listed %d prefixes, %d were saved", l.prog.name, len(ps), len(t.ev.prefixes))
		}
	})
	return nil
}

// outageOn reports whether the server is killed during ticket entry: a
// server elsewhere only when -kill and -restart say how.
func (t *test) outageOn() bool {
	return t.o.outage > 0 && (!t.server.external || t.server.killCmd != "" && t.server.startCmd != "")
}

// enterTickets is the ticket entry, paced to fill -entry, with the server
// killed a quarter of the way in and started again after -outage.
func (t *test) enterTickets() {
	ph := t.newPhase("Ticket entry")
	deals := deal(t.ev.ticketSheets, len(t.laptops), 0)
	most := 0
	for _, d := range deals {
		most = max(most, len(d))
	}
	pace := time.Duration(0)
	if most > 0 {
		pace = t.o.entry / time.Duration(most)
	}
	var saved atomic.Int64
	back, over := make(chan struct{}), make(chan struct{})
	caughtUp := make(chan struct{})
	if t.outageOn() {
		go t.cutPower(&saved, max(1, len(t.ev.ticketSheets)/4), back, over)
		go t.watchCatchUp(back, caughtUp)
	} else {
		close(back)
		close(over)
		close(caughtUp)
	}
	t.each(func(l *laptop) {
		var revisit sync.WaitGroup
		if t.outageOn() {
			revisit.Add(1)
			go func() {
				defer revisit.Done()
				l.revisit(t, ph, back)
			}()
		}
		l.enterTickets(t, ph, deals[l.n-1], pace, &saved)
		revisit.Wait()
	})
	<-over
	<-caughtUp
	ph.end = time.Now()
	if t.outageOn() {
		o := t.outage
		ph.note = fmt.Sprintf("%d sheets, one every %s on each laptop; server killed at %s, back at %s",
			len(t.ev.ticketSheets), ms(pace), secs(o.stoppedAt.Sub(ph.start)), secs(o.backAt.Sub(ph.start)))
		if !o.caughtUpAt.IsZero() {
			ph.note += fmt.Sprintf("; all %d queued saves sent %s after that", o.peakBacklog, secs(o.caughtUpAt.Sub(o.backAt)))
		}
	} else {
		ph.note = fmt.Sprintf("%d sheets, one every %s on each laptop", len(t.ev.ticketSheets), ms(pace))
	}
}

// cutPower kills the server once saved reaches after, and starts it again
// with the same data after -outage.
func (t *test) cutPower(saved *atomic.Int64, after int, back, over chan struct{}) {
	defer close(over)
	for saved.Load() < int64(after) {
		time.Sleep(10 * time.Millisecond)
	}
	t.outage.stoppedAt = time.Now()
	t.server.kill()
	fmt.Printf("  server killed after %d of %d sheets; starting it again in %s\n", saved.Load(), len(t.ev.ticketSheets), t.o.outage)
	time.Sleep(t.o.outage)
	if err := t.server.start(); err != nil {
		t.outage.restartErr = err
	}
	t.outage.backAt = time.Now()
	fmt.Println("  server started again")
	close(back)
}

// watchCatchUp notes when each laptop first shows Connected with nothing
// queued after the restart, and when all of them have.
func (t *test) watchCatchUp(back <-chan struct{}, done chan struct{}) {
	defer close(done)
	<-back
	deadline := time.Now().Add(t.o.settle)
	for time.Now().Before(deadline) {
		all, waiting := true, 0
		for _, l := range t.laptops {
			st, err := l.peek()
			if err != nil {
				all = false
				continue
			}
			waiting += st.Pending
			l.mu.Lock()
			if l.caughtUp.IsZero() && st.State == "connected" && st.Pending == 0 {
				l.caughtUp = time.Now()
			}
			all = all && !l.caughtUp.IsZero()
			l.mu.Unlock()
		}
		t.outage.peakBacklog = max(t.outage.peakBacklog, waiting)
		if all {
			t.outage.caughtUpAt = time.Now()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// settle waits until every laptop shows Connected with nothing queued.
func (t *test) settle(when string) {
	deadline := time.Now().Add(t.o.settle)
	for {
		away, waiting := 0, 0
		for _, l := range t.laptops {
			st, err := l.peek()
			if err != nil || st.State != "connected" {
				away++
			}
			if err == nil {
				waiting += st.Pending
			}
		}
		if away == 0 && waiting == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.problems.add("settle", 1, "%s: %s later %d laptops were not connected and %d saves were still queued", when, t.o.settle, away, waiting)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// correct is the information desk: every 40th ticket gets a new phone
// number, saved from another laptop than the one that saved it last.
func (t *test) correct() {
	ph := t.newPhase("Corrections")
	rng := rand.New(rand.NewPCG(t.o.seed, 0))
	jobs := make([][]store.Ticket, len(t.laptops))
	n := 0
	for _, s := range t.ev.ticketSheets {
		for id := s.from; id <= s.to; id++ {
			if n++; n%40 != 0 {
				continue
			}
			cur, writer, ok := t.ev.ticketNow(key{s.prefix, id})
			if !ok {
				continue
			}
			desk := (writer - 1 + len(t.laptops)/2) % len(t.laptops)
			if len(t.laptops) > 1 && desk == writer-1 {
				desk = (desk + 1) % len(t.laptops)
			}
			jobs[desk] = append(jobs[desk], corrected(cur, rng))
		}
	}
	t.each(func(l *laptop) { l.saveInBatches(t, ph, "save corrections", jobs[l.n-1]) })
	ph.end = time.Now()
	t.settle("after the corrections")
}

// flatOut deals sheets out and has every laptop work through its share as
// fast as it can.
func (t *test) flatOut(name string, offset int, work func(*laptop, *test, *phase, []sheet), sheets []sheet) {
	ph := t.newPhase(name)
	deals := deal(sheets, len(t.laptops), offset)
	t.each(func(l *laptop) { work(l, t, ph, deals[l.n-1]) })
	ph.end = time.Now()
}

// rush has every laptop save its ticket sheets again, as last saved, as fast
// as it can for -rush: the most the server takes.
func (t *test) rush() {
	ph := t.newPhase("Rush")
	deals := deal(t.ev.ticketSheets, len(t.laptops), 0)
	stop := time.Now().Add(t.o.rush)
	t.each(func(l *laptop) {
		mine := deals[l.n-1]
		for i := 0; len(mine) > 0 && time.Now().Before(stop); i++ {
			rows := t.ev.saved(mine[i%len(mine)])
			if _, err := l.call(ph, "save ticket sheet", http.MethodPost, "/api/tickets", rows, nil, len(rows)); err == nil {
				t.ev.savedTickets(l.n, rows)
			}
		}
	})
	ph.end = time.Now()
}

// stopAll stops the pollers, the laptops (the way their Shut Down button
// does) and the server.
func (t *test) stopAll() {
	t.stopOnce.Do(func() {
		close(t.stopPollers)
		t.pollers.Wait()
		t.each(func(l *laptop) { l.prog.shutDown() })
		if t.server != nil && !t.server.external {
			t.server.kill()
		}
	})
}
