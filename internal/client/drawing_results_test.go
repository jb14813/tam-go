package client

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestDrawingResultsRefuseLocalFallbackAfterReportTimeout(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	var stallCounts atomic.Bool
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stallCounts.Load() && r.URL.Path == "/api/reports/counts" {
				<-r.Context().Done()
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	event.configure(t, a, b)
	a.h.sync.Tick()
	b.h.sync.Tick()
	causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
	causalSave(t, a, "/api/tickets", oneTicket(42, "Shared buyer"))
	causalSave(t, b, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Prize"}})
	causalSave(t, b, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}})

	paths := []string{"/api/drawing", "/api/drawing/A"}
	checkShared := func() {
		t.Helper()
		for _, path := range paths {
			code, body := a.do("GET", path, nil, nil)
			if code != http.StatusOK {
				t.Fatalf("shared drawing results: %s = %d %s", path, code, body)
			}
			rows := decode[[]store.DrawingLine](t, body)
			if len(rows) != 1 || rows[0].BID != 1 || rows[0].WinningTicket != 42 || rows[0].FirstName != "Shared buyer" {
				t.Fatalf("shared drawing results must join entries from both clients: %s = %s", path, body)
			}
		}
	}
	checkShared()
	if rows, err := a.st.AllDrawing(); err != nil || len(rows) != 0 {
		t.Fatalf("remote drawing reads must not populate this client's own entries: %+v, %v", rows, err)
	}

	was := readTimeout
	readTimeout = 100 * time.Millisecond
	t.Cleanup(func() { readTimeout = was })
	stallCounts.Store(true)
	if code, body := a.do("GET", "/api/reports/counts", nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("timed-out counts report: %d %s", code, body)
	}
	readTimeout = was
	for _, path := range paths {
		if code, body := a.do("GET", path, nil, nil); code != http.StatusServiceUnavailable {
			t.Errorf("shared drawing results must be unavailable after a report timeout: %s = %d %s", path, code, body)
		}
	}
	// Editing can still open a local range while shared results are unavailable.
	if code, body := a.do("GET", "/api/drawing/A/1/1", nil, nil); code != http.StatusOK {
		t.Fatalf("offline drawing entry must remain available: %d %s", code, body)
	}
	if code, body := a.do("GET", "/api/tickets/A/42", nil, nil); code != http.StatusOK || decode[store.Ticket](t, body).FirstName != "Shared buyer" {
		t.Fatalf("offline drawing lookup must retain this client's buyer: %d %s", code, body)
	}

	stallCounts.Store(false)
	a.h.sync.Tick()
	checkShared()
}
