package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

func TestRestorePreservesCallerSnapshotHistory(t *testing.T) {
	source, target := newTestStore(t), newTestStore(t)
	must(t, source.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "Restored"}}))
	backup, err := source.ExportClientBackup()
	must(t, err)
	before, err := json.Marshal(backup)
	must(t, err)
	must(t, target.RestoreSnapshot(backup))
	after, err := json.Marshal(backup)
	must(t, err)
	if string(before) != string(after) {
		t.Fatal("restore mutated caller's history")
	}
	must(t, ValidateNativeBackup(&backup))
	_, err = target.MatchingReceipt(backup)
	must(t, err)
}

func TestCountsPreserveBuyerTupleBoundaries(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{
		{Prefix: "A", TID: 1, FirstName: "ann", LastName: "able", PhoneNumber: "555"},
		{Prefix: "A", TID: 2, FirstName: "anna", LastName: "ble", PhoneNumber: "555"},
	}))
	counts, err := s.ReportCounts()
	must(t, err)
	if len(counts) != 2 || counts[0].UniqueBuyers != 2 || counts[1].UniqueBuyers != 2 || counts[0].TotalBuys != 2 || counts[1].TotalBuys != 2 {
		t.Fatalf("distinct buyer tuples collapsed: %+v", counts)
	}
}

func TestCountsDistinguishTotalPrefixFromAggregate(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{{Prefix: "Total", TID: 1, FirstName: "Ann"}, {Prefix: "A", TID: 1, FirstName: "Ben"}}))
	counts, err := s.ReportCounts()
	must(t, err)
	if len(counts) != 3 || counts[1].Prefix != "Total" || counts[1].IsTotal || counts[1].TotalBuys != 1 || !counts[2].IsTotal || counts[2].TotalBuys != 2 {
		t.Fatalf("series and total must remain distinct: %+v", counts)
	}
}

func TestUnsafeNumericIdentityRejectedAtomically(t *testing.T) {
	for _, kind := range []string{"ticket", "basket", "drawing"} {
		t.Run(kind, func(t *testing.T) {
			s := newTestStore(t)
			var err error
			switch kind {
			case "ticket":
				err = s.UpsertTickets([]Ticket{{Prefix: "A", TID: 1}, {Prefix: "A", TID: 9007199254740992}})
			case "basket":
				err = s.UpsertBaskets([]Basket{{Prefix: "A", BID: 1}, {Prefix: "A", BID: 9007199254740992}})
			case "drawing":
				err = s.UpsertWinning([]Basket{{Prefix: "A", BID: 1}, {Prefix: "A", BID: 2, WinningTicket: 9007199254740992}})
			}
			if err == nil {
				t.Fatal("unsafe identity accepted")
			}
			backup, err := s.ExportClientBackup()
			must(t, err)
			if len(backup.Tickets) != 0 || len(backup.Baskets) != 0 || len(backup.Revisions) != 0 {
				t.Fatalf("rejected batch partially applied: %+v", backup)
			}
		})
	}
}

func TestLargestSafeIdentitySurvivesNativeBackup(t *testing.T) {
	source := newTestStore(t)
	must(t, source.UpsertTickets([]Ticket{{Prefix: "A", TID: 9007199254740991, FirstName: "Exact"}}))
	must(t, source.UpsertWinning([]Basket{{Prefix: "A", BID: 9007199254740991, WinningTicket: 9007199254740991}}))
	backup, err := source.ExportClientBackup()
	must(t, err)
	destination := newTestStore(t)
	must(t, destination.ImportClientBackup(backup))
	ticket, err := destination.Ticket("A", 9007199254740991)
	must(t, err)
	basket, err := destination.Basket("A", 9007199254740991)
	must(t, err)
	if ticket == nil || ticket.FirstName != "Exact" || basket == nil || basket.WinningTicket != 9007199254740991 {
		t.Fatalf("safe boundary changed identity: ticket=%+v basket=%+v", ticket, basket)
	}
}

