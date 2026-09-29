package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func pairForBackup(t *testing.T, f *fixture) []byte {
	t.Helper()
	rs := httptest.NewServer(server.NewHandler(newServerStore(t), server.FixedPassword("secret")))
	t.Cleanup(rs.Close)
	u, _ := url.Parse(rs.URL)
	code, body := f.do("POST", "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "password": "secret"}, nil)
	if code != 200 {
		t.Fatalf("pair: %d %s", code, body)
	}
	return body
}

func pairingBackup(t *testing.T, f *fixture) store.RecoverySnapshot {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(f.settings), "before-pairing-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("pairing backup files: %v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var backup store.RecoverySnapshot
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatal(err)
	}
	return backup
}

func TestPairingBackupPreservesOwnedBasketComponents(t *testing.T) {
	for _, drawingOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "descriptions", true: "drawing"}[drawingOnly], func(t *testing.T) {
			f := newFixture(t)
			path := "/api/baskets"
			basket := store.Basket{Prefix: "A", BID: 1, Description: "Gift"}
			if drawingOnly {
				path = "/api/drawing"
				basket.Description, basket.WinningTicket = "", 42
			}
			if code, body := f.do("POST", path, []store.Basket{basket}, nil); code != 200 {
				t.Fatalf("save: %d %s", code, body)
			}
			before, err := f.st.ExportClientBackup()
			if err != nil {
				t.Fatal(err)
			}
			pairForBackup(t, f)
			backup := pairingBackup(t, f)
			if len(backup.BasketComponents) != 1 || backup.BasketComponents[0].Drawing != drawingOnly || backup.BasketComponents[0].Metadata == drawingOnly {
				t.Errorf("pairing backup invented ownership: %+v", backup.BasketComponents)
			}
			if len(backup.Revisions) == 0 || !reflect.DeepEqual(before.Revisions, backup.Revisions) {
				t.Error("pairing backup lost the saved component's correction history")
			}
			destination := newFixture(t)
			if err := destination.st.Import(store.BackupFile{Baskets: []store.Basket{{Prefix: "A", BID: 1, Description: "Other client's gift", WinningTicket: 77}}}); err != nil {
				t.Fatal(err)
			}
			if code, body := destination.do("POST", "/api/backuprestore/local", backup, nil); code != 200 {
				t.Fatalf("restore: %d %s", code, body)
			}
			got, err := destination.st.Basket("A", 1)
			if err != nil || got == nil {
				t.Fatalf("restored basket: %+v, %v", got, err)
			}
			wantDescription, wantWinner := "Gift", 77
			if drawingOnly {
				wantDescription, wantWinner = "Other client's gift", 42
			}
			if got.Description != wantDescription || got.WinningTicket != wantWinner {
				t.Fatalf("pairing backup overwrote another client's component: %+v", got)
			}
		})
	}
}

func TestPairingBackupKeepsDeletedPrefixesWithoutLiveRows(t *testing.T) {
	f := newFixture(t)
	if _, err := f.st.DeletePrefix("A"); err != nil {
		t.Fatal(err)
	}
	pairForBackup(t, f)
	backup := pairingBackup(t, f)
	if len(backup.DeletedPrefixes) != 1 || backup.DeletedPrefixes[0] != "A" || len(backup.Revisions) != 1 {
		t.Fatalf("pairing backup lost the only retained entry, a deleted prefix: %+v", backup)
	}
}

func TestPairingBackupMaterializesInterruptedAcceptedSave(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.exec(`CREATE TRIGGER pair_fail_ticket BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'simulated disk write failure'); END`)
	if code, body := f.do("POST", "/api/tickets", oneTicket(8, "Accepted remotely"), nil); code != 500 {
		t.Fatalf("interrupted retention: %d %s", code, body)
	}
	serverHas(t, rst, 8, "Accepted remotely")
	f.exec(`DROP TRIGGER pair_fail_ticket`)
	// Re-pairing to a different name takes a backup even on the same host.
	if _, err := f.h.cfg.Update(func(s config.Settings) (config.Settings, error) { s.RemoteName = "Previous server"; return s, nil }); err != nil {
		t.Fatal(err)
	}
	pairForBackup(t, f)
	backup := pairingBackup(t, f)
	if len(backup.Tickets) != 1 || backup.Tickets[0].FirstName != "Accepted remotely" || len(backup.Revisions) == 0 {
		t.Fatalf("pairing backup omitted a durable interrupted save: %+v", backup)
	}
	if waiting, failed := pending(t, f.st); waiting != 1 || failed != 0 {
		t.Fatalf("pairing changed the pending request: waiting=%d failed=%d", waiting, failed)
	}
}

