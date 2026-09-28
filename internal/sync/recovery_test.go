package sync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestStatusShowsRecoveryUntilUploadAcknowledged(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api" {
			w.Write([]byte(`{"authenticated":true,"recovery_token":"recover"}`))
			return
		}
		if r.URL.Path == "/api/recovery" {
			close(started)
			<-release
			w.Write([]byte(`{"recovered":true}`))
		}
	}))
	defer server.Close()
	s, _ := newSyncer(t, server.URL)
	done := make(chan struct{})
	go func() { s.Tick(); close(done) }()
	defer func() { close(release); <-done }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	statusJSON, err := json.Marshal(s.Status())
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusJSON, &status); err != nil {
		t.Fatal(err)
	}
	if status["state"] != "connected" || status["recovering"] != true || status["pending"] != float64(0) {
		t.Fatalf("status hides active recovery with no queued writes: %s", statusJSON)
	}
	release <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery did not finish")
	}
	statusJSON, _ = json.Marshal(s.Status())
	json.Unmarshal(statusJSON, &status)
	if status["recovering"] != false {
		t.Fatalf("completed recovery still active: %s", statusJSON)
	}
}

func TestRecoveryUploadsLocalCopyBeforeQueue(t *testing.T) {
	var calls []string
	requested := true
	var recovered store.RecoverySnapshot
	var heartbeatClient string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			heartbeatClient = r.Header.Get("X-TAM-Client-Name")
			doc := map[string]any{"authenticated": true}
			if requested {
				doc["recovery_token"] = "recover-this-event"
			}
			json.NewEncoder(w).Encode(doc)
		case "/api/recovery":
			if client := r.Header.Get("X-TAM-Client-Name"); client == "" || client != heartbeatClient {
				t.Errorf("recovery client = %q, want heartbeat client %q", client, heartbeatClient)
			}
			var req struct {
				Token string                 `json:"token"`
				Data  store.RecoverySnapshot `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.Token != "recover-this-event" {
				t.Errorf("token = %q", req.Token)
			}
			recovered = req.Data
			requested = false
			w.Write([]byte(`{"recovered":true}`))
		case "/api/backuprestore":
			json.NewEncoder(w).Encode(store.NewBackupFile())
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	s, st := newSyncer(t, server.URL)
	if err := st.Import(store.BackupFile{
		Prefixes: []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}},
		Tickets:  []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Saved before outage"}},
		Baskets:  []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue("POST", "/api/tickets", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	if len(recovered.Tickets) != 1 || len(recovered.Prefixes) != 1 || len(recovered.Baskets) != 1 || recovered.Baskets[0].WinningTicket != 1 {
		t.Fatalf("recovery did not preserve locally entered rows: %+v; calls %v", recovered, calls)
	}
	if len(calls) < 3 || calls[1] != "POST /api/recovery" || calls[2] != "POST /api/tickets" {
		t.Fatalf("snapshot must precede queued edits: %v", calls)
	}
}

func TestHeartbeatCarriesStableClientIdentityWithoutNumberingASave(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	s.Tick()
	first := f.lastHeartbeat().Get("X-TAM-Client-Name")
	s.Tick()
	if got := f.lastHeartbeat().Get("X-TAM-Client-Name"); first == "" || got != first {
		t.Fatalf("heartbeat identities = %q, %q; want the same nonempty identity", first, got)
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "client"
	}
	order, err := st.NextSave(host)
	if err != nil || order.Client != first || order.Save != 1 {
		t.Fatalf("first actual save after heartbeats = %+v, %v; want client %q save 1", order, err, first)
	}
}

func TestFailedRecoveryKeepsLocalCopyAndQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api" {
			w.Write([]byte(`{"authenticated":true,"recovery_token":"retry"}`))
			return
		}
		if r.URL.Path != "/api/recovery" {
			t.Errorf("must not drain after failed recovery: %s", r.URL.Path)
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	s, st := newSyncer(t, server.URL)
	st.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1, FirstName: "Kept"}})
	s.Enqueue("POST", "/api/tickets", []byte(`[]`))
	s.Tick()
	if p, _ := pendingFailed(t, st); p != 1 {
		t.Fatalf("pending = %d, want 1", p)
	}
	row, err := st.Ticket("A", 1)
	if err != nil || row == nil || row.FirstName != "Kept" {
		t.Fatalf("local copy = %+v, %v", row, err)
	}
}

func TestReconnectDoesNotDownloadOtherClientsRows(t *testing.T) {
	f := newFakeServer(t)
	s, st := newSyncer(t, f.ts.URL)
	if err := st.UpsertTickets([]store.Ticket{{Prefix: "S", TID: 1, FirstName: "Entered here"}}); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	f.set("down")
	s.Tick()
	f.set("up")
	s.Reset()
	s.Tick()
	s.Tick()
	for _, request := range f.seen() {
		if request != "GET /api" {
			t.Errorf("background connection work must not download event rows: %s", request)
		}
	}
	row, err := st.Ticket("S", 1)
	if err != nil || row == nil || row.FirstName != "Entered here" {
		t.Fatalf("reconnect overwrote the locally entered row: %+v, %v", row, err)
	}
}
