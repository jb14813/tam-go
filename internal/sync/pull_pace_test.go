package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	stdsync "sync"
	"testing"
	"time"
)

// TestUnusablePullWaitsAHeartbeat: a mirror download the client cannot use
// (here a ticket without a prefix, which the original app's database can
// hold) is tried again at the heartbeat's pace, not on every one-second
// tick, which downloaded the whole data set again and again.
func TestUnusablePullWaitsAHeartbeat(t *testing.T) {
	var mu stdsync.Mutex
	downloads := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			json.NewEncoder(w).Encode(map[string]any{"whoami": "TAM Server", "authenticated": true, "healthy": true})
		case "/api/backuprestore":
			mu.Lock()
			downloads++
			mu.Unlock()
			w.Write([]byte(`{"prefixes":[],"baskets":[],"tickets":[{"prefix":"","t_id":1,"first_name":"x","pref":"CALL"},{"prefix":"A","t_id":2}]}`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(ts.Close)
	s, _ := newSyncer(t, ts.URL)
	s.t = DefaultTimings() // production cadence: a tick a second, a heartbeat every 5 s
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	s.Run(ctx)
	mu.Lock()
	n := downloads
	mu.Unlock()
	if n > 1 {
		t.Errorf("the whole data set was downloaded %d times in 4.5 s; an unusable download must wait a heartbeat", n)
	}
}

// TestReplayStopsWhenRunEnds: a long replay stops between saves once Run's
// context ends, so a client shutting down does not go on sending (and then
// find its database closed under it).
func TestReplayStopsWhenRunEnds(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api" {
			json.NewEncoder(w).Encode(map[string]any{"whoami": "TAM Server", "authenticated": true, "healthy": true})
			return
		}
		time.Sleep(20 * time.Millisecond) // each queued save takes a moment
		w.Write([]byte(`[]`))
	}))
	t.Cleanup(ts.Close)
	s, st := newSyncer(t, ts.URL)
	for i := 0; i < 200; i++ {
		if _, err := st.EnqueueOutbox(http.MethodPost, "/api/tickets", []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { s.Run(ctx); close(stopped) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Run went on replaying for a second after its context ended")
	}
	if p, _, _ := st.OutboxCounts(); p == 0 {
		t.Fatal("the whole queue was sent; the test did not stop a replay under way")
	}
}
