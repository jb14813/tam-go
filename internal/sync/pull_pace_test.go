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
