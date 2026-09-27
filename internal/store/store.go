// Package store is the data access layer shared by both daemons. Every
// query lives here; handlers never touch SQL.
package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// Store wraps one shared *sql.DB.
//
// Writes take turns through wmu, in the order they arrive. SQLite lets one
// writer in at a time and has the others retry in a busy wait that is not
// first come, first served; where every commit is flushed to a slow disk, a
// writer could time out behind a crowd of others and fail. Reads do not
// wait: with WAL journaling they never block on a writer.
type Store struct {
	db  *sql.DB
	wmu sync.Mutex
	in  *sql.Tx // set on the Store SaveQueued hands out: its writes go into this transaction
}

// New returns a Store over an opened, migrated database.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func nstr(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func nint(v sql.NullInt64) int {
	if v.Valid {
		return int(v.Int64)
	}
	return 0
}

// exec runs one statement that writes, in its turn.
func (s *Store) exec(query string, args ...any) (sql.Result, error) {
	if s.in != nil {
		return s.in.Exec(query, args...)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.db.Exec(query, args...)
}

// execReturning runs one statement that writes and returns a row (DELETE
// ... RETURNING), in its turn, and scans the row into dest.
func (s *Store) execReturning(query string, args []any, dest ...any) error {
	if s.in != nil {
		return s.in.QueryRow(query, args...).Scan(dest...)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.db.QueryRow(query, args...).Scan(dest...)
}

// tx runs fn inside a transaction, in its turn among the writes, and
// commits it when fn returns nil.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	if s.in != nil {
		return fn(s.in)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// --- prefixes ---

const prefixCols = `prefix, color, weight`

func scanPrefixes(rows *sql.Rows) ([]Prefix, error) {
	defer rows.Close()
	out := []Prefix{}
	for rows.Next() {
		var p Prefix
		var color sql.NullString
		var weight sql.NullInt64
		if err := rows.Scan(&p.Prefix, &color, &weight); err != nil {
			return nil, err
		}
		p.Color, p.Weight = nstr(color), nint(weight)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPrefixes returns every prefix ordered by weight, then name.
func (s *Store) ListPrefixes() ([]Prefix, error) {
	rows, err := s.db.Query(`SELECT ` + prefixCols + ` FROM prefixes ORDER BY weight, prefix`)
	if err != nil {
		return nil, err
	}
	return scanPrefixes(rows)
}

const upsertPrefixSQL = `INSERT INTO prefixes (prefix, color, weight) VALUES (?, ?, ?)
	ON CONFLICT (prefix) DO UPDATE SET color = EXCLUDED.color, weight = EXCLUDED.weight`

// UpsertPrefixes inserts or updates the given prefixes in one transaction.
func (s *Store) UpsertPrefixes(ps []Prefix) error {
	return s.tx(func(tx *sql.Tx) error {
		return execEach(tx, upsertPrefixSQL, len(ps), func(i int) []any {
			return []any{ps[i].Prefix, ps[i].Color, ps[i].Weight}
		})
	})
}

// DeletePrefix removes a prefix and returns the deleted row, or nil when
// there was none.
func (s *Store) DeletePrefix(name string) (*Prefix, error) {
	var p Prefix
	var color sql.NullString
	var weight sql.NullInt64
	err := s.execReturning(`DELETE FROM prefixes WHERE prefix = ? RETURNING prefix, color, weight`, []any{name}, &p.Prefix, &color, &weight)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Color, p.Weight = nstr(color), nint(weight)
	return &p, nil
}

// execEach prepares query once inside tx and executes it n times with the
// arguments produced by args(i).
func execEach(tx *sql.Tx, query string, n int, args func(i int) []any) error {
	if n == 0 {
		return nil
	}
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := 0; i < n; i++ {
		if _, err := stmt.Exec(args(i)...); err != nil {
			return err
		}
	}
	return nil
}

// --- auth keys ---

const keyAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateKey() (string, error) {
	buf := make([]byte, 32)
	max := big.NewInt(int64(len(keyAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = keyAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// hasLastSeen reports whether auth_keys has the last_seen column that
// db.MigrateServer adds. The client's database never gets it.
func (s *Store) hasLastSeen() (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(auth_keys)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == "last_seen" {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ListKeys returns every access key ordered by description, then key.
// LastSeen is filled in when the database has the column.
func (s *Store) ListKeys() ([]AuthKey, error) {
	withSeen, err := s.hasLastSeen()
	if err != nil {
		return nil, err
	}
	query := `SELECT auth_key, description, NULL FROM auth_keys ORDER BY description, auth_key`
	if withSeen {
		query = `SELECT auth_key, description, last_seen FROM auth_keys ORDER BY description, auth_key`
	}
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuthKey{}
	for rows.Next() {
		var k AuthKey
		var desc, seen sql.NullString
		if err := rows.Scan(&k.AuthKey, &desc, &seen); err != nil {
			return nil, err
		}
		k.Description, k.LastSeen = nstr(desc), nstr(seen)
		out = append(out, k)
	}
	return out, rows.Err()
}

// TouchKey records now as the key's last_seen time. It needs the column
// db.MigrateServer adds; a missing key is not an error.
func (s *Store) TouchKey(key string) error {
	_, err := s.exec(`UPDATE auth_keys SET last_seen = ? WHERE auth_key = ?`, time.Now().UTC().Format(time.RFC3339), key)
	return err
}

// CreateKey stores a new random 32-character key with a description.
func (s *Store) CreateKey(description string) (AuthKey, error) {
	for attempt := 0; attempt < 5; attempt++ {
		key, err := generateKey()
		if err != nil {
			return AuthKey{}, err
		}
		exists, err := s.KeyExists(key)
		if err != nil {
			return AuthKey{}, err
		}
		if exists {
			continue
		}
		if _, err := s.exec(`INSERT INTO auth_keys (auth_key, description) VALUES (?, ?)`, key, description); err != nil {
			return AuthKey{}, err
		}
		return AuthKey{AuthKey: key, Description: description}, nil
	}
	return AuthKey{}, errors.New("could not generate a unique key")
}

// DeleteKey removes a key and returns the deleted row, or nil when there
// was none.
func (s *Store) DeleteKey(key string) (*AuthKey, error) {
	var k AuthKey
	var desc sql.NullString
	err := s.execReturning(`DELETE FROM auth_keys WHERE auth_key = ? RETURNING auth_key, description`, []any{key}, &k.AuthKey, &desc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.Description = nstr(desc)
	return &k, nil
}

// KeyExists reports whether key is a valid access key.
func (s *Store) KeyExists(key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM auth_keys WHERE auth_key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// --- status ---

// Counts returns the number of prefixes, tickets and baskets.
func (s *Store) Counts() (prefixes, tickets, baskets int, err error) {
	err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM prefixes), (SELECT COUNT(*) FROM tickets), (SELECT COUNT(*) FROM baskets)`).Scan(&prefixes, &tickets, &baskets)
	return prefixes, tickets, baskets, err
}

// --- backup and restore ---

// Export returns every prefix, basket and ticket.
func (s *Store) Export() (BackupFile, error) {
	bf := NewBackupFile()
	var err error
	if bf.Prefixes, err = s.ListPrefixes(); err != nil {
		return bf, err
	}
	if bf.Baskets, err = s.AllBaskets(); err != nil {
		return bf, err
	}
	if bf.Tickets, err = s.AllTickets(); err != nil {
		return bf, err
	}
	return bf, nil
}

// Import upserts every row of a backup file in one transaction. Existing
// rows are overwritten, which is what a restore is for.
func (s *Store) Import(bf BackupFile) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := execEach(tx, upsertPrefixSQL, len(bf.Prefixes), func(i int) []any {
			p := bf.Prefixes[i]
			return []any{p.Prefix, p.Color, p.Weight}
		}); err != nil {
			return fmt.Errorf("prefixes: %w", err)
		}
		if err := execEach(tx, restoreBasketSQL, len(bf.Baskets), func(i int) []any {
			b := bf.Baskets[i]
			return []any{b.Prefix, b.BID, b.Description, b.Donors, b.WinningTicket}
		}); err != nil {
			return fmt.Errorf("baskets: %w", err)
		}
		if err := execEach(tx, upsertTicketSQL, len(bf.Tickets), func(i int) []any {
			t := bf.Tickets[i]
			return []any{t.Prefix, t.TID, t.FirstName, t.LastName, t.PhoneNumber, t.Pref}
		}); err != nil {
			return fmt.Errorf("tickets: %w", err)
		}
		return nil
	})
}
