package client

import (
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

// Pairing again can request a recovery copy even though this live server
// already has the earlier acknowledged versions. Locally queued corrections
// must follow normal ordered replay, not become incomparable recovery copies.
func TestCausalHTTPQueuedCorrectionsAfterPairing(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/tickets", oneTicket(42, "Original buyer"))
	causalSave(t, a, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Confirmed metadata"}})
	causalSave(t, a, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 40}})
	causalSave(t, b, "/api/tickets", oneTicket(42, "Other accepted buyer"))
	causalSave(t, b, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 41}})
	causalCaughtUp(t, a, b)
	if _, err := event.st.DeleteKey(event.key); err != nil {
		t.Fatal(err)
	}
	causalSave(t, a, "/api/tickets", oneTicket(42, "Queued correction"))
	causalSave(t, a, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}})
	if waiting, failed := pending(t, a.st); waiting != 2 || failed != 0 {
		t.Fatalf("refused key did not retain corrections: %d waiting, %d failed", waiting, failed)
	}
	backup, _ := causalLocalBackup(t, a)
	if len(backup.Tickets) != 1 || backup.Tickets[0].FirstName != "Queued correction" || len(backup.Baskets) != 1 || backup.Baskets[0].WinningTicket != 42 {
		t.Fatalf("native backup omitted queued entries: %+v", backup)
	}
	serverHas(t, event.st, 42, "Other accepted buyer")
	u, err := url.Parse(event.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	causalSave(t, a, "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "tls": false, "password": "secret"})
	a.h.sync.Tick()
	if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
		t.Fatalf("unaccepted queued edits manufactured recovery conflicts: %+v %v", conflicts, err)
	}
	causalCaughtUp(t, a)
	check := func() {
		serverHas(t, event.st, 42, "Queued correction")
		basket, err := event.st.Basket("A", 1)
		if err != nil || basket == nil || basket.WinningTicket != 42 || basket.Description != "Confirmed metadata" {
			t.Fatalf("queued correction or independent confirmed component lost: %+v %v", basket, err)
		}
	}
	check()
	// Once replay confirms the correction, ordinary receipts must make it
	// survive another replacement even when the older owner's copy arrives first.
	event = event.replacement(t)
	event.configure(t, a, b)
	b.h.sync.Tick()
	a.h.sync.Tick()
	check()
	causalCaughtUp(t, a, b)
}

func TestCausalHTTPQueuedReplacementPreservesAncestryForSecondReplacement(t *testing.T) {
	for _, correctionFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "older first", true: "correction first"}[correctionFirst], func(t *testing.T) {
			a, b := newFixture(t), newFixture(t)
			event := newCausalEventServer(t, nil, nil)
			event.configure(t, a, b)
			a.h.sync.Tick()
			b.h.sync.Tick()
			causalSave(t, a, "/api/tickets", oneTicket(42, "Original accepted"))
			event.ts.Close()
			causalSave(t, a, "/api/tickets", oneTicket(42, "Queued after outage"))
			if waiting, failed := pending(t, a.st); waiting != 1 || failed != 0 {
				t.Fatalf("offline correction not queued: %d %d", waiting, failed)
			}
			event = event.replacement(t)
			event.configure(t, a, b)
			a.h.sync.Tick()
			b.h.sync.Tick()
			serverHas(t, event.st, 42, "Queued after outage")
			causalCaughtUp(t, a, b)
			causalSave(t, b, "/api/tickets", oneTicket(42, "Later confirmed correction"))
			event = event.replacement(t)
			event.configure(t, a, b)
			if correctionFirst {
				b.h.sync.Tick()
				a.h.sync.Tick()
			} else {
				a.h.sync.Tick()
				b.h.sync.Tick()
			}
			if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
				t.Fatalf("queued replay lost confirmed ancestry: %+v %v", conflicts, err)
			}
			serverHas(t, event.st, 42, "Later confirmed correction")
		})
	}
}

