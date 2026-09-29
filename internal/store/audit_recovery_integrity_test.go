package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// Both workstations' writes were accepted before server loss, and their
// outboxes are empty. The corrected records must not depend on boot order.
func TestAuditRecoveryKeepsAcknowledgedCorrection(t *testing.T) {
	for _, correctedFirst := range []bool{false, true} {
		name := "older-client-first"
		if correctedFirst {
			name = "corrected-client-first"
		}
		t.Run(name, func(t *testing.T) {
			older, corrected := newTestStore(t), newTestStore(t)
			oldBuyer := Ticket{Prefix: "A", TID: 42, FirstName: "Wrong buyer"}
			newBuyer := Ticket{Prefix: "A", TID: 42, FirstName: "Correct buyer"}
			oldDraw := Basket{Prefix: "A", BID: 1, WinningTicket: 41}
			newDraw := Basket{Prefix: "A", BID: 1, WinningTicket: 42}
			original, _ := recoveryStore(t)
			auditAccepted(t, original, older, Order{"old", 1}, []Ticket{oldBuyer}, []Basket{oldDraw}, true)
			auditAccepted(t, original, corrected, Order{"new", 1}, []Ticket{newBuyer}, []Basket{newDraw}, true)
			oldSnapshot, err := older.ExportRecovery()
			must(t, err)
			newSnapshot, err := corrected.ExportRecovery()
			must(t, err)
			replacement, _ := recoveryStore(t)
			key, token := requestedKey(t, replacement)
			first, second := oldSnapshot, newSnapshot
			if correctedFirst {
				first, second = second, first
			}
			must(t, replacement.RecoverSnapshot(key, "first", token, first))
			must(t, replacement.RecoverSnapshot(key, "second", token, second))
			for _, client := range []string{"first", "second"} {
				remaining, err := replacement.RecoveryToken(key, client)
				must(t, err)
				if remaining != "" {
					t.Fatalf("client %s was not acknowledged", client)
				}
			}
			buyer, err := replacement.Ticket("A", 42)
			must(t, err)
			draw, err := replacement.Basket("A", 1)
			must(t, err)
			if buyer.FirstName != newBuyer.FirstName || draw.WinningTicket != newDraw.WinningTicket {
				t.Fatalf("acknowledged correction lost: recovered buyer=%q winner=%d; want buyer=%q winner=%d", buyer.FirstName, draw.WinningTicket, newBuyer.FirstName, newDraw.WinningTicket)
			}
		})
	}
}

func TestAuditClonedActorCountersDoNotInventAncestry(t *testing.T) {
	first, second := newTestStore(t), newTestStore(t)
	for _, client := range []*Store{first, second} {
		must(t, client.WithLocalOperation(Order{"clone", 1}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}) }))
	}
	must(t, second.WithLocalOperation(Order{"clone", 2}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}) }))
	must(t, first.WithLocalOperation(Order{"clone", 2}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 2, WinningTicket: 10}}) }))
	must(t, first.WithLocalOperation(Order{"clone", 3}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 43}}) }))
	a, err := first.ExportRecovery()
	must(t, err)
	b, err := second.ExportRecovery()
	must(t, err)
	for _, reverse := range []bool{false, true} {
		server, _ := recoveryStore(t)
		key, token := requestedKey(t, server)
		left, right := a, b
		if reverse {
			left, right = right, left
		}
		must(t, server.RecoverSnapshot(key, "a", token, left))
		must(t, server.RecoverSnapshot(key, "b", token, right))
		if server.CheckConflicts() == nil {
			t.Fatal("cloned actor counter manufactured causal ordering")
		}
	}
}

