package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"

	"ticket-auction-manager/tam-go/internal/admin"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

type causalEventServer struct {
	st  *store.Store
	ts  *httptest.Server
	key string
}

// Every replacement is a new database. Only actual access keys are copied;
// no event rows, revisions, receipts, save counters or recovery state survive.
func newCausalEventServer(t *testing.T, keys []store.AuthKey, wrap func(http.Handler) http.Handler) *causalEventServer {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "event.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateServer(database); err != nil {
		t.Fatal(err)
	}
	st := store.New(database)
	if len(keys) == 0 {
		key, err := st.CreateKey("shared event key")
		if err != nil {
			t.Fatal(err)
		}
		keys = []store.AuthKey{key}
	} else {
		for _, key := range keys {
			if _, err := database.Exec(`INSERT INTO auth_keys(auth_key,description) VALUES(?,?)`, key.AuthKey, key.Description); err != nil {
				t.Fatal(err)
			}
		}
	}
	var handler http.Handler = server.NewHandler(st, server.FixedPassword("secret"))
	if wrap != nil {
		handler = wrap(handler)
	}
	return &causalEventServer{st, newTestServer(t, handler), keys[0].AuthKey}
}

func (event *causalEventServer) replacement(t *testing.T) *causalEventServer {
	t.Helper()
	keys, err := event.st.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	event.ts.Close()
	return newCausalEventServer(t, keys, nil)
}

func (event *causalEventServer) configure(t *testing.T, clients ...*fixture) {
	t.Helper()
	u, err := url.Parse(event.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if code, b := client.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": event.key}, nil); code != 200 {
			t.Fatalf("configure: %d %s", code, b)
		}
	}
}

func causalSave(t *testing.T, f *fixture, path string, body any) {
	t.Helper()
	if code, b := f.do("POST", path, body, nil); code != 200 {
		t.Fatalf("save %s: %d %s", path, code, b)
	}
}

func causalLocalBackup(t *testing.T, f *fixture) (store.RecoverySnapshot, []byte) {
	t.Helper()
	code, body := f.do("GET", "/api/backuprestore/local", nil, nil)
	if code != 200 {
		t.Fatalf("local backup: %d %s", code, body)
	}
	return decode[store.RecoverySnapshot](t, body), body
}

func causalRevision(t *testing.T, snapshot store.RecoverySnapshot, kind string, id int) store.RecordRevision {
	t.Helper()
	for _, revision := range snapshot.Revisions {
		if revision.Kind == kind && revision.Prefix == "A" && revision.ID == id {
			return revision
		}
	}
	t.Fatalf("backup omitted %s A/%d receipt: %+v", kind, id, snapshot.Revisions)
	return store.RecordRevision{}
}

func causalCaughtUp(t *testing.T, clients ...*fixture) {
	t.Helper()
	for _, client := range clients {
		if p, failed := pending(t, client.st); p != 0 || failed != 0 {
			t.Fatalf("not caught up: pending=%d failed=%d", p, failed)
		}
	}
}

func causalNoForeignRows(t *testing.T, a, b *fixture) {
	t.Helper()
	if row, err := a.st.Ticket("A", 100); err != nil || row != nil {
		t.Fatalf("A imported B's ticket: %+v %v", row, err)
	}
	if row, err := b.st.Ticket("A", 99); err != nil || row != nil {
		t.Fatalf("B imported A's ticket: %+v %v", row, err)
	}
	if row, err := b.st.Basket("A", 1); err != nil || row == nil || row.Description != "" {
		t.Fatalf("drawing client imported another client's metadata: %+v %v", row, err)
	}
}

func causalSeedCorrections(t *testing.T, event *causalEventServer, a, b *fixture) {
	t.Helper()
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/tickets", append(oneTicket(42, "Wrong buyer"), oneTicket(99, "Only A")...))
	causalSave(t, a, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Prize", Donors: "Sponsor"}, {Prefix: "A", BID: 2, Description: "Metadata only"}})
	causalSave(t, a, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 41}})
	causalSave(t, b, "/api/tickets", append(oneTicket(42, "Correct buyer"), oneTicket(100, "Only B")...))
	causalSave(t, b, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}, {Prefix: "A", BID: 2, WinningTicket: 100}})
	causalCaughtUp(t, a, b)
	for _, entry := range []struct {
		client *fixture
		id     int
	}{{a, 100}, {b, 99}} {
		if code, body := entry.client.do("GET", "/api/tickets/A/"+strconv.Itoa(entry.id), nil, nil); code != 200 {
			t.Fatalf("shared lookup: %d %s", code, body)
		}
	}
	causalNoForeignRows(t, a, b)
	// These receipts must come through the ordinary client/server save path.
	// No test writes revision vectors into a client or server database.
	snapshot, _ := causalLocalBackup(t, b)
	for _, record := range []struct {
		kind string
		id   int
	}{{"ticket", 42}, {"drawing", 1}} {
		revision := causalRevision(t, snapshot, record.kind, record.id)
		if revision.Vector["client:"+a.clientName()] == 0 || revision.Vector["client:"+b.clientName()] == 0 {
			t.Fatalf("save acknowledgment did not reach owning client: %+v", revision)
		}
	}
}

