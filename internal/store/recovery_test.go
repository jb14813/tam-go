package store

import (
	"errors"
	"path/filepath"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

func recoveryStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recovery.db")
	return reopenRecoveryStore(t, path), path
}

func reopenRecoveryStore(t *testing.T, path string) *Store {
	t.Helper()
	sqldb, err := db.Open(path)
	must(t, err)
	t.Cleanup(func() { sqldb.Close() })
	must(t, db.Migrate(sqldb))
	must(t, db.MigrateServer(sqldb))
	return New(sqldb)
}

func requestedKey(t *testing.T, s *Store) (string, string) {
	t.Helper()
	key, err := s.CreateKey("client")
	must(t, err)
	must(t, s.BeginRecovery())
	token, err := s.RecoveryToken(key.AuthKey, "")
	must(t, err)
	if token == "" {
		t.Fatal("missing request")
	}
	return key.AuthKey, token
}

func TestRecoveryPrefixDeletionSurvivesRestart(t *testing.T) {
	s, path := recoveryStore(t)
	key, token := requestedKey(t, s)
	other, otherToken := requestedKey(t, s)
	// A queued delete can arrive before any snapshot has restored its row.
	deleted, err := s.DeletePrefix("absent")
	must(t, err)
	if deleted != nil {
		t.Fatal("expected absent prefix")
	}
	data := BackupFile{
		Prefixes: []Prefix{{Prefix: "absent", Color: "blue"}, {Prefix: "present", Color: "red"}},
		Tickets:  []Ticket{{Prefix: "absent", TID: 1}},
		Baskets:  []Basket{{Prefix: "absent", BID: 1, WinningTicket: 1}},
	}
	must(t, s.Recover(key, "", token, data))
	deleted, err = s.DeletePrefix("present")
	must(t, err)
	if deleted == nil {
		t.Fatal("expected restored prefix")
	}
	must(t, s.db.Close())
	s = reopenRecoveryStore(t, path)
	must(t, s.BeginRecovery())
	gotToken, err := s.RecoveryToken(other, "")
	must(t, err)
	if gotToken != otherToken {
		t.Fatal("offline request did not survive reopening database")
	}
	must(t, s.Recover(other, "", otherToken, data))
	got, err := s.Export()
	must(t, err)
	if len(got.Prefixes) != 0 {
		t.Fatalf("late snapshot resurrected prefixes: %+v", got.Prefixes)
	}
	if len(got.Tickets) != 1 || len(got.Baskets) != 1 || got.Baskets[0].WinningTicket != 1 {
		t.Fatalf("prefix deletion lost event rows: %+v", got)
	}
	var tombstones int
	must(t, s.db.QueryRow(`SELECT count(*) FROM recovery_deleted_prefixes`).Scan(&tombstones))
	if tombstones != 2 {
		t.Fatal("late clients still need the deletion tombstones")
	}
}

func TestRecoveryAcknowledgedEmptyClientKeepsReceiptAcrossRestart(t *testing.T) {
	s, path := recoveryStore(t)
	key, token := requestedKey(t, s)
	must(t, s.Recover(key, "desk", token, NewBackupFile()))
	must(t, s.db.Close())
	s = reopenRecoveryStore(t, path)
	must(t, s.BeginRecovery())
	got, err := s.RecoveryToken(key, "desk")
	must(t, err)
	if got != "" {
		t.Fatal("empty restart forgot the sole client's receipt")
	}
	got, err = s.RecoveryToken(key, "offline-desk")
	must(t, err)
	if got != token {
		t.Fatal("empty restart forgot late shared-key client")
	}
}

func TestRecoveryIntentionalLastPrefixDeletionIsNotAnotherLoss(t *testing.T) {
	s, path := recoveryStore(t)
	key, token := requestedKey(t, s)
	data := BackupFile{Prefixes: []Prefix{{Prefix: "A", Color: "blue"}}}
	must(t, s.Recover(key, "desk", token, data))
	_, err := s.DeletePrefix("A")
	must(t, err)
	must(t, s.db.Close())
	s = reopenRecoveryStore(t, path)
	must(t, s.BeginRecovery())
	got, err := s.RecoveryToken(key, "offline-desk")
	must(t, err)
	if got != token {
		t.Fatal("intentional deletion rotated recovery and lost tombstones")
	}
	must(t, s.Recover(key, "offline-desk", token, data))
	ps, err := s.ListPrefixes()
	must(t, err)
	if len(ps) != 0 {
		t.Fatal("late snapshot resurrected intentionally deleted last prefix")
	}
}