func TestAuditResolvedOperationForkPropagatesAndSurvivesRecovery(t *testing.T) {
	for _, winner := range []int{41, 42} {
		t.Run(fmt.Sprint(winner), func(t *testing.T) {
			clients := []*Store{newTestStore(t), newTestStore(t)}
			snapshots := []RecoverySnapshot{}
			for i, client := range clients {
				n := 41 + i
				must(t, client.WithLocalOperation(Order{"fork", 1}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: n}}) }))
				snapshot, err := client.ExportRecovery()
				must(t, err)
				snapshots = append(snapshots, snapshot)
			}
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			for i, snapshot := range snapshots {
				must(t, server.RecoverSnapshot(key, fmt.Sprint(i), token, snapshot))
			}
			unresolved, err := server.ExportRecovery()
			must(t, err)
			blockedReceipt, err := server.MatchingReceipt(snapshots[winner-41])
			must(t, err)
			if len(blockedReceipt.Revisions) != 0 {
				t.Fatal("unresolved value was assigned an authoritative receipt")
			}
			cs, err := server.Conflicts()
			must(t, err)
			oldToken := ConflictToken(cs[0])
			must(t, server.ResolveConflictIfCurrent("drawing", "A", 1, valueHash([]byte(fmt.Sprint(winner))), oldToken))
			review, err := server.ReviewToken()
			must(t, err)
			if review == "" {
				t.Fatal("resolution did not advertise metadata refresh")
			}
			receipt, err := server.MatchingReceipt(snapshots[winner-41])
			must(t, err)
			must(t, clients[winner-41].ApplyReceipt(receipt))
			// A native backup carrying the old conflict cannot reopen the review.
			must(t, server.RecoverSnapshot(key, "late-native", token, unresolved))
			must(t, server.CheckConflicts())
			// Both historical variants are recognizable retries, without rewriting.
			for _, old := range []int{41, 42} {
				err = server.WithLocalOperation(Order{"fork", 1}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: old}}) })
				must(t, err)
			}
			current, err := server.DrawingLine("A", 1)
			must(t, err)
			if current.WinningTicket != winner {
				t.Fatal("reviewed operation retry changed winner")
			}
			resolvedCopy, err := clients[winner-41].ExportRecovery()
			must(t, err)
			must(t, clients[winner-41].ApplyReceipt(SaveReceipt{Conflicts: unresolved.Conflicts}))
			must(t, clients[winner-41].CheckConflicts())
			replacement, _ := recoveryStore(t)
			k, tok := requestedKey(t, replacement)
			must(t, replacement.RecoverSnapshot(k, "loser", tok, snapshots[42-winner]))
			must(t, replacement.RecoverSnapshot(k, "winner", tok, resolvedCopy))
			current, err = replacement.DrawingLine("A", 1)
			must(t, err)
			if current.WinningTicket != winner {
				t.Fatal("second server loss forgot resolution")
			}
			// The resolution must also clear a conflict formed before it arrives.
			fourth, _ := recoveryStore(t)
			k, tok = requestedKey(t, fourth)
			for i, snapshot := range snapshots {
				must(t, fourth.RecoverSnapshot(k, fmt.Sprint(i), tok, snapshot))
			}
			must(t, fourth.RecoverSnapshot(k, "resolved-owner", tok, resolvedCopy))
			current, err = fourth.DrawingLine("A", 1)
			must(t, err)
			if current.WinningTicket != winner {
				t.Fatal("late resolution could not retire recovered alternatives")
			}
			if winner == 41 {
				local := newTestStore(t)
				must(t, local.ImportClientBackup(unresolved))
				must(t, local.ApplyReceipt(receipt))
				must(t, local.CheckConflicts())
				late := newTestStore(t)
				must(t, late.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 99}}))
				lateSnapshot, err := late.ExportRecovery()
				must(t, err)
				lateCandidates, err := snapshotCandidates(lateSnapshot)
				must(t, err)
				must(t, local.ApplyReceipt(SaveReceipt{Conflicts: []RecordConflict{{Kind: "drawing", Prefix: "A", ID: 1, Candidates: lateCandidates}}}))
				must(t, local.ApplyReceipt(receipt))
				if local.CheckConflicts() == nil {
					t.Fatal("receipt erased genuinely new local conflict candidate")
				}
			}
		})
	}
}

