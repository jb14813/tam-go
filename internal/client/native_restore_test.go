package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ticket-auction-manager/tam-go/internal/presence"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestAcceptedMissingDeleteRetainsRecoveryAncestry(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "queued"}[queued], func(t *testing.T) {
			a, b, c := newFixture(t), newFixture(t), newFixture(t)
			event := newCausalEventServer(t, nil, nil)
			event.configure(t, a, b, c)
			for _, f := range []*fixture{a, b, c} {
				f.h.sync.Tick()
			}
			causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}})
			causalSave(t, b, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
			if code, body := c.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
				t.Fatalf("first delete: %d %s", code, body)
			}
			if queued {
				a.h.sync.NoteFailure(errors.New("offline"))
			}
			if code, body := a.do("DELETE", "/api/prefixes?p=A", nil, nil); code != 200 {
				t.Fatalf("already absent delete: %d %s", code, body)
			}
			if queued {
				a.h.sync.Tick()
			}
			causalCaughtUp(t, a, b, c)
			event = event.replacement(t)
			event.configure(t, a, b)
			a.h.sync.Tick()
			b.h.sync.Tick()
			conflicts, err := event.st.Conflicts()
			if err != nil {
				t.Fatal(err)
			}
			if len(conflicts) != 0 {
				t.Fatalf("accepted delete lost ancestry; expected no conflicts, got %+v", conflicts)
			}
		})
	}
}

func TestNativeRestoreDoesNotOverwriteInterveningDrawing(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	var restores, drawingWrites int
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/api/drawing" {
				drawingWrites++
			}
			inner.ServeHTTP(w, r)
			if r.Method == "POST" && r.URL.Path == "/api/backuprestore" {
				restores++
				if code, body := b.do("POST", "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 99}}, nil); code != 200 {
					t.Errorf("intervening save: %d %s", code, body)
				}
			}
		})
	})
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	backup := store.NewBackupFile()
	backup.Baskets = []store.Basket{{Prefix: "A", BID: 1, Description: "Prize", WinningTicket: 22}}
	if code, body := a.do("POST", "/api/backuprestore/remote", nativeFixtureBackup(backup), nil); code != 200 {
		t.Fatalf("restore: %d %s", code, body)
	}
	row, err := event.st.Basket("A", 1)
	if err != nil || row == nil || row.WinningTicket != 99 || restores != 1 || drawingWrites != 1 {
		t.Fatalf("restore overwrote intervening save: row=%+v err=%v restores=%d drawings=%d", row, err, restores, drawingWrites)
	}
}

func TestOldThreeListImportsAreRejected(t *testing.T) {
	f := newFixture(t)
	remoteFixture(t, f)
	for _, path := range []string{"/api/backuprestore/local", "/api/backuprestore/remote"} {
		for _, body := range []string{`{}`, `{"prefixes":[],"tickets":[],"baskets":[]}`} {
			if code, response := f.do("POST", path, body, nil); code != 400 {
				t.Fatalf("legacy import %s: %d %s", path, code, response)
			}
		}
	}
}

func TestSharedKeyHeartbeatsPreserveEachClientQueue(t *testing.T) {
	st := newServerStore(t)
	key, err := st.CreateKey("Shared event key")
	if err != nil {
		t.Fatal(err)
	}
	registry := presence.New(nil)
	handler := server.NewHandler(st, server.FixedPassword("secret"), server.WithPresence(registry))
	for _, heartbeat := range []struct{ client, pending string }{{"workstation-a", "9"}, {"workstation-b", "0"}} {
		req := httptest.NewRequest(http.MethodGet, "/api", nil)
		req.Header.Set("TAM-KEY", key.AuthKey)
		req.Header.Set("X-TAM-Client", "tam-client/audit")
		req.Header.Set("X-TAM-Client-Name", heartbeat.client)
		req.Header.Set("X-TAM-Pending", heartbeat.pending)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("heartbeat: %d %s", res.Code, res.Body.String())
		}
	}
	snapshot := registry.Snapshot()
	first := snapshot[presence.Identity{Key: key.AuthKey, Name: "workstation-a"}]
	second := snapshot[presence.Identity{Key: key.AuthKey, Name: "workstation-b"}]
	if len(snapshot) != 2 || !first.HasPending || first.Pending != 9 || !second.HasPending || second.Pending != 0 {
		t.Fatalf("shared key hid a native client: %+v", snapshot)
	}
}