func TestPairingBackupWaitsForInFlightSave(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	key, err := rst.CreateKey("original")
	if err != nil {
		t.Fatal(err)
	}
	accepted, resume := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(resume) })
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	rs := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		if r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
			close(accepted)
			<-resume
		}
	}))
	u, _ := url.Parse(rs.URL)
	if code, body := f.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": key.AuthKey, "remote_name": "Original"}, nil); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	saved := make(chan int, 1)
	go func() {
		code, _ := f.do("POST", "/api/tickets", oneTicket(9, "Saved before pairing"), nil)
		saved <- code
	}()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not reach the server")
	}
	authenticated := make(chan struct{})
	next := server.NewHandler(newServerStore(t), server.FixedPassword("secret"))
	replacement := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth" {
			close(authenticated)
		}
	}))
	replacementURL, _ := url.Parse(replacement.URL)
	paired := make(chan int, 1)
	go func() {
		code, _ := f.do("POST", "/api/pair", map[string]any{"host": replacementURL.Hostname(), "port": replacementURL.Port(), "password": "secret"}, nil)
		paired <- code
	}()
	select {
	case <-authenticated:
	case <-time.After(3 * time.Second):
		t.Fatal("pairing did not authenticate")
	}
	select {
	case code := <-paired:
		t.Fatalf("pairing completed before its preceding save: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	release.Do(func() { close(resume) })
	for label, result := range map[string]<-chan int{"save": saved, "pair": paired} {
		select {
		case code := <-result:
			if code != 200 {
				t.Fatalf("%s: %d", label, code)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s did not finish", label)
		}
	}
	backup := pairingBackup(t, f)
	if len(backup.Tickets) != 1 || backup.Tickets[0].FirstName != "Saved before pairing" {
		t.Fatalf("backup missed the save immediately before pairing: %+v", backup.Tickets)
	}
}

func TestPairingBackupPreservesUnresolvedAlternatives(t *testing.T) {
	f := newFixture(t)
	conflict := store.RecordConflict{Kind: "ticket", Prefix: "A", ID: 1}
	for _, name := range []string{"First copy", "Conflicting copy"} {
		other := newFixture(t)
		if err := other.st.UpsertTickets(oneTicket(1, name)); err != nil {
			t.Fatal(err)
		}
		snapshot, err := other.st.ExportClientBackup()
		if err != nil {
			t.Fatal(err)
		}
		value, err := json.Marshal(snapshot.Tickets[0])
		if err != nil {
			t.Fatal(err)
		}
		conflict.Candidates = append(conflict.Candidates, store.RecordCandidate{Revision: snapshot.Revisions[0], Value: value})
	}
	if err := f.st.ApplyReceipt(store.SaveReceipt{Conflicts: []store.RecordConflict{conflict}}); err != nil {
		t.Fatal(err)
	}
	pairForBackup(t, f)
	backup := pairingBackup(t, f)
	if len(backup.Conflicts) != 1 || len(backup.Conflicts[0].Candidates) != 2 {
		t.Fatalf("pairing backup discarded unresolved alternatives: %+v", backup.Conflicts)
	}
	destination := newFixture(t)
	if err := destination.st.ImportClientBackup(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.st.Ticket("A", 1); err == nil {
		t.Fatal("restoring the pairing backup silently selected a conflicting value")
	}
	conflicts, err := destination.st.Conflicts()
	if err != nil || len(conflicts) != 1 || len(conflicts[0].Candidates) != 2 {
		t.Fatalf("restored alternatives: %+v, %v", conflicts, err)
	}
}

func TestPairingBackupDoesNotMaterializeRejectedIntent(t *testing.T) {
	f := newFixture(t)
	if err := f.st.UpsertTickets(oneTicket(1, "Retained entry")); err != nil {
		t.Fatal(err)
	}
	order, err := f.st.NextSave("rejected-client")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(oneTicket(2, "Rejected entry"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.st.SaveIntent(http.MethodPost, "/api/tickets", data, order)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`CREATE TRIGGER pair_fail_cleanup BEFORE DELETE ON outbox BEGIN SELECT RAISE(ABORT, 'simulated cleanup failure'); END`)
	if err := f.st.RejectIntent(id, "server refused"); err == nil {
		t.Fatal("rejection cleanup unexpectedly succeeded")
	}
	f.exec(`DROP TRIGGER pair_fail_cleanup`)
	pairForBackup(t, f)
	backup := pairingBackup(t, f)
	if len(backup.Tickets) != 1 || backup.Tickets[0].TID != 1 {
		t.Fatalf("pairing backup treated a rejected save as retained data: %+v", backup.Tickets)
	}
	if waiting, failed := pending(t, f.st); waiting != 0 || failed != 1 {
		t.Fatalf("pairing changed the rejected request: waiting=%d failed=%d", waiting, failed)
	}
}

func TestPairingBackupReportsRetentionFailureAndKeepsJournal(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.exec(`CREATE TRIGGER pair_fail_ticket BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'simulated disk write failure'); END`)
	if code, body := f.do("POST", "/api/tickets", oneTicket(8, "Accepted remotely"), nil); code != 500 {
		t.Fatalf("interrupted retention: %d %s", code, body)
	}
	serverHas(t, rst, 8, "Accepted remotely")
	body := pairForBackup(t, f)
	if !strings.Contains(string(body), "could not be backed up") {
		t.Fatalf("pairing hid its backup failure: %s", body)
	}
	if waiting, failed := pending(t, f.st); waiting != 1 || failed != 0 {
		t.Fatalf("pairing lost the interrupted save: waiting=%d failed=%d", waiting, failed)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(f.settings), "before-pairing-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("pairing left a backup that omits its pending save: %v, %v", files, err)
	}
}
