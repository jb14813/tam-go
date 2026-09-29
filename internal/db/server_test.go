package db

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// columns lists the columns of a table in order.
func columns(t *testing.T, sqldb *sql.DB, table string) []string {
	t.Helper()
	rows, err := sqldb.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}

func TestMigrateServerAddsKeyActivity(t *testing.T) {
	sqldb, err := Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if cols := columns(t, sqldb, "auth_key_activity"); cols != nil {
		t.Fatalf("Migrate alone must not add auth_key_activity; the client shares that schema: %v", cols)
	}

	// Twice: the migration runs on every start.
	for i := 0; i < 2; i++ {
		if err := MigrateServer(sqldb); err != nil {
			t.Fatalf("MigrateServer run %d: %v", i+1, err)
		}
	}
	if cols := columns(t, sqldb, "auth_key_activity"); !reflect.DeepEqual(cols, []string{"auth_key", "last_seen", "last_update"}) {
		t.Fatalf("auth_key_activity after MigrateServer = %v", cols)
	}
	if cols := columns(t, sqldb, "auth_keys"); !reflect.DeepEqual(cols, []string{"auth_key", "description"}) {
		t.Fatalf("auth_keys after MigrateServer = %v, unexpected key columns", cols)
	}
}

func TestMigrateServerPreservesExistingKeys(t *testing.T) {
	sqldb := openWithSchema(t, earlyGoSchema)
	if _, err := sqldb.Exec(`INSERT INTO auth_keys VALUES ('K', 'client')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatalf("MigrateServer over an earlier Go schema: %v", err)
	}
	var desc string
	if err := sqldb.QueryRow(`SELECT description FROM auth_keys WHERE auth_key = 'K'`).Scan(&desc); err != nil || desc != "client" {
		t.Fatalf("existing key after migration = %q, %v", desc, err)
	}
	var activity int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM auth_key_activity`).Scan(&activity); err != nil || activity != 0 {
		t.Fatalf("a key never seen by tam-server has %d activity rows (%v), want none", activity, err)
	}
}

