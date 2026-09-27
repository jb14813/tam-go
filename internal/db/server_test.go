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
	if cols := columns(t, sqldb, "auth_key_activity"); !reflect.DeepEqual(cols, []string{"auth_key", "last_seen"}) {
		t.Fatalf("auth_key_activity after MigrateServer = %v", cols)
	}
	if cols := columns(t, sqldb, "auth_keys"); !reflect.DeepEqual(cols, []string{"auth_key", "description"}) {
		t.Fatalf("auth_keys after MigrateServer = %v, want the original's two columns", cols)
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
	if err := sqldb.QueryRow(`SELECT description FROM auth_keys WHERE auth_key = 'K'`).Scan(&desc); err != nil || desc != "client" {
		t.Fatalf("existing key after migration = %q, %v", desc, err)
	}
	var activity int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM auth_key_activity`).Scan(&activity); err != nil || activity != 0 {
		t.Fatalf("a key never seen by tam-server has %d activity rows (%v), want none", activity, err)
	}
}

// earlierServerDatabase is a database an earlier tam-server migrated: it
// added last_seen to auth_keys itself, which the original server cannot
// read. Key K was seen, key L was not.
func earlierServerDatabase(t *testing.T) *sql.DB {
	t.Helper()
	sqldb := openWithSchema(t, originalServerSchema)
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

// TestMigrateServerMovesLastSeenOutOfAuthKeys: over a database from an
// earlier tam-server the last_seen times move to auth_key_activity and
// auth_keys gets back the original's two columns, so the original server
// can manage keys in it again.
func TestMigrateServerMovesLastSeenOutOfAuthKeys(t *testing.T) {
	sqldb := earlierServerDatabase(t)
	for i := 0; i < 2; i++ {
		if err := Migrate(sqldb); err != nil {
			t.Fatal(err)
		}
		if err := MigrateServer(sqldb); err != nil {
			t.Fatalf("MigrateServer run %d over an earlier tam-server's database: %v", i+1, err)
		}
	}
	if cols := columns(t, sqldb, "auth_keys"); !reflect.DeepEqual(cols, []string{"auth_key", "description"}) {
		t.Fatalf("auth_keys = %v, want the original's two columns", cols)
	}
	rows, err := sqldb.Query(`SELECT k.auth_key, k.description, a.last_seen FROM auth_keys k
		LEFT JOIN auth_key_activity a ON a.auth_key = k.auth_key ORDER BY k.auth_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][3]string
	for rows.Next() {
		var key, desc string
		var seen sql.NullString
		if err := rows.Scan(&key, &desc, &seen); err != nil {
			t.Fatal(err)
		}
		got = append(got, [3]string{key, desc, seen.String})
	}
	want := [][3]string{{"K", "front desk", "2026-09-25T10:00:00Z"}, {"L", "spare", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys after the move = %v, want %v", got, want)
	}
}

// TestMigrateServerMovesEveryAddedColumn: whatever column a tam-server
// build added to auth_keys (one also kept last_update there) moves out, so
// auth_keys ends with the original's two columns whichever build opened the
// folder before; a time already in auth_key_activity stays when it is the
// later one.
func TestMigrateServerMovesEveryAddedColumn(t *testing.T) {
	sqldb := earlierServerDatabase(t)
	for _, stmt := range []string{
		`ALTER TABLE auth_keys ADD COLUMN last_update TEXT`,
		`UPDATE auth_keys SET last_update = '2026-09-25T09:00:00Z' WHERE auth_key = 'K'`,
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
		t.Fatalf("auth_keys = %v, want the original's two columns", cols)
	}
	if cols := columns(t, sqldb, "auth_key_activity"); !reflect.DeepEqual(cols, []string{"auth_key", "last_seen", "last_update"}) {
		t.Fatalf("auth_key_activity = %v", cols)
	}
	var kSeen, kUpdate, lSeen sql.NullString
	if err := sqldb.QueryRow(`SELECT last_seen, last_update FROM auth_key_activity WHERE auth_key = 'K'`).Scan(&kSeen, &kUpdate); err != nil {
		t.Fatal(err)
	}
	if err := sqldb.QueryRow(`SELECT last_seen FROM auth_key_activity WHERE auth_key = 'L'`).Scan(&lSeen); err != nil {
		t.Fatal(err)
	}
	if kSeen.String != "2026-09-26T08:00:00Z" || kUpdate.String != "2026-09-25T09:00:00Z" || lSeen.String != "2026-09-25T11:00:00Z" {
		t.Fatalf("K seen %v updated %v, L seen %v; want the later time of each", kSeen, kUpdate, lSeen)
	}
}

