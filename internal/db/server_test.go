package db

import (
	"path/filepath"
	"testing"
)

func TestMigrateServerAddsLastSeen(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if has, _ := hasColumn(sqldb, "auth_keys", "last_seen"); has {
		t.Fatal("Migrate alone must not add last_seen; the client shares that schema")
	}

	// Twice: the migration runs on every start.
	for i := 0; i < 2; i++ {
		if err := MigrateServer(sqldb); err != nil {
			t.Fatalf("MigrateServer run %d: %v", i+1, err)
		}
	}
	has, err := hasColumn(sqldb, "auth_keys", "last_seen")
	if err != nil || !has {
		t.Fatalf("last_seen after MigrateServer: has=%v err=%v", has, err)
	}
	if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description, last_seen) VALUES ('K', 'laptop', '2026-09-25T10:00:00Z')`); err != nil {
		t.Fatalf("insert with last_seen: %v", err)
	}
	if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description) VALUES ('L', 'old style')`); err != nil {
		t.Fatalf("insert without last_seen must keep working: %v", err)
	}
}

func TestMigrateServerOverDatabaseFromTheOriginalServer(t *testing.T) {
	sqldb := openWithSchema(t, originalServerSchema)
	if _, err := sqldb.Exec(`INSERT INTO auth_keys VALUES ('K', 'laptop')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatalf("MigrateServer over the original server's schema: %v", err)
	}
	var desc string
	var seen *string
	if err := sqldb.QueryRow(`SELECT description, last_seen FROM auth_keys WHERE auth_key = 'K'`).Scan(&desc, &seen); err != nil {
		t.Fatalf("existing key after migration: %v", err)
	}
	if desc != "laptop" || seen != nil {
		t.Fatalf("existing key = %q last_seen=%v, want laptop and NULL", desc, seen)
	}
}

func TestHasColumnUnknownTable(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if has, err := hasColumn(sqldb, "nothing_here", "x"); err != nil || has {
		t.Fatalf("hasColumn on a missing table = %v, %v; want false, nil", has, err)
	}
}
