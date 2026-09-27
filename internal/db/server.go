package db

import (
	"database/sql"
	"fmt"
)

// MigrateServer applies the schema additions only tam-server needs, after
// Migrate: the laptop_saves table and the auth_keys.last_seen column, which
// records the last authenticated request of a key. The original app never
// reads either, so a database shared with the original server keeps
// working. It is safe to run on every start.
func MigrateServer(sqldb *sql.DB) error {
	// laptop_saves holds, per laptop, the number of the last save applied
	// from it, so a copy of an older save the network delivers late is
	// skipped (see store.InOrder).
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS laptop_saves (
		laptop TEXT PRIMARY KEY,
		last_save INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("apply server schema: %w", err)
	}
	has, err := hasColumn(sqldb, "auth_keys", "last_seen")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err := sqldb.Exec(`ALTER TABLE auth_keys ADD COLUMN last_seen TEXT`); err != nil {
		return fmt.Errorf("add auth_keys.last_seen: %w", err)
	}
	return nil
}

// hasColumn reports whether table has a column named column.
func hasColumn(sqldb *sql.DB, table, column string) (bool, error) {
	rows, err := sqldb.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("inspect %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
