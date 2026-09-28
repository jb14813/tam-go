package client

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

type pausedSaveBody struct {
	started chan struct{}
	resume  chan struct{}
	reader  io.Reader
}

func (b *pausedSaveBody) Read(p []byte) (int, error) {
	if b.started != nil {
		close(b.started)
		b.started = nil
		<-b.resume
	}
	return b.reader.Read(p)
}

func TestSaveUsesConnectionSelectedWhileItsBodyWasArriving(t *testing.T) {
	for _, unpair := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "standalone"}[unpair], func(t *testing.T) {
			f := newFixture(t)
			oldStore, _ := remoteFixture(t, f)
			f.h.sync.Tick()
			body := &pausedSaveBody{started: make(chan struct{}), resume: make(chan struct{}), reader: strings.NewReader(`[{"prefix":"A","t_id":22,"first_name":"Arriving"}]`)}
			started := body.started
			r := httptest.NewRequest("POST", "/api/tickets", body)
			r.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { f.h.routes(testDist).ServeHTTP(response, r); close(done) }()
			<-started
			newStore := newServerStore(t)
			if unpair {
				if code, b := f.do("POST", "/api/unpair", `{}`, nil); code != 200 {
					t.Errorf("unpair: %d %s", code, b)
				}
			} else {
				key, _ := newStore.CreateKey("replacement")
				rs := namedServer(t, newStore, "new")
				u, _ := url.Parse(rs.URL)
				if code, b := f.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": key.AuthKey}, nil); code != 200 {
					t.Errorf("switch: %d %s", code, b)
				}
			}
			close(body.resume)
			<-done
			if response.Code != 200 {
				t.Fatalf("save: %d %s", response.Code, response.Body.String())
			}
			if row, _ := oldStore.Ticket("A", 22); row != nil {
				t.Errorf("save went to previous server: %+v", row)
			}
			if !unpair {
				serverHas(t, newStore, 22, "Arriving")
			}
			if row, _ := f.st.Ticket("A", 22); row == nil || row.FirstName != "Arriving" {
				t.Errorf("local save missing: %+v", row)
			}
		})
	}
}

func TestSaveIsDurableWhileRecoveryResponseIsDelayed(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	key, _ := rst.CreateKey("client")
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	started, release := make(chan struct{}), make(chan struct{})
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/recovery" {
			close(started)
			<-release
		}
		inner.ServeHTTP(w, r)
	}))
	defer rs.Close()
	u, _ := url.Parse(rs.URL)
	f.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": key.AuthKey}, nil)
	f.st.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1, FirstName: "Before"}})
	done := make(chan struct{})
	go func() { f.h.sync.Tick(); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatal("recovery never started")
	}
	saved := make(chan int, 1)
	go func() { code, _ := f.do("POST", "/api/tickets", oneTicket(1, "During recovery"), nil); saved <- code }()
	select {
	case code := <-saved:
		if code != 200 {
			t.Errorf("save during recovery = %d", code)
		}
		if row, _ := f.st.Ticket("A", 1); row == nil || row.FirstName != "During recovery" {
			t.Errorf("save not durable yet: %+v", row)
		}
	case <-time.After(2 * time.Second):
		t.Error("local save blocked behind recovery upload")
	}
	close(release)
	<-done
	serverHas(t, rst, 1, "During recovery")
}

// Lose the event tables, retain the real keys, and restart on the same
// address. Nobody presses Pair, Retry, or Push to reconstruct the event.
func TestEmptyRestartRecoversFromAuthenticatedClients(t *testing.T) {
	for _, drawingFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata first", true: "drawing first"}[drawingFirst], func(t *testing.T) {
			testEmptyRestartRecoversFromAuthenticatedClients(t, drawingFirst)
		})
	}
}

