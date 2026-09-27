package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

// TestConcurrentWritesTakeTurns: many requests writing at once must all get
// through. SQLite lets one writer in at a time and has the others retry in
// a busy wait that is not first come, first served, so where every commit
// is flushed to a slow disk a writer can time out behind the others and
// fail. The store's own writes take turns instead. The busy wait is cut to
// a few milliseconds here so the contention shows on a fast disk too.
func TestConcurrentWritesTakeTurns(t *testing.T) {
	sqldb, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "busy.db")+"?_pragma=busy_timeout(5)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	for _, migrate := range []func(*sql.DB) error{db.Migrate, db.MigrateServer, db.MigrateClient} {
		if err := migrate(sqldb); err != nil {
			t.Fatal(err)
		}
	}
	s := New(sqldb)
	key, err := s.CreateKey("laptop")
	if err != nil {
		t.Fatal(err)
	}

	const writers, each = 32, 20
	errs := make(chan error, writers*each*3)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				errs <- s.UpsertTickets([]Ticket{{Prefix: "A", TID: w*each + i, FirstName: "F", Pref: "CALL"}})
				_, err := s.EnqueueOutbox("POST", "/api/tickets", []byte("[]"))
				errs <- err
				errs <- s.TouchKey(key.AuthKey)
			}
		}()
	}
	wg.Wait()
	close(errs)
	failed := 0
	for err := range errs {
		if err != nil {
			if failed < 3 {
				t.Error(err)
			}
			failed++
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d writes failed", failed, writers*each*3)
	}
	if ts, _ := s.AllTickets(); len(ts) != writers*each {
		t.Fatalf("%d tickets saved, want %d", len(ts), writers*each)
	}
}
