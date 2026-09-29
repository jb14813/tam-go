package client

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestDirectSaveDeadlineIncludesHandshakeAndNumberingRetry(t *testing.T) {
	f := newFixture(t)
	hang := make(chan struct{})
	var attempts atomic.Int32
	event := newCausalEventServer(t, nil, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api" {
				time.Sleep(1500 * time.Millisecond)
				inner.ServeHTTP(w, r)
				return
			}
			if r.Method == http.MethodPost && r.URL.Path == "/api/tickets" {
				if attempts.Add(1) == 1 {
					time.Sleep(1500 * time.Millisecond)
					httpx.WriteJSON(w, http.StatusConflict, map[string]any{"last_save": 100})
					return
				}
				<-hang
				return
			}
			inner.ServeHTTP(w, r)
		})
	})
	t.Cleanup(func() { close(hang) })
	event.configure(t, f)
	started := time.Now()
	code, body := f.do("POST", "/api/tickets", oneTicket(9, "Retained through timeout"), nil)
	elapsed := time.Since(started)
	if code != http.StatusOK {
		t.Fatalf("save was not retained: %d %s", code, body)
	}
	if elapsed > writeTimeout+750*time.Millisecond {
		t.Errorf("direct save took %s; handshake, retry and stalled save must share %s", elapsed, writeTimeout)
	}
	if attempts.Load() != 2 {
		t.Fatalf("fixture did not exercise a numbering retry: %d attempts", attempts.Load())
	}
	if p, failed := pending(t, f.st); p != 1 || failed != 0 {
		t.Fatalf("timeout lost journal: pending=%d failed=%d", p, failed)
	}
	request, err := f.st.NextOutbox()
	if err != nil || request == nil || request.Order.Save != 101 {
		t.Fatalf("retry identity was not preserved: %+v %v", request, err)
	}
	row, err := f.st.Ticket("A", 9)
	if err != nil || row == nil || row.FirstName != "Retained through timeout" {
		t.Fatalf("timeout lost entered value: %+v %v", row, err)
	}
}

// TestReadsDoNotWaitForASilentServer: when the Wi-Fi drops without a word,
// the server neither answers nor refuses. A page reading from it must give
// up after readTimeout and show the client's own copy, not hang for as long
// as the connection's own limits allow.
func TestReadsDoNotWaitForASilentServer(t *testing.T) {
	was := readTimeout
	readTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readTimeout = was })

	f := newFixture(t)
	rst := newServerStore(t)
	k, err := rst.CreateKey("client")
	if err != nil {
		t.Fatal(err)
	}
	inner := server.NewHandler(rst, server.FixedPassword("secret"))
	var silent atomic.Bool
	hang := make(chan struct{})
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if silent.Load() {
			<-hang
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(rs.Close)
	t.Cleanup(func() { close(hang) })
	u, _ := url.Parse(rs.URL)
	s := config.Defaults()
	s.RemoteServer, s.RemotePort, s.RemoteKey = u.Hostname(), u.Port(), k.AuthKey
	if err := config.Save(f.settings, s); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("POST", "/api/tickets", oneTicket(4, "Mirror"), nil); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}

	silent.Store(true)
	for _, path := range []string{"/api/tickets/A/4/4", "/api/tickets/A/4", "/api/tickets/A"} {
		began := time.Now()
		code, body := f.do("GET", path, nil, nil)
		took := time.Since(began)
		if code != 200 {
			t.Fatalf("%s with the server silent = %d %s", path, code, body)
		}
		if took > 2*time.Second {
			t.Fatalf("%s with the server silent took %s; it must give up after about %s and read the client's copy", path, took, readTimeout)
		}
	}
	_, body := f.do("GET", "/api/tickets/A/4/4", nil, nil)
	if rows := decode[[]store.Ticket](t, body); len(rows) != 1 || rows[0].FirstName != "Mirror" {
		t.Fatalf("the sheet from the client's copy = %+v, want the saved row", rows)
	}
}
