package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

const editorTestSession = "0123456789abcdef0123456789abcdef"

func editorTestHeaders(sequence string) map[string]string {
	return map[string]string{"X-TAM-Edit-Session": editorTestSession, "X-TAM-Edit-Sequence": sequence}
}

// Begin a real HTTP request whose JSON body cannot finish until the test
// releases it. Newer requests can overtake this stalled upload on another
// connection, as an unload request can overtake a slow ordinary save.
func pausedEditorUpload(t *testing.T, f *fixture, body any, headers map[string]string) func() (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { writer.Close(); reader.Close() })
	request, err := http.NewRequest(http.MethodPost, f.url+"/api/tickets", reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	type response struct {
		code int
		body []byte
		err  error
	}
	done := make(chan response, 1)
	go func() {
		res, err := http.DefaultClient.Do(request)
		if err != nil {
			done <- response{err: err}
			return
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		done <- response{res.StatusCode, body, err}
	}()
	if _, err := writer.Write(raw[:1]); err != nil {
		t.Fatal(err)
	}
	return func() (int, []byte) {
		if _, err := writer.Write(raw[1:]); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case res := <-done:
			if res.err != nil {
				t.Fatal(res.err)
			}
			return res.code, res.body
		case <-time.After(5 * time.Second):
			t.Fatal("stalled upload did not finish")
			return 0, nil
		}
	}
}

func TestEditorLateBodyCannotUndoNewerSave(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "remote"}[remote], func(t *testing.T) {
			f := newFixture(t)
			var rst *store.Store
			if remote {
				rst, _ = remoteFixture(t, f)
			}
			finish := pausedEditorUpload(t, f, oneTicket(1, "Old"), editorTestHeaders("1"))
			if code, b := f.do("POST", "/api/tickets", oneTicket(1, "New"), editorTestHeaders("2")); code != 200 {
				t.Fatalf("new: %d %s", code, b)
			}
			if code, b := finish(); code != 200 {
				t.Fatalf("late old: %d %s", code, b)
			}
			serverHas(t, f.st, 1, "New")
			if remote {
				serverHas(t, rst, 1, "New")
			}
		})
	}
}

func TestEditorLateBatchStillSavesRowsNotSuperseded(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	old := append(oneTicket(1, "Old"), oneTicket(2, "Still needed")...)
	finish := pausedEditorUpload(t, f, old, editorTestHeaders("1"))
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "New"), editorTestHeaders("2")); code != 200 {
		t.Fatalf("new: %d %s", code, b)
	}
	if code, b := finish(); code != 200 {
		t.Fatalf("late old batch: %d %s", code, b)
	}
	for _, st := range []*store.Store{f.st, rst} {
		serverHas(t, st, 1, "New")
		serverHas(t, st, 2, "Still needed")
	}
}

func TestEditorRejectedNewerSaveDoesNotSuppressOlderValidSave(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	key, err := rst.CreateKey("editor")
	if err != nil {
		t.Fatal(err)
	}
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	rs := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				httpx.WriteInternal(w, err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte("Reject")) {
				httpx.WriteError(w, 422, "Rejected value")
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), key.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	finish := pausedEditorUpload(t, f, oneTicket(1, "Valid older"), editorTestHeaders("1"))
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Reject"), editorTestHeaders("2")); code != 422 {
		t.Fatalf("rejection: %d %s", code, b)
	}
	if code, b := finish(); code != 200 {
		t.Fatalf("older valid: %d %s", code, b)
	}
	serverHas(t, f.st, 1, "Valid older")
	serverHas(t, rst, 1, "Valid older")
}

func TestEditorSequenceSurvivesClientRestart(t *testing.T) {
	f := newFixture(t)
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "New"), editorTestHeaders("2")); code != 200 {
		t.Fatalf("new: %d %s", code, b)
	}
	f.h = newHandler(openStoreAt(t, f.dbPath), f.settings, testDist, WithTimings(testTimings))
	f.url = newTestServer(t, f.h.routes(testDist)).URL
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Old after restart"), editorTestHeaders("1")); code != 200 {
		t.Fatalf("late old: %d %s", code, b)
	}
	serverHas(t, f.st, 1, "New")
}

func TestEditorHeadersMustBeValidTogether(t *testing.T) {
	for _, headers := range []map[string]string{
		{"X-TAM-Edit-Session": editorTestSession},
		{"X-TAM-Edit-Sequence": "1"},
		{"X-TAM-Edit-Session": "short", "X-TAM-Edit-Sequence": "1"},
		{"X-TAM-Edit-Session": strings.Repeat("z", 32), "X-TAM-Edit-Sequence": "1"},
		editorTestHeaders("0"), editorTestHeaders("-1"), editorTestHeaders("9223372036854775808"), editorTestHeaders("1.5"),
	} {
		t.Run(headers["X-TAM-Edit-Session"]+"/"+headers["X-TAM-Edit-Sequence"], func(t *testing.T) {
			f := newFixture(t)
			if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Invalid headers"), headers); code != 400 {
				t.Fatalf("invalid headers: %d %s", code, b)
			}
			if row, err := f.st.Ticket("A", 1); err != nil || row != nil {
				t.Fatalf("invalid header saved a row: %+v %v", row, err)
			}
		})
	}
}