func TestClientNameDoesNotConsumeSaveNumbers(t *testing.T) {
	s := newTestStore(t)
	must(t, db.MigrateClient(s.db))
	name, err := s.ClientName("host-one")
	must(t, err)
	again, err := s.ClientName("host-one")
	must(t, err)
	if name == "" || name != again {
		t.Fatal("identity is not stable")
	}
	first, err := s.NextSave("host-one")
	must(t, err)
	if first.Client != name || first.Save != 1 {
		t.Fatalf("identity consumed a number: %+v", first)
	}
	other, err := s.ClientName("host-two")
	must(t, err)
	if other == name || other == "" {
		t.Fatal("copied folder did not get a new identity")
	}
	second, err := s.NextSave("host-two")
	must(t, err)
	if second.Client != other || second.Save != 2 {
		t.Fatalf("host change consumed/reset numbering: %+v", second)
	}
}

func TestRecoveryExplicitPrefixSaveClearsDeletion(t *testing.T) {
	s, _ := recoveryStore(t)
	key, token := requestedKey(t, s)
	_, err := s.DeletePrefix("A")
	must(t, err)
	must(t, s.UpsertPrefixes([]Prefix{{Prefix: "A", Color: "green", Weight: 2}}))
	var tombstones int
	must(t, s.db.QueryRow(`SELECT count(*) FROM recovery_deleted_prefixes`).Scan(&tombstones))
	if tombstones != 0 {
		t.Fatal("intentional prefix save did not clear deletion")
	}
	must(t, s.Recover(key, "", token, BackupFile{Prefixes: []Prefix{{Prefix: "A", Color: "red"}}}))
	backup, err := s.Export()
	must(t, err)
	ps := backup.Prefixes
	if s.CheckConflicts() == nil {
		t.Fatal("different unversioned prefix must require review")
	}
	if len(ps) != 1 || ps[0].Color != "green" || ps[0].Weight != 2 {
		t.Fatalf("snapshot replaced intentional save: %+v", ps)
	}
}

func TestRecoveryEmptyAcknowledgementSurvivesRestart(t *testing.T) {
	s, path := recoveryStore(t)
	key, token := requestedKey(t, s)
	other, otherToken := requestedKey(t, s)
	must(t, s.Recover(key, "", token, NewBackupFile()))
	must(t, s.db.Close())
	s = reopenRecoveryStore(t, path)
	must(t, s.BeginRecovery())
	got, err := s.RecoveryToken(key, "")
	must(t, err)
	if got != "" {
		t.Fatal("empty restart forgot acknowledgement")
	}
	got, err = s.RecoveryToken(other, "")
	must(t, err)
	if got != otherToken {
		t.Fatal("empty restart replaced outstanding token")
	}
}

func TestRecoveryRollsBackRowsAndAcknowledgement(t *testing.T) {
	s, _ := recoveryStore(t)
	key, token := requestedKey(t, s)
	_, err := s.db.Exec(`CREATE TRIGGER reject_recovery BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'test disk failure'); END`)
	must(t, err)
	data := BackupFile{Prefixes: []Prefix{{Prefix: "A", Color: "blue"}}, Tickets: []Ticket{{Prefix: "A", TID: 1}}}
	if err := s.Recover(key, "", token, data); err == nil {
		t.Fatal("expected failed transaction")
	}
	ps, err := s.ListPrefixes()
	must(t, err)
	if len(ps) != 0 {
		t.Fatal("failed recovery left partial prefix rows")
	}
	got, err := s.RecoveryToken(key, "")
	must(t, err)
	if got != token {
		t.Fatal("failed recovery acknowledged the request")
	}
	_, err = s.db.Exec(`DROP TRIGGER reject_recovery`)
	must(t, err)
	must(t, s.Recover(key, "", token, data))
	must(t, s.Recover(key, "", token, data))
	if err := s.Recover(key, "", "stale-token", data); !errors.Is(err, ErrRecoveryToken) {
		t.Fatalf("stale recovery: %v", err)
	}
}

func TestRecoveryNonemptyEventDoesNotRequestSnapshots(t *testing.T) {
	for _, data := range []BackupFile{
		{Prefixes: []Prefix{{Prefix: "A", Color: "white"}}},
		{Tickets: []Ticket{{Prefix: "A", TID: 1}}},
		{Baskets: []Basket{{Prefix: "A", BID: 1}}},
	} {
		s, _ := recoveryStore(t)
		key, err := s.CreateKey("client")
		must(t, err)
		must(t, s.Import(data))
		must(t, s.BeginRecovery())
		token, err := s.RecoveryToken(key.AuthKey, "")
		must(t, err)
		if token != "" {
			t.Fatalf("nonempty event requested snapshots: %+v", data)
		}
	}
}
