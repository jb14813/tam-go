package store

import (
	"reflect"
	"testing"
)

func TestRecoveryContributionRestartEpochAndTarget(t *testing.T) {
	s := newOutboxStore(t)
	accepted := Ticket{Prefix: "A", TID: 1, FirstName: "Accepted"}
	must(t, s.UpsertTickets([]Ticket{accepted}))
	first, err := s.PrepareRecoveryContribution("event-one", "generation-one")
	must(t, err)
	if first.Token == "" || len(first.Data.Tickets) != 1 {
		t.Fatalf("missing durable upload: %+v", first)
	}
	// A newly constructed store has no in-memory request, but retains work.
	restarted := New(s.db)
	resumed, err := restarted.PrepareRecoveryContribution("event-one", "")
	must(t, err)
	if !reflect.DeepEqual(first, resumed) {
		t.Fatal("unfinished upload did not survive store restart")
	}
	other, err := restarted.PrepareRecoveryContribution("other-url-or-key", "")
	must(t, err)
	if other.Token != "" {
		t.Fatal("old contribution crossed remote target")
	}
	rejected := Ticket{Prefix: "A", TID: 1, FirstName: "Rejected"}
	id := queueRecoveryEdit(t, s, Order{"client", 1}, "/api/tickets", []Ticket{rejected}, func(st *Store) error { return st.UpsertTickets([]Ticket{rejected}) })
	must(t, s.FailOutbox(id, "refused after snapshot"))
	must(t, restarted.CompleteRecoveryContribution("event-one", first, nil))
	next, err := restarted.PrepareRecoveryContribution("event-one", "")
	must(t, err)
	if next.Token != first.Token || next.Epoch <= first.Epoch || len(next.Data.Tickets) != 1 || next.Data.Tickets[0] != accepted {
		t.Fatalf("old ACK erased later refusal or predecessor: %+v", next)
	}
	must(t, s.CompleteRecoveryContribution("event-one", next, nil))
	finished, err := New(s.db).PrepareRecoveryContribution("event-one", "")
	must(t, err)
	if finished.Token != "" {
		t.Fatal("completed upload repeated after restart")
	}
	must(t, s.RejectRecoveryContribution("event-one", next.Token))
	blocked, err := s.PrepareRecoveryContribution("event-one", next.Token)
	must(t, err)
	if blocked.Token != "" {
		t.Fatal("repeated 409 token was not suppressed")
	}
	fresh, err := s.PrepareRecoveryContribution("event-one", "generation-two")
	must(t, err)
	if fresh.Token != "generation-two" || fresh.Epoch <= next.Epoch {
		t.Fatal("new generation stayed suppressed")
	}
}

func TestRecoveryContributionTransactionsRetainRetryAndReceipt(t *testing.T) {
	s := newOutboxStore(t)
	row := Ticket{Prefix: "A", TID: 1, FirstName: "Accepted"}
	must(t, s.UpsertTickets([]Ticket{row}))
	sent, err := s.PrepareRecoveryContribution("event", "generation")
	must(t, err)
	before, err := s.ExportRecovery()
	must(t, err)
	server, _ := recoveryStore(t)
	_, _, err = server.InOrder("server", 1, "accepted", func(st *Store) error { return st.UpsertTickets([]Ticket{row}) })
	must(t, err)
	receipt, err := server.Receipt(Order{"server", 1})
	must(t, err)
	_, err = s.db.Exec(`CREATE TRIGGER fail_reoffer_state BEFORE UPDATE ON client_recovery_contribution BEGIN SELECT RAISE(ABORT,'disk failure'); END`)
	must(t, err)
	if err := s.CompleteRecoveryContribution("event", sent, &receipt); err == nil {
		t.Fatal("expected failed ACK transaction")
	}
	after, err := s.ExportRecovery()
	must(t, err)
	if !reflect.DeepEqual(before.Revisions, after.Revisions) {
		t.Fatal("receipt escaped failed state transaction")
	}
	still, err := New(s.db).PrepareRecoveryContribution("event", "")
	must(t, err)
	if still.Token == "" {
		t.Fatal("failed ACK erased retry")
	}
	id, err := s.EnqueueOutbox("POST", "/api/tickets", []byte(`[]`))
	must(t, err)
	if err := s.FailOutbox(id, "refused"); err == nil {
		t.Fatal("expected atomic failure transition")
	}
	if p, f := counts(t, s); p != 1 || f != 0 {
		t.Fatalf("failed bit write moved journal: %d %d", p, f)
	}
	_, err = s.db.Exec(`DROP TRIGGER fail_reoffer_state`)
	must(t, err)
	must(t, s.CompleteRecoveryContribution("event", sent, &receipt))
	must(t, s.FailOutbox(id, "refused"))
	still, err = New(s.db).PrepareRecoveryContribution("event", "")
	must(t, err)
	if still.Token == "" || still.Epoch <= sent.Epoch {
		t.Fatal("durable refusal did not request reoffer")
	}
}

func TestRecoveryRepeatedContributionAndStaleGeneration(t *testing.T) {
	s, path := recoveryStore(t)
	key, token := requestedKey(t, s)
	owner := newOutboxStore(t)
	must(t, owner.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "accepted"}}))
	snapshot, err := owner.ExportRecovery()
	must(t, err)
	must(t, s.RecoverSnapshot(key, "owner", token, snapshot))
	must(t, s.db.Close())
	s = reopenRecoveryStore(t, path)
	must(t, s.BeginRecovery())
	must(t, s.RecoverSnapshot(key, "owner", token, snapshot))
	got, err := s.ExportRecovery()
	must(t, err)
	if len(got.Tickets) != 1 || len(got.Conflicts) != 0 {
		t.Fatalf("duplicate recovery changed values: %+v", got)
	}
	// Simulate a new empty event in the same server data folder.
	_, err = s.db.Exec(`DELETE FROM tickets`)
	must(t, err)
	must(t, s.BeginRecovery())
	newToken, err := s.RecoveryToken(key, "owner")
	must(t, err)
	if newToken == "" || newToken == token {
		t.Fatal("new event did not rotate generation")
	}
	if err := s.RecoverSnapshot(key, "owner", token, snapshot); err != ErrRecoveryToken {
		t.Fatalf("stale generation accepted: %v", err)
	}
	rows, err := s.AllTickets()
	must(t, err)
	if len(rows) != 0 {
		t.Fatal("stale generation mutated empty event")
	}
	must(t, s.RecoverSnapshot(key, "owner", newToken, snapshot))
}

func TestRecoveryContributionOldTargetCannotClearNewTarget(t *testing.T) {
	s := newOutboxStore(t)
	old, err := s.PrepareRecoveryContribution("remote-A", "same-token")
	must(t, err)
	current, err := s.PrepareRecoveryContribution("remote-B", "same-token")
	must(t, err)
	must(t, s.CompleteRecoveryContribution("remote-A", old, nil))
	must(t, s.RejectRecoveryContribution("remote-A", old.Token))
	remaining, err := New(s.db).PrepareRecoveryContribution("remote-B", "")
	must(t, err)
	if remaining.Token != current.Token || remaining.Epoch != current.Epoch {
		t.Fatalf("old target acknowledgement cleared new target: current=%+v remaining=%+v", current, remaining)
	}
}
