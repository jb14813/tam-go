package client

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

// pairTo pairs the fixture's client with the server at rawURL.
func (f *fixture) pairTo(rawURL string) string {
	f.t.Helper()
	u, _ := url.Parse(rawURL)
	code, body := f.do("POST", "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "password": "secret"}, nil)
	if code != 200 {
		f.t.Fatalf("pair with %s = %d %s", rawURL, code, body)
	}
	return string(body)
}

// namedServer is a tam-server handler over st that reports name.
func namedServer(t *testing.T, st *store.Store, name string) *httptest.Server {
	t.Helper()
	rs := httptest.NewServer(server.NewHandler(st, server.FixedPassword("secret"), server.WithInfo(server.Info{Name: name})))
	t.Cleanup(rs.Close)
	return rs
}

// TestPairingAgainSendsTheQueue: when the server refuses a client's key
// (an admin deleted it by mistake), the bar says so and the volunteer pairs
// again. The saves queued meanwhile must then reach the server, not vanish.
func TestPairingAgainSendsTheQueue(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	rs := namedServer(t, rst, "tam-box")
	f.pairTo(rs.URL)
	f.h.sync.Tick()

	keys, _ := rst.ListKeys()
	if _, err := rst.DeleteKey(keys[0].AuthKey); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("POST", "/api/tickets", oneTicket(7, "Queued"), nil); code != 200 {
		t.Fatalf("save with a deleted key = %d %s, want 200 (queued)", code, body)
	}
	if p, _ := pending(t, f.st); p != 1 {
		t.Fatalf("pending = %d, want the refused save queued", p)
	}

	f.pairTo(rs.URL)
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("after pairing again: pending %d failed %d, want both 0", p, fl)
	}
	if rt, _ := rst.Ticket("A", 7); rt == nil || rt.FirstName != "Queued" {
		t.Fatalf("the save queued before pairing again must reach the server, it has %+v", rt)
	}
}

// TestPairingTheSameServerAtANewAddress: a server that came back on another
// address (a new DHCP lease) is still the same server; the queue for it is
// sent there once the client is paired with the new address.
func TestPairingTheSameServerAtANewAddress(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	old := namedServer(t, rst, "tam-box")
	f.pairTo(old.URL)
	f.h.sync.Tick()
	old.Close()
	if code, body := f.do("POST", "/api/tickets", oneTicket(8, "Moved"), nil); code != 200 {
		t.Fatalf("save with the server gone = %d %s", code, body)
	}

	moved := namedServer(t, rst, "tam-box")
	f.pairTo(moved.URL)
	f.h.sync.Tick()
	if p, fl := pending(t, f.st); p != 0 || fl != 0 {
		t.Fatalf("after pairing with the new address: pending %d failed %d, want both 0", p, fl)
	}
	if rt, _ := rst.Ticket("A", 8); rt == nil || rt.FirstName != "Moved" {
		t.Fatalf("the queued save must reach the same server at its new address, it has %+v", rt)
	}
}

// The event's waiting saves follow its replacement server, regardless of name.
func TestPairingAnotherServerSendsTheEventQueue(t *testing.T) {
	f := newFixture(t)
	first := namedServer(t, newServerStore(t), "first-box")
	f.pairTo(first.URL)
	f.h.sync.Tick()
	first.Close()
	if code, body := f.do("POST", "/api/tickets", oneTicket(9, "Waiting"), nil); code != 200 {
		t.Fatalf("save with the server gone = %d %s", code, body)
	}

	other := newServerStore(t)
	second := namedServer(t, other, "second-box")
	msg := f.pairTo(second.URL)
	if p, fl := pending(t, f.st); p != 1 || fl != 0 {
		t.Fatalf("pairing must preserve pending event saves: %d pending, %d failed; %s", p, fl, msg)
	}
	f.h.sync.Tick()
	if rt, _ := other.Ticket("A", 9); rt == nil || rt.FirstName != "Waiting" {
		t.Fatalf("the queued save must reach the replacement automatically, it has %+v", rt)
	}
}

// Standalone work after unpairing must stay newer than the preserved queue.
func TestUnpairKeepsTheQueueAndLaterStandaloneEdits(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	rs := namedServer(t, rst, "tam-box")
	f.pairTo(rs.URL)
	f.h.sync.Tick()
	keys, _ := rst.ListKeys()
	rst.DeleteKey(keys[0].AuthKey)
	f.do("POST", "/api/tickets", oneTicket(10, "Kept"), nil)

	code, body := f.do("POST", "/api/unpair", `{}`, nil)
	if code != 200 || !strings.Contains(string(body), "1 save") {
		t.Fatalf("unpair = %d %s, want it to say a save was kept", code, body)
	}
	if p, fl := pending(t, f.st); p != 1 || fl != 0 {
		t.Fatalf("after unpairing: pending %d failed %d, want 1 and 0", p, fl)
	}
	if code, body := f.do("POST", "/api/tickets", oneTicket(10, "Newer standalone edit"), nil); code != 200 {
		t.Fatalf("standalone edit = %d %s", code, body)
	}
	f.pairTo(rs.URL)
	f.h.sync.Tick()
	if rt, _ := rst.Ticket("A", 10); rt == nil || rt.FirstName != "Newer standalone edit" {
		t.Fatalf("a newer standalone edit must follow the preserved queue, got %+v", rt)
	}
}
