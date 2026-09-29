package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
	tamsync "ticket-auction-manager/tam-go/internal/sync"
)

// Regressions for the save and recovery integrity audit.
func TestAuditPushPreservesBasketComponentsOwnedByOtherClients(t *testing.T) {
	for _, localPart := range []string{"metadata", "drawing"} {
		t.Run(localPart, func(t *testing.T) {
			f := newFixture(t)
			rst, _ := remoteFixture(t, f)
			initial := []store.Basket{{Prefix: "A", BID: 1, Description: "Original prize", Donors: "Sponsor", WinningTicket: 77}}
			if err := rst.Import(store.BackupFile{Baskets: initial}); err != nil {
				t.Fatal(err)
			}
			path := "/api/baskets"
			body := []store.Basket{{Prefix: "A", BID: 1, Description: "Corrected prize", Donors: "Sponsor"}}
			if localPart == "drawing" {
				path = "/api/drawing"
				body = []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 88}}
			}
			if code, b := f.do("POST", path, body, nil); code != 200 {
				t.Fatalf("save: %d %s", code, b)
			}
			before, err := rst.Basket("A", 1)
			if err != nil {
				t.Fatal(err)
			}
			if code, b := f.do("POST", "/api/backuprestore/push/baskets", `{}`, nil); code != 200 {
				t.Fatalf("push: %d %s", code, b)
			}
			after, err := rst.Basket("A", 1)
			if err != nil {
				t.Fatal(err)
			}
			if *after != *before {
				t.Fatalf("pushing this client's unchanged components erased another client's data: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestAuditRemoteAcceptedSaveRepairsMissingOwnerCopy(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	// Fail only row retention, after NextSave has durably allocated a number.
	f.exec(`CREATE TRIGGER audit_fail_ticket BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'simulated disk write failure'); END`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(8, "Accepted remotely"), nil); code != 500 {
		t.Fatalf("save: %d %s", code, b)
	}
	serverHas(t, rst, 8, "Accepted remotely")
	f.exec(`DROP TRIGGER audit_fail_ticket`)
	// Restart recovery/sync and ordinary reads have no way to retain this
	// accepted save because neither its row nor its body was logged locally.
	f.h.sync.Reset()
	f.h.sync.Tick()
	if code, b := f.do("GET", "/api/tickets/A/8", nil, nil); code != 200 {
		t.Fatalf("read: %d %s", code, b)
	}
	row, err := f.st.Ticket("A", 8)
	if err != nil {
		t.Fatal(err)
	}
	p, failed := pending(t, f.st)
	if row == nil && p == 0 && failed == 0 {
		t.Fatal("server accepted ticket, but owner has no row, pending save, or failed save even after reconnect and read; later empty-server recovery cannot restore it")
	}
}

func TestAuditRestoreWaitsForOlderQueuedSave(t *testing.T) {
	for _, target := range []string{"remote", "local", "push/tickets"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			rst, _ := remoteFixture(t, f)
			f.h.sync.NoteFailure(errors.New("temporary outage"))
			if code, b := f.do("POST", "/api/tickets", oneTicket(9, "Old queued edit"), nil); code != 200 {
				t.Fatalf("queue: %d %s", code, b)
			}
			bf := store.NewBackupFile()
			bf.Tickets = oneTicket(9, "Restored correction")
			payload := any(bf)
			if strings.HasPrefix(target, "push/") {
				payload = map[string]any{}
			}
			if code, b := f.do("POST", "/api/backuprestore/"+target, payload, nil); code != 409 || !strings.Contains(string(b), "queued saves") {
				t.Fatalf("restore while queue pending: %d %s", code, b)
			}
			if row, err := rst.Ticket("A", 9); err != nil || row != nil {
				t.Fatalf("refused restore changed server: %+v %v", row, err)
			}
			if row, err := f.st.Ticket("A", 9); err != nil || row == nil || row.FirstName != "Old queued edit" {
				t.Fatalf("refused restore changed local: %+v %v", row, err)
			}
			f.h.sync.Tick()
			serverHas(t, rst, 9, "Old queued edit")
			if code, b := f.do("POST", "/api/backuprestore/"+target, payload, nil); code != 200 {
				t.Fatalf("restore after queue drained: %d %s", code, b)
			}
		})
	}
}

func TestAuditInterruptedIntentCannotOverwriteNewerLocalSave(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.exec(`CREATE TRIGGER audit_fail_ticket BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'simulated disk write failure'); END`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(8, "Old accepted save"), nil); code != 500 {
		t.Fatalf("save: %d %s", code, b)
	}
	f.exec(`DROP TRIGGER audit_fail_ticket`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(8, "New local save"), nil); code != 200 {
		t.Fatalf("newer save: %d %s", code, b)
	}
	if row, err := f.st.Ticket("A", 8); err != nil || row == nil || row.FirstName != "New local save" {
		t.Fatalf("latest local value: %+v %v", row, err)
	}
	f.h.sync.Tick()
	serverHas(t, rst, 8, "New local save")
	if row, err := f.st.Ticket("A", 8); err != nil || row == nil || row.FirstName != "New local save" {
		t.Fatalf("replay overwrote newer local save: %+v %v", row, err)
	}
}

func TestAuditIntentMustBeDurableBeforeServerWrite(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.exec(`CREATE TRIGGER audit_fail_intent BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT, 'simulated journal failure'); END`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(8, "Unjournaled"), nil); code != 500 {
		t.Fatalf("save: %d %s", code, b)
	}
	if row, err := rst.Ticket("A", 8); err != nil || row != nil {
		t.Fatalf("server accepted unjournaled save: %+v %v", row, err)
	}
}

func TestAuditRejectedIntentWithFailedCleanupNeverReplays(t *testing.T) {
	f := newFixture(t)
	var requests atomic.Int32
	rs := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tickets" && r.Method == http.MethodPost {
			requests.Add(1)
			httpx.WriteError(w, http.StatusUnprocessableEntity, "Rejected by server")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"authenticated": true})
	}))
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), "K"
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	f.exec(`CREATE TRIGGER audit_fail_cleanup BEFORE DELETE ON outbox BEGIN SELECT RAISE(ABORT, 'simulated cleanup failure'); END`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(8, "Rejected"), nil); code != 500 {
		t.Fatalf("cleanup failure: %d %s", code, b)
	}
	if p, failed := pending(t, f.st); p != 0 || failed != 1 {
		t.Fatalf("rejection must remain observable: pending=%d failed=%d", p, failed)
	}
	f.exec(`DROP TRIGGER audit_fail_cleanup`)
	f.h.sync.Tick()
	if requests.Load() != 1 {
		t.Fatalf("rejected request replayed %d times", requests.Load())
	}
	if row, err := f.st.Ticket("A", 8); err != nil || row != nil {
		t.Fatalf("rejected intent mutated local row: %+v %v", row, err)
	}
	if code, b := f.do("POST", "/api/outbox/discard", `{}`, nil); code != 200 {
		t.Fatalf("discard: %d %s", code, b)
	}
	if p, failed := pending(t, f.st); p != 0 || failed != 0 {
		t.Fatalf("discard left pending=%d failed=%d", p, failed)
	}
}