func TestImportNewerNativeBackupAfterDatabaseRollback(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}))
	rollbackPath := filepath.Join(t.TempDir(), "rollback.db")
	_, err := s.db.Exec(`VACUUM INTO ?`, rollbackPath)
	must(t, err)
	must(t, s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	latest, err := s.ExportClientBackup()
	must(t, err)
	// Open an actual older full database copy, then restore newer native JSON.
	rolledBackDB, err := db.Open(rollbackPath)
	must(t, err)
	t.Cleanup(func() { rolledBackDB.Close() })
	rolledBack := New(rolledBackDB)
	must(t, rolledBack.ImportClientBackup(latest))
	for attempt := 0; attempt < 2; attempt++ {
		if err := rolledBack.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 43}}); err != nil {
			t.Errorf("new standalone edit after restoring newer native file failed (attempt %d): %v", attempt+1, err)
		}
	}
}

func TestImportNewerNativeBackupAdvancesClientSaveOrderAfterRollback(t *testing.T) {
	s := newOutboxStore(t)
	save := func(winner int) Order {
		t.Helper()
		order, err := s.NextSave("same-workstation")
		must(t, err)
		rows := []Basket{{Prefix: "A", BID: 1, WinningTicket: winner}}
		queueRecoveryEdit(t, s, order, "/api/drawing", rows, func(st *Store) error { return st.UpsertWinning(rows) })
		return order
	}
	first := save(41)
	rollbackPath := filepath.Join(t.TempDir(), "client-rollback.db")
	_, err := s.db.Exec(`VACUUM INTO ?`, rollbackPath)
	must(t, err)
	latestOrder := save(42)
	if latestOrder.Client != first.Client || latestOrder.Save != 2 {
		t.Fatalf("unexpected client lineage: first=%+v latest=%+v", first, latestOrder)
	}
	latest, err := s.ExportClientBackup()
	must(t, err)
	encoded, err := json.Marshal(latest)
	must(t, err)
	var imported RecoverySnapshot
	must(t, json.Unmarshal(encoded, &imported))
	rolledBackDB, err := db.Open(rollbackPath)
	must(t, err)
	t.Cleanup(func() { rolledBackDB.Close() })
	rolledBack := New(rolledBackDB)
	must(t, rolledBack.ImportClientBackup(imported))
	next, err := rolledBack.NextSave("same-workstation")
	must(t, err)
	if next.Client != latestOrder.Client || next.Save != 3 {
		t.Errorf("import reused a client operation identity: got %+v after %+v", next, latestOrder)
	}
	rows := []Basket{{Prefix: "A", BID: 1, WinningTicket: 43}}
	body, err := json.Marshal(rows)
	must(t, err)
	_, err = rolledBack.SaveQueued("POST", "/api/drawing", body, next, func(st *Store) error { return st.UpsertWinning(rows) })
	must(t, err)
	got, err := rolledBack.Basket("A", 1)
	must(t, err)
	if got.WinningTicket != 43 {
		t.Fatalf("correction after native import was not retained: %+v", got)
	}
}

func TestRestoreUnresolvedBackupRetainsConflictAfterRecovery(t *testing.T) {
	a, b := newTestStore(t), newTestStore(t)
	must(t, a.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}))
	must(t, b.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	as, err := a.ExportRecovery()
	must(t, err)
	bs, err := b.ExportRecovery()
	must(t, err)
	server, _ := recoveryStore(t)
	key, token := requestedKey(t, server)
	must(t, server.RecoverSnapshot(key, "a", token, as))
	must(t, server.RecoverSnapshot(key, "b", token, bs))
	unresolved, err := server.ExportRecovery()
	must(t, err)
	if server.CheckConflicts() == nil {
		t.Fatal("setup lacks conflict")
	}
	// Download and restore the unresolved native backup into this server.
	must(t, server.RestoreSnapshot(unresolved))
	if server.CheckConflicts() == nil {
		t.Fatal("restore immediately lost conflict")
	}
	restored, err := server.ExportRecovery()
	must(t, err)
	client := newTestStore(t)
	must(t, client.ImportClientBackup(restored))
	if client.CheckConflicts() == nil {
		t.Error("unresolved server backup silently chose a winner when restored into a new client")
	}
	replacement, _ := recoveryStore(t)
	k, tok := requestedKey(t, replacement)
	must(t, replacement.RecoverSnapshot(k, "backup", tok, restored))
	if replacement.CheckConflicts() == nil {
		got, err := replacement.Basket("A", 1)
		must(t, err)
		t.Fatalf("unresolved backup silently chose a winner after recovery: %+v", got)
	}
}
