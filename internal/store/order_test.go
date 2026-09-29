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
// than the last one it applied from that client. The last one arriving
// again (the client gave up waiting and resent it, or the network delivered
// a copy late) is a repeat: not applied twice, answered as done. Anything
// else at or below the last number is behind: not applied, and the caller
// learns the last number, so a client whose count went back can number the
// save anew instead of losing it.
func TestInOrder(t *testing.T) {
	s := newOrderStore(t)
	save := func(client string, n int64, phone string) (Outcome, int64) {
		t.Helper()
		out, last, err := s.InOrder(client, n, "digest of "+phone, func(st *Store) error {
			return st.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, PhoneNumber: phone}})
		})
		if err != nil {
			t.Fatal(err)
		}
		return out, last
	}
	if out, last := save("L1", 2, "second"); out != Applied || last != 2 {
		t.Fatalf("the first save of a client = %v %d, want applied", out, last)
	}
	if out, last := save("L1", 2, "second"); out != Repeat || last != 2 || phoneOf(t, s, 1) != "second" {
		t.Fatalf("the last save sent again = %v %d, want a repeat (the ticket reads %q)", out, last, phoneOf(t, s, 1))
	}
	if out, last := save("L1", 1, "first, late"); out != Behind || last != 2 || phoneOf(t, s, 1) != "second" {
		t.Fatalf("an older save = %v %d, want behind 2 and not applied (the ticket reads %q)", out, last, phoneOf(t, s, 1))
	}
	// The same number with different content is not a repeat: a client whose
	// data folder was put back from a copy numbers a new save as 2 again.
	if out, last := save("L1", 2, "a different second"); out != Behind || last != 2 || phoneOf(t, s, 1) != "second" {
		t.Fatalf("a different save under the last number = %v %d, want behind (the ticket reads %q)", out, last, phoneOf(t, s, 1))
	}
	if out, _ := save("L2", 1, "other client"); out != Applied || phoneOf(t, s, 1) != "other client" {
		t.Fatal("each client has numbers of its own")
	}
	if out, _ := save("L1", 3, "third"); out != Applied || phoneOf(t, s, 1) != "third" {
		t.Fatal("a newer save must apply")
	}

	// A write that fails leaves the record as it was, so the same save can
	// be sent again.
	boom := errors.New("boom")
	if _, _, err := s.InOrder("L1", 4, "digest of fourth", func(*Store) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("InOrder = %v, want the write's error", err)
	}
	if out, _ := save("L1", 4, "fourth"); out != Applied {
		t.Fatal("a save whose first try failed must apply when sent again")
	}
}

// TestNextSaveAfter: told the last number the server applied from it, a
// client numbers its next save past it; a count already past stays.
func TestNextSaveAfter(t *testing.T) {
	s := newOrderStore(t)
	a, _ := s.NextSave("desk")
	b, err := s.NextSaveAfter("desk", a.Save+40)
	if err != nil || b.Client != a.Client || b.Save != a.Save+41 {
		t.Fatalf("NextSaveAfter = %+v, %v; want %s and %d", b, err, a.Client, a.Save+41)
	}
	c, _ := s.NextSaveAfter("desk", 3)
	if c.Save != b.Save+1 {
		t.Fatalf("NextSaveAfter below the count = %+v, want %d", c, b.Save+1)
	}
	if err := s.SkipSavesTo(c.Save + 10); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.NextSave("desk"); d.Save != c.Save+11 {
		t.Fatalf("after SkipSavesTo the next save is %d, want %d", d.Save, c.Save+11)
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
// retried later is sent again now, after the saves already queued, so it
// needs a new number and a place at the end of the queue: the server takes
// a client's saves in the order of their numbers, and the replay sends the
// queue in its order, so the two must agree.
func TestRetriedSavesGetNewNumbers(t *testing.T) {
	s := newOrderStore(t)
	const firstBody = `[{"prefix":"A","t_id":1,"first_name":"set aside"}]`
	const laterBody = `[{"prefix":"A","t_id":1,"first_name":"queued"}]`
	first, _ := s.NextSave("desk")
	if _, err := s.SaveQueued("POST", "/api/tickets", []byte(firstBody), first, func(st *Store) error {
		return st.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "set aside"}})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailAllOutbox("set aside"); err != nil {
		t.Fatal(err)
	}
	later, _ := s.NextSave("desk")
	if _, err := s.SaveQueued("POST", "/api/tickets", []byte(laterBody), later, func(st *Store) error {
		return st.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "queued"}})
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RetryFailed("desk"); err != nil || n != 1 {
		t.Fatalf("RetryFailed = %d, %v", n, err)
	}
	if ticket, err := s.Ticket("A", 1); err != nil || ticket == nil || ticket.FirstName != "set aside" {
		t.Fatalf("retry did not retain its new local choice: %+v %v", ticket, err)
	}
	var queue []Outbox
	for {
		o, err := s.NextOutbox()
		if err != nil {
			t.Fatal(err)
		}
		if o == nil {
			break
		}
		queue = append(queue, *o)
		if err := s.DeleteOutbox(o.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(queue) != 2 || string(queue[0].Body) != laterBody || string(queue[1].Body) != firstBody {
		t.Fatalf("queue after the retry = %+v, want the queued save first, then the retried one", queue)
	}
	if queue[1].Order.Client != first.Client || queue[1].Order.Save <= queue[0].Order.Save {
		t.Fatalf("the retried save carries %+v, want the client's name and a number above the queued save's %d", queue[1].Order, queue[0].Order.Save)
	}
}
