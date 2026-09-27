package client

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/server"
)

// TestSavesCarryTheirNumber: every save the client sends names the client
// and numbers the save, one after the other. A save the server could not
// take keeps its number in the queue, so the copy the network may still
// deliver and the replay are one and the same save to the server.
func TestSavesCarryTheirNumber(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	k, err := rst.CreateKey("client")
	if err != nil {
		t.Fatal(err)
	}
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	var mu sync.Mutex
	var sent []string // client#save of every ticket save the server saw
	busy := false
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
			mu.Lock()
			sent = append(sent, r.Header.Get("X-TAM-Client-Name")+"#"+r.Header.Get("X-TAM-Save"))
			refuse := busy
			mu.Unlock()
			if refuse {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
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

	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "One"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	mu.Lock()
	busy = true
	mu.Unlock()
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "Two"), nil); code != 200 {
		t.Fatalf("save while the server is busy = %d %s, want 200 (queued)", code, body)
	}
	mu.Lock()
	busy = false
	mu.Unlock()
	f.h.sync.Reset()
	f.h.sync.Tick()
	if code, body := f.do("POST", "/api/tickets", oneTicket(1, "Three"), nil); code != 200 {
		t.Fatalf("save after the replay = %d %s", code, body)
	}

	mu.Lock()
	got := append([]string(nil), sent...)
	mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("the server saw %v, want the first save, the refused try, its replay and the third save", got)
	}
	client, first := split(t, got[0])
	for i, want := range []int64{first, first + 1, first + 1, first + 2} {
		name, n := split(t, got[i])
		if name != client || n != want {
			t.Fatalf("the server saw %v: save %d should be %s#%d", got, i+1, client, want)
		}
	}
	if rt, _ := rst.Ticket("A", 1); rt == nil || rt.FirstName != "Three" {
		t.Fatalf("server has %+v, want the last save", rt)
	}
}

func split(t *testing.T, s string) (string, int64) {
	t.Helper()
	name, number, ok := strings.Cut(s, "#")
	n, err := strconv.ParseInt(number, 10, 64)
	if !ok || name == "" || err != nil || n <= 0 {
		t.Fatalf("a save sent as %q, want a client name and a save number", s)
	}
	return name, n
}
