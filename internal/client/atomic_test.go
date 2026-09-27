package client

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/store"
)

// brokenQueueFixture is a client with its server gone, so saves are
// queued, over a database whose outbox table is dropped: queueing fails
// after the save to the client's own copy could already have been written.
// That is the moment a client stopping (a flat battery) would hit.
func brokenQueueFixture(t *testing.T) *fixture {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	for _, migrate := range []func(*sql.DB) error{db.Migrate, db.MigrateClient} {
		if err := migrate(sqldb); err != nil {
			t.Fatal(err)
		}
	}
	st := store.New(sqldb)
	settings := filepath.Join(t.TempDir(), "settings.json")
	h := newHandler(st, settings, testDist, WithTimings(testTimings))
	ts := httptest.NewServer(h.routes(testDist))
	t.Cleanup(ts.Close)
	f := &fixture{t: t, url: ts.URL, st: st, settings: settings, h: h}
	_, rs := remoteFixture(t, f)
	if code, body := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatalf("save while the server answers = %d %s", code, body)
	}
	rs.Close()
	f.h.sync.Tick()
	if _, err := sqldb.Exec(`DROP TABLE outbox`); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestQueuedSaveIsAllOrNothing: a save that cannot be queued must not stay
// in the client's own copy either. Otherwise the client shows a save that
// never reaches the server, and nobody types it again.
func TestQueuedSaveIsAllOrNothing(t *testing.T) {
	f := brokenQueueFixture(t)
	if code, _ := f.do("POST", "/api/tickets", oneTicket(3, "Stranded"), nil); code != 500 {
		t.Fatalf("a save that could not be queued = %d, want 500", code)
	}
	if lt, _ := f.st.Ticket("A", 3); lt != nil {
		t.Fatalf("the client's copy holds a save that is not queued: %+v", lt)
	}
}

// TestQueuedDeleteIsAllOrNothing: the same for deleting a prefix.
func TestQueuedDeleteIsAllOrNothing(t *testing.T) {
	f := brokenQueueFixture(t)
	if code, _ := f.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 500 {
		t.Fatalf("a delete that could not be queued = %d, want 500", code)
	}
	if ps, _ := f.st.ListPrefixes(); len(ps) != 1 {
		t.Fatalf("the client's copy dropped a prefix whose delete is not queued: %+v", ps)
	}
}
