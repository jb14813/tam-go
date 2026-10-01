package db

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// MigrateServer applies the schema additions only tam-server needs, after
// Migrate: the client_saves table and the auth_key_activity table. It is
// safe to run on every start.
func MigrateServer(sqldb *sql.DB) error {
	// client_saves holds, per client, the number and digest of the last save
	// applied from it, so a copy of an older save the network delivers late
	// is skipped, and a repeat of the last one is told from a different save
	// (see store.InOrder).
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS client_saves (
		client TEXT PRIMARY KEY,
		last_save INTEGER NOT NULL,
		last_hash TEXT NOT NULL DEFAULT '')`); err != nil {
		return fmt.Errorf("apply server schema: %w", err)
	}
	has, err := hasColumn(sqldb, "client_saves", "last_hash")
	if err != nil {
		return err
	}
	if !has {
		if _, err := sqldb.Exec(`ALTER TABLE client_saves ADD COLUMN last_hash TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add client_saves.last_hash: %w", err)
		}
	}
	return migrateKeyActivity(sqldb)
}

// keyActivity are the columns of auth_key_activity besides the key: last_seen
// is the time of the key's last authenticated request and last_update that
// of its last accepted write.
var keyActivity = []string{"last_seen", "last_update"}

// migrateKeyActivity creates auth_key_activity, where tam-server records
// when each access key was last seen and last saved anything.
func migrateKeyActivity(sqldb *sql.DB) error {
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS auth_key_activity (auth_key TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("apply server schema: %w", err)
	}
	for _, column := range keyActivity {
		has, err := hasColumn(sqldb, "auth_key_activity", column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := sqldb.Exec(`ALTER TABLE auth_key_activity ADD COLUMN ` + column + ` TEXT`); err != nil {
			return fmt.Errorf("add auth_key_activity.%s: %w", column, err)
		}
	}
	return nil
}

// quoteName quotes an SQL identifier, one read from the schema included.
func quoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// tableColumns lists the columns of table in order; none when there is no
// such table.
func tableColumns(sqldb *sql.DB, table string) ([]string, error) {
	rows, err := sqldb.Query(`PRAGMA table_info(` + quoteName(table) + `)`)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("inspect %s: %w", table, err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// hasColumn reports whether table has a column named column.
func hasColumn(sqldb *sql.DB, table, column string) (bool, error) {
	columns, err := tableColumns(sqldb, table)
	return slices.Contains(columns, column), err
}
