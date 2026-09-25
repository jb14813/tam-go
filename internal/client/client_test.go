package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func newStore(t *testing.T, name string) *store.Store {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	return store.New(sqldb)
}

var testDist = fstest.MapFS{
	"index.html":          {Data: []byte("<!doctype html><title>TAM</title><div id=app></div>")},
	"_app/immutable/x.js": {Data: []byte("console.log('x')")},
	"robots.txt":          {Data: []byte("User-agent: *")},
}

type fixture struct {
	t        *testing.T
	url      string
	st       *store.Store
	settings string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := newStore(t, "local.db")
	settings := filepath.Join(t.TempDir(), "settings.json")
	ts := httptest.NewServer(NewHandler(st, settings, testDist))
	t.Cleanup(ts.Close)
	return &fixture{t: t, url: ts.URL, st: st, settings: settings}
}

func (f *fixture) do(method, path string, body any, headers map[string]string) (int, []byte) {
	f.t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			data, _ := json.Marshal(b)
			rdr = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequest(method, f.url+path, rdr)
	if err != nil {
		f.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

// remoteFixture wires a real server handler as the remote and points the
// client's settings at it.
func remoteFixture(t *testing.T, f *fixture) (*store.Store, *httptest.Server) {
	t.Helper()
	rst := newStore(t, "remote.db")
	rs := httptest.NewServer(server.NewHandler(rst, "secret"))
	t.Cleanup(rs.Close)
	k, err := rst.CreateKey("client")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), k.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	return rst, rs
}

func TestSPA(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do("GET", "/", nil, nil); code != 302 {
		t.Fatalf("/ = %d, want 302", code)
	}
	for _, p := range []string{"/web/", "/web/tickets/CALL/", "/web/settings/prefixes/"} {
		code, body := f.do("GET", p, nil, nil)
		if code != 200 || !strings.Contains(string(body), "<div id=app>") {
			t.Fatalf("%s = %d %q, want the app shell", p, code, body)
		}
	}
	if code, body := f.do("GET", "/web/_app/immutable/x.js", nil, nil); code != 200 || !strings.Contains(string(body), "console.log") {
		t.Fatalf("asset = %d %q", code, body)
	}
	res, err := http.Get(f.url + "/web/_app/immutable/x.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("hashed bundle files must be cacheable, got Cache-Control %q", cc)
	}
	if code, body := f.do("GET", "/api/nope", nil, nil); code != 404 || !strings.Contains(string(body), `"detail"`) {
		t.Fatalf("unknown api path = %d %q", code, body)
	}
	if code, _ := f.do("GET", "/web/_app/immutable/missing.js", nil, nil); code != 404 {
		t.Fatalf("missing asset = %d, want 404", code)
	}
	if code, _ := f.do("GET", "/web/_app/", nil, nil); code != 200 {
		t.Fatalf("directory path falls back to the app = %d", code)
	}
}

func TestStandalonePrefixesAndTickets(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("GET", "/api", nil, nil)
	if code != 200 || decode[map[string]any](t, body)["whoami"] != "TAM Client" {
		t.Fatalf("root = %d %s", code, body)
	}

	code, body = f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A&B", Color: "blue", Weight: 1}}, nil)
	if code != 200 {
		t.Fatalf("post prefixes: %d %s", code, body)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=A%26B", nil, nil); code != 200 {
		t.Fatalf("delete encoded: %d", code)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=A%26B", nil, nil); code != 404 {
		t.Fatalf("delete missing: %d, want 404", code)
	}

	_, body = f.do("GET", "/api/tickets/A/7", nil, nil)
	ph := decode[store.Ticket](t, body)
	if ph.Prefix != "A" || ph.TID != 7 || ph.Pref != "CALL" {
		t.Fatalf("placeholder = %+v", ph)
	}

	f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 2, FirstName: "Amy", Pref: "TEXT"}}, nil)
	_, body = f.do("GET", "/api/tickets/A/3/1", nil, nil)
	rng := decode[[]store.Ticket](t, body)
	if len(rng) != 3 || rng[0].TID != 1 || rng[1].FirstName != "Amy" || rng[2].Pref != "CALL" {
		t.Fatalf("range = %+v", rng)
	}
	_, body = f.do("GET", "/api/tickets/A/1/1000", nil, nil)
	if rng = decode[[]store.Ticket](t, body); len(rng) != 301 {
		t.Fatalf("range cap = %d rows, want 301", len(rng))
	}

	if code, _ = f.do("POST", "/api/tickets", `[{"prefix":"A","t_id":3,"pref":"CALL"},{"prefix":"A","t_id":1.5,"pref":"CALL"}]`, nil); code != 400 {
		t.Fatalf("invalid ticket batch: %d", code)
	}
	if one, _ := f.st.Ticket("A", 3); one != nil {
		t.Fatal("an invalid batch must not write any row")
	}

	_, body = f.do("GET", "/api/drawing/A/1/2", nil, nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 2 || d[0].BID != 1 {
		t.Fatalf("drawing placeholders = %+v", d)
	}
	f.do("POST", "/api/drawing", `[{"prefix":"A","b_id":1,"winning_ticket":2,"description":"","changed":true}]`, nil)
	_, body = f.do("GET", "/api/drawing/A/1", nil, nil)
	if d := decode[store.DrawingLine](t, body); d.WinningTicket != 2 || d.FirstName != "Amy" {
		t.Fatalf("drawing line = %+v", d)
	}
	_, body = f.do("GET", "/api/baskets/A/1", nil, nil)
	if b := decode[store.Basket](t, body); b.WinningTicket != 2 {
		t.Fatalf("basket = %+v", b)
	}
	_, body = f.do("GET", "/api/reports/counts", nil, nil)
	if c := decode[[]store.ReportCountLine](t, body); len(c) != 2 {
		t.Fatalf("counts = %+v", c)
	}
	_, body = f.do("GET", "/api/search/tickets?first_name=am", nil, nil)
	if s := decode[[]store.Ticket](t, body); len(s) != 1 {
		t.Fatalf("search = %+v", s)
	}
}

