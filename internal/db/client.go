package db

import (
	"database/sql"
	"fmt"
)

// ClientTables are the tables only tam-client has: the outbox of saves that
// have not reached the server yet, and the ones the server rejected.
var ClientTables = []string{
	`CREATE TABLE IF NOT EXISTS outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		method TEXT NOT NULL,
		path TEXT NOT NULL,
		body BLOB,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '')`,
	`CREATE TABLE IF NOT EXISTS outbox_failed (
		id INTEGER PRIMARY KEY,
		created_at TEXT NOT NULL,
		method TEXT NOT NULL,
		path TEXT NOT NULL,
		body BLOB,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		failed_at TEXT NOT NULL)`,
}

// MigrateClient creates the client-only tables. It runs after Migrate and
// is safe to run on every start; the original app ignores these tables.
func MigrateClient(sqldb *sql.DB) error {
	for _, stmt := range ClientTables {
		if _, err := sqldb.Exec(stmt); err != nil {
			return fmt.Errorf("apply client schema: %w", err)
		}
	}
	return nil
}
