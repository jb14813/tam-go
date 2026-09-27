package client

// Saves in line: a client's saves reach its server one at a time, in the
// order of their numbers, whatever the network, the Retry button or a data
// folder put back from a copy does. Each test here is a way a save used to
// be lost: the server skipped it as stale while the client took it as done.

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/tlscert"
)

// flakyServer is a real tam-server handler whose saves can be made to
// answer 503 for a while (a server that is up but unwell, or a proxy in
// front of it that times out).
type flakyServer struct {
	mu   sync.Mutex
	busy bool
	ts   *httptest.Server
}

func newFlakyServer(t *testing.T, st *store.Store, name string) *flakyServer {
	t.Helper()
	fs := &flakyServer{}
	inner := server.NewHandler(st, server.FixedPassword("secret"), server.WithInfo(server.Info{Name: name}))
	fs.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		busy := fs.busy
		fs.mu.Unlock()
		if busy && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(fs.ts.Close)
	return fs
}

func (fs *flakyServer) setBusy(b bool) {
	fs.mu.Lock()
	fs.busy = b
	fs.mu.Unlock()
}

// serverHas fails the test unless the server holds ticket A id with name.
func serverHas(t *testing.T, rst *store.Store, id int, name string) {
	t.Helper()
	if rt, _ := rst.Ticket("A", id); rt == nil || rt.FirstName != name {
		t.Errorf("the server has %+v for ticket A %d, want %q", rt, id, name)
	}
}

// TestRetryPutsSavesBehindTheQueue: the Settings page offers Retry for the
// saves set aside when the client paired with another server. A save still
// queued for the new server when Retry is pressed must not be overtaken:
// the retried save goes behind it, with a newer number, and both arrive.
func TestRetryPutsSavesBehindTheQueue(t *testing.T) {
	f := newFixture(t)
	first := namedServer(t, newServerStore(t), "first-box")
	f.pairTo(first.URL)
	f.h.sync.Tick()
	first.Close()
	if code, body := f.do("POST", "/api/tickets", oneTicket(9, "SetAside"), nil); code != 200 {
		t.Fatalf("save with the first server gone = %d %s", code, body)
	}
	other := newServerStore(t)
	second := newFlakyServer(t, other, "second-box")
	f.pairTo(second.ts.URL)
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 1 {
		t.Fatalf("after pairing with the second server: pending %d failed %d, want 0 and 1", p, fl)
	}

	second.setBusy(true)
	if code, body := f.do("POST", "/api/tickets", oneTicket(11, "Queued"), nil); code != 200 {
		t.Fatalf("save while the server is busy = %d %s, want 200 (queued)", code, body)
	}
	if code, body := f.do("POST", "/api/outbox/retry", `{}`, nil); code != 200 {
		t.Fatalf("retry = %d %s", code, body)
	}
	second.setBusy(false)
	f.h.sync.Reset()
	f.h.sync.Tick()

	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Errorf("after the replay: pending %d failed %d, want both 0", p, fl)
	}
	serverHas(t, other, 9, "SetAside")
	serverHas(t, other, 11, "Queued")
}

// TestConcurrentSavesReachTheServer: two saves of different rows made on
// one client a moment apart (the tickets page saves the old page without
// waiting when the volunteer moves on, or two tabs are open) while the
// first is held up on the way (Wi-Fi retransmitting). The second must not
// overtake it and make the server skip it.
func TestConcurrentSavesReachTheServer(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	k, err := rst.CreateKey("client")
	if err != nil {
		t.Fatal(err)
	}
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	var mu sync.Mutex
	first := true
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
			mu.Lock()
			delay := first
			first = false
			mu.Unlock()
			if delay {
				time.Sleep(800 * time.Millisecond) // held up on the way; well under writeTimeout
			}
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(rs.Close)
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), k.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	f.h.sync.Tick()

	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		codes[0], _ = f.do("POST", "/api/tickets", oneTicket(1, "PageOne"), nil)
	}()
	time.Sleep(200 * time.Millisecond)
	codes[1], _ = f.do("POST", "/api/tickets", oneTicket(2, "PageTwo"), nil)
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("saves answered %v, want both 200", codes)
	}
	f.h.sync.Tick()
	serverHas(t, rst, 1, "PageOne")
	serverHas(t, rst, 2, "PageTwo")
}