func TestSettings(t *testing.T) {
	f := newFixture(t)
	_, body := f.do("GET", "/api/settings", nil, nil)
	if s := decode[config.Settings](t, body); s != config.Defaults() {
		t.Fatalf("defaults = %+v", s)
	}

	code, body := f.do("POST", "/api/settings", `{"venue_name":" Hall ","remote_port":" 8443 "}`, nil)
	s := decode[config.Settings](t, body)
	if code != 200 || s.VenueName != "Hall" || s.RemotePort != "8443" || s.DefaultPref != "CALL" {
		t.Fatalf("partial save = %d %+v", code, s)
	}

	before, _ := os.ReadFile(f.settings)
	for _, bad := range []string{`nope`, `{"remote_port":"abc"}`, `{"default_pref":"PHONE"}`, `{"colour":"x"}`, `{"remote_tls":"yes"}`, `{"remote_server":"http://tam.lan"}`, `{"remote_server":"tam.lan/api"}`} {
		if code, _ := f.do("POST", "/api/settings", bad, nil); code != 400 {
			t.Errorf("POST %s = %d, want 400", bad, code)
		}
	}
	after, _ := os.ReadFile(f.settings)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected saves must not touch the file")
	}

	// A hand edit is picked up; a broken file keeps the last good settings.
	edited := s
	edited.VenueName = "Edited by hand"
	config.Save(f.settings, edited)
	_, body = f.do("GET", "/api/settings", nil, nil)
	if decode[config.Settings](t, body).VenueName != "Edited by hand" {
		t.Fatalf("hand edit not picked up: %s", body)
	}
	os.WriteFile(f.settings, []byte(`{"venue_name": "Typo",}`), 0o644)
	code, body = f.do("GET", "/api/settings", nil, nil)
	if code != 200 || decode[config.Settings](t, body).VenueName != "Edited by hand" {
		t.Fatalf("malformed file should keep the last good settings: %d %s", code, body)
	}
}

func TestCrossSiteWritesRefused(t *testing.T) {
	f := newFixture(t)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	if code, _ := f.do("POST", "/api/settings", `{"venue_name":"X"}`, cross); code != 403 {
		t.Fatalf("cross-site POST = %d, want 403", code)
	}
	if code, _ := f.do("DELETE", "/api/prefixes?p=A", nil, cross); code != 403 {
		t.Fatalf("cross-site DELETE = %d, want 403", code)
	}
	if code, _ := f.do("GET", "/api/settings", nil, cross); code != 200 {
		t.Fatalf("cross-site GET = %d, want 200", code)
	}
	if code, _ := f.do("POST", "/api/settings", `{"venue_name":"X"}`, map[string]string{"Sec-Fetch-Site": "same-origin"}); code != 200 {
		t.Fatalf("same-origin POST = %d, want 200", code)
	}
}

