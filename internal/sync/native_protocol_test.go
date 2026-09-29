package sync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestHTMLSuccessRetainsUnacceptedQueue(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>Wrong proxy route</html>"))
	}))
	defer web.Close()
	s, st := newSyncer(t, web.URL)
	rows := []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Never accepted"}}
	body, _ := json.Marshal(rows)
	order, err := st.NextSave("audit")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQueued("POST", "/api/tickets", body, order, func(st *store.Store) error { return st.UpsertTickets(rows) }); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	p, f := pendingFailed(t, st)
	if p != 1 || f != 0 || s.State() == Connected {
		t.Fatalf("unaccepted request must remain pending: state=%s pending=%d failed=%d", s.State(), p, f)
	}
}