// TestTheOriginalServerKeepsManagingKeys runs the original server's own
// statements on auth_keys (api/app/system/auth.py at 19eab77) over every
// database tam-server migrates. The original reads the table with SELECT *
// into a two-field model and inserts two values, so its GET, POST and
// DELETE /api/auth answered 500 once tam-server had added a column.
func TestTheOriginalServerKeepsManagingKeys(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *sql.DB{
		"new": func(t *testing.T) *sql.DB {
			sqldb, err := Open(filepath.Join(t.TempDir(), "new.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sqldb.Close() })
			return sqldb
		},
		"the original server's":   func(t *testing.T) *sql.DB { return openWithSchema(t, originalServerSchema) },
		"an earlier tam-server's": earlierServerDatabase,
	} {
		t.Run(name, func(t *testing.T) {
			sqldb := open(t)
			if err := Migrate(sqldb); err != nil {
				t.Fatal(err)
			}
			if err := MigrateServer(sqldb); err != nil {
				t.Fatal(err)
			}
			if _, err := sqldb.Exec(`INSERT INTO auth_keys (auth_key, description) VALUES ('G', 'go client')`); err != nil {
				t.Fatal(err)
			}

			twoFields := func(what string, rows *sql.Rows, err error) int {
				t.Helper()
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				defer rows.Close()
				cols, _ := rows.Columns()
				if len(cols) != 2 {
					t.Fatalf("%s gives %d columns %v; AuthKey(*r) takes 2", what, len(cols), cols)
				}
				n := 0
				for rows.Next() {
					n++
				}
				return n
			}
			rows, err := sqldb.Query("SELECT * FROM auth_keys ORDER BY description, auth_key") // get_all_keys
			if n := twoFields("GET /api/auth", rows, err); n == 0 {
				t.Fatal("GET /api/auth lists no key")
			}
			rows, err = sqldb.Query("SELECT * FROM auth_keys WHERE auth_key = ?", "NEWKEY") // create_key's check
			twoFields("POST /api/auth, looking for a clash", rows, err)
			rows, err = sqldb.Query("INSERT INTO auth_keys VALUES (?, ?) RETURNING *", "NEWKEY", "original client") // create_key
			if n := twoFields("POST /api/auth", rows, err); n != 1 {
				t.Fatalf("POST /api/auth returned %d rows", n)
			}
			rows, err = sqldb.Query("SELECT * FROM auth_keys WHERE auth_key = ?", "NEWKEY") // check_key, verify_key
			if n := twoFields("a data route's key check", rows, err); n != 1 {
				t.Fatal("the key the original created does not check out")
			}
			rows, err = sqldb.Query("DELETE FROM auth_keys WHERE auth_key = ? RETURNING *", "NEWKEY") // del_key
			if n := twoFields("DELETE /api/auth", rows, err); n != 1 {
				t.Fatalf("DELETE /api/auth returned %d rows", n)
			}

			// What tam-server records about a key does not get in the way:
			// the original deletes such a key as any other and leaves the
			// record, which the next start of tam-server drops.
			if _, err := sqldb.Exec(`INSERT INTO auth_key_activity (auth_key, last_seen) VALUES ('G', '2026-09-27T10:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			rows, err = sqldb.Query("DELETE FROM auth_keys WHERE auth_key = ? RETURNING *", "G")
			if n := twoFields("DELETE /api/auth of a key tam-server saw", rows, err); n != 1 {
				t.Fatalf("DELETE /api/auth returned %d rows", n)
			}
			if err := MigrateServer(sqldb); err != nil {
				t.Fatal(err)
			}
			var left int
			if err := sqldb.QueryRow(`SELECT COUNT(*) FROM auth_key_activity WHERE auth_key = 'G'`).Scan(&left); err != nil || left != 0 {
				t.Fatalf("the record of a key the original deleted is still there after a restart: %d (%v)", left, err)
			}
		})
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
