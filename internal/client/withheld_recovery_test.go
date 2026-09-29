package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestRejectedReplayReoffersAcceptedPredecessorToCurrentReplacement(t *testing.T) {
	for _, boundary := range []string{"immediate", "before-upload", "lost-response"} {
		t.Run(boundary, func(t *testing.T) {
			f := newFixture(t)
			var offline atomic.Bool
			event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if offline.Load() && r.Method == "POST" && r.URL.Path == "/api/tickets" {
						httpx.WriteError(w, 503, "offline")
						return
					}
					inner.ServeHTTP(w, r)
				})
			})
			event.configure(t, f)
			f.h.sync.Tick()
			causalSave(t, f, "/api/tickets", oneTicket(42, "Accepted before loss"))
			offline.Store(true)
			causalSave(t, f, "/api/tickets", oneTicket(42, "Refused correction"))
			keys, err := event.st.ListKeys()
			if err != nil {
				t.Fatal(err)
			}
			event.ts.Close()
			var uploads atomic.Int32
			var interrupt atomic.Bool
			interrupt.Store(boundary != "immediate")
			event = newCausalEventServer(t, keys, func(inner http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" && r.URL.Path == "/api/tickets" {
						httpx.WriteError(w, 400, "refused correction")
						return
					}
					if r.URL.Path == "/api/recovery" && uploads.Add(1) > 1 && interrupt.Load() {
						if boundary == "lost-response" {
							inner.ServeHTTP(httptest.NewRecorder(), r)
						}
						httpx.WriteError(w, 503, "interrupted recovery acknowledgement")
						return
					}
					inner.ServeHTTP(w, r)
				})
			})
			event.configure(t, f)
			f.h.sync.Tick()
			if waiting, failed := pending(t, f.st); waiting != 0 || failed != 1 {
				t.Fatalf("replay did not fail durably: pending=%d failed=%d", waiting, failed)
			}
			if boundary != "immediate" {
				interrupt.Store(false)
				// A new syncer has neither the first heartbeat token nor any
				// in-memory knowledge that its queue exposed a predecessor.
				f.h = newHandler(openStoreAt(t, f.dbPath), f.settings, testDist, WithTimings(testTimings))
				f.h.sync.Tick()
			}
			serverHas(t, event.st, 42, "Accepted before loss")
			if uploads.Load() < 2 {
				t.Fatal("no reoffer after rejected replay")
			}
			event = event.replacement(t)
			event.configure(t, f)
			f.h.sync.Tick()
			serverHas(t, event.st, 42, "Accepted before loss")
		})
	}
}

func TestDiscardedSaveAndHTTPNativeBackupKeepAcceptedPredecessor(t *testing.T) {
	f := newFixture(t)
	var status atomic.Int32
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code := int(status.Load()); code != 0 && r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
				httpx.WriteError(w, code, "temporarily reject this save")
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	event.configure(t, f)
	f.h.sync.Tick()
	causalSave(t, f, "/api/tickets", oneTicket(42, "Accepted & <林>"))
	status.Store(http.StatusServiceUnavailable)
	causalSave(t, f, "/api/tickets", oneTicket(42, "Rejected and discarded"))
	status.Store(http.StatusBadRequest)
	f.h.sync.Tick()
	if waiting, failed := pending(t, f.st); waiting != 0 || failed != 1 {
		t.Fatalf("wanted rejection pending=%d failed=%d", waiting, failed)
	}
	causalSave(t, f, "/api/outbox/discard", map[string]any{})
	file, raw := causalLocalBackup(t, f)
	if len(file.WithheldRecords) != 1 || len(file.Tickets) != 1 || file.Tickets[0].FirstName != "Rejected and discarded" {
		t.Fatalf("native backup lost entered value or provenance: %+v", file)
	}
	restored := newFixture(t)
	if code, body := restored.do("POST", "/api/backuprestore/local", json.RawMessage(raw), nil); code != http.StatusOK {
		t.Fatalf("native client restore: %d %s", code, body)
	}
	serverHas(t, restored.st, 42, "Rejected and discarded")
	for replacement := 0; replacement < 2; replacement++ {
		event = event.replacement(t)
		event.configure(t, f, restored)
		restored.h.sync.Tick()
		f.h.sync.Tick()
		serverHas(t, event.st, 42, "Accepted & <林>")
		causalCaughtUp(t, f, restored)
	}
	// A new deliberate save of the refused value is eligible under its new
	// operation, even though the text matches the discarded request exactly.
	causalSave(t, restored, "/api/tickets", oneTicket(42, "Rejected and discarded"))
	event = event.replacement(t)
	event.configure(t, f, restored)
	f.h.sync.Tick()
	restored.h.sync.Tick()
	serverHas(t, event.st, 42, "Rejected and discarded")
}

func TestNativeWithheldBackupRefusesOlderRemote(t *testing.T) {
	f := newFixture(t)
	if err := f.st.UpsertTickets(oneTicket(42, "Entered")); err != nil {
		t.Fatal(err)
	}
	file, err := f.st.ExportClientBackup()
	if err != nil {
		t.Fatal(err)
	}
	r := file.Revisions[0]
	r.Vector, r.Operations, r.Heads, r.Reviewed, r.ReviewedOperations = nil, nil, nil, nil, nil
	file.Revisions, file.BasketComponents = nil, nil
	file.WithheldRecords = []store.RecoveryHoldback{{Current: r}}
	if err := store.ValidateRecoverySnapshot(&file); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	old := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"authenticated": true})
	}))
	u, _ := url.Parse(old.URL)
	causalSave(t, f, "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": "old-key"})
	if code, body := f.do("POST", "/api/backuprestore/remote", file, nil); code != http.StatusBadGateway {
		t.Fatalf("older server accepted native withheld metadata: %d %s", code, body)
	}
	if posts.Load() != 0 {
		t.Fatal("restore sent values before checking the older server's capability")
	}
}
