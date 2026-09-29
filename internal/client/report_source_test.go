package client

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/store"
)

var eventReportPaths = []string{"/api/reports/byname/A", "/api/reports/bybasket/A", "/api/reports/counts"}

func TestEventReportsRefusePartialLocalFallback(t *testing.T) {
	for _, path := range eventReportPaths {
		t.Run(path, func(t *testing.T) {
			a, b := newFixture(t), newFixture(t)
			event := newCausalEventServer(t, nil, nil)
			event.configure(t, a, b)
			a.h.sync.Tick()
			b.h.sync.Tick()
			causalSave(t, a, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
			causalSave(t, a, "/api/tickets", oneTicket(42, "Shared buyer"))
			causalSave(t, b, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Prize"}})
			causalSave(t, b, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}})
			if code, body := a.do("GET", path, nil, nil); code != http.StatusOK {
				t.Fatalf("shared report before outage: %d %s", code, body)
			}
			event.ts.Close()
			// First read discovers the outage; subsequent reads see offline state.
			for attempt := 0; attempt < 2; attempt++ {
				code, body := a.do("GET", path, nil, nil)
				if code != http.StatusServiceUnavailable || !strings.Contains(string(body), "report") {
					t.Fatalf("report during outage must be unavailable, not partial local data: %d %s", code, body)
				}
			}
		})
	}
}

func TestEventReportsWaitForQueuedSaves(t *testing.T) {
	f := newFixture(t)
	_, _ = remoteFixture(t, f)
	s, err := config.Load(f.settings)
	if err != nil {
		t.Fatal(err)
	}
	key := s.RemoteKey
	s.RemoteKey = "WRONG"
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	causalSave(t, f, "/api/tickets", oneTicket(42, "Queued buyer"))
	s.RemoteKey = key
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	f.h.sync.NoteSuccess()
	for _, path := range eventReportPaths {
		if code, body := f.do("GET", path, nil, nil); code != http.StatusServiceUnavailable {
			t.Errorf("report with queued saves must wait: %s = %d %s", path, code, body)
		}
	}
	f.h.sync.Tick()
	for _, path := range eventReportPaths {
		if code, body := f.do("GET", path, nil, nil); code != http.StatusOK {
			t.Errorf("report after queue drained: %s = %d %s", path, code, body)
		}
	}
}

func TestEventReportsRemainAvailableStandalone(t *testing.T) {
	f := newFixture(t)
	causalSave(t, f, "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
	causalSave(t, f, "/api/tickets", oneTicket(42, "Standalone buyer"))
	causalSave(t, f, "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Prize"}})
	causalSave(t, f, "/api/drawing", []store.Basket{{Prefix: "A", BID: 1, WinningTicket: 42}})
	for _, path := range eventReportPaths {
		code, body := f.do("GET", path, nil, nil)
		if code != http.StatusOK || string(body) == "[]\n" {
			t.Errorf("standalone report: %s = %d %s", path, code, body)
		}
	}
}

func TestEventReportsWaitForRecovery(t *testing.T) {
	f := newFixture(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
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
	for _, path := range eventReportPaths {
		answer := make(chan int, 1)
		go func() { code, _ := f.do("GET", path, nil, nil); answer <- code }()
		select {
		case code := <-answer:
			if code != http.StatusServiceUnavailable {
				t.Errorf("report during recovery = %d, want 503", code)
			}
		case <-time.After(time.Second):
			t.Error("report waited for recovery instead of explaining that it is unavailable")
			unblock()
			<-answer
		}
	}
	unblock()
	<-done
}

func TestEventReportsWaitForRefusedSaves(t *testing.T) {
	f := newFixture(t)
	var refusal atomic.Int32
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code := int(refusal.Load()); code != 0 && r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
				w.WriteHeader(code)
				w.Write([]byte(`{"detail":"save refused"}`))
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	event.configure(t, f)
	f.h.sync.Tick()
	refusal.Store(http.StatusServiceUnavailable)
	causalSave(t, f, "/api/tickets", oneTicket(42, "Queued buyer"))
	refusal.Store(http.StatusBadRequest)
	f.h.sync.Tick()
	if waiting, failed := pending(t, f.st); waiting != 0 || failed != 1 {
		t.Fatalf("expected a refused save: %d waiting, %d failed", waiting, failed)
	}
	for _, path := range eventReportPaths {
		if code, body := f.do("GET", path, nil, nil); code != http.StatusServiceUnavailable {
			t.Errorf("report with a known refused save: %s = %d %s", path, code, body)
		}
	}
	refusal.Store(0)
	causalSave(t, f, "/api/outbox/retry", map[string]any{})
	f.h.sync.Tick()
	for _, path := range eventReportPaths {
		if code, body := f.do("GET", path, nil, nil); code != http.StatusOK {
			t.Errorf("report after refused save delivered: %s = %d %s", path, code, body)
		}
	}
}

func TestEventReportsRefuseInvalidRemoteAnswers(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newFixture(t)
			event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/api/reports/") {
						w.WriteHeader(status)
						w.Write([]byte(`{"detail":"report failed"}`))
						return
					}
					inner.ServeHTTP(w, r)
				})
			})
			event.configure(t, f)
			f.h.sync.Tick()
			code, body := f.do("GET", "/api/reports/counts", nil, nil)
			if code == http.StatusOK {
				t.Fatalf("invalid remote report became successful local data: %d %s", code, body)
			}
			if status == http.StatusConflict && code != status {
				t.Errorf("review conflict must remain visible: %d %s", code, body)
			}
		})
	}
}