// TestConcurrentOfflineSavesAllArrive: many saves made at once while the
// server is away are queued in the order they are numbered, so the replay
// sends them in order and the server takes every one.
func TestConcurrentOfflineSavesAllArrive(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)
	f.h.sync.Tick()
	addr := rs.Listener.Addr().String()
	rs.Close()
	f.h.sync.Tick()

	const n = 40
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if code, body := f.do("POST", "/api/tickets", oneTicket(id, "Offline"), nil); code != 200 {
				t.Errorf("save %d offline = %d %s", id, code, body)
			}
		}(i)
	}
	wg.Wait()
	if p, _ := pending(t, f.st); p != n {
		t.Fatalf("pending = %d, want %d queued", p, n)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen again on %s: %v", addr, err)
	}
	back := &httptest.Server{Listener: ln, Config: &http.Server{Handler: server.NewHandler(rst, server.FixedPassword("secret"))}}
	back.Start()
	t.Cleanup(back.Close)
	f.h.sync.Reset()
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("after the replay: pending %d failed %d, want both 0", p, fl)
	}
	for i := 1; i <= n; i++ {
		serverHas(t, rst, i, "Offline")
	}
}

// TestRestoredDataFolderSavesStillArrive: a client whose data folder was put
// back from a copy counts its saves from an older number than the server has
// seen from it. Its saves must still arrive: the server answers that they
// are behind, and the client numbers them past the server's count.
func TestRestoredDataFolderSavesStillArrive(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.h.sync.Tick()
	for id := 1; id <= 3; id++ {
		if code, body := f.do("POST", "/api/tickets", oneTicket(id, "Before"), nil); code != 200 {
			t.Fatalf("save %d = %d %s", id, code, body)
		}
	}
	// The copy put back remembers only the first save.
	f.exec(`UPDATE save_order SET last_save = 1 WHERE id = 1`)
	for id := 10; id <= 12; id++ {
		if code, body := f.do("POST", "/api/tickets", oneTicket(id, "After"), nil); code != 200 {
			t.Fatalf("save %d after the restore = %d %s", id, code, body)
		}
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Errorf("pending %d failed %d, want both 0", p, fl)
	}
	for id := 10; id <= 12; id++ {
		serverHas(t, rst, id, "After")
	}
}

