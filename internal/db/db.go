// Package db opens the SQLite database and applies the schema.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Statements is the schema of both daemons, copied from the original
// Ticket Auction Manager. Every statement is idempotent.
var Statements = []string{
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
	`CREATE VIEW IF NOT EXISTS drawing AS
		SELECT b.prefix, b.b_id, b.description, b.winning_ticket, t.last_name, t.first_name, t.phone_number
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id
		ORDER BY b.prefix, b.b_id`,
	`CREATE VIEW IF NOT EXISTS report_by_name AS
		SELECT t.last_name, t.first_name, t.phone_number, t.pref, b.prefix, b.b_id, b.description, b.donors, b.winning_ticket
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id
		ORDER BY t.last_name, t.first_name, t.phone_number, b.prefix, b.b_id`,
	`CREATE VIEW IF NOT EXISTS report_by_basket AS
		SELECT b.prefix, b.b_id, b.description, b.donors, b.winning_ticket, t.last_name, t.first_name, t.phone_number, t.pref
		FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id
		ORDER BY b.prefix, b.b_id`,
	`CREATE VIEW IF NOT EXISTS report_counts AS
		SELECT prefix, COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))) AS unique_buyers, COUNT(*) AS total_buys
		FROM tickets
		GROUP BY prefix
		UNION ALL
		SELECT 'Total', COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))), COUNT(*)
		FROM tickets`,
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

// Migrate applies every schema statement. It is safe to run on every start.
func Migrate(sqldb *sql.DB) error {
	for _, stmt := range Statements {
		if _, err := sqldb.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	return nil
}
