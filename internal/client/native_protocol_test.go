package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestMissingReceiptRetainsDirectAndQueuedSaves(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "queued"}[queued], func(t *testing.T) {
			f := newFixture(t)
			var strip atomic.Bool
			strip.Store(true)
			event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" && r.URL.Path == "/api/tickets" && strip.Load() {
						response := httptest.NewRecorder()
						inner.ServeHTTP(response, r)
						if response.Code != 200 {
							t.Errorf("real server refused fixture save: %d %s", response.Code, response.Body.String())
						}
						w.Write([]byte(`<html>Receipt removed by proxy</html>`))
						return
					}
					inner.ServeHTTP(w, r)
				})
			})
			event.configure(t, f)
			if queued {
				f.h.sync.NoteFailure(errors.New("offline"))
			}
			causalSave(t, f, "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Kept"}})
			if queued {
				f.h.sync.Tick()
			}
			if p, failed := pending(t, f.st); p != 1 || failed != 0 {
				t.Fatalf("missing receipt lost journal: pending=%d failed=%d", p, failed)
			}
			serverHas(t, event.st, 1, "Kept")
			strip.Store(false)
			f.h.sync.Reset()
			f.h.sync.Tick()
			if p, failed := pending(t, f.st); p != 0 || failed != 0 {
				t.Fatalf("valid repeat receipt did not finish delivery: pending=%d failed=%d", p, failed)
			}
		})
	}
}

// nativeFixtureBackup declares ownership for synthetic complete fixture rows.
// Rejection cases pass their raw three-list JSON directly to the HTTP boundary.
func nativeFixtureBackup(bf store.BackupFile) store.RecoverySnapshot {
	snapshot := store.RecoverySnapshot{BackupFile: bf, Format: store.NativeBackupFormat, BasketComponents: []store.BasketComponents{}}
	for _, row := range bf.Baskets {
		snapshot.BasketComponents = append(snapshot.BasketComponents, store.BasketComponents{Prefix: row.Prefix, BID: row.BID, Metadata: true, Drawing: true})
	}
	return snapshot
}

func TestDirectSaveChecksNativeProtocolBeforeFirstHeartbeat(t *testing.T) {
	f := newFixture(t)
	writes := 0
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writes++
		}
		w.Write([]byte("<html>Wrong proxy route</html>"))
	}))
	defer web.Close()
	u, _ := url.Parse(web.URL)
	causalSave(t, f, "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": "key"})
	causalSave(t, f, "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Kept"}})
	p, failed := pending(t, f.st)
	if p != 1 || failed != 0 || writes != 0 {
		t.Fatalf("unsupported server drained or received save: pending=%d failed=%d writes=%d", p, failed, writes)
	}
}
