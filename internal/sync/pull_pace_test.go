package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
