package db

import (
	"database/sql"
	"fmt"
)

// ClientTables are the tables only tam-client has: the outbox of saves that
// have not reached the server yet, and the ones the server rejected.
var ClientTables = []string{
	// A refused replay can expose an accepted predecessor after the initial
	// recovery upload. Keep that generation's reoffer durable across restarts.
	`CREATE TABLE IF NOT EXISTS client_recovery_contribution (id INTEGER PRIMARY KEY CHECK(id=1), target TEXT NOT NULL, token TEXT NOT NULL, epoch INTEGER NOT NULL, needed INTEGER NOT NULL, rejected INTEGER NOT NULL DEFAULT 0)`,
	// A queued edit may replace the only complete accepted local value. Keep
	// that predecessor and the exact withheld operation independently of the
	// delivery journal, including after a volunteer discards a refused save.
	`CREATE TABLE IF NOT EXISTS recovery_holdbacks (kind TEXT NOT NULL, prefix TEXT NOT NULL, record_id INTEGER NOT NULL, holdback TEXT NOT NULL, PRIMARY KEY(kind,prefix,record_id))`,
	// Shared menu configuration is a read cache, never an authored recovery
	// contribution. The marker distinguishes a fetched empty menu from a
	// client that has never fetched configuration and should use its own rows.
	`CREATE TABLE IF NOT EXISTS prefix_menu_cache (prefix TEXT PRIMARY KEY, color TEXT, weight INTEGER)`,
	`CREATE TABLE IF NOT EXISTS prefix_menu_state (id INTEGER PRIMARY KEY CHECK(id = 1))`,
	`CREATE TRIGGER IF NOT EXISTS prefix_menu_insert AFTER INSERT ON prefixes
		WHEN EXISTS (SELECT 1 FROM prefix_menu_state) BEGIN
		INSERT INTO prefix_menu_cache(prefix,color,weight) VALUES(NEW.prefix,NEW.color,NEW.weight)
		ON CONFLICT(prefix) DO UPDATE SET color=excluded.color,weight=excluded.weight; END`,
	`CREATE TRIGGER IF NOT EXISTS prefix_menu_update AFTER UPDATE ON prefixes
		WHEN EXISTS (SELECT 1 FROM prefix_menu_state) BEGIN
		INSERT INTO prefix_menu_cache(prefix,color,weight) VALUES(NEW.prefix,NEW.color,NEW.weight)
		ON CONFLICT(prefix) DO UPDATE SET color=excluded.color,weight=excluded.weight; END`,
	`CREATE TRIGGER IF NOT EXISTS prefix_menu_delete AFTER DELETE ON prefixes BEGIN
		DELETE FROM prefix_menu_cache WHERE prefix=OLD.prefix; END`,
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
	// save_order is the client's name for the server and the number of its
	// last save (see store.NextSave): one row.
	`CREATE TABLE IF NOT EXISTS save_order (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		client TEXT NOT NULL,
		host TEXT NOT NULL,
		last_save INTEGER NOT NULL)`,
	// Browser uploads can arrive after a newer save, even across a daemon
	// restart. Track retained generations per page and record/component.
	`CREATE TABLE IF NOT EXISTS editor_generations (
		session TEXT NOT NULL,
		kind TEXT NOT NULL,
		prefix TEXT NOT NULL,
		record_id INTEGER NOT NULL,
		sequence INTEGER NOT NULL,
		PRIMARY KEY (session, kind, prefix, record_id))`,
}

// clientColumns were added to the client tables after they first shipped:
// a queued save keeps the client name and number it was first sent with.
var clientColumns = []struct{ table, column, decl string }{
	{"outbox", "client", "TEXT NOT NULL DEFAULT ''"},
	{"outbox", "save_number", "INTEGER NOT NULL DEFAULT 0"},
	{"outbox_failed", "client", "TEXT NOT NULL DEFAULT ''"},
	{"outbox_failed", "save_number", "INTEGER NOT NULL DEFAULT 0"},
	// Existing queued saves already have their local rows. New online saves
	// first record an unapplied intent, before the first network request.
	{"outbox", "local_applied", "INTEGER NOT NULL DEFAULT 1"},
	{"outbox_failed", "local_applied", "INTEGER NOT NULL DEFAULT 1"},
	{"outbox", "rejected", "TEXT NOT NULL DEFAULT ''"},
	{"outbox_failed", "rejected", "TEXT NOT NULL DEFAULT ''"},
}

// MigrateClient creates the client-only tables. It runs after Migrate and
// is safe to run on every start.
func MigrateClient(sqldb *sql.DB) error {
	for _, stmt := range ClientTables {
		if _, err := sqldb.Exec(stmt); err != nil {
			return fmt.Errorf("apply client schema: %w", err)
		}
	}
	for _, c := range clientColumns {
		has, err := hasColumn(sqldb, c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := sqldb.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.decl); err != nil {
			return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}
