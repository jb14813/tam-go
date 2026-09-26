package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestStatusAndPairing(t *testing.T) {
	f := newFixture(t)
	_, body := f.do("GET", "/api/status", nil, nil)
	if strings.TrimSpace(string(body)) != `{"mode":"standalone"}` {
		t.Fatalf("standalone status = %s", body)
	}
	if code, _ := f.do("POST", "/api/unpair", `{}`, nil); code != 400 {
		t.Fatalf("unpair while standalone = %d, want 400", code)
	}

	rst := newStore(t, "remote.db")
	rs := httptest.NewServer(server.NewHandler(rst, "secret"))
	t.Cleanup(rs.Close)
	u, _ := url.Parse(rs.URL)
	pair := func(host, port, password string) (int, []byte) {
		return f.do("POST", "/api/pair", map[string]any{"host": host, "port": port, "tls": false, "password": password}, nil)
	}

	if code, body := pair(u.Hostname(), u.Port(), "nope"); code != 401 || !strings.Contains(string(body), "rejected the password") {
		t.Fatalf("pair with a wrong password = %d %s", code, body)
	}
	if code, body := pair("", u.Port(), "secret"); code != 400 || !strings.Contains(string(body), "host is required") {
		t.Fatalf("pair without a host = %d %s", code, body)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) }))
	t.Cleanup(other.Close)
	ou, _ := url.Parse(other.URL)
	if code, body := pair(ou.Hostname(), ou.Port(), "secret"); code != 502 || !strings.Contains(string(body), "did not answer as a TAM server") {
		t.Fatalf("pair with a non-TAM server = %d %s", code, body)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	du, _ := url.Parse(dead.URL)
	dead.Close()
	if code, body := pair(du.Hostname(), du.Port(), "secret"); code != 502 || !strings.Contains(string(body), "unreachable") {
		t.Fatalf("pair with nothing listening = %d %s", code, body)
	}

	code, body := pair(u.Hostname(), u.Port(), "secret")
	if code != 200 || !strings.Contains(string(body), "Paired with") {
		t.Fatalf("pair = %d %s", code, body)
	}
	s, _ := config.Load(f.settings)
	if s.RemoteServer != u.Hostname() || s.RemotePort != u.Port() || s.RemoteKey == "" || s.RemoteName == "" || s.RemoteTLS {
		t.Fatalf("settings after pairing = %+v", s)
	}
	keys, _ := rst.ListKeys()
	if len(keys) != 1 || keys[0].AuthKey != s.RemoteKey || keys[0].Description == "" {
		t.Fatalf("server keys after pairing = %+v", keys)
	}
	f.h.sync.Tick()
	_, body = f.do("GET", "/api/status", nil, nil)
	st := decode[map[string]any](t, body)
	if st["mode"] != "remote" || st["state"] != "connected" || st["pending"] != float64(0) || st["failed"] != float64(0) || st["server_name"] == "" {
		t.Fatalf("status after pairing = %v", st)
	}
	if code, _ := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "P", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatalf("save after pairing = %d", code)
	}
	if ps, _ := rst.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("save did not reach the server: %v", ps)
	}

	code, body = f.do("POST", "/api/unpair", `{"password":"secret"}`, nil)
	if code != 200 || !strings.Contains(string(body), "Standalone again") {
		t.Fatalf("unpair = %d %s", code, body)
	}
	if keys, _ := rst.ListKeys(); len(keys) != 0 {
		t.Fatalf("unpair with the password must delete the key: %+v", keys)
	}
	s, _ = config.Load(f.settings)
	if s.RemoteServer != "" || s.RemoteKey != "" || s.RemoteName != "" {
		t.Fatalf("settings after unpair = %+v", s)
	}
	_, body = f.do("GET", "/api/status", nil, nil)
	if strings.TrimSpace(string(body)) != `{"mode":"standalone"}` {
		t.Fatalf("status after unpair = %s", body)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("unpair must keep the local data: %v", ps)
	}
}

func TestQueuedDeleteAndFailedList(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)
	if code, _ := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatal("seed save failed")
	}
	f.h.sync.Tick()

	addr := rs.Listener.Addr().String()
	rs.Close()
	f.h.sync.Tick()
	if code, _ := f.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
		t.Fatalf("delete with the server down = %d, want 200 (queued)", code)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("the mirror must drop the prefix at once: %v", ps)
	}
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending = %d, want the queued delete", p)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("cannot listen again on %s: %v", addr, err)
	}
	rs2 := &httptest.Server{Listener: ln, Config: &http.Server{Handler: server.NewHandler(rst, "secret")}}
	rs2.Start()
	t.Cleanup(rs2.Close)
	f.h.sync.Reset()
	f.h.sync.Tick()
	if ps, _ := rst.ListPrefixes(); len(ps) != 0 {
		t.Fatalf("the queued delete must reach the server: %v", ps)
	}
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("outbox after replay: pending %d failed %d", p, fl)
	}

	// A queued request the server refuses lands in the failed list, where
	// it can be retried or discarded.
	if err := f.h.sync.Enqueue("POST", "/api/nowhere", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 1 {
		t.Fatalf("after a refused replay: pending %d failed %d, want 0 and 1", p, fl)
	}
	_, body := f.do("GET", "/api/status", nil, nil)
	if st := decode[map[string]any](t, body); st["failed"] != float64(1) {
		t.Fatalf("status = %v, want failed 1", st)
	}
	code, body := f.do("POST", "/api/outbox/retry", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "Retrying 1 save") {
		t.Fatalf("retry = %d %s", code, body)
	}
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 1 {
		t.Fatalf("after retrying a hopeless request: pending %d failed %d", p, fl)
	}
	code, body = f.do("POST", "/api/outbox/discard", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "Discarded 1 save") {
		t.Fatalf("discard = %d %s", code, body)
	}
	if _, fl := pending(t, f.st); fl != 0 {
		t.Fatalf("failed after discard = %d", fl)
	}
}
