package client

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestPushHTTPExplicitChoiceSurvivesRecovery(t *testing.T) {
	tests := []struct {
		name, target, path string
		chosen, changed    any
		check              func(*testing.T, *store.Store)
	}{
		{"winner", "baskets", "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}, []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}, func(t *testing.T, st *store.Store) {
			row, err := st.Basket("A", 1)
			if err != nil || row == nil || row.WinningTicket != 42 {
				t.Fatalf("pushed winner lost: %+v %v", row, err)
			}
		}},
		{"metadata", "baskets", "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Chosen prize"}}, []store.Basket{{Prefix: "A", BID: 1, Description: "Changed prize"}}, func(t *testing.T, st *store.Store) {
			row, err := st.Basket("A", 1)
			if err != nil || row == nil || row.Description != "Chosen prize" {
				t.Fatalf("pushed metadata lost: %+v %v", row, err)
			}
		}},
		{"ticket", "tickets", "/api/tickets", oneTicket(42, "Chosen buyer"), oneTicket(42, "Changed buyer"), func(t *testing.T, st *store.Store) { serverHas(t, st, 42, "Chosen buyer") }},
		{"prefix", "prefixes", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}}, func(t *testing.T, st *store.Store) {
			rows, err := st.ListPrefixes()
			if err != nil || len(rows) != 1 || rows[0].Color != "red" {
				t.Fatalf("pushed prefix lost: %+v %v", rows, err)
			}
		}},
	}
	for _, test := range tests {
		for _, pusherFirst := range []bool{false, true} {
			t.Run(test.name+"/"+map[bool]string{false: "pusher last", true: "pusher first"}[pusherFirst], func(t *testing.T) {
				a, b := newFixture(t), newFixture(t)
				event := newCausalEventServer(t, nil, nil)
				event.configure(t, a, b)
				a.h.sync.Tick()
				b.h.sync.Tick()
				causalSave(t, b, test.path, test.chosen)
				causalSave(t, a, test.path, test.changed)
				causalSave(t, b, "/api/backuprestore/push/"+test.target, map[string]any{})
				causalCaughtUp(t, a, b)
				test.check(t, event.st)
				event = event.replacement(t)
				event.configure(t, a, b)
				if pusherFirst {
					b.h.sync.Tick()
					a.h.sync.Tick()
				} else {
					a.h.sync.Tick()
					b.h.sync.Tick()
				}
				test.check(t, event.st)
				if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
					t.Fatalf("explicit pushed choice became conflict: %+v %v", conflicts, err)
				}
			})
		}
	}
}

func TestPushHTTPLostAcknowledgmentQueuesRemainingOwnedComponents(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	var lose atomic.Bool
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/baskets" && lose.Swap(false) {
				accepted := httptest.NewRecorder()
				inner.ServeHTTP(accepted, r)
				if accepted.Code == 200 {
					httpx.WriteError(w, http.StatusServiceUnavailable, "Acknowledgment lost after push commit")
					return
				}
				w.WriteHeader(accepted.Code)
				w.Write(accepted.Body.Bytes())
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, b, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Chosen prize"}})
	causalSave(t, b, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}})
	causalSave(t, a, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Changed prize"}})
	causalSave(t, a, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 41}})
	lose.Store(true)
	if code, body := b.do("POST", "/api/backuprestore/push/baskets", map[string]any{}, nil); code < 400 {
		t.Fatalf("partial push reported success: %d %s", code, body)
	}
	if p, failed := pending(t, b.st); p != 2 || failed != 0 {
		t.Fatalf("interrupted push did not retain both parts: pending=%d failed=%d", p, failed)
	}
	row, err := event.st.Basket("A", 1)
	if err != nil || row == nil || row.Description != "Chosen prize" || row.WinningTicket != 41 {
		t.Fatalf("push remainder overtook uncertain request: %+v %v", row, err)
	}
	b.h = newHandler(openStoreAt(t, b.dbPath), b.settings, testDist, WithTimings(testTimings))
	b.url = newTestServer(t, b.h.routes(testDist)).URL
	b.h.sync.Tick()
	causalCaughtUp(t, b)
	row, err = event.st.Basket("A", 1)
	if err != nil || row == nil || row.Description != "Chosen prize" || row.WinningTicket != 42 {
		t.Fatalf("replayed push incomplete: %+v %v", row, err)
	}
	event = event.replacement(t)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	row, err = event.st.Basket("A", 1)
	if err != nil || row == nil || row.Description != "Chosen prize" || row.WinningTicket != 42 {
		t.Fatalf("replayed push receipts were lost: %+v %v", row, err)
	}
}

