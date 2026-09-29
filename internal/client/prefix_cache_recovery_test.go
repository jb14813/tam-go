package client

import (
	"fmt"
	"reflect"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestPrefixReadCacheCannotConflictDuringRecovery(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		for _, staleFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("delete=%v/stale-first=%v", deletion, staleFirst), func(t *testing.T) {
				a, b := newFixture(t), newFixture(t)
				event := newCausalEventServer(t, nil, nil)
				event.configure(t, a, b)
				a.h.sync.Tick()
				b.h.sync.Tick()
				causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}})
				causalSave(t, a, "/api/tickets", oneTicket(42, "Confirmed buyer"))
				if code, body := b.do("GET", "/api/prefixes", nil, nil); code != 200 {
					t.Fatalf("cache prefix: %d %s", code, body)
				}
				if deletion {
					if code, body := a.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
						t.Fatalf("delete prefix: %d %s", code, body)
					}
				} else {
					causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
				}
				causalCaughtUp(t, a, b)
				event = event.replacement(t)
				event.configure(t, a, b)
				if staleFirst {
					b.h.sync.Tick()
					a.h.sync.Tick()
				} else {
					a.h.sync.Tick()
					b.h.sync.Tick()
				}
				conflicts, err := event.st.Conflicts()
				if err != nil {
					t.Fatal(err)
				}
				_, reportErr := event.st.AllTickets()
				if len(conflicts) != 0 {
					t.Fatalf("read-only prefix cache made %d conflict(s), blocked unrelated ticket read=%v; conflict=%+v", len(conflicts), reportErr, conflicts)
				}
				serverHas(t, event.st, 42, "Confirmed buyer")
				prefixes, err := event.st.ListPrefixes()
				if err != nil || deletion && len(prefixes) != 0 || !deletion && (len(prefixes) != 1 || prefixes[0].Color != "red") {
					t.Fatalf("recovery lost the accepted prefix choice: %+v %v", prefixes, err)
				}
			})
		}
	}
}

func TestPrefixReadCachePreservesAuthoredHistory(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}})
	before, _ := causalLocalBackup(t, a)
	causalSave(t, b, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
	if code, body := a.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("refresh: %d %s", code, body)
	}
	after, _ := causalLocalBackup(t, a)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("shared read replaced an authored prefix or its receipt: before=%+v after=%+v", before, after)
	}
	event.ts.Close()
	a.h.sync.Tick()
	code, body := a.do("GET", "/api/prefixes", nil, nil)
	menu := decode[[]store.Prefix](t, body)
	if code != 200 || len(menu) != 1 || menu[0].Color != "red" {
		t.Fatalf("offline menu lost latest shared configuration: %d %s", code, body)
	}
}

func TestPrefixReadCachePushIncludesOnlyAuthoredChoices(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}})
	causalSave(t, b, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}, {Prefix: "B", Color: "green", Weight: 2}})
	if code, body := a.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("cache: %d %s", code, body)
	}
	causalSave(t, b, "/api/prefixes", []store.Prefix{{Prefix: "B", Color: "yellow", Weight: 2}})
	causalSave(t, a, "/api/backuprestore/push/prefixes", map[string]any{})
	prefixes, err := event.st.ListPrefixes()
	want := []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}, {Prefix: "B", Color: "yellow", Weight: 2}}
	if err != nil || !reflect.DeepEqual(prefixes, want) {
		t.Fatalf("Push used cached configuration instead of authored choices: %+v %v", prefixes, err)
	}
}

func TestPrefixReadCacheNativeRestoreUpdatesOfflineMenuAndKeepsHistory(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}})
	backup, _ := causalLocalBackup(t, a)
	causalSave(t, b, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}, {Prefix: "B", Color: "green", Weight: 2}})
	if code, body := a.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("cache: %d %s", code, body)
	}
	event.ts.Close()
	a.h.sync.Tick()
	causalSave(t, a, "/api/backuprestore/local", backup)
	after, _ := causalLocalBackup(t, a)
	if !reflect.DeepEqual(after, backup) {
		t.Fatalf("native restore changed authored history: before=%+v after=%+v", backup, after)
	}
	// Opening the same client database preserves the shared menu and the
	// restored authored choice across another schema initialization.
	reopened := openStoreAt(t, a.dbPath)
	menu, err := reopened.ClientPrefixes()
	want := []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}, {Prefix: "B", Color: "green", Weight: 2}}
	if err != nil || !reflect.DeepEqual(menu, want) {
		t.Fatalf("restored prefix did not update the offline menu: %+v %v", menu, err)
	}
}

func TestPrefixReadCachePreservesUnknownLegacyRows(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO prefixes(prefix,color,weight) VALUES(' A/B ','blue',-1)`)
	before, _ := causalLocalBackup(t, f)
	rst, rs := remoteFixture(t, f)
	if err := rst.UpsertPrefixes([]store.Prefix{{Prefix: " A/B ", Color: "red", Weight: -1}}); err != nil {
		t.Fatal(err)
	}
	// GET returns the original API's plain rows, with no receipt extension.
	if code, body := f.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("legacy configuration: %d %s", code, body)
	}
	after, _ := causalLocalBackup(t, f)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("refresh guessed ownership or replaced an unknown legacy row: before=%+v after=%+v", before, after)
	}
	rs.Close()
	f.h.sync.Tick()
	code, body := f.do("GET", "/api/prefixes", nil, nil)
	menu := decode[[]store.Prefix](t, body)
	if code != 200 || len(menu) != 1 || menu[0].Prefix != " A/B " || menu[0].Color != "red" {
		t.Fatalf("legacy server cache unavailable offline: %d %s", code, body)
	}
}

func TestPrefixReadCacheRefreshRemovesDeletedMenuWithoutAuthorship(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}})
	if code, body := b.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("cache: %d %s", code, body)
	}
	if code, body := a.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if code, body := b.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("refresh: %d %s", code, body)
	}
	backup, _ := causalLocalBackup(t, b)
	if len(backup.Prefixes) != 0 || len(backup.DeletedPrefixes) != 0 || len(backup.Revisions) != 0 {
		t.Fatalf("a read-only cache became authored recovery data: %+v", backup)
	}
	event.ts.Close()
	b.h.sync.Tick()
	code, body := b.do("GET", "/api/prefixes", nil, nil)
	menu := decode[[]store.Prefix](t, body)
	if code != 200 || len(menu) != 0 {
		t.Fatalf("offline menu resurrected deleted configuration: %d %s", code, body)
	}
}

func TestPrefixReadCacheAllowsOfflineDeleteAndRecreate(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)
	if err := rst.UpsertPrefixes([]store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}}); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("cache: %d %s", code, body)
	}
	rs.Close()
	f.h.sync.Tick()
	if code, body := f.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
		t.Fatalf("offline delete of cached-only prefix: %d %s", code, body)
	}
	code, body := f.do("GET", "/api/prefixes", nil, nil)
	menu := decode[[]store.Prefix](t, body)
	if code != 200 || len(menu) != 0 {
		t.Fatalf("offline delete did not update menu: %d %s", code, body)
	}
	backup, _ := causalLocalBackup(t, f)
	if len(backup.DeletedPrefixes) != 1 || len(backup.Revisions) != 1 {
		t.Fatalf("explicit delete did not retain ownership/history: %+v", backup)
	}
	causalSave(t, f, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 2}})
	code, body = f.do("GET", "/api/prefixes", nil, nil)
	menu = decode[[]store.Prefix](t, body)
	if code != 200 || len(menu) != 1 || menu[0].Color != "red" {
		t.Fatalf("offline recreate did not update menu: %d %s", code, body)
	}
}
