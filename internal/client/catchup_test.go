package client

import (
	"testing"

	"ticket-auction-manager/tam-go/internal/config"
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
