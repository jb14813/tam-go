package db

import (
	"database/sql"
	"fmt"
)

// serverColumns are the auth_keys columns only tam-server needs: last_seen
// records the last authenticated request of a key and last_update its
// last accepted write.
var serverColumns = []string{"last_seen", "last_update"}

// MigrateServer applies the schema additions only tam-server needs, after
// Migrate: the auth_keys columns in serverColumns. The original app never
// reads them, so a database shared with the original server keeps working.
// It is safe to run on every start, and adds only what is missing, so a
// database from an earlier tam-server gains the newer columns.
func MigrateServer(sqldb *sql.DB) error {
	for _, column := range serverColumns {
		has, err := hasColumn(sqldb, "auth_keys", column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := sqldb.Exec(`ALTER TABLE auth_keys ADD COLUMN ` + column + ` TEXT`); err != nil {
			return fmt.Errorf("add auth_keys.%s: %w", column, err)
		}
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