func TestEditorJournalAndReservationCommitTogether(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.exec(`CREATE TRIGGER editor_fail_reservation BEFORE INSERT ON editor_generations BEGIN SELECT RAISE(ABORT, 'simulated generation disk failure'); END`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Cannot retain"), editorTestHeaders("2")); code != 500 {
		t.Fatalf("failed reservation: %d %s", code, b)
	}
	if row, err := rst.Ticket("A", 1); err != nil || row != nil {
		t.Fatalf("unjournaled save sent: %+v %v", row, err)
	}
	if p, failed := pending(t, f.st); p != 0 || failed != 0 {
		t.Fatalf("partial journal committed: pending=%d failed=%d", p, failed)
	}
	f.exec(`DROP TRIGGER editor_fail_reservation`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Older retained"), editorTestHeaders("1")); code != 200 {
		t.Fatalf("earlier save: %d %s", code, b)
	}
	serverHas(t, f.st, 1, "Older retained")
	serverHas(t, rst, 1, "Older retained")
}

func TestEditorFailedLocalSaveDoesNotAdvanceSequence(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "queued"}[queued], func(t *testing.T) {
			f := newFixture(t)
			if queued {
				_, rs := remoteFixture(t, f)
				rs.Close()
				f.h.sync.Tick()
			}
			f.exec(`CREATE TRIGGER editor_fail_local BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'simulated local disk failure'); END`)
			if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Cannot retain"), editorTestHeaders("2")); code != 500 {
				t.Fatalf("failed local save: %d %s", code, b)
			}
			f.exec(`DROP TRIGGER editor_fail_local`)
			if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Earlier retained"), editorTestHeaders("1")); code != 200 {
				t.Fatalf("earlier save: %d %s", code, b)
			}
			serverHas(t, f.st, 1, "Earlier retained")
		})
	}
}

func TestEditorDurableIntentSuppressesDelayedOlderSave(t *testing.T) {
	f := newFixture(t)
	rst, _ := remoteFixture(t, f)
	f.exec(`CREATE TRIGGER editor_fail_retention BEFORE INSERT ON tickets BEGIN SELECT RAISE(ABORT, 'simulated local retention failure'); END`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Durable newer intent"), editorTestHeaders("2")); code != 500 {
		t.Fatalf("retention failure: %d %s", code, b)
	}
	if p, failed := pending(t, f.st); p != 1 || failed != 0 {
		t.Fatalf("intent missing: pending=%d failed=%d", p, failed)
	}
	f.exec(`DROP TRIGGER editor_fail_retention`)
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "Delayed old"), editorTestHeaders("1")); code != 200 {
		t.Fatalf("late older: %d %s", code, b)
	}
	f.h.sync.Tick()
	serverHas(t, f.st, 1, "Durable newer intent")
	serverHas(t, rst, 1, "Durable newer intent")
}

func TestEditorGenerationSeparatesBasketComponentsAndPageSessions(t *testing.T) {
	f := newFixture(t)
	if code, b := f.do("POST", "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 22}}, editorTestHeaders("2")); code != 200 {
		t.Fatalf("drawing: %d %s", code, b)
	}
	if code, b := f.do("POST", "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Prize"}}, editorTestHeaders("1")); code != 200 {
		t.Fatalf("earlier metadata: %d %s", code, b)
	}
	if row, err := f.st.Basket("A", 1); err != nil || row == nil || row.Description != "Prize" || row.WinningTicket != 22 {
		t.Fatalf("separate components: %+v %v", row, err)
	}
	if code, b := f.do("POST", "/api/tickets", oneTicket(1, "First page"), editorTestHeaders("2")); code != 200 {
		t.Fatalf("first page: %d %s", code, b)
	}
	other := editorTestHeaders("1")
	other["X-TAM-Edit-Session"] = strings.Repeat("f", 32)
	if code, b := f.do("POST", "/api/search/tickets", oneTicket(1, "Second page"), other); code != 200 {
		t.Fatalf("new page: %d %s", code, b)
	}
	serverHas(t, f.st, 1, "Second page")
}

func TestPrefixCacheCannotOverwriteSaveWithDelayedResponse(t *testing.T) {
	f := newFixture(t)
	rst := newServerStore(t)
	key, err := rst.CreateKey("prefix cache")
	if err != nil {
		t.Fatal(err)
	}
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	started, release := make(chan struct{}), make(chan struct{})
	rs := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/prefixes" {
			snapshot := httptest.NewRecorder()
			inner.ServeHTTP(snapshot, r)
			close(started)
			<-release
			for key, values := range snapshot.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(snapshot.Code)
			w.Write(snapshot.Body.Bytes())
			return
		}
		inner.ServeHTTP(w, r)
	}))
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), key.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	if code, b := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}, nil); code != 200 {
		t.Fatalf("initial: %d %s", code, b)
	}
	readDone := make(chan int, 1)
	go func() { code, _ := f.do("GET", "/api/prefixes", nil, nil); readDone <- code }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("prefix read did not start")
	}
	writeDone := make(chan int, 1)
	go func() {
		code, _ := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}}, nil)
		writeDone <- code
	}()
	var writeStatus int
	select {
	case writeStatus = <-writeDone:
		t.Error("prefix save passed an older outstanding cache read")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if code := <-readDone; code != 200 {
		t.Fatalf("prefix read: %d", code)
	}
	if writeStatus == 0 {
		writeStatus = <-writeDone
	}
	if writeStatus != 200 {
		t.Fatalf("prefix save: %d", writeStatus)
	}
	for _, st := range []*store.Store{f.st, rst} {
		rows, err := st.ListPrefixes()
		if err != nil || len(rows) != 1 || rows[0].Color != "blue" {
			t.Fatalf("latest prefix overwritten by cache: %+v %v", rows, err)
		}
	}
}
