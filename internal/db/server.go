package db

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// MigrateServer applies the schema additions only tam-server needs, after
// Migrate: save ordering, key activity, and event recovery. It is safe to run
// on every start.
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
	if err := migrateKeyActivity(sqldb); err != nil {
		return err
	}
	// Recovery is server-only metadata. Each client has its own receipt,
	// including when several clients use the same access key.
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS causal_review (id INTEGER PRIMARY KEY CHECK(id=1), token TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS recovery_state (
			id INTEGER PRIMARY KEY CHECK (id = 1), token TEXT NOT NULL,
			populated INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS recovery_requests (
			auth_key TEXT PRIMARY KEY, token TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS recovery_receipts (
			auth_key TEXT NOT NULL, client TEXT NOT NULL, token TEXT NOT NULL,
			PRIMARY KEY (auth_key, client))`,
		`CREATE TABLE IF NOT EXISTS recovery_deleted_prefixes (prefix TEXT PRIMARY KEY)`,
	} {
		if _, err := sqldb.Exec(statement); err != nil {
			return fmt.Errorf("apply recovery schema: %w", err)
		}
	}
	// Remember actual writes, even if the event is emptied before another
	// heartbeat or restart. The next empty startup can then distinguish a
	// new loss from an unfinished recovery whose clients had no data yet.
	for _, table := range []string{"prefixes", "tickets", "baskets"} {
		for _, operation := range []string{"INSERT", "UPDATE"} {
			statement := `CREATE TRIGGER IF NOT EXISTS recovery_populated_` + table + `_` + strings.ToLower(operation) +
				` AFTER ` + operation + ` ON ` + table + ` BEGIN
				UPDATE recovery_state SET populated = 1 WHERE id = 1 AND populated = 0; END`
			if _, err := sqldb.Exec(statement); err != nil {
				return fmt.Errorf("apply recovery trigger: %w", err)
			}
		}
	}
	return nil
}

// keyActivity are the columns of auth_key_activity besides the key: last_seen
// is the time of the key's last authenticated request and last_update that
// of its last accepted write.
var keyActivity = []string{"last_seen", "last_update"}

// migrateKeyActivity preserves activity recorded by earlier Go versions in
// auth_keys. Only the two known timestamp columns belong to this upgrade;
// other columns are neither moved nor removed.
func migrateKeyActivity(sqldb *sql.DB) error {
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS auth_key_activity (auth_key TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("apply server schema: %w", err)
	}
	inKeys, err := tableColumns(sqldb, "auth_keys")
	if err != nil {
		return err
	}
	var added []string
	for _, column := range inKeys {
		if slices.Contains(keyActivity, column) {
			added = append(added, column)
		}
	}
	have, err := tableColumns(sqldb, "auth_key_activity")
	if err != nil {
		return err
	}
	for _, column := range keyActivity {
		if slices.Contains(have, column) {
			continue
		}
		if _, err := sqldb.Exec(`ALTER TABLE auth_key_activity ADD COLUMN ` + quoteName(column) + ` TEXT`); err != nil {
			return fmt.Errorf("add auth_key_activity.%s: %w", column, err)
		}
		have = append(have, column)
	}
	for _, column := range added {
		if err := moveKeyColumn(sqldb, column); err != nil {
			return fmt.Errorf("move auth_keys.%s to auth_key_activity: %w", column, err)
		}
	}
	if _, err := sqldb.Exec(`DELETE FROM auth_key_activity WHERE auth_key NOT IN (SELECT auth_key FROM auth_keys)`); err != nil {
		return fmt.Errorf("drop the activity of deleted keys: %w", err)
	}
	return nil
}

// moveKeyColumn copies a column of auth_keys into auth_key_activity and
// drops it from auth_keys, in one transaction. Where both hold a value (a
// time), the later one stays.
func moveKeyColumn(sqldb *sql.DB, column string) error {
	c := quoteName(column)
	tx, err := sqldb.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO auth_key_activity (auth_key, ` + c + `)
		SELECT auth_key, ` + c + ` FROM auth_keys WHERE ` + c + ` IS NOT NULL
		ON CONFLICT (auth_key) DO UPDATE SET ` + c + ` = excluded.` + c + `
		WHERE excluded.` + c + ` > coalesce(auth_key_activity.` + c + `, '')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE auth_keys DROP COLUMN ` + c); err != nil {
		return err
	}
	return tx.Commit()
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