func TestRemoteMode(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)

	_, body := f.do("GET", "/api", nil, nil)
	root := decode[map[string]any](t, body)
	if root["whoami"] != "TAM Server" || root["authenticated"] != true || root["healthy"] != true {
		t.Fatalf("root = %v", root)
	}

	code, body := f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Rem", Pref: "CALL"}}, nil)
	if code != 200 {
		t.Fatalf("post tickets: %d %s", code, body)
	}
	if rt, _ := rst.Ticket("A", 1); rt == nil || rt.FirstName != "Rem" {
		t.Fatalf("remote store not written: %+v", rt)
	}
	if lt, _ := f.st.Ticket("A", 1); lt == nil || lt.FirstName != "Rem" {
		t.Fatalf("local mirror not written: %+v", lt)
	}

	_, body = f.do("GET", "/api/tickets/A/1/2", nil, nil)
	if rng := decode[[]store.Ticket](t, body); len(rng) != 2 || rng[0].FirstName != "Rem" || rng[1].Pref != "CALL" {
		t.Fatalf("remote range = %+v", rng)
	}
	_, body = f.do("GET", "/api/tickets/A/1", nil, nil)
	if one := decode[store.Ticket](t, body); one.FirstName != "Rem" {
		t.Fatalf("remote single = %+v", one)
	}

	f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil)
	if ps, _ := rst.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("remote prefixes = %v", ps)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
		t.Fatalf("remote delete = %d", code)
	}
	if ps, _ := rst.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("remote prefix not deleted: %v", ps)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("local mirror not deleted: %v", ps)
	}

	// Key management is proxied, translating TAM-PWD to TAM-PW.
	if code, _ = f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "wrong"}); code != 401 {
		t.Fatalf("auth with wrong password = %d, want 401", code)
	}
	code, body = f.do("POST", "/api/auth", `{"description":"tablet"}`, map[string]string{"TAM-PWD": "secret"})
	if code != 200 {
		t.Fatalf("create key = %d %s", code, body)
	}
	created := decode[store.AuthKey](t, body)
	_, body = f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "secret"})
	if keys := decode[[]store.AuthKey](t, body); len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	if code, _ = f.do("DELETE", "/api/auth?key_to_del="+created.AuthKey, nil, map[string]string{"TAM-PWD": "secret"}); code != 200 {
		t.Fatalf("delete key = %d", code)
	}

	// A prefix that exists only locally is still removed locally when the
	// server answers 404.
	f.st.UpsertPrefixes([]store.Prefix{{Prefix: "LOCALONLY", Color: "red", Weight: 1}})
	if code, _ = f.do("DELETE", "/api/prefixes?p=LOCALONLY", nil, nil); code != 200 {
		t.Fatalf("delete of a local-only prefix = %d, want 200", code)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("local-only prefix must be gone: %v", ps)
	}
	if code, _ = f.do("DELETE", "/api/prefixes?p=NOWHERE", nil, nil); code != 404 {
		t.Fatalf("delete of a prefix nobody has = %d, want 404", code)
	}

	// Push and remote backup.
	f.st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 5, Description: "Local basket"}})
	code, body = f.do("POST", "/api/backuprestore/push/baskets", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "Baskets pushed") {
		t.Fatalf("push = %d %s", code, body)
	}
	if rb, _ := rst.Basket("A", 5); rb == nil {
		t.Fatal("push did not reach the remote store")
	}
	if code, _ = f.do("POST", "/api/backuprestore/push/keys", `{}`, nil); code != 400 {
		t.Fatalf("push bad target = %d", code)
	}
	if code, _ = f.do("POST", "/api/backuprestore/push/baskets", nil, nil); code != 400 {
		t.Fatalf("push without a JSON body (a plain form post) = %d, want 400", code)
	}
	_, body = f.do("GET", "/api/backuprestore/remote", nil, nil)
	if bf := decode[store.BackupFile](t, body); len(bf.Baskets) != 1 || len(bf.Tickets) != 1 {
		t.Fatalf("remote export = %+v", bf)
	}
	code, _ = f.do("POST", "/api/backuprestore/remote", store.BackupFile{Prefixes: []store.Prefix{{Prefix: "Z", Color: "red"}}}, nil)
	if code != 200 {
		t.Fatalf("remote import = %d", code)
	}
	if ps, _ := rst.ListPrefixes(); len(ps) != 1 || ps[0].Prefix != "Z" {
		t.Fatalf("remote import result = %v", ps)
	}

	// A rejected remote write is forwarded and nothing is mirrored.
	s, _ := config.Load(f.settings)
	s.RemoteKey = "WRONG"
	config.Save(f.settings, s)
	code, body = f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 9, FirstName: "No", Pref: "CALL"}}, nil)
	if code != 401 || !strings.Contains(string(body), "Invalid Key") {
		t.Fatalf("forwarded error = %d %s", code, body)
	}
	if lt, _ := f.st.Ticket("A", 9); lt != nil {
		t.Fatal("rejected write must not be mirrored locally")
	}
	_, body = f.do("GET", "/api", nil, nil)
	if root = decode[map[string]any](t, body); root["authenticated"] != false || root["healthy"] != true {
		t.Fatalf("root with bad key = %v", root)
	}

	// Server down: reads answer empty, root reports unhealthy, writes 502.
	rs.Close()
	_, body = f.do("GET", "/api", nil, nil)
	if root = decode[map[string]any](t, body); root["healthy"] != false {
		t.Fatalf("root with server down = %v", root)
	}
	_, body = f.do("GET", "/api/tickets/A/1/2", nil, nil)
	if rng := decode[[]store.Ticket](t, body); len(rng) != 0 {
		t.Fatalf("range with server down = %+v, want []", rng)
	}
	if code, _ = f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 9, Pref: "CALL"}}, nil); code != 502 {
		t.Fatalf("write with server down = %d, want 502", code)
	}
}

