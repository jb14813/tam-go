package db

import (
	"path/filepath"
	"testing"
)

func TestMigrateServerAddsLastSeenAndLastUpdate(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"last_seen", "last_update"} {
		if has, _ := hasColumn(sqldb, "auth_keys", column); has {
			t.Fatalf("Migrate alone must not add %s; the client shares that schema", column)
		}
	}

	// Twice: the migration runs on every start.
	for i := 0; i < 2; i++ {
		if err := MigrateServer(sqldb); err != nil {
			t.Fatalf("MigrateServer run %d: %v", i+1, err)
		}
	}
	for _, column := range []string{"last_seen", "last_update"} {
		has, err := hasColumn(sqldb, "auth_keys", column)
		if err != nil || !has {
			t.Fatalf("%s after MigrateServer: has=%v err=%v", column, has, err)
		}
	}
	if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description, last_seen, last_update) VALUES ('K', 'client', '2026-09-25T10:00:00Z', '2026-09-25T10:01:00Z')`); err != nil {
		t.Fatalf("insert with last_seen and last_update: %v", err)
	}
	if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description) VALUES ('L', 'old style')`); err != nil {
		t.Fatalf("insert without the columns must keep working: %v", err)
	}
}

// TestMigrateServerAddsLastUpdateToAnEarlierGoDatabase: a database the
// previous tam-server migrated has last_seen but not last_update; the
// missing column is added and the present one left alone.
func TestMigrateServerAddsLastUpdateToAnEarlierGoDatabase(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.Exec(`ALTER TABLE auth_keys ADD COLUMN last_seen TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description, last_seen) VALUES ('K', 'client', '2026-09-25T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatalf("MigrateServer over a database with last_seen only: %v", err)
	}
	var seen, update *string
	if err := sqldb.QueryRow(`SELECT last_seen, last_update FROM auth_keys WHERE auth_key = 'K'`).Scan(&seen, &update); err != nil {
		t.Fatalf("existing key after migration: %v", err)
	}
	if seen == nil || *seen != "2026-09-25T10:00:00Z" || update != nil {
		t.Fatalf("existing key: last_seen=%v last_update=%v, want the old value and NULL", seen, update)
	}
}

func TestMigrateServerOverDatabaseFromTheOriginalServer(t *testing.T) {
	sqldb := openWithSchema(t, originalServerSchema)
	if _, err := sqldb.Exec(`INSERT INTO auth_keys VALUES ('K', 'client')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatalf("MigrateServer over the original server's schema: %v", err)
	}
	var desc string
	var seen, update *string
	if err := sqldb.QueryRow(`SELECT description, last_seen, last_update FROM auth_keys WHERE auth_key = 'K'`).Scan(&desc, &seen, &update); err != nil {
		t.Fatalf("existing key after migration: %v", err)
	}
	if desc != "client" || seen != nil || update != nil {
		t.Fatalf("existing key = %q last_seen=%v last_update=%v, want client and NULLs", desc, seen, update)
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
