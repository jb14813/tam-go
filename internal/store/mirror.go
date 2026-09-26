package store

import "database/sql"

// ReplacePrefixes makes the prefix table equal to ps in one transaction.
// The client uses it when it copies the server's prefixes into its mirror,
// because prefixes are the only rows that can be deleted on the server.
func (s *Store) ReplacePrefixes(ps []Prefix) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM prefixes`); err != nil {
			return err
		}
		return execEach(tx, upsertPrefixSQL, len(ps), func(i int) []any {
			return []any{ps[i].Prefix, ps[i].Color, ps[i].Weight}
		})
	})
}