func TestAuditConflictResolutionRejectsAChangedReview(t *testing.T) {
	server, _ := recoveryStore(t)
	key, token := requestedKey(t, server)
	for i, winner := range []int{1, 2} {
		must(t, server.Recover(key, fmt.Sprint(i), token, BackupFile{Baskets: []Basket{{Prefix: "A", BID: 1, WinningTicket: winner}}}))
	}
	cs, err := server.Conflicts()
	must(t, err)
	before := ConflictToken(cs[0])
	must(t, server.Recover(key, "late", token, BackupFile{Baskets: []Basket{{Prefix: "A", BID: 1, WinningTicket: 3}}}))
	if err := server.ResolveConflictIfCurrent("drawing", "A", 1, valueHash([]byte("1")), before); !errors.Is(err, ErrConflictChanged) {
		t.Fatalf("stale review accepted: %v", err)
	}
}

func TestAuditNativeConflictPayloadValidationIsAtomic(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		value      any
	}{
		{"different ticket identity", "ticket", Ticket{Prefix: "B", TID: 99}},
		{"negative winner", "drawing", -1},
		{"invalid metadata", "metadata", []string{"only one"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			must(t, err)
			candidate := RecordCandidate{Revision: RecordRevision{Kind: tc.kind, Prefix: "A", ID: 1, Hash: valueHash(raw)}, Value: raw}
			snapshot := RecoverySnapshot{BackupFile: BackupFile{Tickets: []Ticket{{Prefix: "A", TID: 1}}}, Conflicts: []RecordConflict{{Kind: tc.kind, Prefix: "A", ID: 1, Candidates: []RecordCandidate{candidate, candidate}}}}
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			if err = server.RecoverSnapshot(key, "bad", token, snapshot); err == nil {
				t.Fatal("invalid conflict candidate was accepted")
			}
			backup, err := server.Export()
			must(t, err)
			if len(backup.Tickets) != 0 {
				t.Fatal("invalid restore partially wrote event")
			}
			remaining, err := server.RecoveryToken(key, "bad")
			must(t, err)
			if remaining != token {
				t.Fatal("invalid snapshot was acknowledged")
			}
		})
	}
}

func TestAuditPrefixDeletionSurvivesReplacement(t *testing.T) {
	original, _ := recoveryStore(t)
	owner, deleter := newTestStore(t), newTestStore(t)
	write := func(s *Store) error { return s.UpsertPrefixes([]Prefix{{Prefix: "A", Color: "blue"}}) }
	must(t, owner.WithLocalOperation(Order{"owner", 1}, write))
	_, _, err := original.InOrder("owner", 1, "prefix", write)
	must(t, err)
	receipt, err := original.Receipt(Order{"owner", 1})
	must(t, err)
	must(t, owner.ApplyReceipt(receipt))
	remove := func(s *Store) error { _, err := s.DeletePrefix("A"); return err }
	must(t, deleter.WithLocalOperation(Order{"deleter", 1}, remove))
	_, _, err = original.InOrder("deleter", 1, "delete", remove)
	must(t, err)
	receipt, err = original.Receipt(Order{"deleter", 1})
	must(t, err)
	must(t, deleter.ApplyReceipt(receipt))
	a, err := owner.ExportRecovery()
	must(t, err)
	b, err := deleter.ExportRecovery()
	must(t, err)
	for _, reverse := range []bool{false, true} {
		server, _ := recoveryStore(t)
		key, token := requestedKey(t, server)
		left, right := a, b
		if reverse {
			left, right = right, left
		}
		must(t, server.RecoverSnapshot(key, "first", token, left))
		must(t, server.RecoverSnapshot(key, "second", token, right))
		prefixes, err := server.ListPrefixes()
		must(t, err)
		if len(prefixes) != 0 {
			t.Fatal("deleted prefix was revived")
		}
	}
}

