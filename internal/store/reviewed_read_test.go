package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReviewedReadsDoNotBlockOtherReports(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{{Prefix: "A", TID: 1, FirstName: "Ann"}}))
	must(t, s.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Prize", WinningTicket: 1}}))

	reading, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	otherDone := make(chan error, 1)
	otherStarted, otherFinished := false, false
	go func() {
		_, err := reviewedRead(s, func(view *Store) ([]ReportCountLine, error) {
			close(reading)
			<-release
			return view.ReportCounts()
		})
		firstDone <- err
	}()
	t.Cleanup(func() {
		close(release)
		if err := <-firstDone; err != nil {
			t.Error(err)
		}
		if otherStarted && !otherFinished {
			<-otherDone
		}
	})
	select {
	case <-reading:
	case <-time.After(2 * time.Second):
		t.Fatal("first report did not establish its snapshot")
	}

	otherStarted = true
	go func() {
		counts, err := s.ReportCounts()
		if err == nil && (len(counts) != 2 || counts[0].TotalBuys != 1) {
			err = fmt.Errorf("counts = %+v", counts)
		}
		if err == nil {
			var names []ReportByNameLine
			names, err = s.ReportByName("A")
			if err == nil && (len(names) != 1 || names[0].FirstName != "Ann") {
				err = fmt.Errorf("names = %+v", names)
			}
		}
		if err == nil {
			var baskets []ReportByBasketLine
			baskets, err = s.ReportByBasket("A")
			if err == nil && (len(baskets) != 1 || baskets[0].FirstName != "Ann") {
				err = fmt.Errorf("baskets = %+v", baskets)
			}
		}
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		otherFinished = true
		must(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("other reports waited for an unrelated report to finish")
	}
}

func TestReviewedReadKeepsConflictCheckSnapshot(t *testing.T) {
	s := newTestStore(t)
	before := Ticket{Prefix: "A", TID: 1, FirstName: "Before"}
	after := Ticket{Prefix: "A", TID: 1, FirstName: "Unreviewed"}
	must(t, s.UpsertTickets([]Ticket{before}))
	oldValue, err := json.Marshal(before)
	must(t, err)
	newValue, err := json.Marshal(after)
	must(t, err)

	writerDone := make(chan error, 1)
	writerFinished := false
	got, err := reviewedRead(s, func(view *Store) (*Ticket, error) {
		// Recovery can commit after the conflict check. Both the new value and
		// its alternatives belong to that later state, never this read.
		go func() {
			writerDone <- s.tx(func(tx *sql.Tx) error {
				if _, err := tx.Exec(`UPDATE tickets SET first_name='Unreviewed' WHERE prefix='A' AND t_id=1`); err != nil {
					return err
				}
				for _, raw := range [][]byte{oldValue, newValue} {
					candidate := RecordCandidate{
						Revision: RecordRevision{Kind: "ticket", Prefix: "A", ID: 1, Hash: valueHash(raw)},
						Value:    raw,
					}
					if err := addCandidate(tx, candidate); err != nil {
						return err
					}
				}
				return nil
			})
		}()
		select {
		case err := <-writerDone:
			writerFinished = true
			if err != nil {
				return nil, err
			}
		case <-time.After(2 * time.Second):
			return nil, errors.New("recovery write waited for the report snapshot to finish")
		}
		return view.Ticket("A", 1)
	})
	if !writerFinished {
		must(t, <-writerDone)
	}
	must(t, err)
	if got == nil || *got != before {
		t.Fatalf("read exposed data committed after its conflict check: %+v", got)
	}
	_, err = s.Ticket("A", 1)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("next read did not reject the committed conflict: %v", err)
	}
}

func TestReviewedReadUsesEnclosingTransaction(t *testing.T) {
	s := newTestStore(t)
	rollback := errors.New("roll back this save")
	err := s.tx(func(tx *sql.Tx) error {
		view := s.view(tx)
		if err := view.UpsertTickets([]Ticket{{Prefix: "A", TID: 1}}); err != nil {
			return err
		}
		counts, err := view.ReportCounts()
		if err != nil {
			return err
		}
		if len(counts) != 2 || counts[0].TotalBuys != 1 {
			return fmt.Errorf("report lost the enclosing transaction's uncommitted ticket: %+v", counts)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("enclosing transaction = %v", err)
	}
	counts, err := s.ReportCounts()
	must(t, err)
	if len(counts) != 1 || counts[0].TotalBuys != 0 {
		t.Fatalf("report committed the enclosing transaction: %+v", counts)
	}
}
