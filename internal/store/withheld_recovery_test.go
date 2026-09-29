package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDiscardKeepsLastAcceptedRecoveryValue(t *testing.T) {
	client := newOutboxStore(t)
	server, _ := recoveryStore(t)
	accepted := Ticket{Prefix: "A", TID: 1, FirstName: "Accepted"}
	rejected := Ticket{Prefix: "A", TID: 1, FirstName: "Rejected"}
	auditAccepted(t, server, client, Order{"client", 1}, []Ticket{accepted}, nil, true)
	id := queueRecoveryEdit(t, client, Order{"client", 2}, "/api/tickets", []Ticket{rejected}, func(s *Store) error { return s.UpsertTickets([]Ticket{rejected}) })
	must(t, client.FailOutbox(id, "refused"))
	_, err := client.DiscardFailed()
	must(t, err)
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != accepted {
		t.Fatalf("discard should recover the accepted predecessor, got %+v", snapshot.Tickets)
	}
	local, err := client.Ticket("A", 1)
	must(t, err)
	if local == nil || *local != rejected {
		t.Fatalf("discard destroyed locally entered work: %+v", local)
	}
}

func TestWithheldAcknowledgementAdvancesPredecessorUnderNewerPendingSave(t *testing.T) {
	client := newOutboxStore(t)
	server, _ := recoveryStore(t)
	first := Ticket{Prefix: "A", TID: 1, FirstName: "First"}
	accepted := Ticket{Prefix: "A", TID: 1, FirstName: "Accepted & <林>"}
	rejected := Ticket{Prefix: "A", TID: 1, FirstName: "Rejected"}
	auditAccepted(t, server, client, Order{"client", 1}, []Ticket{first}, nil, true)
	ids := []int64{}
	for i, row := range []Ticket{accepted, rejected} {
		row := row
		ids = append(ids, queueRecoveryEdit(t, client, Order{"client", int64(i + 2)}, "/api/tickets", []Ticket{row}, func(s *Store) error { return s.UpsertTickets([]Ticket{row}) }))
	}
	_, _, err := server.InOrder("client", 2, "accepted", func(s *Store) error { return s.UpsertTickets([]Ticket{accepted}) })
	must(t, err)
	receipt, err := server.Receipt(Order{"client", 2})
	must(t, err)
	must(t, client.AcknowledgeOutbox(ids[0], &receipt))
	must(t, client.FailOutbox(ids[1], "refused"))
	_, err = client.DiscardFailed()
	must(t, err)
	backup, err := client.ExportClientBackup()
	must(t, err)
	if len(backup.WithheldRecords) != 1 || backup.Tickets[0] != rejected || backup.WithheldRecords[0].Prior == nil {
		t.Fatalf("backup lost withheld data or accepted predecessor: %+v", backup)
	}
	raw, err := json.MarshalIndent(backup, "", "  ")
	must(t, err)
	var restoredFile RecoverySnapshot
	must(t, json.Unmarshal(raw, &restoredFile))
	restored := newOutboxStore(t)
	must(t, restored.ImportClientBackup(restoredFile))
	for _, owner := range []*Store{client, restored} {
		snapshot, err := owner.ExportRecoveryForSync()
		must(t, err)
		if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != accepted || len(snapshot.WithheldRecords) != 0 {
			t.Fatalf("did not recover latest accepted payload: %+v", snapshot)
		}
		replacement, _ := recoveryStore(t)
		key, token := requestedKey(t, replacement)
		must(t, replacement.RecoverSnapshot(key, "owner", token, snapshot))
		// The server also honors a native file supplied directly as recovery.
		must(t, replacement.RecoverSnapshot(key, "file", token, restoredFile))
		got, err := replacement.Ticket("A", 1)
		must(t, err)
		if got == nil || *got != accepted {
			t.Fatalf("replacement promoted withheld value: %+v", got)
		}
	}
	// A deliberate operator restore chooses the file's entered value now.
	must(t, server.RestoreSnapshot(restoredFile))
	got, err := server.Ticket("A", 1)
	must(t, err)
	if got == nil || *got != rejected {
		t.Fatalf("operator restore failed to select entered value: %+v", got)
	}
	// A new intentional save with the same payload supersedes the old refusal.
	must(t, restored.UpsertTickets([]Ticket{rejected}))
	snapshot, err := restored.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != rejected {
		t.Fatal("old holdback hid a later intentional same-value save")
	}
}

