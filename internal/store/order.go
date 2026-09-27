package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

// Order names a save for the server: the client that made it and its
// number among that client's saves. The zero Order is a save sent without
// a number, as the original client sends them.
type Order struct {
	Client string
	Save   int64
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
	var client, known string
	var last int64
	err := tx.QueryRow(`SELECT client, host, last_save FROM save_order WHERE id = 1`).Scan(&client, &known, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Order{}, err
	}
	if client == "" || known != host {
		if client, err = newClientName(); err != nil {
			return Order{}, err
		}
	}
	last++
	if _, err := tx.Exec(`INSERT INTO save_order (id, client, host, last_save) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET client = excluded.client, host = excluded.host, last_save = excluded.last_save`,
		client, host, last); err != nil {
		return Order{}, err
	}
	return Order{Client: client, Save: last}, nil
}

func newClientName() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InOrder applies write only when save is newer than the last save applied
// from client, and records it, in one transaction; it reports whether write
// ran. A client numbers its saves in the order it makes them, so a save
// that is not newer is one the network delivered late (the client had
// given up on it and sent it again) or one sent twice. Applying it again
// could only undo newer saves. A write that fails leaves the number where
// it was, so the same save can be sent again.
func (s *Store) InOrder(client string, save int64, write func(*Store) error) (bool, error) {
	applied := false
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO client_saves (client, last_save) VALUES (?, ?)
			ON CONFLICT (client) DO UPDATE SET last_save = excluded.last_save WHERE excluded.last_save > client_saves.last_save`,
			client, save)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		if err := write(&Store{db: s.db, in: tx}); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied && err == nil, err
}