// earlierServerDatabase is a database an earlier tam-server migrated: it
// added last_seen and last_update to auth_keys itself. Key K was seen and
// wrote; key L was not.
func earlierServerDatabase(t *testing.T) *sql.DB {
	t.Helper()
	sqldb := openWithSchema(t, earlyGoSchema)
	for _, stmt := range []string{
		`ALTER TABLE auth_keys ADD COLUMN last_seen TEXT`,
		`ALTER TABLE auth_keys ADD COLUMN last_update TEXT`,
		`INSERT INTO auth_keys VALUES ('K', 'front desk', '2026-09-25T10:00:00Z', '2026-09-25T09:00:00Z')`,
		`INSERT INTO auth_keys (auth_key, description) VALUES ('L', 'spare')`,
	} {
		if _, err := sqldb.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return sqldb
}

// olderServerDatabase is a database a tam-server from before last_update
// migrated: it added last_seen alone to auth_keys.
func olderServerDatabase(t *testing.T) *sql.DB {
	t.Helper()
	sqldb := openWithSchema(t, earlyGoSchema)
	for _, stmt := range []string{
		`ALTER TABLE auth_keys ADD COLUMN last_seen TEXT`,
		`INSERT INTO auth_keys VALUES ('K', 'front desk', '2026-09-25T10:00:00Z')`,
		`INSERT INTO auth_keys (auth_key, description) VALUES ('L', 'spare')`,
	} {
		if _, err := sqldb.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return sqldb
}

// keyTimes lists every key with its last_seen and last_update.
func keyTimes(t *testing.T, sqldb *sql.DB) [][4]string {
	t.Helper()
	rows, err := sqldb.Query(`SELECT k.auth_key, k.description, a.last_seen, a.last_update FROM auth_keys k
		LEFT JOIN auth_key_activity a ON a.auth_key = k.auth_key ORDER BY k.auth_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][4]string
	for rows.Next() {
		var key, desc string
		var seen, update sql.NullString
		if err := rows.Scan(&key, &desc, &seen, &update); err != nil {
			t.Fatal(err)
		}
		got = append(got, [4]string{key, desc, seen.String, update.String})
	}
	return got
}

// TestMigrateServerMovesTheTimesOutOfAuthKeys: over a database from an
// earlier tam-server, or one from before last_update, the times move to
// auth_key_activity, retaining their recorded values.
func TestMigrateServerMovesTheTimesOutOfAuthKeys(t *testing.T) {
	for name, c := range map[string]struct {
		open func(*testing.T) *sql.DB
		want [][4]string
	}{
		"earlier": {earlierServerDatabase, [][4]string{{"K", "front desk", "2026-09-25T10:00:00Z", "2026-09-25T09:00:00Z"}, {"L", "spare", "", ""}}},
		"older":   {olderServerDatabase, [][4]string{{"K", "front desk", "2026-09-25T10:00:00Z", ""}, {"L", "spare", "", ""}}},
	} {
		t.Run(name, func(t *testing.T) {
			sqldb := c.open(t)
			for i := 0; i < 2; i++ {
				if err := Migrate(sqldb); err != nil {
					t.Fatal(err)
				}
				if err := MigrateServer(sqldb); err != nil {
					t.Fatalf("MigrateServer run %d over an %s tam-server's database: %v", i+1, name, err)
				}
			}
			if cols := columns(t, sqldb, "auth_keys"); !reflect.DeepEqual(cols, []string{"auth_key", "description"}) {
				t.Fatalf("auth_keys = %v, unexpected key columns", cols)
			}
			if cols := columns(t, sqldb, "auth_key_activity"); !reflect.DeepEqual(cols, []string{"auth_key", "last_seen", "last_update"}) {
				t.Fatalf("auth_key_activity = %v", cols)
			}
			if got := keyTimes(t, sqldb); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("keys after the move = %v, want %v", got, c.want)
			}
		})
	}
}

// TestMigrateServerCompletesTheActivityTable: a database whose activity
// table has last_seen only (from a tam-server without last_update) gains
// last_update, and a time already there stays when it is the later one.
func TestMigrateServerCompletesTheActivityTable(t *testing.T) {
	sqldb := olderServerDatabase(t)
	for _, stmt := range []string{
		`CREATE TABLE auth_key_activity (auth_key TEXT PRIMARY KEY, last_seen TEXT)`,
		`INSERT INTO auth_key_activity VALUES ('K', '2026-09-26T08:00:00Z'), ('L', '2026-09-24T08:00:00Z')`,
		`UPDATE auth_keys SET last_seen = '2026-09-25T11:00:00Z' WHERE auth_key = 'L'`,
	} {
		if _, err := sqldb.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatal(err)
	}
	if cols := columns(t, sqldb, "auth_keys"); !reflect.DeepEqual(cols, []string{"auth_key", "description"}) {
		t.Fatalf("auth_keys = %v, unexpected key columns", cols)
	}
	if cols := columns(t, sqldb, "auth_key_activity"); !reflect.DeepEqual(cols, []string{"auth_key", "last_seen", "last_update"}) {
		t.Fatalf("auth_key_activity = %v", cols)
	}
	want := [][4]string{{"K", "front desk", "2026-09-26T08:00:00Z", ""}, {"L", "spare", "2026-09-25T11:00:00Z", ""}}
	if got := keyTimes(t, sqldb); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want the later time of each: %v", got, want)
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

func TestMigrateServerLeavesUnrecognizedKeyColumnsUntouched(t *testing.T) {
	sqldb := openWithSchema(t, earlyGoSchema)
	for _, stmt := range []string{
		`ALTER TABLE auth_keys ADD COLUMN operator_note TEXT`,
		`INSERT INTO auth_keys(auth_key,description,operator_note) VALUES('K','desk','keep here')`,
	} {
		if _, err := sqldb.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateServer(sqldb); err != nil {
		t.Fatal(err)
	}
	var note string
	if err := sqldb.QueryRow(`SELECT operator_note FROM auth_keys WHERE auth_key='K'`).Scan(&note); err != nil || note != "keep here" {
		t.Fatalf("unrecognized data moved or changed: %q, %v", note, err)
	}
}