func TestAuditLocalBackupRoundTripKeepsBasketOwnership(t *testing.T) {
	metadata, drawing := newTestStore(t), newTestStore(t)
	must(t, metadata.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Gift basket"}}))
	must(t, drawing.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	backup, err := metadata.ExportClientBackup()
	must(t, err)
	encoded, err := json.Marshal(backup)
	must(t, err)
	var decoded RecoverySnapshot
	must(t, json.Unmarshal(encoded, &decoded))
	restored := newTestStore(t)
	must(t, restored.ImportClientBackup(decoded))
	metadataSnapshot, err := restored.ExportRecovery()
	must(t, err)
	drawingSnapshot, err := drawing.ExportRecovery()
	must(t, err)
	replacement, _ := recoveryStore(t)
	key, token := requestedKey(t, replacement)
	must(t, replacement.RecoverSnapshot(key, "restored-metadata-desk", token, metadataSnapshot))
	must(t, replacement.RecoverSnapshot(key, "drawing-desk", token, drawingSnapshot))
	basket, err := replacement.Basket("A", 1)
	must(t, err)
	if basket.WinningTicket != 42 {
		t.Fatalf("backup round trip invented a drawing clear: components=%+v, recovered winner=%d; want 42", metadataSnapshot.BasketComponents, basket.WinningTicket)
	}
}

func TestAuditLostAckReplayDoesNotUndoOtherClientCorrection(t *testing.T) {
	older, corrected := newTestStore(t), newTestStore(t)
	oldDraw := Basket{Prefix: "A", BID: 1, WinningTicket: 41}
	newDraw := Basket{Prefix: "A", BID: 1, WinningTicket: 42}
	original, _ := recoveryStore(t)
	auditAccepted(t, original, older, Order{"old-desk", 1}, nil, []Basket{oldDraw}, false)
	auditAccepted(t, original, corrected, Order{"correcting-desk", 1}, nil, []Basket{newDraw}, true)
	oldSnapshot, err := older.ExportRecovery()
	must(t, err)
	newSnapshot, err := corrected.ExportRecovery()
	must(t, err)
	replacement, _ := recoveryStore(t)
	key, token := requestedKey(t, replacement)
	// Give the correction every advantage: its snapshot arrives first.
	must(t, replacement.RecoverSnapshot(key, "correcting-desk", token, newSnapshot))
	must(t, replacement.RecoverSnapshot(key, "old-desk", token, oldSnapshot))
	_, _, err = replacement.InOrder("old-desk", 1, "old-write", func(st *Store) error { return st.UpsertWinning([]Basket{oldDraw}) })
	must(t, err)
	basket, err := replacement.Basket("A", 1)
	must(t, err)
	if basket.WinningTicket != 42 {
		t.Fatalf("retry of already committed save undid acknowledged correction: winner=%d; want 42", basket.WinningTicket)
	}
}

func auditAccepted(t *testing.T, server, client *Store, order Order, tickets []Ticket, drawings []Basket, ack bool) {
	t.Helper()
	write := func(st *Store) error {
		if err := st.UpsertTickets(tickets); err != nil {
			return err
		}
		return st.UpsertWinning(drawings)
	}
	must(t, client.WithLocalOperation(order, write))
	_, _, err := server.InOrder(order.Client, order.Save, "write", write)
	must(t, err)
	if ack {
		receipt, err := server.Receipt(order)
		must(t, err)
		must(t, client.ApplyReceipt(receipt))
	}
}

func TestAuditIncomparableRecoveryBlocksUntilReviewed(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "modern", true: "legacy"}[legacy], func(t *testing.T) {
			left, right := newTestStore(t), newTestStore(t)
			must(t, left.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}))
			must(t, right.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
			a, err := left.ExportRecovery()
			must(t, err)
			b, err := right.ExportRecovery()
			must(t, err)
			if legacy {
				a.Revisions = nil
				b.Revisions = nil
			}
			server, path := recoveryStore(t)
			key, token := requestedKey(t, server)
			must(t, server.RecoverSnapshot(key, "a", token, a))
			must(t, server.RecoverSnapshot(key, "b", token, b))
			_, err = server.AllDrawing()
			var conflict *ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("ambiguous winner remained usable: %v", err)
			}
			if len(conflict.Conflicts) != 1 || len(conflict.Conflicts[0].Candidates) != 2 {
				t.Fatalf("conflict values lost: %+v", conflict)
			}
			server = reopenRecoveryStore(t, path)
			must(t, server.BeginRecovery())
			cs, err := server.Conflicts()
			must(t, err)
			chosen := valueHash([]byte("42"))
			must(t, server.ResolveConflictIfCurrent("drawing", "A", 1, chosen, ConflictToken(cs[0])))
			must(t, server.RecoverSnapshot(key, "late-old", token, a))
			lines, err := server.AllDrawing()
			must(t, err)
			if lines[0].WinningTicket != 42 {
				t.Fatalf("resolution lost: %+v", lines)
			}
		})
	}
}

