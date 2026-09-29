package store

import (
	"database/sql"
	"errors"
)

// CachePrefixes retains the server's complete menu for offline forms without
// replacing this client's authored entries, receipts, or deletion history.
// Legacy rows remain authored/unknown; their provenance is never guessed.
func (s *Store) CachePrefixes(prefixes []Prefix) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM prefix_menu_cache`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO prefix_menu_state(id) VALUES(1) ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		return execEach(tx, `INSERT INTO prefix_menu_cache(prefix,color,weight) VALUES(?,?,?)
			ON CONFLICT(prefix) DO UPDATE SET color=excluded.color,weight=excluded.weight`, len(prefixes), func(i int) []any {
			p := prefixes[i]
			return []any{p.Prefix, p.Color, p.Weight}
		})
	})
}

// ClientPrefixes is the last shared menu plus deliberate local changes.
// Before any shared menu arrives, standalone/legacy authored rows are the menu.
// Export and Push continue to read ListPrefixes, which contains only retained
// entries and never this disposable configuration cache.
func (s *Store) ClientPrefixes() ([]Prefix, error) {
	if !s.readGuarded {
		return reviewedRead(s, func(v *Store) ([]Prefix, error) { return v.ClientPrefixes() })
	}
	rows, err := s.query(`SELECT prefix,color,weight FROM prefix_menu_cache
		WHERE EXISTS (SELECT 1 FROM prefix_menu_state)
		UNION ALL SELECT prefix,color,weight FROM prefixes
		WHERE NOT EXISTS (SELECT 1 FROM prefix_menu_state) ORDER BY weight,prefix`)
	if err != nil {
		return nil, err
	}
	return scanPrefixes(rows)
}

// Deleting a shared menu row is a deliberate local edit even if this client
// never authored the prefix. The caller has already recorded its tombstone.
// Server stores do not have the client cache tables.
func deleteCachedPrefix(tx *sql.Tx, name string) (*Prefix, error) {
	var has bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='prefix_menu_cache')`).Scan(&has); err != nil || !has {
		return nil, err
	}
	var p Prefix
	var color sql.NullString
	var weight sql.NullInt64
	err := tx.QueryRow(`DELETE FROM prefix_menu_cache WHERE prefix=? RETURNING prefix,color,weight`, name).Scan(&p.Prefix, &color, &weight)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Color, p.Weight = nstr(color), nint(weight)
	return &p, nil
}
