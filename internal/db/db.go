// Package db opens the SQLite database and applies the schema.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Tables is the table schema of both daemons, copied from the original
// Ticket Auction Manager. Every statement is idempotent.
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
}

// View is a named view definition. Views are recreated on every start so a
// database written by an older version (or by the original app, whose
// server labelled the counts total "Totals") ends up with the current
// definitions.
type View struct {
	Name string
	SQL  string
}

// Views are the report views of the original.
var Views = []View{
	{"drawing", `CREATE VIEW drawing AS
		SELECT b.prefix, b.b_id, b.description, b.winning_ticket, t.last_name, t.first_name, t.phone_number
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id
		ORDER BY b.prefix, b.b_id`},
	{"report_by_name", `CREATE VIEW report_by_name AS
		SELECT t.last_name, t.first_name, t.phone_number, t.pref, b.prefix, b.b_id, b.description, b.donors, b.winning_ticket
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id
		ORDER BY t.last_name, t.first_name, t.phone_number, b.prefix, b.b_id`},
	{"report_by_basket", `CREATE VIEW report_by_basket AS
		SELECT b.prefix, b.b_id, b.description, b.donors, b.winning_ticket, t.last_name, t.first_name, t.phone_number, t.pref
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id
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
func Open(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
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
