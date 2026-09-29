package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func queueRecoveryEdit(t *testing.T, s *Store, order Order, path string, value any, write func(*Store) error) int64 {
	t.Helper()
	body, err := json.Marshal(value)
	must(t, err)
	id, err := s.SaveQueued("POST", path, body, order, write)
	must(t, err)
	return id
}

func TestSyncRecoveryOmitsCurrentQueuedCorrection(t *testing.T) {
	client := newOutboxStore(t)
	server, _ := recoveryStore(t)
	key, token := requestedKey(t, server)
	old := Ticket{Prefix: "A", TID: 640, FirstName: "Original"}
	confirmed := Ticket{Prefix: "A", TID: 640, FirstName: "Other desk"}
	pending := Ticket{Prefix: "A", TID: 640, FirstName: "Pending correction"}
	auditAccepted(t, server, client, Order{"client", 1}, []Ticket{old}, nil, true)
	auditAccepted(t, server, newTestStore(t), Order{"other", 1}, []Ticket{confirmed}, nil, true)
	order := Order{"client", 2}
	id := queueRecoveryEdit(t, client, order, "/api/tickets", []Ticket{pending}, func(s *Store) error { return s.UpsertTickets([]Ticket{pending}) })
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 0 || len(snapshot.Revisions) != 0 {
		t.Fatalf("queued correction escaped as historical data: %+v", snapshot)
	}
	complete, err := client.ExportRecovery()
	must(t, err)
	if len(complete.Tickets) != 1 || complete.Tickets[0] != pending || len(complete.Revisions) != 1 {
		t.Fatalf("native backup lost pending data: %+v", complete)
	}
	must(t, server.RecoverSnapshot(key, "late", token, snapshot))
	must(t, server.CheckConflicts())
	_, _, err = server.InOrder(order.Client, order.Save, "pending", func(s *Store) error { return s.UpsertTickets([]Ticket{pending}) })
	must(t, err)
	receipt, err := server.Receipt(order)
	must(t, err)
	must(t, client.AcknowledgeOutbox(id, &receipt))
	got, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(got.Tickets) != 1 || got.Tickets[0] != pending {
		t.Fatalf("accepted correction unavailable for recovery: %+v", got)
	}
}

func TestSyncRecoveryFiltersComponentsAndKeepsNativeBackup(t *testing.T) {
	s := newOutboxStore(t)
	confirmed := []Basket{{Prefix: "A", BID: 1, Description: "Confirmed metadata", Donors: "Donor"}, {Prefix: "A", BID: 2, Description: "Before", WinningTicket: 42}, {Prefix: "A", BID: 3, Description: "Same winner", WinningTicket: 42}}
	must(t, s.WithLocalOperation(Order{"client", 1}, func(v *Store) error { return v.UpsertBaskets(confirmed) }))
	queueRecoveryEdit(t, s, Order{"client", 2}, "/api/drawing", []Basket{{Prefix: "A", BID: 1, WinningTicket: 80}}, func(v *Store) error { return v.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 80}}) })
	metadata := []Basket{{Prefix: "A", BID: 2, Description: "Pending metadata", WinningTicket: 99}, {Prefix: "A", BID: 3, Description: "Pending too", WinningTicket: 42}, {Prefix: "A", BID: 4, Description: "New pending", WinningTicket: 80}}
	queueRecoveryEdit(t, s, Order{"client", 3}, "/api/baskets", metadata, func(v *Store) error { return v.UpsertBaskets(metadata) })
	queuedPrefix := []Prefix{{Prefix: " E ", Color: "white", Weight: -1}}
	queueRecoveryEdit(t, s, Order{"client", 4}, "/api/backuprestore", BackupFile{Prefixes: queuedPrefix, Tickets: []Ticket{}, Baskets: []Basket{}}, func(v *Store) error { return v.UpsertPrefixes(queuedPrefix) })
	must(t, s.UpsertPrefixes([]Prefix{{Prefix: "A/B", Color: "white", Weight: 1}, {Prefix: "C", Color: "red", Weight: 2}}))
	_, err := s.SaveQueued("DELETE", "/api/prefixes?p=A%2FB", nil, Order{"client", 5}, func(v *Store) error { _, e := v.DeletePrefix("A/B"); return e })
	must(t, err)
	before, err := s.ExportRecovery()
	must(t, err)
	snapshot, err := s.ExportRecoveryForSync()
	must(t, err)
	must(t, ValidateRecoverySnapshot(&snapshot))
	want := []Basket{{Prefix: "A", BID: 1, Description: "Confirmed metadata", Donors: "Donor"}, {Prefix: "A", BID: 2, WinningTicket: 42}, {Prefix: "A", BID: 3, WinningTicket: 42}}
	if !reflect.DeepEqual(snapshot.Baskets, want) || len(snapshot.Prefixes) != 1 || snapshot.Prefixes[0].Prefix != "C" || len(snapshot.DeletedPrefixes) != 0 {
		t.Fatalf("incorrect sync components: %+v", snapshot)
	}
	for _, r := range snapshot.Revisions {
		if r.Kind == "drawing" && r.ID == 1 || r.Kind == "metadata" && (r.ID == 2 || r.ID == 3) || r.ID == 4 || r.Prefix == " E " || r.Prefix == "A/B" {
			t.Fatalf("queued revision remains: %+v", r)
		}
	}
	after, err := s.ExportRecovery()
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("sync export changed the complete native backup")
	}
}