func TestWithheldRecoveryKeepsComponentsAndPrefixDeletionHistory(t *testing.T) {
	client := newOutboxStore(t)
	must(t, client.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Accepted metadata", Donors: "Donor"}}))
	must(t, client.UpsertWinning([]Basket{{Prefix: "A", BID: 2, WinningTicket: 42}}))
	must(t, client.UpsertPrefixes([]Prefix{{Prefix: "A", Color: "blue"}}))
	_, err := client.DeletePrefix("B")
	must(t, err)
	before, err := client.ExportRecoveryForSync()
	must(t, err)
	metadata := []Basket{{Prefix: "A", BID: 1, Description: "Refused metadata"}}
	drawing := []Basket{{Prefix: "A", BID: 2, WinningTicket: 0}}
	prefix := []Prefix{{Prefix: "B", Color: "red"}}
	ids := []int64{
		queueRecoveryEdit(t, client, Order{"client", 1}, "/api/baskets", metadata, func(s *Store) error { return s.UpsertBaskets(metadata) }),
		queueRecoveryEdit(t, client, Order{"client", 2}, "/api/drawing", drawing, func(s *Store) error { return s.UpsertWinning(drawing) }),
		queueRecoveryEdit(t, client, Order{"client", 3}, "/api/prefixes", prefix, func(s *Store) error { return s.UpsertPrefixes(prefix) }),
	}
	id, err := client.SaveQueued("DELETE", "/api/prefixes?p=A", nil, Order{"client", 4}, func(s *Store) error { _, err := s.DeletePrefix("A"); return err })
	must(t, err)
	ids = append(ids, id)
	for _, id := range ids {
		must(t, client.FailOutbox(id, "refused"))
	}
	_, err = client.DiscardFailed()
	must(t, err)
	file, err := client.ExportClientBackup()
	must(t, err)
	restored := newOutboxStore(t)
	must(t, restored.ImportClientBackup(file))
	after, err := restored.ExportRecoveryForSync()
	must(t, err)
	must(t, ValidateRecoverySnapshot(&after))
	if !reflect.DeepEqual(before.BackupFile, after.BackupFile) || !reflect.DeepEqual(before.BasketComponents, after.BasketComponents) || !reflect.DeepEqual(before.DeletedPrefixes, after.DeletedPrefixes) {
		t.Fatalf("discard or backup restore lost accepted components: before=%+v after=%+v", before, after)
	}
}

func TestWithheldJournalAndAcknowledgementAreAtomic(t *testing.T) {
	client := newOutboxStore(t)
	server, _ := recoveryStore(t)
	first := Ticket{Prefix: "A", TID: 1, FirstName: "First"}
	next := Ticket{Prefix: "A", TID: 1, FirstName: "Accepted next"}
	last := Ticket{Prefix: "A", TID: 1, FirstName: "Last pending"}
	auditAccepted(t, server, client, Order{"client", 1}, []Ticket{first}, nil, true)
	_, err := client.db.Exec(`CREATE TRIGGER fail_holdback BEFORE INSERT ON recovery_holdbacks BEGIN SELECT RAISE(ABORT,'disk failure'); END`)
	must(t, err)
	body, err := json.Marshal([]Ticket{next})
	must(t, err)
	if _, err := client.SaveQueued("POST", "/api/tickets", body, Order{"client", 2}, func(s *Store) error { return s.UpsertTickets([]Ticket{next}) }); err == nil {
		t.Fatal("holdback write failure did not abort save")
	}
	got, err := client.Ticket("A", 1)
	must(t, err)
	if got == nil || *got != first {
		t.Fatalf("failed journal lost accepted row: %+v", got)
	}
	_, err = client.db.Exec(`DROP TRIGGER fail_holdback`)
	must(t, err)
	id := queueRecoveryEdit(t, client, Order{"client", 2}, "/api/tickets", []Ticket{next}, func(s *Store) error { return s.UpsertTickets([]Ticket{next}) })
	queueRecoveryEdit(t, client, Order{"client", 3}, "/api/tickets", []Ticket{last}, func(s *Store) error { return s.UpsertTickets([]Ticket{last}) })
	_, _, err = server.InOrder("client", 2, "next", func(s *Store) error { return s.UpsertTickets([]Ticket{next}) })
	must(t, err)
	receipt, err := server.Receipt(Order{"client", 2})
	must(t, err)
	_, err = client.db.Exec(`CREATE TRIGGER fail_holdback BEFORE UPDATE ON recovery_holdbacks BEGIN SELECT RAISE(ABORT,'disk failure'); END`)
	must(t, err)
	if err := client.AcknowledgeOutbox(id, &receipt); err == nil {
		t.Fatal("predecessor failure discarded its acknowledgement")
	}
	if pending, _, err := client.OutboxCounts(); err != nil || pending != 2 {
		t.Fatalf("failed acknowledgement lost journal: %d %v", pending, err)
	}
	_, err = client.db.Exec(`DROP TRIGGER fail_holdback`)
	must(t, err)
	must(t, client.AcknowledgeOutbox(id, &receipt))
	snapshot, err := client.ExportClientBackup()
	must(t, err)
	if len(snapshot.WithheldRecords) != 1 || snapshot.WithheldRecords[0].Prior == nil {
		t.Fatal("retry did not retain accepted predecessor")
	}
	var accepted Ticket
	must(t, json.Unmarshal(snapshot.WithheldRecords[0].Prior.Value, &accepted))
	if accepted != next {
		t.Fatalf("retry retained wrong predecessor: %+v", accepted)
	}
}
