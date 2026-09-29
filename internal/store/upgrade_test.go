package store

import (
	"path/filepath"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

// TestQueueFromAnEarlierVersion: a client updated while saves were queued
// kept them in the outbox of the earlier version, which had no client name
// or save number. After the update they are still there, in order, and go
// out without a number, as they would have before.
func TestQueueFromAnEarlierVersion(t *testing.T) {
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, method TEXT NOT NULL,
			path TEXT NOT NULL, body BLOB, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE outbox_failed (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, method TEXT NOT NULL,
			path TEXT NOT NULL, body BLOB, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '',
			failed_at TEXT NOT NULL)`,
		`INSERT INTO outbox (created_at, method, path, body) VALUES ('2026-09-26T10:00:00Z', 'POST', '/api/tickets', '[1]')`,
		`INSERT INTO outbox (created_at, method, path, body) VALUES ('2026-09-26T10:00:01Z', 'DELETE', '/api/prefixes?p=Z', NULL)`,
	} {
		if _, err := sqldb.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.MigrateClient(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateClient(sqldb); err != nil {
		t.Fatalf("migrating twice: %v", err)
	}
	s := New(sqldb)
	o, err := s.NextOutbox()
	if err != nil || o == nil || o.Method != "POST" || string(o.Body) != "[1]" || o.Order != (Order{}) {
		t.Fatalf("the first queued save after the update = %+v, %v; want the POST, unnumbered", o, err)
	}
	if err := s.DeleteOutbox(o.ID); err != nil {
		t.Fatal(err)
	}
	if o, _ = s.NextOutbox(); o == nil || o.Method != "DELETE" || o.Order != (Order{}) {
		t.Fatalf("the second queued save after the update = %+v; want the DELETE, unnumbered", o)
	}

	// New saves are numbered from then on, after the old ones in the queue.
	next, err := s.NextSave("desk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveQueued("POST", "/api/tickets", []byte("[]"), next, func(*Store) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := s.OutboxCounts(); n != 2 {
		t.Fatalf("pending = %d, want the old delete and the new save", n)
	}
}
