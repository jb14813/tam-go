package main

import (
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
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
	clients  []*client
	ev       *event
	admin    *adminPage
	version  string // what the server reports as its version

	problems problems
	outage   struct {
		stoppedAt, backAt, caughtUpAt time.Time
		restartErr                    error
		peakBacklog                   int // saves queued on all clients together, at most
		crashed                       int // clients that crashed while the server was down
	}
	wifi struct {
		dropped    []int // the clients whose Wi-Fi dropped
		at, backAt time.Time
		caughtUpAt time.Time
	}
	stalled     atomic.Int64 // saves that hung until the client gave up and queued them
	soakSamples []soakSample

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

// each runs fn for every client at the same time and waits for all.
func (t *test) each(fn func(l *client)) {
	var wg sync.WaitGroup
	for _, l := range t.clients {
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
	// From here on every client's status bar polls, and so does an open
	// admin page.
	for _, l := range t.clients {
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
	t.refuseKey()
	t.storm()
	t.flatOut("Baskets", 0, (*client).enterBaskets, t.ev.basketSheets)
	t.settle("after the baskets")
	// The drawing sheets go to other clients than the ones that entered the
	// baskets.
	t.flatOut("Drawing", 1, (*client).draw, t.ev.basketSheets)
	t.settle("after the drawing")
	ph := t.newPhase("Reports and searches")
	t.each(func(l *client) { l.readReports(t, ph) })
	ph.end = time.Now()

	t.checkData("after the drawing")
	if t.o.rush > 0 {
		t.rush()
		t.settle("after the rush")
		t.checkServer("after the rush")
	}
	if t.o.soak > 0 {
		t.soak()
		t.checkData("after the soak")
		t.checkSoak()
	}
	t.checkClients()
	t.checkPresence()
	t.checkRequests()
	return nil
}

// start builds or finds the programs, starts the server and the clients'
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
	ports, err := freePorts(1)
	if err != nil {
		return err
	}
	if t.o.server != "" {
		t.password = t.o.password
		t.server, err = externalServer(t.o)
	} else {
		t.password = randomPassword()
		args := []string{"-tray=false", "-announce=false"}
		if t.o.tls {
			args = append(args, "-tls")
		}
		t.server, err = newProgram("tam-server", serverPath, filepath.Join(t.work, "server"), ports[0], args, []string{"TAM_PWD=" + t.password})
		if err == nil && t.o.tls {
			t.server.url = "https://127.0.0.1:" + ports[0]
		}
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
	if err := t.prepareClients(clientPath); err != nil {
		return err
	}
	var failed atomic.Value
	t.each(func(l *client) {
		if err := l.prog.start(); err != nil {
			failed.Store(err)
		}
	})
	if err, _ := failed.Load().(error); err != nil {
		return err
	}
	fmt.Printf("%d tam-client programs answering after %s\n", len(t.clients), secs(time.Since(began)))
	return nil
}

func (t *test) prepareClients(clientPath string) (err error) {
	// Relays keep their listeners open before client ports are selected. If
	// created afterward, a relay could claim a client's released reservation.
	relays := make([]*relay, 0, t.o.clients)
	defer func() {
		if err != nil {
			for _, relay := range relays {
				relay.close()
			}
		}
	}()
	for i := 0; i < t.o.clients; i++ {
		r, err := newRelay(net.JoinHostPort(t.server.host, t.server.port), t.o.late, t.o.seed+uint64(i))
		if err != nil {
			return err
		}
		relays = append(relays, r)
	}
	ports, err := freePorts(t.o.clients)
	if err != nil {
		return err
	}
	clients := make([]*client, t.o.clients)
	for i := range clients {
		name := fmt.Sprintf("client-%02d", i+1)
		p, err := newProgram(name, clientPath, filepath.Join(t.work, name), ports[i], []string{"-open=false", "-tray=false"}, nil)
		if err != nil {
			return err
		}
		p.readyPath = "/api/status"
		clients[i] = &client{n: i + 1, prog: p, relay: relays[i], rng: rand.New(rand.NewPCG(t.o.seed, uint64(i+2)))}
	}
	t.clients = clients
	return nil
}

// tls reports whether the server speaks HTTPS.
func (t *test) tls() bool { return strings.HasPrefix(t.server.url, "https:") }

// pair pairs every client with the server through its Settings route, all
// at once, and waits until each is connected with recovery and queued saves done.
func (t *test) pair() error {
	ph := t.newPhase("Pairing")
	defer func() { ph.end = time.Now() }()
	t.each(func(l *client) {
		l.call(ph, "pair with the server", http.MethodPost, "/api/pair", l.pairing(t), nil, 0)
	})
	if _, errs, _, _, first := ph.rec.totals(); errs > 0 {
		return fmt.Errorf("%d clients could not pair: %v", errs, first)
	}
	deadline := time.Now().Add(30 * time.Second)
	for _, l := range t.clients {
		for {
			st, err := l.peek()
			if err == nil && st.caughtUp() {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s was not connected and caught up within 30s of pairing (%+v, %v)", l.prog.name, st, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.record("every client paired, finished recovery and showed Connected", true, "%d clients, in %s", len(t.clients), secs(time.Since(ph.start)))
	return nil
}

// setup saves the prefixes from the first client; every client then opens a
// form, which lists them.
func (t *test) setup() error {
	ph := t.newPhase("Setup")
	defer func() { ph.end = time.Now() }()
	if _, err := t.clients[0].call(ph, "save prefixes", http.MethodPost, "/api/prefixes", t.ev.prefixes, nil, len(t.ev.prefixes)); err != nil {
		return err
	}
	if !t.untilCaughtUp(t.clients[0]) {
		return fmt.Errorf("setup prefixes did not finish recovery/delivery within %s", t.o.settle)
	}
	t.each(func(l *client) {
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
	deals := deal(t.ev.ticketSheets, len(t.clients), 0)
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
	wifiOver := make(chan struct{})
	go t.dropWifi(&saved, len(t.ev.ticketSheets)*3/5, wifiOver)
	t.each(func(l *client) {
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
	<-wifiOver
	ph.end = time.Now()
	ph.note = fmt.Sprintf("%d sheets, one every %s on each client", len(t.ev.ticketSheets), ms(pace))
	if t.outageOn() {
		o := t.outage
		ph.note += fmt.Sprintf("; server killed at %s, back at %s", secs(o.stoppedAt.Sub(ph.start)), secs(o.backAt.Sub(ph.start)))
		if o.crashed > 0 {
			ph.note += fmt.Sprintf(" (%d clients crashed and restarted meanwhile)", o.crashed)
		}
		if !o.caughtUpAt.IsZero() {
			ph.note += fmt.Sprintf("; all %d queued saves sent %s after that", o.peakBacklog, secs(o.caughtUpAt.Sub(o.backAt)))
		}
	}
	if w := t.wifi; len(w.dropped) > 0 {
		ph.note += fmt.Sprintf("; the Wi-Fi of %d clients dropped at %s for %s (%d saves hung until queued)",
			len(w.dropped), secs(w.at.Sub(ph.start)), secs(w.backAt.Sub(w.at)), t.stalled.Load())
		if !w.caughtUpAt.IsZero() {
			ph.note += fmt.Sprintf(", all caught up %s after it was back", secs(w.caughtUpAt.Sub(w.backAt)))
		}
	}
}

// dropWifi takes the Wi-Fi of every so many clients (-wifi) down silently
// once saved reaches after, for -wifi-drop, and waits until each of them has
// sent what it queued meanwhile.
func (t *test) dropWifi(saved *atomic.Int64, after int, over chan struct{}) {
	defer close(over)
	if t.o.wifi <= 0 || t.o.wifiDrop <= 0 {
		return
	}
	every := max(1, int(1/t.o.wifi+0.5))
	var group []*client
	for _, l := range t.clients {
		if l.n%every == 0 {
			group = append(group, l)
		}
	}
	if len(group) == 0 {
		return
	}
	for saved.Load() < int64(after) {
		time.Sleep(10 * time.Millisecond)
	}
	t.wifi.at = time.Now()
	fmt.Printf("  Wi-Fi of %d clients dropping for %s\n", len(group), t.o.wifiDrop)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, l := range group {
		l.dropped = make(chan struct{})
		l.dropNow.Store(true)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// It drops when the client is between opening a sheet and saving
			// it; one with no sheet left loses it where it is.
			select {
			case <-l.dropped:
			case <-time.After(t.o.entry/2 + 5*time.Second):
				if l.dropNow.CompareAndSwap(true, false) {
					l.relay.setDown(true)
				}
			}
			window := l.openAway(time.Now().Add(-5 * time.Second))
			time.Sleep(t.o.wifiDrop)
			l.relay.setDown(false)
			mu.Lock()
			t.wifi.dropped = append(t.wifi.dropped, l.n)
			if now := time.Now(); now.After(t.wifi.backAt) {
				t.wifi.backAt = now
			}
			mu.Unlock()
			if !t.untilCaughtUp(l) {
				t.problems.add("reconnect", 1, "%s did not send what it queued within %s of its Wi-Fi coming back", l.prog.name, t.o.settle)
			}
			l.closeAway(window, time.Now())
		}()
	}
	wg.Wait()
	t.wifi.caughtUpAt = time.Now()
	sort.Ints(t.wifi.dropped)
	fmt.Println("  Wi-Fi back everywhere, queues sent")
}

// untilCaughtUp waits until connection, recovery and queue delivery are ready.
func (t *test) untilCaughtUp(l *client) bool {
	deadline := time.Now().Add(t.o.settle)
	for time.Now().Before(deadline) {
		if st, err := l.peek(); err == nil && st.caughtUp() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// cutPower kills the server once saved reaches after, and starts it again
// with the same data after -outage.
func (t *test) cutPower(saved *atomic.Int64, after int, back, over chan struct{}) {
	defer close(over)
	for saved.Load() < int64(after) {
		time.Sleep(10 * time.Millisecond)
	}
	t.outage.stoppedAt = time.Now()
	for _, l := range t.clients {
		l.outage = l.openAway(t.outage.stoppedAt.Add(-5 * time.Second))
	}
	t.server.kill()
	fmt.Printf("  server killed after %d of %d sheets; starting it again in %s\n", saved.Load(), len(t.ev.ticketSheets), t.o.outage)
	crashed := t.crashSome()
	time.Sleep(time.Until(t.outage.stoppedAt.Add(t.o.outage)))
	<-crashed
	if err := t.server.start(); err != nil {
		t.outage.restartErr = err
	}
	t.outage.backAt = time.Now()
	fmt.Println("  server started again")
	close(back)
}

// crashSome crashes -crashes clients, spread over the lot, while the server
// is down and they have saves queued, and starts them again after
// -crash-down. Each must come back with its queue. The channel it returns
// is closed once all are back.
func (t *test) crashSome() <-chan struct{} {
	done := make(chan struct{})
	n := min(t.o.crashes, len(t.clients))
	if n <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		time.Sleep(t.o.outage / 4)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			l := t.clients[(2*i+1)*len(t.clients)/(2*n)]
			wg.Add(1)
			go func() {
				defer wg.Done()
				before, after, err := l.crash(t.o.crashDown)
				switch {
				case err != nil:
					t.problems.add("crash", 1, "%s did not come back after its crash: %v", l.prog.name, err)
				case after != before:
					t.problems.add("crash", 1, "%s had %d saves queued before its crash and %d after", l.prog.name, before, after)
				}
			}()
		}
		wg.Wait()
		t.outage.crashed = n
		fmt.Printf("  %d clients crashed and started again\n", n)
	}()
	return done
}

// watchCatchUp notes when each client first shows Connected with nothing
// queued after the restart, and when all of them have.
func (t *test) watchCatchUp(back <-chan struct{}, done chan struct{}) {
	defer close(done)
	<-back
	deadline := time.Now().Add(t.o.settle)
	for time.Now().Before(deadline) {
		all, waiting := true, 0
		for _, l := range t.clients {
			st, err := l.peek()
			if err != nil {
				all = false
				continue
			}
			waiting += st.Pending
			l.mu.Lock()
			// The outage's window closes the first time the client shows
			// Connected with nothing queued.
			if w := &l.away[l.outage]; w.to.IsZero() && st.caughtUp() {
				w.to = time.Now()
			}
			all = all && !l.away[l.outage].to.IsZero()
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

// settle waits until every client's recovery and queued saves have finished.
func (t *test) settle(when string) {
	deadline := time.Now().Add(t.o.settle)
	for {
		away, waiting := 0, 0
		for _, l := range t.clients {
			st, err := l.peek()
			if err != nil || st.State != "connected" || st.Recovering {
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
			t.problems.add("settle", 1, "%s: %s later %d clients were disconnected or recovering and %d saves were still queued", when, t.o.settle, away, waiting)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// correct is the information desk: every 40th ticket gets a new phone
// number, saved from another client than the one that saved it last.
func (t *test) correct() {
	ph := t.newPhase("Corrections")
	rng := rand.New(rand.NewPCG(t.o.seed, 0))
	jobs := make([][]store.Ticket, len(t.clients))
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
			desk := (writer - 1 + len(t.clients)/2) % len(t.clients)
			if len(t.clients) > 1 && desk == writer-1 {
				desk = (desk + 1) % len(t.clients)
			}
			jobs[desk] = append(jobs[desk], corrected(cur, rng))
		}
	}
	t.each(func(l *client) { l.saveInBatches(t, ph, "save corrections", jobs[l.n-1]) })
	ph.end = time.Now()
	t.settle("after the corrections")
}

// refuseKey is an admin deleting a client's key by mistake in the middle of
// the event: the client's saves are refused and queued, its bar says the
// key was refused, and the volunteer pairs again. Nothing may be lost.
func (t *test) refuseKey() {
	ph := t.newPhase("A client's key deleted")
	defer func() { ph.end = time.Now() }()
	l := t.clients[len(t.clients)/2]
	var settings struct {
		RemoteKey string `json:"remote_key"`
	}
	if _, err := l.call(ph, "open Settings", http.MethodGet, "/api/settings", nil, &settings, 0); err != nil || settings.RemoteKey == "" {
		t.problems.add("key", 1, "%s: could not read its key (%v)", l.prog.name, err)
		return
	}
	req, _ := http.NewRequest(http.MethodDelete, t.server.url+"/api/auth?key_to_del="+url.QueryEscape(settings.RemoteKey), nil)
	req.Header.Set("TAM-PW", t.password)
	res, err := web.Do(req)
	if err != nil {
		t.problems.add("key", 1, "deleting %s's key: %v", l.prog.name, err)
		return
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.problems.add("key", 1, "deleting %s's key answered %d", l.prog.name, res.StatusCode)
		return
	}
	window := l.openAway(time.Now())

	// The volunteer goes on correcting its own tickets.
	var fixes []store.Ticket
	for _, s := range deal(t.ev.ticketSheets, len(t.clients), 0)[l.n-1] {
		for id := s.from; id <= s.to && len(fixes) < 2*t.o.page; id++ {
			if cur, _, ok := t.ev.ticketNow(key{s.prefix, id}); ok {
				fixes = append(fixes, l.fix(cur))
			}
		}
	}
	l.saveInBatches(t, ph, "save while the key is refused", fixes)
	if st, err := l.peek(); err != nil || st.State != "unauthenticated" || st.Pending == 0 {
		t.problems.add("key", 1, "%s showed %+v (%v) with its key deleted, want unauthenticated with its saves queued", l.prog.name, st, err)
	}

	// Pairing again, as the bar asks, sends what was queued.
	if _, err := l.call(ph, "pair again", http.MethodPost, "/api/pair", l.pairing(t), nil, 0); err != nil {
		t.problems.add("key", 1, "%s could not pair again: %v", l.prog.name, err)
	}
	if !t.untilCaughtUp(l) {
		t.problems.add("key", 1, "%s did not send its queue within %s of pairing again", l.prog.name, t.o.settle)
	}
	l.closeAway(window, time.Now())
	ph.note = fmt.Sprintf("%s's key deleted on the server; %d tickets corrected meanwhile, then paired again", l.prog.name, len(fixes))
}

// storm has every client save the same few tickets at once, over and over,
// for -storm, each save naming who wrote it and when in every field. The
// server must end with one whole save per ticket, never a mix of two, and
// every client must then show just that.
func (t *test) storm() {
	if t.o.storm <= 0 {
		return
	}
	ph := t.newPhase("Everyone on the same tickets")
	var keys []key
	for _, s := range t.ev.ticketSheets[:min(10, len(t.ev.ticketSheets))] {
		keys = append(keys, key{s.prefix, s.from})
	}
	var mu sync.Mutex
	written := map[store.Ticket]bool{}
	baseline := map[key]store.Ticket{}
	for _, k := range keys {
		if row, _, ok := t.ev.ticketNow(k); ok {
			// A short storm may never reach this target. Only the known
			// accepted model row is valid, not an unchecked server read.
			baseline[k], written[row] = row, true
		}
	}
	stop := time.Now().Add(t.o.storm)
	t.each(func(l *client) {
		for seq := 0; time.Now().Before(stop); seq++ {
			k := keys[l.intN(len(keys))]
			tk := store.Ticket{Prefix: k.prefix, TID: k.id, FirstName: fmt.Sprintf("L%03d", l.n), LastName: fmt.Sprintf("W%06d", seq),
				PhoneNumber: fmt.Sprintf("555-%03d-%06d", l.n, seq), Pref: prefs[1+seq%2]}
			if _, err := l.call(ph, "save a ticket everyone saves", http.MethodPost, "/api/tickets", []store.Ticket{tk}, nil, 1); err == nil {
				mu.Lock()
				// Once a storm write succeeds, retaining the old value would
				// be a lost save, so its baseline must no longer pass.
				delete(written, baseline[k])
				written[tk] = true
				mu.Unlock()
			}
		}
	})
	ph.end = time.Now()
	t.settle("after everyone saved the same tickets")

	for _, k := range keys {
		var final store.Ticket
		path := fmt.Sprintf("/api/tickets/%s/%d", url.PathEscape(k.prefix), k.id)
		if _, err := t.clients[0].call(nil, "", http.MethodGet, path, nil, &final, 0); err != nil {
			t.problems.add("storm", 1, "reading %s: %v", k, err)
			continue
		}
		if !written[final] {
			t.problems.add("storm", 1, "%s ended as %s, which no client saved whole", k, describe(final))
		}
		t.each(func(l *client) {
			var shown store.Ticket
			if _, err := l.call(ph, "open a ticket everyone saved", http.MethodGet, path, nil, &shown, 0); err == nil && shown != final {
				t.problems.add("storm", 1, "%s shows %s for %s, the server has %s", l.prog.name, describe(shown), k, describe(final))
			}
		})
		// From here on this is the ticket's value, and no single client's.
		t.ev.mu.Lock()
		t.ev.ticket[k], t.ev.tWriter[k] = final, 0
		t.ev.mu.Unlock()
	}
	ph.note = fmt.Sprintf("%d clients saving the same %d tickets for %s", len(t.clients), len(keys), secs(t.o.storm))
}

// flatOut deals sheets out and has every client work through its share as
// fast as it can.
func (t *test) flatOut(name string, offset int, work func(*client, *test, *phase, []sheet), sheets []sheet) {
	ph := t.newPhase(name)
	deals := deal(sheets, len(t.clients), offset)
	t.each(func(l *client) { work(l, t, ph, deals[l.n-1]) })
	ph.end = time.Now()
}

// rush has every client save its ticket sheets again as fast as it can for
// -rush, every row with a new phone number: the most the server takes.
// The rows must change; SQLite does not write a row saved with the values
// it already has, which would make the server look faster than it is.
func (t *test) rush() {
	ph := t.newPhase("Rush")
	deals := deal(t.ev.ticketSheets, len(t.clients), 0)
	stop := time.Now().Add(t.o.rush)
	t.each(func(l *client) {
		mine := deals[l.n-1]
		for i := 0; len(mine) > 0 && time.Now().Before(stop); i++ {
			rows := t.ev.saved(mine[i%len(mine)])
			for j := range rows {
				rows[j] = l.fix(rows[j])
			}
			if _, err := l.call(ph, "save a changed ticket sheet", http.MethodPost, "/api/tickets", rows, nil, len(rows)); err == nil {
				t.ev.savedTickets(l.n, rows)
			}
		}
	})
	ph.end = time.Now()
	ph.note = "every save changes every row of its sheet"
}

// stopAll stops the pollers, the clients (the way their Shut Down button
// does) and the server.
func (t *test) stopAll() {
	t.stopOnce.Do(func() {
		close(t.stopPollers)
		t.pollers.Wait()
		t.each(func(l *client) {
			l.prog.shutDown()
			l.relay.close()
		})
		if t.server != nil && !t.server.external {
			t.server.kill()
		}
	})
}
