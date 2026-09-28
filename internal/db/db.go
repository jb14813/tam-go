// Package db opens the SQLite database and applies the schema.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Tables is the shared schema. The original event tables keep their column
// layout; basket_components records which form supplied each basket part.
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
}

// View is a named view definition. Views are recreated on every start so a
// database written by an older version (or by the original app, whose
// server labelled the counts total "Totals") ends up with the current
// definitions.
type View struct {
	Name string
	SQL  string
}

// Views are the report views of the original, but for one thing: a basket
// joins its winner only once it is drawn. Winning ticket 0 means not drawn
// yet, and the original's join made a ticket numbered 0 the winner of every
// basket still to draw.
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
		SELECT prefix, COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))) AS unique_buyers, COUNT(*) AS total_buys
		FROM tickets
		GROUP BY prefix
		UNION ALL
		SELECT 'Total', COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))), COUNT(*)
		FROM tickets`},
}

// Open opens (and creates when missing) the SQLite database at path with a
// busy timeout and WAL journaling, and verifies the connection.
//
// Transactions begin IMMEDIATE, taking the write lock at the start: SQLite
// does not apply the busy timeout to a transaction that has read and then
// asks to write, but fails it at once with "database is locked" when the
// lock is held for a moment, as a reader under load repairing the WAL index
// does. Every transaction here writes, so none loses anything by it.
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
	// The original apps can reopen this database and write the shared
	// basket columns directly. Track their explicit column updates too,
	// including clearing a field to its existing empty/zero value. Rows
	// without provenance already use the conservative complete fallback.
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