func causalAssertCorrected(t *testing.T, event *causalEventServer) {
	t.Helper()
	serverHas(t, event.st, 42, "Correct buyer")
	basket, err := event.st.Basket("A", 1)
	if err != nil || basket == nil || basket.Description != "Prize" || basket.Donors != "Sponsor" || basket.WinningTicket != 42 {
		t.Fatalf("corrected basket did not survive: %+v %v", basket, err)
	}
	basket, err = event.st.Basket("A", 2)
	if err != nil || basket == nil || basket.Description != "Metadata only" || basket.WinningTicket != 100 {
		t.Fatalf("metadata-only copy erased another client's winner: %+v %v", basket, err)
	}
	conflicts, err := event.st.Conflicts()
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("ordered acknowledged edits became conflicts: %+v %v", conflicts, err)
	}
}

func TestCausalHTTPConfirmedCorrectionsSurviveTwoReplacements(t *testing.T) {
	for _, newerFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "older first", true: "corrected first"}[newerFirst], func(t *testing.T) {
			a, b := newFixture(t), newFixture(t)
			event := newCausalEventServer(t, nil, nil)
			causalSeedCorrections(t, event, a, b)
			for round := 1; round <= 2; round++ {
				event = event.replacement(t)
				event.configure(t, a, b)
				first, second := a, b
				if newerFirst {
					first, second = b, a
				}
				first.h.sync.Tick()
				second.h.sync.Tick()
				causalAssertCorrected(t, event)
				causalCaughtUp(t, a, b)
				causalNoForeignRows(t, a, b)
			}
		})
	}
}

func TestCausalHTTPLostAcknowledgmentReplayCannotUndoCorrection(t *testing.T) {
	for _, kind := range []string{"ticket", "drawing"} {
		t.Run(kind, func(t *testing.T) {
			a, b := newFixture(t), newFixture(t)
			path := "/api/tickets"
			old, new := any(oneTicket(42, "Old accepted")), any(oneTicket(42, "Corrected"))
			if kind == "drawing" {
				path = "/api/drawing"
				old = []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 41}}
				new = []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}
			}
			var lose atomic.Bool
			lose.Store(true)
			event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.URL.Path == path && lose.Swap(false) {
						accepted := httptest.NewRecorder()
						inner.ServeHTTP(accepted, r)
						if accepted.Code != 200 {
							for k, vs := range accepted.Header() {
								for _, v := range vs {
									w.Header().Add(k, v)
								}
							}
							w.WriteHeader(accepted.Code)
							w.Write(accepted.Body.Bytes())
							return
						}
						httpx.WriteError(w, http.StatusServiceUnavailable, "Acknowledgment lost after commit")
						return
					}
					inner.ServeHTTP(w, r)
				})
			})
			event.configure(t, a, b)
			a.h.sync.Tick()
			b.h.sync.Tick()
			causalSave(t, a, path, old)
			if p, failed := pending(t, a.st); p != 1 || failed != 0 {
				t.Fatalf("lost acknowledgment did not remain queued: %d %d", p, failed)
			}
			causalSave(t, b, path, new)
			causalCaughtUp(t, b)
			for round := 0; round < 2; round++ {
				current := event.replacement(t)
				current.configure(t, a, b)
				event = current
				if round == 0 {
					b.h.sync.Tick()
					a.h.sync.Tick()
				} else {
					a.h.sync.Tick()
					b.h.sync.Tick()
				}
				causalCaughtUp(t, a, b)
				if kind == "ticket" {
					serverHas(t, event.st, 42, "Corrected")
				} else {
					row, err := event.st.Basket("A", 1)
					if err != nil || row == nil || row.WinningTicket != 42 {
						t.Fatalf("old replay undid corrected winner: %+v %v", row, err)
					}
				}
				if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
					t.Fatalf("known earlier replay became conflict: %+v %v", conflicts, err)
				}
			}
		})
	}
}

