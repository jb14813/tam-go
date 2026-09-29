package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Earlier Go versions stored event data before causal/ownership metadata.
var earlyGoSchema = Tables[:4]

func openWithSchema(t *testing.T, schema []string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "existing.db")
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range schema {
		if _, err := plain.Exec(stmt); err != nil {
			t.Fatalf("earlier Go schema: %v", err)
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
