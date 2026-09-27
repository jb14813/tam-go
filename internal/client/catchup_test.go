package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func oneTicket(id int, first string) []store.Ticket {
	return []store.Ticket{{Prefix: "A", TID: id, FirstName: first, Pref: "CALL"}}
}

// TestSavesAfterAnOutageKeepTheirOrder: a client that sees its server again
// while saves are still queued must not let a new save overtake them, or
// the older queued save lands last and wins; and a sheet opened meanwhile
// must show what the client saved, not the server's older copy.
func TestSavesAfterAnOutageKeepTheirOrder(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "First"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}

	// The server refuses the client for a while, so the next save is queued.
	s, _ := config.Load(f.settings)
	goodKey := s.RemoteKey
	s.RemoteKey = "WRONG"
	config.Save(f.settings, s)
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "Second"), nil); code != 200 {
		t.Fatalf("save while refused = %d %s", code, body)
	}
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending = %d, want 1", p)
	}

	// The heartbeat sees the server again; the queued save is not sent yet.
	s.RemoteKey = goodKey
	config.Save(f.settings, s)
	f.h.sync.NoteSuccess()

	_, body := f.do("GET", "/api/tickets/A/1/1", nil, nil)
	if rows := decode[[]store.Ticket](t, body); len(rows) != 1 || rows[0].FirstName != "Second" {
		t.Errorf("the sheet opened while saves are queued shows %+v, want the client's own save (Second)", rows)
	}
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "Third"), nil); code != 200 {
		t.Fatalf("save while saves are queued = %d %s", code, body)
	}
	f.h.sync.Tick()

	if p, _ := pending(t, f.st); p != 0 {
		t.Fatalf("pending after the replay = %d, want 0", p)
	}
	if rt, _ := rst.Ticket("A", 1); rt == nil || rt.FirstName != "Third" {
		t.Fatalf("server has %+v, want the last save (Third)", rt)
	}
	if lt, _ := f.st.Ticket("A", 1); lt == nil || lt.FirstName != "Third" {
		t.Fatalf("the client's copy has %+v, want the last save (Third)", lt)
	}
}

// TestPullKeepsSavesMadeWhileItDownloads: after a reconnect the client
// downloads the server's data into its own copy. A save made while that
// download was on its way is newer than the download, so the download must
// not overwrite it.
func TestPullKeepsSavesMadeWhileItDownloads(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	k, err := rst.CreateKey("client")
	if err != nil {
		t.Fatal(err)
	}
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	var mu sync.Mutex
	var during func() // runs after the server has read the download, before it answers
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hook := during
		if hook != nil && r.Method == http.MethodGet && r.URL.Path == "/api/backuprestore" {
			during = nil
		} else {
			hook = nil
		}
		mu.Unlock()
		if hook == nil {
			inner.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		hook()
		for name, values := range rec.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(rs.Close)
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), k.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}

	if code, body := f.do("POST", "/api/tickets", oneTicket(2, "Old"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	mu.Lock()
	during = func() {
		body, _ := json.Marshal(oneTicket(2, "New"))
		res, err := http.Post(f.url+"/api/tickets", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Errorf("save during the download: %v", err)
			return
		}
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("save during the download = %d %s", res.StatusCode, data)
		}
	}
	mu.Unlock()
	f.h.sync.Tick() // the first tick after connecting pulls the server's data

	mu.Lock()
	pulled := during == nil
	mu.Unlock()
	if !pulled {
		t.Fatal("the tick did not download the server's data")
	}
	if rt, _ := rst.Ticket("A", 2); rt == nil || rt.FirstName != "New" {
		t.Fatalf("server has %+v, want New", rt)
	}
	if lt, _ := f.st.Ticket("A", 2); lt == nil || lt.FirstName != "New" {
		t.Fatalf("the client's copy has %+v after the download, want the newer save (New)", lt)
	}
}

// TestPullTakesLegacyPrefixNames: a server whose database came from the
// original app can hold prefix names the pages would not let anyone type
// now, such as A/B. A client copying the server's data must take them as
// they are; refusing one refused the whole copy, and the client kept none of
// the server's tickets.
func TestPullTakesLegacyPrefixNames(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	if err := rst.UpsertPrefixes([]store.Prefix{{Prefix: "A/B", Color: "red", Weight: 1}, {Prefix: "C", Color: "blue", Weight: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := rst.UpsertTickets([]store.Ticket{{Prefix: "A/B", TID: 1, FirstName: "Old", Pref: "CALL"}, {Prefix: "C", TID: 2, FirstName: "New", Pref: "CALL"}}); err != nil {
		t.Fatal(err)
	}
	f.h.sync.Tick() // connects and copies the server's data
	if ps, _ := f.st.ListPrefixes(); len(ps) != 2 {
		t.Fatalf("the client's prefixes after the copy = %+v, want A/B and C", ps)
	}
	if ts, _ := f.st.AllTickets(); len(ts) != 2 {
		t.Fatalf("the client's tickets after the copy = %+v, want both", ts)
	}
}
