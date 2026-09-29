package store

import (
	"reflect"
	"testing"
)

func frontierReviewDrawing(t *testing.T, snapshot RecoverySnapshot) RecordRevision {
	t.Helper()
	for _, revision := range snapshot.Revisions {
		if revision.Kind == "drawing" && revision.Prefix == "A" && revision.ID == 1 {
			return revision
		}
	}
	t.Fatal("snapshot omitted drawing revision")
	return RecordRevision{}
}

// Returning to an earlier value is still a new operation. Another desk that
// saw the old value has not seen this independent later return to that value.
func TestFrontierReviewSameValueReversionNeedsReview(t *testing.T) {
	for _, correctionFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "reversion-first", true: "correction-first"}[correctionFirst], func(t *testing.T) {
			a, b := newTestStore(t), newTestStore(t)
			original, _ := recoveryStore(t)
			auditAccepted(t, original, a, Order{"A", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}, true)
			auditAccepted(t, original, a, Order{"A", 2}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}, true)
			oldReceipt, err := original.Receipt(Order{"A", 2})
			must(t, err)
			auditAccepted(t, original, b, Order{"B", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 43}}, true)
			for _, edit := range []struct {
				save   int64
				winner int
			}{{3, 44}, {4, 42}} {
				must(t, a.WithLocalOperation(Order{"A", edit.save}, func(s *Store) error {
					return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: edit.winner}})
				}))
			}
			// A delayed response has the same value but must not reinstate A/2
			// as the current operation after A/4 has already been retained.
			must(t, a.ApplyReceipt(oldReceipt))
			left, err := a.ExportRecovery()
			must(t, err)
			right, err := b.ExportRecovery()
			must(t, err)
			must(t, ValidateRecoverySnapshot(&left))
			if revision := frontierReviewDrawing(t, left); !reflect.DeepEqual(revision.Heads, []string{"client:A/4"}) {
				t.Fatalf("old same-value acknowledgement replaced the reversion head: %+v", revision)
			}
			if revisionDominates(frontierReviewDrawing(t, right), frontierReviewDrawing(t, left)) {
				t.Fatal("knowing old A/2 was mistaken for observing independent A/4")
			}
			if correctionFirst {
				left, right = right, left
			}
			replacement, _ := recoveryStore(t)
			key, token := requestedKey(t, replacement)
			must(t, replacement.RecoverSnapshot(key, "first", token, left))
			must(t, replacement.RecoverSnapshot(key, "second", token, right))
			conflicts, err := replacement.Conflicts()
			must(t, err)
			if len(conflicts) != 1 || len(conflicts[0].Candidates) != 2 {
				t.Fatalf("independent reversion silently lost: %+v", conflicts)
			}
		})
	}
}

// Equal values learned from unrelated branches must keep both current heads.
// A correction that observed just one branch cannot supersede the other.
func TestFrontierReviewSameValueMergeKeepsUnobservedHead(t *testing.T) {
	a, b, c := newTestStore(t), newTestStore(t), newTestStore(t)
	original, _ := recoveryStore(t)
	auditAccepted(t, original, a, Order{"A", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}, true)
	auditAccepted(t, original, b, Order{"B", 1}, nil, []Basket{{Prefix: "A", BID: 1, WinningTicket: 43}}, true)
	must(t, c.WithLocalOperation(Order{"C", 1}, func(s *Store) error {
		return s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}})
	}))
	cSnapshot, err := c.ExportRecovery()
	must(t, err)
	must(t, a.ApplyReceipt(SaveReceipt{Revisions: cSnapshot.Revisions}))
	aSnapshot, err := a.ExportRecovery()
	must(t, err)
	bSnapshot, err := b.ExportRecovery()
	must(t, err)
	if revision := frontierReviewDrawing(t, aSnapshot); !reflect.DeepEqual(revision.Heads, []string{"client:A/1", "client:C/1"}) {
		t.Fatalf("same-value merge discarded an independent head: %+v", revision)
	}
	if revisionDominates(frontierReviewDrawing(t, bSnapshot), frontierReviewDrawing(t, aSnapshot)) {
		t.Fatal("correction that saw only A silently superseded independent C")
	}
}