func TestCausalHTTPRecoveryBlocksPushAndRestore(t *testing.T) {
	for _, endpoint := range []string{"/api/backuprestore/push/tickets", "/api/backuprestore/local", "/api/backuprestore/remote"} {
		t.Run(endpoint, func(t *testing.T) {
			f := newFixture(t)
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.URL.Path == "/api/recovery" {
						close(started)
						<-release
					}
					inner.ServeHTTP(w, r)
				})
			})
			event.configure(t, f)
			go func() { f.h.sync.Tick(); close(done) }()
			<-started
			answer := make(chan int, 1)
			go func() {
				code, _ := f.do("POST", endpoint, nativeFixtureBackup(store.NewBackupFile()), nil)
				answer <- code
			}()
			select {
			case code := <-answer:
				if code != http.StatusConflict {
					t.Errorf("restore or Push during recovery = %d, want 409", code)
				}
			case <-time.After(time.Second):
				t.Error("restore or Push waited for recovery instead of rejecting the overlapping operation")
				unblock()
				<-answer
			}
			unblock()
			<-done
		})
	}
}

func TestCausalHTTPImmediateSaveBeforeFirstRecoveryKeepsLaterCorrection(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/tickets", oneTicket(42, "Original accepted"))
	event = event.replacement(t)
	event.configure(t, a, b)
	// Existing clients can save immediately after configuration, before a
	// heartbeat discovers the replacement's outstanding recovery request.
	causalSave(t, a, "/api/tickets", oneTicket(42, "Immediate new save"))
	causalSave(t, b, "/api/tickets", oneTicket(42, "Later confirmed correction"))
	causalCaughtUp(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
		t.Fatalf("late initial recovery conflicted with a confirmed descendant: %+v %v", conflicts, err)
	}
	serverHas(t, event.st, 42, "Later confirmed correction")
}

func TestCausalHTTPExplicitRetryRetainsItsNewChoice(t *testing.T) {
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
	causalSave(t, f, "/api/tickets", oneTicket(42, "Original"))
	status.Store(http.StatusServiceUnavailable)
	causalSave(t, f, "/api/tickets", oneTicket(42, "Retry chosen value"))
	status.Store(http.StatusBadRequest)
	f.h.sync.Tick()
	if waiting, failed := pending(t, f.st); waiting != 0 || failed != 1 {
		t.Fatalf("expected the queued rejection: %d waiting %d failed", waiting, failed)
	}
	status.Store(0)
	causalSave(t, f, "/api/tickets", oneTicket(42, "Entered after the rejection"))
	causalSave(t, f, "/api/outbox/retry", map[string]any{})
	serverHas(t, f.st, 42, "Retry chosen value")
	f.h.sync.Tick()
	causalCaughtUp(t, f)
	serverHas(t, event.st, 42, "Retry chosen value")
	serverHas(t, f.st, 42, "Retry chosen value")
	event = event.replacement(t)
	event.configure(t, f)
	f.h.sync.Tick()
	serverHas(t, event.st, 42, "Retry chosen value")
}

func TestCausalHTTPRebasedQueuePreservesOnlyItsOwnHistory(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/tickets", oneTicket(42, "Original"))
	causalSave(t, a, "/api/tickets", oneTicket(99, "Unrelated original"))
	causalSave(t, a, "/api/tickets", oneTicket(100, "Another original"))
	// A restored data folder can put the counter behind the server. The
	// new save2 edits ticket42; old confirmed save2 belongs to ticket99.
	a.exec(`UPDATE save_order SET last_save = 1 WHERE id = 1`)
	if _, err := event.st.DeleteKey(event.key); err != nil {
		t.Fatal(err)
	}
	causalSave(t, a, "/api/tickets", oneTicket(42, "Rebased queued choice"))
	u, err := url.Parse(event.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	causalSave(t, a, "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "tls": false, "password": "secret"})
	a.h.sync.Tick()
	if waiting, failed := pending(t, a.st); waiting != 0 || failed != 1 {
		t.Fatalf("older copied queue was not held for review: %d waiting %d failed", waiting, failed)
	}
	causalSave(t, a, "/api/outbox/retry", map[string]any{})
	a.h.sync.Tick()
	causalCaughtUp(t, a)
	serverHas(t, event.st, 42, "Rebased queued choice")
	event.key = a.h.settings().RemoteKey
	event.configure(t, b)
	b.h.sync.Tick()
	causalSave(t, b, "/api/tickets", append(oneTicket(42, "Later corrected choice"), oneTicket(99, "Unrelated corrected choice")...))
	event = event.replacement(t)
	event.configure(t, a, b)
	b.h.sync.Tick()
	a.h.sync.Tick()
	if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
		t.Fatalf("renumbering corrupted record ancestry: %+v %v", conflicts, err)
	}
	serverHas(t, event.st, 42, "Later corrected choice")
	serverHas(t, event.st, 99, "Unrelated corrected choice")
	serverHas(t, event.st, 100, "Another original")
}