func TestSyncRecoveryFailedEditsDoNotHideNewerValues(t *testing.T) {
	for _, sameValue := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrected-value", true: "same-value-later-save"}[sameValue], func(t *testing.T) {
			s := newOutboxStore(t)
			failed := Ticket{Prefix: "A", TID: 1, FirstName: "Rejected"}
			id := queueRecoveryEdit(t, s, Order{"client", 1}, "/api/tickets", []Ticket{failed}, func(v *Store) error { return v.UpsertTickets([]Ticket{failed}) })
			must(t, s.FailOutbox(id, "refused"))
			snapshot, err := s.ExportRecoveryForSync()
			must(t, err)
			if len(snapshot.Tickets) != 0 {
				t.Fatal("failed value entered automatic recovery")
			}
			accepted := failed
			if !sameValue {
				accepted.FirstName = "Accepted correction"
			}
			server, _ := recoveryStore(t)
			auditAccepted(t, server, s, Order{"client", 2}, []Ticket{accepted}, nil, true)
			snapshot, err = s.ExportRecoveryForSync()
			must(t, err)
			if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != accepted {
				t.Fatalf("old failure hid accepted value: %+v", snapshot)
			}
		})
	}
}

func TestSyncRecoveryUnappliedRejectionKeepsConfirmedPredecessor(t *testing.T) {
	s := newOutboxStore(t)
	confirmed := Ticket{Prefix: "A", TID: 1, FirstName: "Confirmed"}
	must(t, s.WithLocalOperation(Order{"client", 1}, func(v *Store) error { return v.UpsertTickets([]Ticket{confirmed}) }))
	body, err := json.Marshal([]Ticket{confirmed})
	must(t, err)
	id, err := s.SaveIntent("POST", "/api/tickets", body, Order{"client", 2})
	must(t, err)
	must(t, s.FailOutbox(id, "rejected before local apply"))
	snapshot, err := s.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != confirmed {
		t.Fatalf("unapplied intent hid confirmed predecessor: %+v", snapshot)
	}
}

func TestSyncRecoveryFailedEditDoesNotHideLaterStandaloneSave(t *testing.T) {
	s := newOutboxStore(t)
	ticket := Ticket{Prefix: "A", TID: 1, FirstName: "Retained"}
	id := queueRecoveryEdit(t, s, Order{"client", 1}, "/api/tickets", []Ticket{ticket}, func(v *Store) error { return v.UpsertTickets([]Ticket{ticket}) })
	must(t, s.FailOutbox(id, "refused"))
	must(t, s.UpsertTickets([]Ticket{ticket}))
	snapshot, err := s.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 1 {
		t.Fatal("old failed request hid later standalone save with another actor")
	}
}

func TestSyncRecoveryFailedEditDoesNotHideRestoredIndependentCopy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "numbered", true: "legacy"}[legacy], func(t *testing.T) {
			s := newOutboxStore(t)
			ticket := Ticket{Prefix: "A", TID: 1, FirstName: "Same value"}
			order := Order{"client", 1}
			if legacy {
				order = Order{}
			}
			id := queueRecoveryEdit(t, s, order, "/api/tickets", []Ticket{ticket}, func(v *Store) error { return v.UpsertTickets([]Ticket{ticket}) })
			must(t, s.FailOutbox(id, "refused"))
			independent := newTestStore(t)
			must(t, independent.UpsertTickets([]Ticket{ticket}))
			restored, err := independent.ExportRecovery()
			must(t, err)
			must(t, s.ImportClientBackup(restored))
			snapshot, err := s.ExportRecoveryForSync()
			must(t, err)
			if len(snapshot.Tickets) != 1 {
				t.Fatal("old rejection hid independent restored copy")
			}
		})
	}
}

