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

	n, err := s.RetryFailed()
	if err != nil || n != 1 {
		t.Fatalf("RetryFailed = %d, %v", n, err)
	}
	if next, _ := s.NextOutbox(); next == nil || next.ID != a {
		t.Fatalf("a retried request keeps its place in the order: next = %+v", next)
	}
	if p, f := counts(t, s); p != 2 || f != 0 {
		t.Fatalf("counts = %d, %d; want 2, 0", p, f)
	}

	if err := s.FailOutbox(a, "again"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DiscardFailed(); err != nil || n != 1 {
		t.Fatalf("DiscardFailed = %d, %v", n, err)
	}
	// Setting the queue aside (pairing with another server, unpairing)
	// moves what waits to the failed list, with its id and the reason.
	waiting, _ := s.NextOutbox()
	if n, err := s.FailAllOutbox("set aside"); err != nil || n != 1 {
		t.Fatalf("FailAllOutbox = %d, %v", n, err)
	}
	if p, f := counts(t, s); p != 0 || f != 1 {
		t.Fatalf("counts after setting the queue aside = %d, %d; want 0, 1", p, f)
	}
	setAside, _ := s.ListFailed()
	if len(setAside) != 1 || setAside[0].ID != waiting.ID || setAside[0].LastError != "set aside" {
		t.Fatalf("failed list = %+v, want the request that waited, with the reason", setAside)
	}
}
