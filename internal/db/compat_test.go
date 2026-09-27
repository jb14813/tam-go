package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// originalClientSchema is what the original SvelteKit client created through
// drizzle (client/drizzle/0000_init.sql), and originalServerSchema is what the
// FastAPI server created (api/app/db.py). A data directory produced by either
// must keep working when the Go daemons open it.
var originalClientSchema = []string{
	"CREATE TABLE IF NOT EXISTS `baskets` (`prefix` text, `b_id` integer, `description` text, `donors` text, `winning_ticket` integer, PRIMARY KEY(`prefix`, `b_id`))",
	"CREATE TABLE IF NOT EXISTS `prefixes` (`prefix` text PRIMARY KEY NOT NULL, `color` text, `weight` integer)",
	"CREATE TABLE IF NOT EXISTS `tickets` (`prefix` text, `t_id` integer, `first_name` text, `last_name` text, `phone_number` text, `pref` text, PRIMARY KEY(`prefix`, `t_id`))",
	"CREATE VIEW IF NOT EXISTS `drawing` AS SELECT b.prefix, b.b_id, b.description, b.winning_ticket, t.last_name, t.first_name, t.phone_number FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id ORDER BY b.prefix, b.b_id",
	"CREATE VIEW IF NOT EXISTS `report_by_basket` AS SELECT b.prefix, b.b_id, b.description, b.donors, b.winning_ticket, t.last_name, t.first_name, t.phone_number, t.pref FROM baskets b LEFT JOIN tickets t on b.prefix = t.prefix AND b.winning_ticket = t.t_id ORDER BY b.prefix, b.b_id",
	"CREATE VIEW IF NOT EXISTS `report_by_name` AS SELECT t.last_name, t.first_name, t.phone_number, t.pref, b.prefix, b.b_id, b.description, b.donors, b.winning_ticket FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id ORDER BY t.last_name, t.first_name, t.phone_number, b.prefix, b.b_id",
	"CREATE VIEW IF NOT EXISTS `report_counts` AS SELECT prefix, COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))) AS unique_buyers, COUNT(*) AS total_buys FROM tickets GROUP BY prefix UNION ALL SELECT 'Total', COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))), COUNT(*) FROM tickets",
}

var originalServerSchema = []string{
	"CREATE TABLE IF NOT EXISTS auth_keys (auth_key TEXT PRIMARY KEY, description TEXT)",
	"CREATE TABLE IF NOT EXISTS prefixes (prefix TEXT PRIMARY KEY, color TEXT, weight INTEGER)",
	"CREATE TABLE IF NOT EXISTS tickets (prefix TEXT, t_id INTEGER, first_name TEXT, last_name TEXT, phone_number TEXT, pref TEXT, PRIMARY KEY (prefix, t_id))",
	"CREATE TABLE IF NOT EXISTS baskets (prefix TEXT, b_id INTEGER, description TEXT, donors TEXT, winning_ticket INTEGER, PRIMARY KEY (prefix, b_id))",
	"CREATE VIEW IF NOT EXISTS drawing AS SELECT b.prefix, b.b_id, b.description, b.winning_ticket, t.last_name, t.first_name, t.phone_number FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id ORDER BY b.prefix, b.b_id",
	"CREATE VIEW IF NOT EXISTS report_by_name AS SELECT t.last_name, t.first_name, t.phone_number, t.pref, b.* FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id ORDER BY t.last_name, t.first_name, t.phone_number, b.prefix, b.b_id",
	"CREATE VIEW IF NOT EXISTS report_by_basket AS SELECT b.*, t.last_name, t.first_name, t.phone_number, t.pref FROM baskets b LEFT JOIN tickets t ON b.prefix = t.prefix AND b.winning_ticket = t.t_id ORDER BY b.prefix, b.b_id",
	"CREATE VIEW IF NOT EXISTS report_counts AS SELECT prefix, COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))) AS unique_buyers, COUNT(*) AS total_buys FROM tickets GROUP BY prefix UNION ALL SELECT 'Totals', COUNT(DISTINCT(CONCAT(first_name, last_name, phone_number))), COUNT(*) FROM tickets",
}

func openWithSchema(t *testing.T, schema []string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "existing.db")
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range schema {
		if _, err := plain.Exec(stmt); err != nil {
			t.Fatalf("original schema: %v", err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO prefixes VALUES ('CALL', 'green', 1)",
		"INSERT INTO tickets VALUES ('CALL', 7, 'Old', 'Data', '555', 'TEXT')",
		"INSERT INTO baskets VALUES ('CALL', 1, 'Wine', 'Smiths', 7)",
	} {
		if _, err := plain.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	plain.Close()

	sqldb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	return sqldb
}

func TestMigrateOverDatabaseFromTheOriginalApp(t *testing.T) {
	for name, schema := range map[string][]string{"client": originalClientSchema, "server": originalServerSchema} {
		t.Run(name, func(t *testing.T) {
			sqldb := openWithSchema(t, schema)
			if err := Migrate(sqldb); err != nil {
				t.Fatalf("Migrate over a %s database from the original app: %v", name, err)
			}
			// Each program's own additions must apply over the file as well,
			// which is what happens when TAM_DATA_DIR points at the old folder.
			more := map[string]func(*sql.DB) error{"client": MigrateClient, "server": MigrateServer}[name]
			if err := more(sqldb); err != nil {
				t.Fatalf("program-specific migration over a %s database from the original app: %v", name, err)
			}
			if err := more(sqldb); err != nil {
				t.Fatalf("the migration must be safe to run again: %v", err)
			}
			var winner string
			err := sqldb.QueryRow(`SELECT last_name FROM drawing WHERE prefix = 'CALL' AND b_id = 1`).Scan(&winner)
			if err != nil || winner != "Data" {
				t.Fatalf("existing rows not readable through the views: %q %v", winner, err)
			}
			var keys int
			if err := sqldb.QueryRow(`SELECT COUNT(*) FROM auth_keys`).Scan(&keys); err != nil {
				t.Fatalf("auth_keys must exist after migration: %v", err)
			}
			// The views are recreated, so the original server's "Totals" label
			// becomes "Total" everywhere.
			var total int
			if err := sqldb.QueryRow(`SELECT total_buys FROM report_counts WHERE prefix = 'Total'`).Scan(&total); err != nil || total != 1 {
				t.Fatalf("counts view over existing data: %d %v", total, err)
			}
		})
	}
}
