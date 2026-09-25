package db

import (
	"path/filepath"
	"testing"
)

func TestMigrateCreatesSchema(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	// Twice: the schema must be idempotent because it runs on every start.
	for i := 0; i < 2; i++ {
		if err := Migrate(sqldb); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}

	rows, err := sqldb.Query(`SELECT name FROM sqlite_master WHERE type IN ('table', 'view')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		have[name] = true
	}
	for _, want := range []string{"prefixes", "tickets", "baskets", "auth_keys", "drawing", "report_by_name", "report_by_basket", "report_counts"} {
		if !have[want] {
			t.Errorf("schema is missing %q", want)
		}
	}

	var mode string
	if err := sqldb.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestOpenFailsOnDirectory(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("opening a directory as a database should fail")
	}
}