// TestPushSendsEveryList pins the wire shape the original server requires:
// all three lists present, never null.
func TestPushSendsEveryList(t *testing.T) {
	f := newFixture(t)
	var got map[string]json.RawMessage
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/backuprestore" {
			json.NewDecoder(r.Body).Decode(&got)
			httpx.WriteJSON(w, 200, map[string]string{"message": "ok"})
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"whoami": "TAM Server", "authenticated": true, "healthy": true})
	}))
	defer rs.Close()
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), "K"
	config.Save(f.settings, s)
	f.st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 1, Description: "B"}})

	if code, body := f.do("POST", "/api/backuprestore/push/baskets", `{}`, nil); code != 200 {
		t.Fatalf("push = %d %s", code, body)
	}
	for _, k := range []string{"prefixes", "baskets", "tickets"} {
		v, ok := got[k]
		if !ok || strings.TrimSpace(string(v)) == "null" {
			t.Fatalf("push body must contain a %s list, got %s", k, got[k])
		}
	}
	if !strings.Contains(string(got["baskets"]), `"description":"B"`) {
		t.Fatalf("pushed baskets = %s", got["baskets"])
	}
}

// TestRemoteClientIsReused pins the connection pool across requests.
func TestRemoteClientIsReused(t *testing.T) {
	h := &handler{}
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = "tam.lan", "8000", "K1"
	if h.remote(s) == nil {
		t.Fatal("remote mode should return a client")
	}
	first := h.rc
	s.RemoteKey = "K2"
	h.remote(s)
	if h.rc != first {
		t.Fatal("a changed key must not rebuild the connection pool")
	}
	s.RemotePort = "8443"
	h.remote(s)
	if h.rc == first {
		t.Fatal("a changed server address must rebuild the connection pool")
	}
	s.RemoteServer = ""
	if h.remote(s) != nil {
		t.Fatal("standalone mode must return nil")
	}
}

func TestShutdownRoute(t *testing.T) {
	plain := newFixture(t)
	if code, _ := plain.do("POST", "/api/shutdown", `{}`, nil); code != 501 {
		t.Fatalf("without a hook = %d, want 501", code)
	}

	called := make(chan struct{}, 1)
	st := newStore(t, "local.db")
	settings := filepath.Join(t.TempDir(), "settings.json")
	ts := httptest.NewServer(NewHandler(st, settings, testDist, WithShutdown(func() { called <- struct{}{} })))
	t.Cleanup(ts.Close)
	f := &fixture{t: t, url: ts.URL, st: st, settings: settings}

	if code, _ := f.do("POST", "/api/shutdown", nil, nil); code != 400 {
		t.Fatalf("a plain form post must not stop the app: %d, want 400", code)
	}
	if code, _ := f.do("POST", "/api/shutdown", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site"}); code != 403 {
		t.Fatalf("cross-site = %d, want 403", code)
	}
	select {
	case <-called:
		t.Fatal("rejected requests must not stop the app")
	default:
	}
	code, body := f.do("POST", "/api/shutdown", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "shutting down") {
		t.Fatalf("shutdown = %d %s", code, body)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("the shutdown hook was not called")
	}
}

func TestStandaloneAuthAndPush(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "x"}); code != 500 {
		t.Fatalf("auth standalone = %d, want 500", code)
	}
	if code, _ := f.do("POST", "/api/backuprestore/push/tickets", `{}`, nil); code != 500 {
		t.Fatalf("push standalone = %d, want 500", code)
	}
	code, body := f.do("GET", "/api/backuprestore/remote", nil, nil)
	if code != 200 || strings.TrimSpace(string(body)) != "{}" {
		t.Fatalf("remote export standalone = %d %s", code, body)
	}
	f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red"}}, nil)
	_, body = f.do("GET", "/api/backuprestore/local", nil, nil)
	bf := decode[store.BackupFile](t, body)
	if len(bf.Prefixes) != 1 || bf.Tickets == nil {
		t.Fatalf("local export = %+v", bf)
	}
	other := newFixture(t)
	if code, _ := other.do("POST", "/api/backuprestore/local", bf, nil); code != 200 {
		t.Fatalf("local import = %d", code)
	}
	if ps, _ := other.st.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("local import result = %v", ps)
	}
}
