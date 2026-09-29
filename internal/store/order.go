package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Order names a save for the server: the client that made it and its
// number among that client's saves. The zero Order is a save sent without
// a number, as the original client sends them.
type Order struct {
	Client string
	Save   int64
}

// ClientName returns this data folder's stable save-order identity without
// consuming a save number. Recovery uses it to distinguish clients that
// share an access key. A copied data folder on a new host gets a new name.
func (s *Store) ClientName(host string) (string, error) {
	var name string
	err := s.tx(func(tx *sql.Tx) error {
		var err error
		name, _, err = clientName(tx, host)
		return err
	})
	return name, err
}

// NextSave returns this client's name and the number of its next save. The
// name is made on first use; a data folder copied to another machine
// (another host name) makes a name of its own there, so two clients never
// share one. The numbers only ever grow, across restarts and new names.
func (s *Store) NextSave(host string) (Order, error) {
	var o Order
	err := s.tx(func(tx *sql.Tx) error {
		var err error
		o, err = nextSave(tx, host)
		return err
	})
	return o, err
}

// nextSave is NextSave inside a transaction.
func nextSave(tx *sql.Tx, host string) (Order, error) {
	client, last, err := clientName(tx, host)
	if err != nil {
		return Order{}, err
	}
	last++
	if _, err := tx.Exec(`UPDATE save_order SET last_save = ? WHERE id = 1`, last); err != nil {
		return Order{}, err
	}
	return Order{Client: client, Save: last}, nil
}

func clientName(tx *sql.Tx, host string) (string, int64, error) {
	var client, known string
	var last int64
	err := tx.QueryRow(`SELECT client, host, last_save FROM save_order WHERE id = 1`).Scan(&client, &known, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	if client == "" || known != host {
		if client, err = newClientName(); err != nil {
			return "", 0, err
		}
		if _, err := tx.Exec(`INSERT INTO save_order (id, client, host, last_save) VALUES (1, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET client = excluded.client, host = excluded.host`, client, host, last); err != nil {
			return "", 0, err
		}
	}
	return client, last, nil
}

func newClientName() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// NextSaveAfter numbers this client's next save past last, the number of the
// last save the server applied from it. The server refused a save as older
// than that: the client's own count went back, because its data folder was
// put back from a copy (or another client shares its name). Saves numbered
// at or below last would all be refused.
func (s *Store) NextSaveAfter(host string, last int64) (Order, error) {
	var o Order
	err := s.tx(func(tx *sql.Tx) error {
		if err := skipSavesTo(tx, last); err != nil {
			return err
		}
		var err error
		o, err = nextSave(tx, host)
		return err
	})
	return o, err
}

// SkipSavesTo moves this client's count of saves up to last when it is below,
// so the next save is numbered past what the server has applied from it
// (see NextSaveAfter).
func (s *Store) SkipSavesTo(last int64) error {
	return s.tx(func(tx *sql.Tx) error { return skipSavesTo(tx, last) })
}

func skipSavesTo(tx *sql.Tx, last int64) error {
	_, err := tx.Exec(`UPDATE save_order SET last_save = ? WHERE id = 1 AND last_save < ?`, last, last)
	return err
}

// Outcome is what InOrder did with a numbered save.
type Outcome int

const (
	// Applied: the save is newer than the last one applied from its client
	// and was applied.
	Applied Outcome = iota
	// Repeat: the save is the last one applied from its client, arriving
	// again: the client gave up waiting for the answer and sent it again, or
	// the network delivered a copy late. It is not applied a second time.
	Repeat
	// Behind: the save is older than the last one applied from its client
	// and is not that one. Either it is a late copy of a save the client has
	// long had answered, or the client's numbers went back (its data folder
	// was put back from a copy). It is not applied; a client still waiting
	// for the answer numbers it again, past the last number, and resends it.
	Behind
)

// InOrder applies write when save is newer than the last save applied from
// client, and records its number and digest (what the save says), in one
// transaction. A client sends its saves one at a time in the order it
// numbered them (tam-client does), so a save that is not newer was already
// applied or must not be: applying an older save again could undo newer
// ones. The digest tells a repeat of the last save from a different save
// under a number the server has seen. InOrder returns what it did and the
// number of the last save applied from client. A write that fails leaves
// the record as it was, so the same save can be sent again.
func (s *Store) InOrder(client string, save int64, digest string, write func(*Store) error) (Outcome, int64, error) {
	var out Outcome
	var last int64
	err := s.tx(func(tx *sql.Tx) error {
		var lastDigest string
		err := tx.QueryRow(`SELECT last_save, last_hash FROM client_saves WHERE client = ?`, client).Scan(&last, &lastDigest)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case save == last && digest == lastDigest:
			out = Repeat
			return nil
		case save <= last:
			out = Behind
			return nil
		}
		receipt := SaveReceipt{Revisions: []RecordRevision{}}
		view := s.view(tx)
		view.operation = &Order{Client: client, Save: save}
		view.receipt = &receipt
		view.touched = map[string]bool{}
		if err := write(view); err != nil {
			return err
		}
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO operation_receipts(client,save,digest,receipt) VALUES(?,?,?,?) ON CONFLICT(client,save) DO UPDATE SET digest=excluded.digest,receipt=excluded.receipt`, client, save, digest, string(raw)); err != nil {
			return err
		}
		// Only the last save can be repeated; older numbers return Behind.
		// Avoid retaining quadratic copies of a frequently corrected row's history.
		if _, err = tx.Exec(`DELETE FROM operation_receipts WHERE client=? AND save<>?`, client, save); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO client_saves (client, last_save, last_hash) VALUES (?, ?, ?)
			ON CONFLICT (client) DO UPDATE SET last_save = excluded.last_save, last_hash = excluded.last_hash`,
			client, save, digest); err != nil {
			return err
		}
		out, last = Applied, save
		return nil
	})
	return out, last, err
}
