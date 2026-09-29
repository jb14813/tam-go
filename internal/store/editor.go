package store

import (
	"database/sql"
	"errors"
)

// EditorRecord identifies the part of a row one form can save. Basket
// metadata and drawing results are separate records despite sharing an ID.
type EditorRecord struct {
	Kind   string
	Prefix string
	ID     int
}

// EditorReservation remembers the prior generation so a definitive online
// rejection can undo only this reservation. The client holds Numbering from
// preparing it through retaining or rejecting the save.
type EditorReservation struct {
	session  string
	sequence int64
	previous map[EditorRecord]int64
}

// PrepareEditor filters only records superseded by a durably retained newer
// save from this same page. Equal generations can retry the same request.
func (s *Store) PrepareEditor(session string, sequence int64, records []EditorRecord) ([]bool, *EditorReservation, error) {
	keep := make([]bool, len(records))
	reservation := &EditorReservation{session: session, sequence: sequence, previous: make(map[EditorRecord]int64)}
	err := s.tx(func(tx *sql.Tx) error {
		for i, record := range records {
			var prior int64
			err := tx.QueryRow(`SELECT sequence FROM editor_generations WHERE session=? AND kind=? AND prefix=? AND record_id=?`, session, record.Kind, record.Prefix, record.ID).Scan(&prior)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			keep[i] = prior <= sequence
			if keep[i] {
				reservation.previous[record] = prior
			}
		}
		return nil
	})
	return keep, reservation, err
}

// Reserve joins either the durable intent transaction or the local save
// transaction. A failed write must never advance the retained generation.
func (e *EditorReservation) Reserve(s *Store) error {
	if e == nil {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		for record := range e.previous {
			if _, err := tx.Exec(`INSERT INTO editor_generations(session,kind,prefix,record_id,sequence) VALUES(?,?,?,?,?)
				ON CONFLICT(session,kind,prefix,record_id) DO UPDATE SET sequence=max(editor_generations.sequence,excluded.sequence)`, e.session, record.Kind, record.Prefix, record.ID, e.sequence); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *EditorReservation) Retain(s *Store, write func(*Store) error) error {
	if e == nil {
		return write(s)
	}
	return s.tx(func(tx *sql.Tx) error {
		view := s.view(tx)
		if err := write(view); err != nil {
			return err
		}
		return e.Reserve(view)
	})
}

// Rollback is committed together with marking an online intent rejected.
// If that transaction fails, the still-durable intent retains its generation
// and remains observable instead of falsely acknowledging a vanished save.
func (e *EditorReservation) Rollback(s *Store) error {
	if e == nil {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		for record, prior := range e.previous {
			var err error
			if prior == 0 {
				_, err = tx.Exec(`DELETE FROM editor_generations WHERE session=? AND kind=? AND prefix=? AND record_id=? AND sequence=?`, e.session, record.Kind, record.Prefix, record.ID, e.sequence)
			} else {
				_, err = tx.Exec(`UPDATE editor_generations SET sequence=? WHERE session=? AND kind=? AND prefix=? AND record_id=? AND sequence=?`, prior, e.session, record.Kind, record.Prefix, record.ID, e.sequence)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}
