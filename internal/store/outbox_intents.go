package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SaveIntent durably records an online save before sending it. Its local
// entries remain untouched until acceptance or a transition to offline work.
func (s *Store) SaveIntent(method, path string, body []byte, order Order, reserve ...func(*Store) error) (int64, error) {
	var id int64
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO outbox (created_at, method, path, body, client, save_number, local_applied)
			VALUES (?, ?, ?, ?, ?, ?, 0)`, time.Now().UTC().Format(time.RFC3339Nano), method, path, body, order.Client, order.Save)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		for _, reserve := range reserve {
			if err := reserve(s.view(tx)); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// RenumberOutbox keeps a resent intent under the number that actually
// leaves the client, even if it crashes before receiving the response.
func (s *Store) RenumberOutbox(id int64, order Order) error {
	return s.tx(func(tx *sql.Tx) error {
		request, err := scanOutbox(tx.QueryRow(`SELECT `+outboxCols+` FROM outbox WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if err := renumberLocalOperation(tx, request, order); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE outbox SET client = ?, save_number = ? WHERE id = ?`, order.Client, order.Save, id)
		return err
	})
}

// CompleteIntent retains the accepted entries and removes their request
// atomically. A failure leaves the durable request available for recovery.
func (s *Store) CompleteIntent(id int64, write func(*Store) error, receipt ...*SaveReceipt) error {
	return s.retainIntent(id, true, write, receipt...)
}

// RetainIntent transitions an interrupted online request into a normal
// offline save, keeping its original number for eventual replay.
func (s *Store) RetainIntent(id int64, write func(*Store) error) error {
	return s.retainIntent(id, false, write)
}

func (s *Store) retainIntent(id int64, complete bool, write func(*Store) error, receipts ...*SaveReceipt) error {
	return s.tx(func(tx *sql.Tx) error {
		var applied bool
		var order Order
		if err := tx.QueryRow(`SELECT local_applied, client, save_number FROM outbox WHERE id = ?`, id).Scan(&applied, &order.Client, &order.Save); err != nil {
			return err
		}
		if !applied {
			if err := (&Store{db: s.db, in: tx}).WithLocalOperation(order, write); err != nil {
				return err
			}
		}
		for _, receipt := range receipts {
			if receipt != nil {
				if err := s.view(tx).ApplyReceipt(*receipt); err != nil {
					return err
				}
			}
		}
		statement := `UPDATE outbox SET local_applied = 1 WHERE id = ?`
		if complete {
			statement = `DELETE FROM outbox WHERE id = ?`
		}
		_, err := tx.Exec(statement, id)
		return err
	})
}

// AcknowledgeOutbox preserves the accepted causal history before forgetting
// an already locally applied save. A crash commits both changes or neither.
func (s *Store) AcknowledgeOutbox(id int64, receipt *SaveReceipt) error {
	return s.tx(func(tx *sql.Tx) error {
		if receipt != nil {
			if err := s.view(tx).ApplyReceipt(*receipt); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM outbox WHERE id = ?`, id)
		return err
	})
}

// RejectIntent records a definitive server rejection before cleanup. If
// deleting its journal row fails, it stays in the visible failed list and
// cannot be replayed or applied locally by accident.
func (s *Store) RejectIntent(id int64, reason string, rollback ...func(*Store) error) error {
	if err := s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE outbox SET rejected = ?, last_error = ? WHERE id = ?`, reason, reason, id); err != nil {
			return err
		}
		for _, rollback := range rollback {
			if err := rollback(s.view(tx)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return s.DeleteOutbox(id)
}

// MaterializeIntents completes local retention of abandoned online saves,
// in order, before later local writes or recovery snapshots. Callers hold
// the client's Numbering lock. Normal queued saves already have their local
// rows and are never reapplied over newer entries.
func (s *Store) MaterializeIntents() error {
	return s.tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT ` + outboxCols + ` FROM outbox WHERE local_applied = 0 AND rejected = '' ORDER BY id`)
		if err != nil {
			return err
		}
		var intents []*Outbox
		for rows.Next() {
			o, err := scanOutbox(rows)
			if err != nil {
				rows.Close()
				return err
			}
			intents = append(intents, o)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		view := &Store{db: s.db, in: tx}
		for _, o := range intents {
			if err := view.WithLocalOperation(o.Order, func(st *Store) error { return st.applyIntent(o) }); err != nil {
				return fmt.Errorf("retain save %d: %w", o.Order.Save, err)
			}
			if _, err := tx.Exec(`UPDATE outbox SET local_applied = 1 WHERE id = ?`, o.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) applyIntent(o *Outbox) error {
	if o.Method == http.MethodDelete {
		u, err := url.Parse(o.Path)
		if err != nil {
			return err
		}
		if u.Path != "/api/prefixes" || u.Query().Get("p") == "" {
			return fmt.Errorf("unsupported delete intent %q", o.Path)
		}
		_, err = s.DeletePrefix(u.Query().Get("p"))
		return err
	}
	if o.Method != http.MethodPost {
		return fmt.Errorf("unsupported intent method %q", o.Method)
	}
	switch o.Path {
	case "/api/backuprestore":
		var backup BackupFile
		if err := json.Unmarshal(o.Body, &backup); err != nil {
			return err
		}
		// Prefix Push uses backup validation to preserve legacy identities;
		// complete restores are never journaled as client save operations.
		if len(backup.Tickets) != 0 || len(backup.Baskets) != 0 {
			return fmt.Errorf("unsupported non-prefix backup intent")
		}
		return s.UpsertPrefixes(backup.Prefixes)
	case "/api/prefixes":
		var rows []Prefix
		if err := json.Unmarshal(o.Body, &rows); err != nil {
			return err
		}
		return s.UpsertPrefixes(rows)
	case "/api/tickets", "/api/search/tickets":
		var rows []Ticket
		if err := json.Unmarshal(o.Body, &rows); err != nil {
			return err
		}
		return s.UpsertTickets(rows)
	case "/api/baskets", "/api/drawing":
		var rows []Basket
		if err := json.Unmarshal(o.Body, &rows); err != nil {
			return err
		}
		if o.Path == "/api/drawing" {
			return s.UpsertWinning(rows)
		}
		return s.UpsertBaskets(rows)
	default:
		return fmt.Errorf("unsupported save intent %q", o.Path)
	}
}
