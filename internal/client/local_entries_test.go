package client

import (
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestRemoteReadsDoNotAdoptServerRows(t *testing.T) {
	for _, path := range []string{
		"/api/tickets", "/api/tickets/A", "/api/tickets/A/1", "/api/tickets/A/1/2",
		"/api/search/tickets?first_name=Shared", "/api/baskets", "/api/baskets/A",
		"/api/baskets/A/1", "/api/baskets/A/1/2", "/api/drawing", "/api/drawing/A",
		"/api/drawing/A/1", "/api/drawing/A/1/2",
	} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			for route, data := range map[string]any{
				"/api/tickets": []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Entered here"}},
				"/api/baskets": []store.Basket{{Prefix: "A", BID: 1, Description: "Entered here", WinningTicket: 1}},
			} {
				if code, body := f.do("POST", route, data, nil); code != 200 {
					t.Fatalf("local save: %d %s", code, body)
				}
			}
			rst, _ := remoteFixture(t, f)
			if err := rst.Import(store.BackupFile{
				Tickets: []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Shared newer"}, {Prefix: "A", TID: 2, FirstName: "Shared other"}},
				Baskets: []store.Basket{{Prefix: "A", BID: 1, Description: "Shared newer", WinningTicket: 2}, {Prefix: "A", BID: 2, Description: "Shared other", WinningTicket: 2}},
			}); err != nil {
				t.Fatal(err)
			}
			code, body := f.do("GET", path, nil, nil)
			if code != 200 || !strings.Contains(string(body), "Shared") {
				t.Fatalf("online page must show the shared server rows: %d %s", code, body)
			}
			tickets, err := f.st.AllTickets()
			if err != nil || len(tickets) != 1 || tickets[0].TID != 1 || tickets[0].FirstName != "Entered here" {
				t.Errorf("remote read changed the locally entered tickets: %+v, %v", tickets, err)
			}
			baskets, err := f.st.AllBaskets()
			if err != nil || len(baskets) != 1 || baskets[0].BID != 1 || baskets[0].Description != "Entered here" || baskets[0].WinningTicket != 1 {
				t.Errorf("remote read changed the locally entered basket/drawing: %+v, %v", baskets, err)
			}
		})
	}
}

func TestRemotePrefixConfigurationRemainsAvailableOffline(t *testing.T) {
	f := newFixture(t)
	rst, rs := remoteFixture(t, f)
	if err := rst.UpsertPrefixes([]store.Prefix{{Prefix: " A ", Color: "blue", Weight: 1}}); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("GET", "/api/prefixes", nil, nil); code != 200 {
		t.Fatalf("online prefix configuration: %d %s", code, body)
	}
	rs.Close()
	f.h.sync.Tick()
	code, body := f.do("GET", "/api/prefixes", nil, nil)
	prefixes := decode[[]store.Prefix](t, body)
	if code != 200 || len(prefixes) != 1 || prefixes[0].Prefix != " A " {
		t.Fatalf("offline prefix configuration: %d %s", code, body)
	}
}