func TestPushHTTPJournalFailurePreventsRemoteMutation(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, b, "/api/tickets", oneTicket(42, "Old local"))
	causalSave(t, a, "/api/tickets", oneTicket(42, "Current server"))
	b.exec(`CREATE TRIGGER push_fail_journal BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT, 'push journal unavailable'); END`)
	if code, body := b.do("POST", "/api/backuprestore/push/tickets", map[string]any{}, nil); code != 500 {
		t.Fatalf("unjournaled push: %d %s", code, body)
	}
	serverHas(t, event.st, 42, "Current server")
}

func TestPushHTTPLegacyPrefixesPreserveExactIdentity(t *testing.T) {
	for _, prefix := range []store.Prefix{
		{Prefix: " A ", Color: "red", Weight: 1},
		{Prefix: "A/B", Color: "red", Weight: 1},
		{Prefix: "A", Color: "legacy color", Weight: -1},
	} {
		for _, pusherFirst := range []bool{false, true} {
			t.Run(prefix.Prefix+"/"+map[bool]string{false: "pusher last", true: "pusher first"}[pusherFirst], func(t *testing.T) {
				a, b := newFixture(t), newFixture(t)
				event := newCausalEventServer(t, nil, nil)
				event.configure(t, a, b)
				backup := store.NewBackupFile()
				backup.Prefixes = []store.Prefix{prefix}
				causalSave(t, b, "/api/backuprestore/local", backup)
				// Push before the first heartbeat: the server has never seen
				// this exact legacy identity, so form validation cannot help.
				causalSave(t, b, "/api/backuprestore/push/prefixes", map[string]any{})
				backup.Prefixes[0].Color, backup.Prefixes[0].Weight = "blue", 2
				causalSave(t, a, "/api/backuprestore/local", backup)
				causalSave(t, a, "/api/backuprestore/push/prefixes", map[string]any{})
				causalSave(t, b, "/api/backuprestore/push/prefixes", map[string]any{})
				causalCaughtUp(t, a, b)
				want := prefix
				if want.Color == "legacy color" {
					want.Color = "white" // The existing backup compatibility rule.
				}
				check := func(st *store.Store) {
					rows, err := st.ListPrefixes()
					if err != nil || len(rows) != 1 || rows[0] != want {
						t.Fatalf("legacy prefix changed: %+v %v; want %+v", rows, err, want)
					}
				}
				check(event.st)
				event = event.replacement(t)
				event.configure(t, a, b)
				if pusherFirst {
					b.h.sync.Tick()
					a.h.sync.Tick()
				} else {
					a.h.sync.Tick()
					b.h.sync.Tick()
				}
				check(event.st)
			})
		}
	}
}

func TestPushHTTPLegacyPrefixLostAcknowledgmentCannotUndoCorrectionAfterReplacement(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	var lose atomic.Bool
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/backuprestore" && lose.Swap(false) {
				accepted := httptest.NewRecorder()
				inner.ServeHTTP(accepted, r)
				if accepted.Code == 200 {
					httpx.WriteError(w, http.StatusServiceUnavailable, "Acknowledgment lost after prefix push")
					return
				}
				w.WriteHeader(accepted.Code)
				w.Write(accepted.Body.Bytes())
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	event.configure(t, a, b)
	backup := store.NewBackupFile()
	backup.Prefixes = []store.Prefix{{Prefix: " A/B ", Color: "red", Weight: -1}}
	causalSave(t, b, "/api/backuprestore/local", backup)
	lose.Store(true)
	if code, body := b.do("POST", "/api/backuprestore/push/prefixes", map[string]any{}, nil); code < 400 {
		t.Fatalf("unacknowledged prefix push reported success: %d %s", code, body)
	}
	if p, failed := pending(t, b.st); p != 1 || failed != 0 {
		t.Fatalf("prefix push was not durably queued: pending=%d failed=%d", p, failed)
	}
	backup.Prefixes[0].Color = "blue"
	causalSave(t, a, "/api/backuprestore/local", backup)
	causalSave(t, a, "/api/backuprestore/push/prefixes", map[string]any{})
	for replacement := 0; replacement < 2; replacement++ {
		event = event.replacement(t)
		event.configure(t, a, b)
		a.h.sync.Tick()
		b.h.sync.Tick()
		causalCaughtUp(t, a, b)
		rows, err := event.st.ListPrefixes()
		if err != nil || len(rows) != 1 || rows[0] != backup.Prefixes[0] {
			t.Fatalf("replacement %d replayed old prefix push: %+v %v", replacement+1, rows, err)
		}
	}
}
