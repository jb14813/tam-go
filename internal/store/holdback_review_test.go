package store

import "testing"

func TestIndependentLegacyBasketAckCannotInventWinner(t *testing.T) {
	client := newOutboxStore(t)
	must(t, client.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Accepted", WinningTicket: 7}}))
	metadata := []Basket{{Prefix: "A", BID: 1, Description: "New metadata", WinningTicket: 99}}
	metadataID := queueRecoveryEdit(t, client, Order{"client", 1}, "/api/baskets", metadata, func(s *Store) error { return s.UpsertBaskets(metadata) })
	drawing := []Basket{{Prefix: "A", BID: 1, WinningTicket: 22}}
	drawingID := queueRecoveryEdit(t, client, Order{"client", 2}, "/api/drawing", drawing, func(s *Store) error { return s.UpsertWinning(drawing) })
	must(t, client.AcknowledgeOutbox(metadataID, nil))
	must(t, client.FailOutbox(drawingID, "refused"))
	_, err := client.DiscardFailed()
	must(t, err)
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Baskets) != 1 || snapshot.Baskets[0].WinningTicket != 7 {
		t.Fatalf("ignored metadata winner became recovery value: %+v", snapshot.Baskets)
	}
}

func TestIndependentPreHoldbackJournalDiscard(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "discard", true: "retry-refuse-discard"}[retry], func(t *testing.T) {
			client := newOutboxStore(t)
			ticket := Ticket{Prefix: "A", TID: 1, FirstName: "Unaccepted legacy copy"}
			id := queueRecoveryEdit(t, client, Order{"client", 1}, "/api/tickets", []Ticket{ticket}, func(s *Store) error { return s.UpsertTickets([]Ticket{ticket}) })
			_, err := client.db.Exec(`DELETE FROM recovery_holdbacks`)
			must(t, err)
			must(t, client.FailOutbox(id, "refused"))
			before, err := client.ExportRecoveryForSync()
			must(t, err)
			if len(before.Tickets) != 0 {
				t.Fatal("legacy precondition unexpectedly recovers")
			}
			if retry {
				_, err = client.RetryFailed("host")
				must(t, err)
				_, err = client.FailAllOutbox("refused again")
				must(t, err)
			}
			_, err = client.DiscardFailed()
			must(t, err)
			after, err := client.ExportRecoveryForSync()
			must(t, err)
			if len(after.Tickets) != 0 {
				t.Fatalf("discard lets an unaccepted legacy head recover: %+v", after.Tickets)
			}
		})
	}
}

func TestIndependentSameValueOlderAckKeepsAcceptedPrior(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil-receipt", true: "modern-receipt"}[receipt], func(t *testing.T) {
			client := newOutboxStore(t)
			server, _ := recoveryStore(t)
			first := Ticket{Prefix: "A", TID: 1, FirstName: "First"}
			accepted := Ticket{Prefix: "A", TID: 1, FirstName: "Repeated"}
			auditAccepted(t, server, client, Order{"client", 1}, []Ticket{first}, nil, true)
			ids := []int64{}
			for _, number := range []int64{2, 3} {
				ids = append(ids, queueRecoveryEdit(t, client, Order{"client", number}, "/api/tickets", []Ticket{accepted}, func(s *Store) error { return s.UpsertTickets([]Ticket{accepted}) }))
			}
			var ack *SaveReceipt
			if receipt {
				_, _, err := server.InOrder("client", 2, "accepted", func(s *Store) error { return s.UpsertTickets([]Ticket{accepted}) })
				must(t, err)
				r, err := server.Receipt(Order{"client", 2})
				must(t, err)
				ack = &r
			}
			must(t, client.AcknowledgeOutbox(ids[0], ack))
			must(t, client.FailOutbox(ids[1], "refused repeated value"))
			_, err := client.DiscardFailed()
			must(t, err)
			snapshot, err := client.ExportRecoveryForSync()
			must(t, err)
			if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != accepted {
				t.Fatalf("same-value later refusal hid earlier accepted value: %+v", snapshot)
			}
		})
	}
}

func TestIndependentRenumberedAckUpdatesNewerPendingPrior(t *testing.T) {
	client := newOutboxStore(t)
	server, _ := recoveryStore(t)
	first := Ticket{Prefix: "A", TID: 1, FirstName: "First"}
	accepted := Ticket{Prefix: "A", TID: 1, FirstName: "Renumbered accepted"}
	pending := Ticket{Prefix: "A", TID: 1, FirstName: "Later local refused"}
	auditAccepted(t, server, client, Order{"client", 1}, []Ticket{first}, nil, true)
	id := queueRecoveryEdit(t, client, Order{"client", 2}, "/api/tickets", []Ticket{accepted}, func(s *Store) error { return s.UpsertTickets([]Ticket{accepted}) })
	later := queueRecoveryEdit(t, client, Order{"client", 3}, "/api/tickets", []Ticket{pending}, func(s *Store) error { return s.UpsertTickets([]Ticket{pending}) })
	must(t, client.RenumberOutbox(id, Order{"client", 4}))
	_, _, err := server.InOrder("client", 4, "renumbered", func(s *Store) error { return s.UpsertTickets([]Ticket{accepted}) })
	must(t, err)
	receipt, err := server.Receipt(Order{"client", 4})
	must(t, err)
	must(t, client.AcknowledgeOutbox(id, &receipt))
	must(t, client.FailOutbox(later, "older than renumbered accepted save"))
	_, err = client.DiscardFailed()
	must(t, err)
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != accepted {
		t.Fatalf("renumbered ack lost accepted baseline: %+v", snapshot)
	}
}

func TestIndependentLegacyRestoreSupersedesSameValueRefusal(t *testing.T) {
	client := newOutboxStore(t)
	row := Ticket{Prefix: "A", TID: 1, FirstName: "Deliberately restored"}
	id := queueRecoveryEdit(t, client, Order{"client", 1}, "/api/tickets", []Ticket{row}, func(s *Store) error { return s.UpsertTickets([]Ticket{row}) })
	must(t, client.FailOutbox(id, "refused"))
	legacy := RecoverySnapshot{BackupFile: NewBackupFile()}
	legacy.Tickets = []Ticket{row}
	must(t, client.ImportClientBackup(legacy))
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Tickets) != 1 || snapshot.Tickets[0] != row {
		t.Fatalf("explicit legacy restore still withheld by old refusal: %+v", snapshot)
	}
}

func TestIndependentUnappliedRenumberLeavesAcceptedHead(t *testing.T) {
	client := newOutboxStore(t)
	server, _ := recoveryStore(t)
	row := Ticket{Prefix: "A", TID: 1, FirstName: "Accepted original"}
	auditAccepted(t, server, client, Order{"client", 1}, []Ticket{row}, nil, true)
	body := []byte(`[{"prefix":"A","t_id":1,"first_name":"Accepted original","last_name":"","phone_number":"","pref":""}]`)
	id, err := client.SaveIntent("POST", "/api/tickets", body, Order{"client", 1})
	must(t, err)
	must(t, client.RenumberOutbox(id, Order{"client", 3}))
	must(t, client.RejectIntent(id, "definitively refused"))
	snapshot, err := client.ExportRecoveryForSync()
	must(t, err)
	if len(snapshot.Revisions) != 1 || len(snapshot.Revisions[0].Heads) != 1 || snapshot.Revisions[0].Heads[0] != "client:client/1" {
		t.Fatalf("unapplied refused intent rewrote accepted history: %+v", snapshot.Revisions)
	}
}