// TestRestoredDataFolderQueuedSavesAreKept: a queued save in a data folder
// put back from a copy may have reached the server before the copy was put
// back, or not. The server answers that it is behind; the client keeps it
// in the failed list for the volunteer to retry or discard, and its later
// saves are numbered past the server's count and arrive.
func TestRestoredDataFolderQueuedSavesAreKept(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.h.sync.Tick()
	var first store.Order
	for id := 1; id <= 3; id++ {
		if code, body := f.do("POST", "/api/tickets", oneTicket(id, "Before"), nil); code != 200 {
			t.Fatalf("save %d = %d %s", id, code, body)
		}
	}
	// The copy put back had save 2 still queued, with other content.
	body, _ := json.Marshal(oneTicket(2, "Queued in the copy"))
	first = store.Order{Client: f.clientName(), Save: 2}
	if _, err := f.st.SaveQueued(http.MethodPost, "/api/tickets", body, first, func(*store.Store) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE save_order SET last_save = 2 WHERE id = 1`)
	f.h.sync.Tick()
	failed, err := f.st.ListFailed()
	if err != nil || len(failed) != 1 || !strings.Contains(failed[0].LastError, "newer saves from this client") {
		t.Fatalf("failed list = %+v, %v; want the queued save kept with the reason", failed, err)
	}
	serverHas(t, rst, 2, "Before")
	if code, body := f.do("POST", "/api/tickets", oneTicket(20, "Later"), nil); code != 200 {
		t.Fatalf("save after the replay = %d %s", code, body)
	}
	serverHas(t, rst, 20, "Later")

	// Retry numbers the kept save anew; the volunteer chose to send it.
	if code, body := f.do("POST", "/api/outbox/retry", `{}`, nil); code != 200 {
		t.Fatalf("retry = %d %s", code, body)
	}
	f.h.sync.Tick()
	serverHas(t, rst, 2, "Queued in the copy")
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Errorf("after the retry: pending %d failed %d, want both 0", p, fl)
	}
}

// TestQueuedDeleteOfAPrefixGoneFromTheServerIsDone: a prefix deleted
// offline on this client and meanwhile on the server by another client
// ends as both wanted; the replay takes the server's 404 as done, as a
// delete made online does, instead of filing it as refused.
func TestQueuedDeleteOfAPrefixGoneFromTheServerIsDone(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)
	if code, _ := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "P", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatal("seed save failed")
	}
	f.h.sync.Tick()
	addr := rs.Listener.Addr().String()
	rs.Close()
	f.h.sync.Tick()
	if code, _ := f.do("DELETE", "/api/prefixes?p=P", nil, nil); code != 200 {
		t.Fatalf("delete offline = %d, want 200 (queued)", code)
	}
	if _, err := rst.DeletePrefix("P"); err != nil { // another client deletes it too
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen again on %s: %v", addr, err)
	}
	back := &httptest.Server{Listener: ln, Config: &http.Server{Handler: server.NewHandler(rst, server.FixedPassword("secret"))}}
	back.Start()
	t.Cleanup(back.Close)
	f.h.sync.Reset()
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Errorf("after the replay: pending %d, failed %d; want both 0", p, fl)
	}
}

// TestBrokenSettingsFileKeepsTheClientPaired: a paired client restarts with
// a settings.json it cannot parse (zeros after a power cut, or a hand edit
// with a typo). It stays paired, from the copy kept next to the file, and
// says so on the pages; its saves go on reaching the server.
func TestBrokenSettingsFileKeepsTheClientPaired(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "BeforeRestart"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	info, err := os.Stat(f.settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.settings, make([]byte, info.Size()), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newHandler(f.st, f.settings, testDist, WithTimings(testTimings))
	f.url = newTestServer(t, h.routes(testDist)).URL
	f.h = h
	_, body := f.do("GET", "/api/status", nil, nil)
	var st struct {
		Mode          string `json:"mode"`
		SettingsError string `json:"settings_error"`
	}
	json.Unmarshal(body, &st)
	if st.Mode != "remote" || !strings.Contains(st.SettingsError, "settings.json.bak") {
		t.Fatalf("status after the restart = %s; want remote mode and the problem named", body)
	}
	if code, body := f.do("POST", "/api/tickets", oneTicket(2, "AfterRestart"), nil); code != 200 {
		t.Fatalf("save after the restart = %d %s", code, body)
	}
	f.h.sync.Tick()
	serverHas(t, rst, 2, "AfterRestart")

	// Saving the settings again repairs the file and clears the problem.
	if code, body := f.do("POST", "/api/settings", map[string]any{"venue_name": "Hall"}, nil); code != 200 {
		t.Fatalf("save settings = %d %s", code, body)
	}
	_, body = f.do("GET", "/api/status", nil, nil)
	st.SettingsError = ""
	json.Unmarshal(body, &st)
	if st.SettingsError != "" {
		t.Fatalf("status after the settings were saved again = %s; want no problem", body)
	}
}

// TestBrokenSettingsFileWithoutACopyIsReported: with neither the file nor
// its copy readable the client can only run with defaults, standalone; the
// pages are told, rather than the client quietly working on its own.
func TestBrokenSettingsFileWithoutACopyIsReported(t *testing.T) {
	f := newFixture(t)
	remoteFixture(t, f)
	for _, p := range []string{f.settings, config.BackupPath(f.settings)} {
		if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := newHandler(f.st, f.settings, testDist, WithTimings(testTimings))
	f.url = newTestServer(t, h.routes(testDist)).URL
	_, body := f.do("GET", "/api/status", nil, nil)
	if !strings.Contains(string(body), `"mode":"standalone"`) || !strings.Contains(string(body), "no good copy") {
		t.Fatalf("status = %s; want standalone with the problem named", body)
	}
}

// TestPairingWithAnotherServerKeepsLocalDataInAFile: a client with data of
// its own (entered standalone) that pairs with a server gets the server's
// data, which replaces its rows with the same numbers; its own data is
// saved to a file in its data folder first, and the answer says where.
func TestPairingWithAnotherServerKeepsLocalDataInAFile(t *testing.T) {
	f := newFixture(t)
	if code, body := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatalf("prefix = %d %s", code, body)
	}
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "Local Only"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	rst := newServerStore(t)
	rst.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1, FirstName: "Alice"}})
	msg := f.pairTo(namedServer(t, rst, "front-desk").URL)
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(f.settings), "before-pairing-*.json"))
	if len(matches) != 1 || !strings.Contains(msg, filepath.Base(matches[0])) {
		t.Fatalf("pairing answered %s; files %v; want one before-pairing file, named in the answer", msg, matches)
	}
	data, _ := os.ReadFile(matches[0])
	var bf store.BackupFile
	if err := json.Unmarshal(data, &bf); err != nil || len(bf.Tickets) != 1 || bf.Tickets[0].FirstName != "Local Only" {
		t.Fatalf("the file holds %s (%v), want the client's own ticket", data, err)
	}
	// Pairing again with the same server keeps no second file.
	f.pairTo(namedServer(t, rst, "front-desk").URL)
	if again, _ := filepath.Glob(filepath.Join(filepath.Dir(f.settings), "before-pairing-*.json")); len(again) != 1 {
		t.Fatalf("pairing again with the same server wrote %v", again)
	}
}

// TestChangedCertificateIsItsOwnState: a server whose certificate is no
// longer the one pinned at pairing is not "offline": the status says the
// certificate changed, and pairing again with it gets the client going,
// keeping what was queued.
func TestChangedCertificateIsItsOwnState(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	handler := server.NewHandler(rst, server.FixedPassword("secret"), server.WithInfo(server.Info{Name: "front-desk"}))
	certFor := func(dir string) tls.Certificate {
		t.Helper()
		crt, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
		if _, err := tlscert.EnsurePair(crt, key, []string{"127.0.0.1"}); err != nil {
			t.Fatal(err)
		}
		c, err := tls.LoadX509KeyPair(crt, key)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	start := func(ln net.Listener, cert tls.Certificate) *httptest.Server {
		ts := httptest.NewUnstartedServer(handler)
		if ln != nil {
			ts.Listener.Close()
			ts.Listener = ln
		}
		ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
		ts.StartTLS()
		t.Cleanup(ts.Close)
		return ts
	}
	ts := start(nil, certFor(t.TempDir()))
	u, _ := url.Parse(ts.URL)
	if code, body := f.do("POST", "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "tls": true, "password": "secret"}, nil); code != 200 {
		t.Fatalf("pair over TLS = %d %s", code, body)
	}
	f.h.sync.Tick()

	addr := ts.Listener.Addr().String()
	ts.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen again on %s: %v", addr, err)
	}
	start(ln, certFor(t.TempDir()))
	f.h.sync.Kick()
	f.h.sync.Tick()
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "Queued"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	_, body := f.do("GET", "/api/status", nil, nil)
	if !strings.Contains(string(body), `"state":"certificate"`) {
		t.Fatalf("status with a changed certificate = %s, want state certificate", body)
	}

	if code, body := f.do("POST", "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "tls": true, "password": "secret"}, nil); code != 200 {
		t.Fatalf("pair again = %d %s", code, body)
	}
	f.h.sync.Tick()
	_, body = f.do("GET", "/api/status", nil, nil)
	if !strings.Contains(string(body), `"state":"connected"`) || !strings.Contains(string(body), `"pending":0`) {
		t.Fatalf("status after pairing again = %s, want connected with nothing waiting", body)
	}
	serverHas(t, rst, 1, "Queued")
}