func TestSyncRecoveryRenumberKeepsPendingInsertWinnerAndAncestry(t *testing.T) {
	client := newOutboxStore(t)
	basket := Basket{Prefix: "A", BID: 1, Description: "Pending", WinningTicket: 41}
	old, next := Order{"client", 1}, Order{"client", 3}
	id := queueRecoveryEdit(t, client, old, "/api/baskets", []Basket{basket}, func(s *Store) error { return s.UpsertBaskets([]Basket{basket}) })
	must(t, client.RenumberOutbox(id, next))
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Baskets) != 0 || len(snapshot.Revisions) != 0 {
		t.Fatal("renumbered pending insert winner escaped into recovery")
	}
	server, _ := recoveryStore(t)
	_, _, err = server.InOrder(next.Client, next.Save, "renumbered", func(s *Store) error { return s.UpsertBaskets([]Basket{basket}) })
	must(t, err)
	receipt, err := server.Receipt(next)
	must(t, err)
	must(t, client.AcknowledgeOutbox(id, &receipt))
	second := newTestStore(t)
	auditAccepted(t, server, second, Order{"other", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}, true)
	a, err := client.ExportRecoveryForSync()
	must(t, err)
	b, err := second.ExportRecovery()
	must(t, err)
	for _, reverse := range []bool{false, true} {
		replacement, _ := recoveryStore(t)
		key, token := requestedKey(t, replacement)
		left, right := a, b
		if reverse {
			left, right = right, left
		}
		must(t, replacement.RecoverSnapshot(key, "first", token, left))
		must(t, replacement.RecoverSnapshot(key, "second", token, right))
		current, err := replacement.Basket("A", 1)
		must(t, err)
		if current.WinningTicket != 42 {
			t.Fatalf("renumbered history lost later correction: %+v", current)
		}
	}
}

func TestClientBackupRestoresExactComponentProvenance(t *testing.T) {
	target, source := newOutboxStore(t), newTestStore(t)
	must(t, target.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Keep metadata", Donors: "Keep donor"}}))
	must(t, target.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	before, err := target.ExportRecovery()
	must(t, err)
	must(t, source.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	file, err := source.ExportRecovery()
	must(t, err)
	must(t, target.ImportClientBackup(file))
	after, err := target.ExportRecovery()
	must(t, err)
	if after.Baskets[0].Description != "Keep metadata" || after.Baskets[0].Donors != "Keep donor" {
		t.Fatal("drawing import changed unimported metadata")
	}
	var beforeMetadata, afterMetadata, afterDrawing RecordRevision
	for _, r := range before.Revisions {
		if r.Kind == "metadata" {
			beforeMetadata = r
		}
	}
	for _, r := range after.Revisions {
		if r.Kind == "metadata" {
			afterMetadata = r
		}
		if r.Kind == "drawing" {
			afterDrawing = r
		}
	}
	if !reflect.DeepEqual(beforeMetadata, afterMetadata) {
		t.Fatal("drawing import changed unimported metadata ancestry")
	}
	if !reflect.DeepEqual(afterDrawing, file.Revisions[0]) {
		t.Fatalf("restored drawing inherited unrelated local ancestry: %+v", afterDrawing)
	}
	legacy := RecoverySnapshot{BackupFile: NewBackupFile()}
	legacy.Baskets = []Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}
	must(t, target.ImportClientBackup(legacy))
	after, err = target.ExportRecovery()
	must(t, err)
	if len(after.Revisions) != 0 {
		t.Fatal("legacy file inherited modern local provenance")
	}
}

func TestClientBackupRestoresNativeConflictCandidatesExactly(t *testing.T) {
	server, _ := recoveryStore(t)
	key, token := requestedKey(t, server)
	for i, winner := range []int{41, 42} {
		file := RecoverySnapshot{BackupFile: NewBackupFile()}
		file.Baskets = []Basket{{Prefix: "A", BID: 1, WinningTicket: winner}}
		file.BasketComponents = []BasketComponents{{Prefix: "A", BID: 1, Drawing: true}}
		must(t, server.RecoverSnapshot(key, string(rune('a'+i)), token, file))
	}
	file, err := server.ExportRecovery()
	must(t, err)
	target := newOutboxStore(t)
	must(t, target.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 99}}))
	must(t, target.ImportClientBackup(file))
	got, err := target.ExportRecovery()
	must(t, err)
	gotJSON, err := json.Marshal(got.Conflicts)
	must(t, err)
	fileJSON, err := json.Marshal(file.Conflicts)
	must(t, err)
	if string(gotJSON) != string(fileJSON) {
		t.Fatalf("restored conflict differs from file: got %+v want %+v", got.Conflicts, file.Conflicts)
	}
	if target.CheckConflicts() == nil {
		t.Fatal("native unresolved conflict import became readable")
	}
}
