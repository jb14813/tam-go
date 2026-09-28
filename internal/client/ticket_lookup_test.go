package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func checkTicketLookup(t *testing.T, f *fixture, id int, mode, source, found string) store.Ticket {
	t.Helper()
	res, err := http.Get(fmt.Sprintf("%s/api/tickets/A/%d", f.url, id))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ticket lookup status = %d", res.StatusCode)
	}
	if got := res.Header.Get("X-TAM-Mode"); got != mode {
		t.Errorf("ticket %d mode = %q, want %q", id, got, mode)
	}
	if got := res.Header.Get("X-TAM-Source"); got != source {
		t.Errorf("ticket %d source = %q, want %q", id, got, source)
	}
	if got := res.Header.Get("X-TAM-Found"); got != found {
		t.Errorf("ticket %d found = %q, want %q", id, got, found)
	}
	var ticket store.Ticket
	if err := json.NewDecoder(res.Body).Decode(&ticket); err != nil {
		t.Fatal(err)
	}
	if ticket.Prefix != "A" || ticket.TID != id {
		t.Errorf("ticket lookup changed response shape: %+v", ticket)
	}
	return ticket
}

func TestTicketLookupReadsAnotherClientsBuyerAndQueuedRejoin(t *testing.T) {
	owner, reader := newFixture(t), newFixture(t)
	fs := newFlakyServer(t, newServerStore(t), "shared event")
	for _, f := range []*fixture{owner, reader} {
		f.pairTo(fs.ts.URL)
		f.h.sync.Tick()
	}
	buyer := store.Ticket{Prefix: "A", TID: 42, FirstName: "Alice", LastName: "Buyer", PhoneNumber: "555-0142", Pref: "TEXT"}
	if code, body := owner.do("POST", "/api/tickets", []store.Ticket{buyer, {Prefix: "A", TID: 43}}, nil); code != 200 {
		t.Fatalf("buyer entry: %d %s", code, body)
	}
	if got := checkTicketLookup(t, reader, 42, "remote", "server", "1"); got != buyer {
		t.Errorf("shared buyer = %+v, want %+v", got, buyer)
	}
	// A saved blank ticket is distinct from the historical empty placeholder.
	checkTicketLookup(t, reader, 43, "remote", "server", "1")
	checkTicketLookup(t, reader, 99, "remote", "server", "0")

	// This client can reach the aggregate server while another workstation
	// still has an unsent buyer. A server result cannot certify every queue.
	fs.setBusy(true)
	queuedBuyer := store.Ticket{Prefix: "A", TID: 44, FirstName: "Queued", LastName: "Buyer", PhoneNumber: "555-0144", Pref: "CALL"}
	if code, body := owner.do("POST", "/api/tickets", []store.Ticket{queuedBuyer}, nil); code != 200 {
		t.Fatalf("queued buyer entry: %d %s", code, body)
	}
	if p, failed := pending(t, owner.st); p != 1 || failed != 0 {
		t.Fatalf("owner queue = %d pending, %d failed", p, failed)
	}
	checkTicketLookup(t, reader, 44, "remote", "server", "0")
	fs.setBusy(false)
	owner.h.sync.Tick()
	if p, failed := pending(t, owner.st); p != 0 || failed != 0 {
		t.Fatalf("owner queue after reconnect = %d pending, %d failed", p, failed)
	}
	if got := checkTicketLookup(t, reader, 44, "remote", "server", "1"); got != queuedBuyer {
		t.Errorf("buyer after queue delivery = %+v, want %+v", got, queuedBuyer)
	}
	if rows, err := reader.st.AllTickets(); err != nil || len(rows) != 0 {
		t.Fatalf("shared buyer lookups adopted another client's tickets: %+v, %v", rows, err)
	}
}

func TestTicketLookupIdentifiesLocalFallbackWhilePendingOrOffline(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "offline"}[offline], func(t *testing.T) {
			owner, reader := newFixture(t), newFixture(t)
			fs := newFlakyServer(t, newServerStore(t), "shared event")
			for _, f := range []*fixture{owner, reader} {
				f.pairTo(fs.ts.URL)
				f.h.sync.Tick()
			}
			if code, body := owner.do("POST", "/api/tickets", oneTicket(42, "Other client"), nil); code != 200 {
				t.Fatalf("shared buyer entry: %d %s", code, body)
			}
			if offline {
				fs.ts.Close()
				reader.h.sync.Tick()
			} else {
				fs.setBusy(true)
			}
			if code, body := reader.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 51, FirstName: "Entered here"}, {Prefix: "A", TID: 52}}, nil); code != 200 {
				t.Fatalf("local buyer entry: %d %s", code, body)
			}
			if p, failed := pending(t, reader.st); p != 1 || failed != 0 {
				t.Fatalf("local queue = %d pending, %d failed", p, failed)
			}
			if got := checkTicketLookup(t, reader, 51, "remote", "local", "1"); got.FirstName != "Entered here" {
				t.Errorf("local buyer = %+v", got)
			}
			checkTicketLookup(t, reader, 52, "remote", "local", "1")
			checkTicketLookup(t, reader, 42, "remote", "local", "0")
			checkTicketLookup(t, reader, 99, "remote", "local", "0")
			if rows, err := reader.st.AllTickets(); err != nil || len(rows) != 2 {
				t.Fatalf("fallback should retain only own entries: %+v, %v", rows, err)
			}
		})
	}
}

func TestTicketLookupIdentifiesStandaloneLocalEntries(t *testing.T) {
	f := newFixture(t)
	if code, body := f.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 51, FirstName: "Standalone buyer"}, {Prefix: "A", TID: 52}}, nil); code != 200 {
		t.Fatalf("standalone buyer entry: %d %s", code, body)
	}
	if got := checkTicketLookup(t, f, 51, "standalone", "local", "1"); got.FirstName != "Standalone buyer" {
		t.Errorf("standalone buyer = %+v", got)
	}
	checkTicketLookup(t, f, 52, "standalone", "local", "1")
	checkTicketLookup(t, f, 99, "standalone", "local", "0")
}