func testEmptyRestartRecoversFromAuthenticatedClients(t *testing.T, drawingFirst bool) {
	database, err := db.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateServer(database); err != nil {
		t.Fatal(err)
	}
	st := store.New(database)
	old := httptest.NewServer(server.NewHandler(st, server.FixedPassword("secret")))
	defer old.Close()
	a, b := newFixture(t), newFixture(t)
	a.pairTo(old.URL)
	// Legacy installations can share one event key. Recovery acknowledgements
	// must still distinguish these two local data folders.
	s := a.h.cfg.Get()
	if code, body := b.do("POST", "/api/settings", map[string]any{"remote_server": s.RemoteServer, "remote_port": s.RemotePort, "remote_key": s.RemoteKey}, nil); code != 200 {
		t.Fatalf("configure shared key: %d %s", code, body)
	}
	a.h.sync.Tick()
	b.h.sync.Tick()
	// Each workstation retains only what it actually entered. No server
	// snapshot is downloaded to make this recovery work.
	for _, entry := range []struct {
		client *fixture
		path   string
		body   any
	}{
		{a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}},
		{a, "/api/tickets", oneTicket(1, "Completed save")},
		{a, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Prize"}}},
		{b, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 1}}},
	} {
		if code, body := entry.client.do("POST", entry.path, entry.body, nil); code != 200 {
			t.Fatalf("entry before outage: %d %s", code, body)
		}
	}
	if row, _ := b.st.Ticket("A", 1); row != nil {
		t.Fatalf("second client must not need the first client's ticket: %+v", row)
	}
	if row, _ := a.st.Basket("A", 1); row == nil || row.Description != "Prize" || row.WinningTicket != 0 {
		t.Fatalf("first client should retain only its basket metadata: %+v", row)
	}
	if row, _ := b.st.Basket("A", 1); row == nil || row.Description != "" || row.WinningTicket != 1 {
		t.Fatalf("second client should retain only its drawing entry: %+v", row)
	}
	keysBefore, _ := st.ListKeys()
	if len(keysBefore) != 1 {
		t.Fatalf("expected one shared event key, got %d", len(keysBefore))
	}
	address := old.Listener.Addr().String()
	old.Close()
	// Each workstation contributes a different save made during the outage.
	for i, f := range []*fixture{a, b} {
		if code, body := f.do("POST", "/api/tickets", oneTicket(i+2, "Offline"), nil); code != 200 {
			t.Fatalf("offline save: %d %s", code, body)
		}
	}
	for _, table := range []string{"prefixes", "tickets", "baskets"} {
		if _, err := database.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	replacement := &httptest.Server{Listener: listener, Config: &http.Server{Handler: server.NewHandler(st, server.FixedPassword("secret"))}}
	replacement.Start()
	defer replacement.Close()
	if drawingFirst {
		b.h.sync.Tick()
		a.h.sync.Tick()
	} else {
		a.h.sync.Tick()
		b.h.sync.Tick()
	}
	serverHas(t, st, 1, "Completed save")
	serverHas(t, st, 2, "Offline")
	serverHas(t, st, 3, "Offline")
	basket, err := st.Basket("A", 1)
	if err != nil || basket == nil || basket.WinningTicket != 1 || basket.Description != "Prize" {
		t.Fatalf("recovered basket: %+v, %v", basket, err)
	}
	keysAfter, _ := st.ListKeys()
	if len(keysBefore) != len(keysAfter) {
		t.Fatalf("recovery must keep keys: %d -> %d", len(keysBefore), len(keysAfter))
	}
	for _, f := range []*fixture{a, b} {
		if p, failed := pending(t, f.st); p != 0 || failed != 0 {
			t.Fatalf("after recovery: pending %d, failed %d", p, failed)
		}
	}
}

func TestManualServerChangeKeepsEventKeyAndClearsOldPin(t *testing.T) {
	f := newFixture(t)
	_, err := f.h.cfg.Update(func(s config.Settings) (config.Settings, error) {
		s.RemoteServer, s.RemoteKey, s.RemoteName, s.RemoteFingerprint = "old-server", "event-key", "old name", strings.Repeat("a", 64)
		s.RemoteTLS = true
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("POST", "/api/settings", map[string]any{"remote_server": "new-server"}, nil); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	s := f.h.cfg.Get()
	if s.RemoteKey != "event-key" || s.RemoteName != "" || s.RemoteFingerprint != "" {
		t.Fatalf("replacement settings: %+v", s)
	}
}

func TestLegacySettingsDeliverWaitingEventSaves(t *testing.T) {
	f := newFixture(t)
	first := namedServer(t, newServerStore(t), "first")
	f.pairTo(first.URL)
	f.h.sync.Tick()
	first.Close()
	if code, body := f.do("POST", "/api/tickets", oneTicket(4, "Waiting"), nil); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	rst := newServerStore(t)
	key, err := rst.CreateKey("existing event key")
	if err != nil {
		t.Fatal(err)
	}
	other := namedServer(t, rst, "replacement")
	u, _ := url.Parse(other.URL)
	if code, body := f.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": key.AuthKey}, nil); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	f.h.sync.Tick()
	serverHas(t, rst, 4, "Waiting")
}