func TestAuditRestartRetainsInterruptedOnlineIntent(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "before send", true: "after server commit"}[accepted], func(t *testing.T) {
			f := newFixture(t)
			rst, _ := remoteFixture(t, f)
			body, err := json.Marshal(oneTicket(12, "Survives restart"))
			if err != nil {
				t.Fatal(err)
			}
			order, err := f.st.NextSave(f.h.host)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.st.SaveIntent(http.MethodPost, "/api/tickets", body, order); err != nil {
				t.Fatal(err)
			}
			if accepted {
				res, err := f.h.remote(f.h.settings()).Do(http.MethodPost, "/api/tickets", tamsync.OrderHeaders(order), json.RawMessage(body))
				if err != nil || !res.OK() {
					t.Fatalf("remote acceptance: %v %v", res, err)
				}
			}
			// A fresh handler/store connection has no in-memory knowledge of
			// the unfinished request; only its journal row survives.
			restarted := newHandler(openStoreAt(t, f.dbPath), f.settings, testDist, WithTimings(testTimings))
			restarted.sync.Tick()
			serverHas(t, rst, 12, "Survives restart")
			if row, err := f.st.Ticket("A", 12); err != nil || row == nil || row.FirstName != "Survives restart" {
				t.Fatalf("owner after restart: %+v %v", row, err)
			}
			if p, failed := pending(t, f.st); p != 0 || failed != 0 {
				t.Fatalf("restart left pending=%d failed=%d", p, failed)
			}
		})
	}
}

func TestAuditAcceptedDeleteSurvivesLocalWriteFailure(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	if code, b := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatalf("prefix: %d %s", code, b)
	}
	f.exec(`CREATE TRIGGER audit_fail_delete BEFORE DELETE ON prefixes BEGIN SELECT RAISE(ABORT, 'simulated delete failure'); END`)
	if code, b := f.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 500 {
		t.Fatalf("delete: %d %s", code, b)
	}
	if rows, err := rst.ListPrefixes(); err != nil || len(rows) != 0 {
		t.Fatalf("server delete missing: %+v %v", rows, err)
	}
	f.exec(`DROP TRIGGER audit_fail_delete`)
	f.h.sync.Tick()
	if rows, err := f.st.ListPrefixes(); err != nil || len(rows) != 0 {
		t.Fatalf("local delete missing after replay: %+v %v", rows, err)
	}
	if p, failed := pending(t, f.st); p != 0 || failed != 0 {
		t.Fatalf("delete left pending=%d failed=%d", p, failed)
	}
}