func TestAuditReceiptSurvivesSecondServerLossAndOldAck(t *testing.T) {
	old, newer := newTestStore(t), newTestStore(t)
	server, _ := recoveryStore(t)
	auditAccepted(t, server, old, Order{"a", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 1}}, true)
	auditAccepted(t, server, newer, Order{"b", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 2}}, true)
	a, err := old.ExportRecovery()
	must(t, err)
	b, err := newer.ExportRecovery()
	must(t, err)
	second, _ := recoveryStore(t)
	key, token := requestedKey(t, second)
	must(t, second.RecoverSnapshot(key, "b", token, b))
	auditAccepted(t, second, newer, Order{"b", 2}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 3}}, true)
	b, err = newer.ExportRecovery()
	must(t, err)
	third, _ := recoveryStore(t)
	key, token = requestedKey(t, third)
	must(t, third.RecoverSnapshot(key, "a", token, a))
	must(t, third.RecoverSnapshot(key, "b", token, b))
	drawing, err := third.DrawingLine("A", 1)
	must(t, err)
	if drawing.WinningTicket != 3 {
		t.Fatalf("second replacement reset ordering: %+v", drawing)
	}
	// Receiving an earlier matching-value receipt must not remove a newer dot.
	prior, err := newer.ExportRecovery()
	must(t, err)
	must(t, newer.WithLocalOperation(Order{"b", 3}, func(s *Store) error { return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 3}}) }))
	must(t, newer.ApplyReceipt(SaveReceipt{Revisions: prior.Revisions}))
	final, err := newer.ExportRecovery()
	must(t, err)
	if final.Revisions[0].Vector["client:b"] != 3 {
		t.Fatalf("late receipt rolled local operation back: %+v", final.Revisions)
	}
}

func TestAuditFreshWriteDoesNotHideUnknownRecoveredCorrection(t *testing.T) {
	old, newer := newTestStore(t), newTestStore(t)
	original, _ := recoveryStore(t)
	auditAccepted(t, original, old, Order{"a", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 1}}, true)
	auditAccepted(t, original, newer, Order{"b", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 2}}, true)
	a, err := old.ExportRecovery()
	must(t, err)
	b, err := newer.ExportRecovery()
	must(t, err)
	server, _ := recoveryStore(t)
	key, token := requestedKey(t, server)
	must(t, server.RecoverSnapshot(key, "a", token, a))
	must(t, server.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 3}}))
	must(t, server.RecoverSnapshot(key, "b", token, b))
	backup, err := server.Export()
	must(t, err)
	current := backup.Baskets[0]
	if current.WinningTicket != 3 {
		t.Fatal("late snapshot overwrote live write")
	}
	_, err = server.AllDrawing()
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("unknown correction was silently discarded: %v", err)
	}
}
