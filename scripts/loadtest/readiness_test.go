package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestUntilCaughtUpWaitsForRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"connected","pending":0,"recovering":true}`))
	}))
	defer server.Close()
	run := &test{o: options{settle: 30 * time.Millisecond}}
	client := &client{prog: &program{url: server.URL, name: "desk"}}
	if run.untilCaughtUp(client) {
		t.Fatal("connected client was considered caught up during recovery")
	}
}

func TestSetupWaitsForQueuedPrefixesBeforeOtherClientsRead(t *testing.T) {
	var queued atomic.Bool
	var waits atomic.Int32
	prefixes := []store.Prefix{{Prefix: "A", Color: "blue"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/prefixes":
			queued.Store(true)
			w.Header().Set("X-TAM-Queued", "1")
			json.NewEncoder(w).Encode(prefixes)
		case r.URL.Path == "/api/status":
			pending := 0
			if queued.Load() && waits.Add(1) < 3 {
				pending = 1
			}
			json.NewEncoder(w).Encode(map[string]any{"state": "connected", "pending": pending})
		case r.URL.Path == "/api/prefixes":
			if waits.Load() >= 3 {
				json.NewEncoder(w).Encode(prefixes)
			} else {
				w.Write([]byte(`[]`))
			}
		}
	}))
	defer server.Close()
	run := &test{o: options{settle: time.Second}, ev: &event{prefixes: prefixes}, clients: []*client{
		{prog: &program{url: server.URL, name: "first"}},
		{prog: &program{url: server.URL, name: "second"}},
	}}
	if err := run.setup(); err != nil {
		t.Fatal(err)
	}
	if !run.problemFree("report") {
		t.Fatal(run.problemText("report"))
	}
}

func TestQueuedSaveNeedsPreSaveRecoveryCatchupOrOutageEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, before    string
		outage, allowed bool
	}{
		{"recovery", `{"state":"connected","recovering":true,"pending":0}`, false, true},
		{"catchup", `{"state":"connected","pending":2}`, false, true},
		{"unexplained online queue", `{"state":"connected","pending":0}`, false, false},
		{"unplanned offline", `{"state":"offline","pending":2}`, false, false},
		{"planned outage", `{"state":"offline","pending":2}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var saved atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/status" {
					if saved.Load() {
						// Reading only after the save would incorrectly classify
						// its own pending entry as catch-up in every test case.
						w.Write([]byte(`{"state":"connected","pending":1}`))
					} else {
						w.Write([]byte(tc.before))
					}
					return
				}
				saved.Store(true)
				w.Header().Set("X-TAM-Queued", "1")
				w.Write([]byte(`[]`))
			}))
			defer server.Close()
			client := &client{prog: &program{url: server.URL, name: "desk"}}
			if tc.outage {
				client.openAway(time.Now().Add(-time.Second))
			}
			queued, err := client.call(nil, "", "POST", "/api/tickets", []store.Ticket{}, nil, 0)
			if err != nil || !queued || len(client.queuedAt) != 1 {
				t.Fatalf("save = %v, %v; records %+v", queued, err, client.queuedAt)
			}
			if got := client.queuedRightly(client.queuedAt[0]); got != tc.allowed {
				t.Fatalf("allowed=%v, want %v; record %+v", got, tc.allowed, client.queuedAt[0])
			}
		})
	}
}
