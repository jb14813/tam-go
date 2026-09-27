package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
)

// TestTransactionsWaitForTheWriteLock: a transaction that reads before it
// writes, as InOrder does, waits for another holder of SQLite's write lock
// (the busy timeout) instead of failing at once with "database is locked".
// SQLite does not wait for a transaction that already read when it asks to
// write; under load a reader can take the write lock for a moment to repair
// the WAL index, which failed a save now and then with 50 clients.
func TestTransactionsWaitForTheWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.db")
	sqldb, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateServer(sqldb); err != nil {
		t.Fatal(err)
	}
	s := New(sqldb)
	if out, _, err := s.InOrder("L1", 1, "d1", func(st *Store) error { return nil }); err != nil || out != Applied {
		t.Fatalf("first save = %v, %v", out, err)
	}

	// Another connection holds the write lock for a moment.
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	conn, err := other.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		conn.ExecContext(t.Context(), "COMMIT")
		close(released)
	}()

	out, _, err := s.InOrder("L1", 2, "d2", func(st *Store) error {
		return st.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "Waited"}})
	})
	<-released
	if err != nil || out != Applied {
		t.Fatalf("a save while another connection held the write lock = %v, %v; want it applied after the wait", out, err)
	}
}
