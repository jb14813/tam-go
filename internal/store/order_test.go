package store

import (
	"errors"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

func newOrderStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := db.MigrateServer(s.db); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateClient(s.db); err != nil {
		t.Fatal(err)
	}
	return s
}

func phoneOf(t *testing.T, s *Store, id int) string {
	t.Helper()
	tk, err := s.Ticket("A", id)
	if err != nil || tk == nil {
		t.Fatalf("ticket A %d: %+v, %v", id, tk, err)
	}
	return tk.PhoneNumber
}

// TestInOrder: the server applies a client's save only when it is newer
// than the last one it applied from that client. A copy the network
// delivered late, or one sent twice, is skipped: applying it again could
// only undo newer saves.
func TestInOrder(t *testing.T) {
	s := newOrderStore(t)
	save := func(client string, n int64, phone string) bool {
		t.Helper()
		applied, err := s.InOrder(client, n, func(st *Store) error {
			return st.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, PhoneNumber: phone}})
		})
		if err != nil {
			t.Fatal(err)
		}
		return applied
	}
	if !save("L1", 2, "second") {
		t.Fatal("the first save of a client must apply")
	}
	if save("L1", 1, "first, late") || phoneOf(t, s, 1) != "second" {
		t.Fatalf("an older save applied: the ticket reads %q", phoneOf(t, s, 1))
	}
	if save("L1", 2, "second, again") || phoneOf(t, s, 1) != "second" {
		t.Fatalf("a save sent twice applied twice: the ticket reads %q", phoneOf(t, s, 1))
	}
	if !save("L2", 1, "other client") || phoneOf(t, s, 1) != "other client" {
		t.Fatal("each client has numbers of its own")
	}
	if !save("L1", 3, "third") || phoneOf(t, s, 1) != "third" {
		t.Fatal("a newer save must apply")
	}

	// A write that fails leaves the number where it was, so the same save
	// can be sent again.
	boom := errors.New("boom")
	if _, err := s.InOrder("L1", 4, func(*Store) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("InOrder = %v, want the write's error", err)
	}
	if !save("L1", 4, "fourth") {
		t.Fatal("a save whose first try failed must apply when sent again")
	}
}

// TestNextSave: a client numbers its saves, under a name made on first
// use. The numbers only grow. A data folder copied to another machine
// (another host name) gets a name of its own, so two clients never share
// one, and its numbers go on growing.
func TestNextSave(t *testing.T) {
	s := newOrderStore(t)
	a, err := s.NextSave("desk-1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.NextSave("desk-1")
	if a.Client == "" || b.Client != a.Client || b.Save != a.Save+1 {
		t.Fatalf("NextSave = %+v then %+v, want one name and numbers one apart", a, b)
	}
	c, _ := s.NextSave("desk-2")
	if c.Client == a.Client || c.Save <= b.Save {
		t.Fatalf("after a move to another machine NextSave = %+v, want a new name and a higher number than %d", c, b.Save)
	}
}

// TestRetriedSavesGetNewNumbers: a save set aside in the failed list and
// retried later is sent again now, after the client's newer saves, so it
// needs a new number; with its old one the server would skip it as stale.
func TestRetriedSavesGetNewNumbers(t *testing.T) {
	s := newOrderStore(t)
	first, _ := s.NextSave("desk")
	if _, err := s.SaveQueued("POST", "/api/tickets", []byte("[]"), first, func(*Store) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailAllOutbox("set aside"); err != nil {
		t.Fatal(err)
	}
	later, _ := s.NextSave("desk")
	if n, err := s.RetryFailed("desk"); err != nil || n != 1 {
		t.Fatalf("RetryFailed = %d, %v", n, err)
	}
	o, err := s.NextOutbox()
	if err != nil || o == nil {
		t.Fatalf("NextOutbox = %+v, %v", o, err)
	}
	if o.Order.Client != first.Client || o.Order.Save <= later.Save {
		t.Fatalf("the retried save carries %+v, want the client's name and a number above %d", o.Order, later.Save)
	}
}