func TestCausalHTTPLocalBackupPreservesOwnershipAndReceipts(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	event := newCausalEventServer(t, nil, nil)
	causalSeedCorrections(t, event, a, b)
	clones := []*fixture{newFixture(t), newFixture(t)}
	for i, original := range []*fixture{a, b} {
		before, body := causalLocalBackup(t, original)
		causalSave(t, clones[i], "/api/backuprestore/local", json.RawMessage(body))
		after, _ := causalLocalBackup(t, clones[i])
		beforeBytes, _ := json.Marshal(before.BasketComponents)
		afterBytes, _ := json.Marshal(after.BasketComponents)
		if string(beforeBytes) != string(afterBytes) {
			t.Fatalf("HTTP backup invented component ownership: %s -> %s", beforeBytes, afterBytes)
		}
		beforeRevision := causalRevision(t, before, "ticket", 42)
		afterRevision := causalRevision(t, after, "ticket", 42)
		beforeBytes, _ = json.Marshal(beforeRevision)
		afterBytes, _ = json.Marshal(afterRevision)
		if string(beforeBytes) != string(afterBytes) {
			t.Fatalf("HTTP backup lost accepted history: %s -> %s", beforeBytes, afterBytes)
		}
	}
	event = event.replacement(t)
	event.configure(t, clones...)
	clones[0].h.sync.Tick()
	clones[1].h.sync.Tick()
	causalAssertCorrected(t, event)
	causalNoForeignRows(t, clones[0], clones[1])
	causalCaughtUp(t, clones...)
}

func TestCausalHTTPReviewChoicePropagatesBeforeAnotherReplacement(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	causalSave(t, a, "/api/tickets", oneTicket(42, "Chosen buyer"))
	causalSave(t, b, "/api/tickets", oneTicket(42, "Rejected buyer"))
	event := newCausalEventServer(t, nil, nil)
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	conflicts, err := event.st.Conflicts()
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("independent entries need review: %+v %v", conflicts, err)
	}
	conflict := conflicts[0]
	choice := ""
	for _, candidate := range conflict.Candidates {
		var ticket store.Ticket
		if err := json.Unmarshal(candidate.Value, &ticket); err != nil {
			t.Fatal(err)
		}
		if ticket.FirstName == "Chosen buyer" {
			choice = candidate.Revision.Hash
		}
	}
	if choice == "" {
		t.Fatal("chosen buyer was not retained as a review candidate")
	}
	causalResolveThroughAdmin(t, event.st, conflict, choice)
	// Heartbeat review_token and /api/recovery/receipts carry the reviewed
	// ancestry back to the existing owner, without downloading the other value.
	a.h.sync.Tick()
	b.h.sync.Tick()
	snapshot, _ := causalLocalBackup(t, a)
	if revision := causalRevision(t, snapshot, "ticket", 42); len(revision.Reviewed) < 2 {
		t.Fatalf("chosen owner did not receive reviewed history: %+v", revision)
	}
	serverHas(t, b.st, 42, "Rejected buyer")
	event = event.replacement(t)
	event.configure(t, a, b)
	b.h.sync.Tick()
	a.h.sync.Tick()
	serverHas(t, event.st, 42, "Chosen buyer")
	if conflicts, err := event.st.Conflicts(); err != nil || len(conflicts) != 0 {
		t.Fatalf("already-reviewed alternatives reopened: %+v %v", conflicts, err)
	}
}

func causalResolveThroughAdmin(t *testing.T, st *store.Store, conflict store.RecordConflict, choice string) {
	t.Helper()
	pw, err := admin.Load(t.TempDir(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	ui := newTestServer(t, admin.NewHandler(st, pw, admin.Info{}))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrfPattern := regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`)
	getToken := func(path string) string {
		res, err := browser.Get(ui.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		match := csrfPattern.FindSubmatch(body)
		if res.StatusCode != 200 || len(match) != 2 {
			t.Fatalf("admin form %s: %d %s", path, res.StatusCode, body)
		}
		return string(match[1])
	}
	post := func(path string, form url.Values) {
		res, err := browser.PostForm(ui.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("admin post %s: %d %s", path, res.StatusCode, body)
		}
	}
	post("/admin/login", url.Values{"csrf": {getToken("/admin/")}, "password": {"secret"}})
	post("/admin/conflicts/resolve", url.Values{"csrf": {getToken("/admin/conflicts")}, "kind": {conflict.Kind}, "prefix": {conflict.Prefix}, "id": {strconv.Itoa(conflict.ID)}, "choice": {choice}, "expected": {store.ConflictToken(conflict)}, "confirm": {"yes"}})
}
