package store

import (
	"path/filepath"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

func newOutboxStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateClient(sqldb); err != nil {
		t.Fatal(err)
	}
	return New(sqldb)
}

func counts(t *testing.T, s *Store) (int, int) {
	t.Helper()
	pending, failed, err := s.OutboxCounts()
	if err != nil {
		t.Fatal(err)
	}
	return pending, failed
}

func TestOutboxOrderAndDelete(t *testing.T) {
	s := newOutboxStore(t)
	if next, err := s.NextOutbox(); err != nil || next != nil {
		t.Fatalf("empty outbox: next = %v, %v", next, err)
	}
	a, err := s.EnqueueOutbox("POST", "/api/tickets", []byte(`[{"a":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnqueueOutbox("DELETE", "/api/prefixes?p=A", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a >= b {
		t.Fatalf("ids must grow: %d, %d", a, b)
	}
	next, err := s.NextOutbox()
	if err != nil || next == nil || next.ID != a || next.Method != "POST" || string(next.Body) != `[{"a":1}]` || next.CreatedAt.IsZero() {
		t.Fatalf("next = %+v, %v; want the first request", next, err)
	}
	if err := s.NoteOutboxAttempt(a, "dial tcp: refused"); err != nil {
		t.Fatal(err)
	}
	next, _ = s.NextOutbox()
	if next.Attempts != 1 || next.LastError != "dial tcp: refused" {
		t.Fatalf("attempt not recorded: %+v", next)
	}
	if err := s.DeleteOutbox(a); err != nil {
		t.Fatal(err)
	}
	next, _ = s.NextOutbox()
	if next == nil || next.ID != b || next.Body != nil {
		t.Fatalf("after delete next = %+v, want the DELETE with no body", next)
	}
	if p, f := counts(t, s); p != 1 || f != 0 {
		t.Fatalf("counts = %d, %d; want 1, 0", p, f)
	}
}

func TestOutboxFailRetryDiscard(t *testing.T) {
	s := newOutboxStore(t)
	a, _ := s.EnqueueOutbox("POST", "/api/tickets", []byte(`[1]`))
	b, _ := s.EnqueueOutbox("POST", "/api/baskets", []byte(`[2]`))
	if err := s.FailOutbox(a, "400: prefix name must not be empty"); err != nil {
		t.Fatal(err)
	}
	if p, f := counts(t, s); p != 1 || f != 1 {
		t.Fatalf("counts = %d, %d; want 1, 1", p, f)
	}
	failed, err := s.ListFailed()
	if err != nil || len(failed) != 1 || failed[0].ID != a || failed[0].LastError != "400: prefix name must not be empty" || failed[0].Attempts != 1 {
		t.Fatalf("failed = %+v, %v", failed, err)
	}
	if next, _ := s.NextOutbox(); next == nil || next.ID != b {
		t.Fatalf("next = %+v, want %d", next, b)
	}

	n, err := s.RetryFailed("desk")
	if err != nil || n != 1 {
		t.Fatalf("RetryFailed = %d, %v", n, err)
	}
	// A retried request goes to the end of the queue, behind what still
	// waits, as it is sent again after that (see RetryFailed).
	if next, _ := s.NextOutbox(); next == nil || next.ID != b {
		t.Fatalf("after a retry the request that waited comes first: next = %+v", next)
	}
	if p, f := counts(t, s); p != 2 || f != 0 {
		t.Fatalf("counts = %d, %d; want 2, 0", p, f)
	}

	// Setting the queue aside (pairing with another server, unpairing)
	// moves what waits to the failed list, in order, with the reason.
	if n, err := s.FailAllOutbox("set aside"); err != nil || n != 2 {
		t.Fatalf("FailAllOutbox = %d, %v", n, err)
	}
	setAside, _ := s.ListFailed()
	if len(setAside) != 2 || setAside[0].ID != b || string(setAside[1].Body) != `[1]` || setAside[1].ID <= b || setAside[0].LastError != "set aside" {
		t.Fatalf("failed list = %+v, want the waiting request, then the retried one, with the reason", setAside)
	}
	if n, err := s.DiscardFailed(); err != nil || n != 2 {
		t.Fatalf("DiscardFailed = %d, %v", n, err)
	}
	if p, f := counts(t, s); p != 0 || f != 0 {
		t.Fatalf("counts after discarding = %d, %d; want 0, 0", p, f)
	}
}
