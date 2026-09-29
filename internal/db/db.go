// Package db opens the SQLite database and applies the schema.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Tables is the event schema. Existing tables retain their column layout;
// basket_components records which form supplied each basket part.
// Every statement is idempotent.
var Tables = []string{
	`CREATE TABLE IF NOT EXISTS prefixes (
		prefix TEXT PRIMARY KEY,
		color TEXT,
		weight INTEGER)`,
	`CREATE TABLE IF NOT EXISTS tickets (
		prefix TEXT,
		t_id INTEGER,
		first_name TEXT,
		last_name TEXT,
		phone_number TEXT,
		pref TEXT,
		PRIMARY KEY (prefix, t_id))`,
	`CREATE TABLE IF NOT EXISTS baskets (
		prefix TEXT,
		b_id INTEGER,
		description TEXT,
		donors TEXT,
		winning_ticket INTEGER,
		PRIMARY KEY (prefix, b_id))`,
	`CREATE TABLE IF NOT EXISTS auth_keys (
		auth_key TEXT PRIMARY KEY,
		description TEXT)`,
	`CREATE TABLE IF NOT EXISTS basket_components (
		prefix TEXT NOT NULL,
		b_id INTEGER NOT NULL,
		metadata INTEGER NOT NULL DEFAULT 0,
		drawing INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (prefix, b_id))`,
	`CREATE TABLE IF NOT EXISTS causal_identity (id INTEGER PRIMARY KEY CHECK(id = 1), actor TEXT NOT NULL, counter INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS record_revisions (kind TEXT NOT NULL, prefix TEXT NOT NULL, record_id INTEGER NOT NULL, revision TEXT NOT NULL, PRIMARY KEY(kind, prefix, record_id))`,
	`CREATE TABLE IF NOT EXISTS record_conflicts (kind TEXT NOT NULL, prefix TEXT NOT NULL, record_id INTEGER NOT NULL, candidate_id TEXT NOT NULL, candidate TEXT NOT NULL, PRIMARY KEY(kind, prefix, record_id, candidate_id))`,
	`CREATE TABLE IF NOT EXISTS operation_receipts (client TEXT NOT NULL, save INTEGER NOT NULL, digest TEXT NOT NULL, receipt TEXT NOT NULL, PRIMARY KEY(client, save))`,
}

// View is a named view definition. Views are recreated on every start so
// databases written by earlier Go versions use the current definitions.
type View struct {
	Name string
	SQL  string
}

// Views join a basket with a winner only after it is drawn. Winning ticket
// zero means not drawn yet, even if a sold ticket is numbered zero.
var Views = []View{
	{"drawing", `CREATE VIEW drawing AS
		SELECT b.prefix, b.b_id, b.description, b.winning_ticket, t.last_name, t.first_name, t.phone_number
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id AND b.winning_ticket > 0
		ORDER BY b.prefix, b.b_id`},
	{"report_by_name", `CREATE VIEW report_by_name AS
		SELECT t.last_name, t.first_name, t.phone_number, t.pref, b.prefix, b.b_id, b.description, b.donors, b.winning_ticket
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id AND b.winning_ticket > 0
		ORDER BY t.last_name, t.first_name, t.phone_number, b.prefix, b.b_id`},
	{"report_by_basket", `CREATE VIEW report_by_basket AS
		SELECT b.prefix, b.b_id, b.description, b.donors, b.winning_ticket, t.last_name, t.first_name, t.phone_number, t.pref
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id AND b.winning_ticket > 0
		ORDER BY b.prefix, b.b_id`},
	{"report_counts", `CREATE VIEW report_counts AS
		SELECT prefix, 0 AS is_total, COUNT(*) AS unique_buyers, SUM(purchases) AS total_buys
		FROM (SELECT prefix, first_name, last_name, phone_number, COUNT(*) AS purchases
			FROM tickets GROUP BY prefix, first_name, last_name, phone_number)
		GROUP BY prefix
		UNION ALL
		SELECT 'Total', 1, COUNT(*), coalesce(SUM(purchases), 0)
		FROM (SELECT first_name, last_name, phone_number, COUNT(*) AS purchases
			FROM tickets GROUP BY first_name, last_name, phone_number)`},
}

// Open opens (and creates when missing) the SQLite database at path with a
// busy timeout and WAL journaling, and verifies the connection.
//
// Transactions begin IMMEDIATE, taking the write lock at the start: SQLite
// does not apply the busy timeout to a transaction that has read and then
// asks to write, but fails it at once with "database is locked" when the
// lock is held for a moment, as a reader under load repairing the WAL index
// does. Read-only transactions explicitly opt out of IMMEDIATE so reports
// can share a consistent snapshot without reserving the write lock.
func Open(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return sqldb, nil
}

// Migrate creates missing tables and recreates the views. It is safe to
// run on every start.
func Migrate(sqldb *sql.DB) error {
	for _, stmt := range Tables {
		if _, err := sqldb.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	if _, err := sqldb.Exec(`CREATE TRIGGER IF NOT EXISTS basket_components_delete
		AFTER DELETE ON baskets BEGIN
		DELETE FROM basket_components WHERE prefix = OLD.prefix AND b_id = OLD.b_id; END`); err != nil {
		return fmt.Errorf("apply basket component cleanup: %w", err)
	}
	// Native recovery and restore apply explicit component column updates.
	// Track ownership even when clearing a field to its existing empty value.
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS basket_components_metadata
			AFTER UPDATE OF description, donors ON baskets BEGIN
			UPDATE basket_components SET metadata = 1 WHERE prefix = NEW.prefix AND b_id = NEW.b_id; END`,
		`CREATE TRIGGER IF NOT EXISTS basket_components_drawing
			AFTER UPDATE OF winning_ticket ON baskets BEGIN
			UPDATE basket_components SET drawing = 1 WHERE prefix = NEW.prefix AND b_id = NEW.b_id; END`,
	} {
		if _, err := sqldb.Exec(statement); err != nil {
			return fmt.Errorf("apply basket component tracking: %w", err)
		}
	}
	for _, v := range Views {
		if _, err := sqldb.Exec("DROP VIEW IF EXISTS " + v.Name); err != nil {
			return fmt.Errorf("replace view %s: %w", v.Name, err)
		}
		if _, err := sqldb.Exec(v.SQL); err != nil {
			return fmt.Errorf("create view %s: %w", v.Name, err)
		}
	}
	return nil
}
