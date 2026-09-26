package client

// Compatibility tests: the Go client against whatever server TAM_COMPAT_SERVER
// points at (the original FastAPI server, or tam-server). They pair the
// client with the server using TAM_COMPAT_PASSWORD (default "changeme") and
// then drive every route through the client. scripts/compat/run.sh sets
// this up; without the variable the tests are skipped.

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

func compatFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	target := os.Getenv("TAM_COMPAT_SERVER")
	if target == "" {
		t.Skip("TAM_COMPAT_SERVER is not set")
	}
	password := os.Getenv("TAM_COMPAT_PASSWORD")
	if password == "" {
		password = "changeme"
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("TAM_COMPAT_SERVER %q: %v", target, err)
	}
	f := newFixture(t)
	code, body := f.do("POST", "/api/pair", map[string]any{"host": u.Hostname(), "port": u.Port(), "tls": u.Scheme == "https", "password": password}, nil)
	if code != 200 {
		t.Fatalf("pair with %s: %d %s", target, code, body)
	}
	f.h.sync.Tick()
	// Every run works in its own prefix so leftovers on a shared server
	// never collide.
	prefix := fmt.Sprintf("C%d", time.Now().UnixNano()%1000000)
	return f, prefix
}

func TestCompatEverything(t *testing.T) {
	f, p := compatFixture(t)
	_, body := f.do("GET", "/api", nil, nil)
	root := decode[map[string]any](t, body)
	if root["whoami"] != "TAM Server" || root["authenticated"] != true || root["healthy"] != true {
		t.Fatalf("root through the client = %v", root)
	}
	_, body = f.do("GET", "/api/status", nil, nil)
	if st := decode[map[string]any](t, body); st["state"] != "connected" {
		t.Fatalf("status = %v", st)
	}

	// Prefixes.
	if code, body := f.do("POST", "/api/prefixes", []store.Prefix{{Prefix: p, Color: "green", Weight: 9}}, nil); code != 200 {
		t.Fatalf("save prefix: %d %s", code, body)
	}
	_, body = f.do("GET", "/api/prefixes", nil, nil)
	found := false
	for _, x := range decode[[]store.Prefix](t, body) {
		if x.Prefix == p && x.Color == "green" && x.Weight == 9 {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved prefix missing from the list: %s", body)
	}

	// Tickets: batch save, list, range with placeholders, single, search.
	tickets := []store.Ticket{
		{Prefix: p, TID: 1, FirstName: "Ann", LastName: "Compat", PhoneNumber: "555-0001", Pref: "CALL"},
		{Prefix: p, TID: 3, FirstName: "Bob", LastName: "Compat", PhoneNumber: "555-0003", Pref: "TEXT"},
	}
	if code, body := f.do("POST", "/api/tickets", tickets, nil); code != 200 {
		t.Fatalf("save tickets: %d %s", code, body)
	}
	_, body = f.do("GET", "/api/tickets/"+p, nil, nil)
	if got := decode[[]store.Ticket](t, body); len(got) != 2 || got[0].FirstName != "Ann" || got[1].Pref != "TEXT" {
		t.Fatalf("tickets by prefix = %+v", got)
	}
	_, body = f.do("GET", "/api/tickets/"+p+"/1/3", nil, nil)
	if rng := decode[[]store.Ticket](t, body); len(rng) != 3 || rng[0].FirstName != "Ann" || rng[1].FirstName != "" || rng[1].TID != 2 || rng[2].FirstName != "Bob" {
		t.Fatalf("ticket range = %+v", rng)
	}
	_, body = f.do("GET", "/api/tickets/"+p+"/3", nil, nil)
	if one := decode[store.Ticket](t, body); one.LastName != "Compat" {
		t.Fatalf("single ticket = %+v", one)
	}
	_, body = f.do("GET", "/api/search/tickets?last_name=Compat&first_name=&phone_number=", nil, nil)
	hits := 0
	for _, x := range decode[[]store.Ticket](t, body) {
		if x.Prefix == p {
			hits++
		}
	}
	if hits != 2 {
		t.Fatalf("search found %d rows in %s: %s", hits, p, body)
	}
	if code, body := f.do("POST", "/api/search/tickets", []store.Ticket{{Prefix: p, TID: 3, FirstName: "Bobby", LastName: "Compat", PhoneNumber: "555-0003", Pref: "TEXT"}}, nil); code != 200 {
		t.Fatalf("save from search: %d %s", code, body)
	}
	_, body = f.do("GET", "/api/tickets/"+p+"/3", nil, nil)
	if one := decode[store.Ticket](t, body); one.FirstName != "Bobby" {
		t.Fatalf("ticket after search save = %+v", one)
	}

	// Baskets and drawing.
	baskets := []store.Basket{
		{Prefix: p, BID: 1, Description: "Wine", Donors: "Smiths"},
		{Prefix: p, BID: 2, Description: "Spa", Donors: "Local Spa"},
	}
	if code, body := f.do("POST", "/api/baskets", baskets, nil); code != 200 {
		t.Fatalf("save baskets: %d %s", code, body)
	}
	_, body = f.do("GET", "/api/baskets/"+p+"/1/2", nil, nil)
	if rng := decode[[]store.Basket](t, body); len(rng) != 2 || rng[1].Description != "Spa" {
		t.Fatalf("basket range = %+v", rng)
	}
	if code, body := f.do("POST", "/api/drawing", []store.Basket{{Prefix: p, BID: 1, WinningTicket: 3}, {Prefix: p, BID: 2, WinningTicket: 77}}, nil); code != 200 {
		t.Fatalf("save drawing: %d %s", code, body)
	}
	_, body = f.do("GET", "/api/drawing/"+p, nil, nil)
	lines := decode[[]store.DrawingLine](t, body)
	if len(lines) != 2 || lines[0].WinningTicket != 3 || lines[0].FirstName != "Bobby" || lines[1].WinningTicket != 77 || lines[1].FirstName != "" {
		t.Fatalf("drawing lines = %+v", lines)
	}

	// Reports.
	_, body = f.do("GET", "/api/reports/byname/"+p, nil, nil)
	if rows := decode[[]store.ReportByNameLine](t, body); len(rows) != 2 {
		t.Fatalf("by name = %+v", rows)
	}
	_, body = f.do("GET", "/api/reports/bybasket/"+p, nil, nil)
	if rows := decode[[]store.ReportByBasketLine](t, body); len(rows) != 2 || rows[0].Description != "Wine" {
		t.Fatalf("by basket = %+v", rows)
	}
	_, body = f.do("GET", "/api/reports/counts", nil, nil)
	counted := false
	for _, x := range decode[[]store.ReportCountLine](t, body) {
		if x.Prefix == p && x.TotalBuys == 2 && x.UniqueBuyers == 2 {
			counted = true
		}
	}
	if !counted {
		t.Fatalf("counts lack %s with 2 buys: %s", p, body)
	}

	// Backup: the server's file downloads through the client, push and
	// restore reach the server, and the local mirror holds the same rows.
	_, body = f.do("GET", "/api/backuprestore/remote", nil, nil)
	bf := decode[store.BackupFile](t, body)
	if bf.Prefixes == nil || bf.Tickets == nil || bf.Baskets == nil {
		t.Fatalf("remote backup must carry all three lists: %s", body)
	}
	if code, body := f.do("POST", "/api/backuprestore/push/tickets", `{}`, nil); code != 200 {
		t.Fatalf("push: %d %s", code, body)
	}
	restore := store.NewBackupFile()
	restore.Baskets = []store.Basket{{Prefix: p, BID: 2, Description: "Spa day", Donors: "Local Spa", WinningTicket: 1}}
	if code, body := f.do("POST", "/api/backuprestore/remote", restore, nil); code != 200 {
		t.Fatalf("restore to server: %d %s", code, body)
	}
	_, body = f.do("GET", "/api/baskets/"+p+"/2", nil, nil)
	b := decode[store.Basket](t, body)
	if b.Description != "Spa day" {
		t.Fatalf("basket after restore = %+v (a restore overwrites the description)", b)
	}
	if b.WinningTicket != 1 {
		// The original server leaves winning tickets alone on a restore;
		// tam-server overwrites them. Both are accepted here.
		t.Logf("this server keeps winning tickets on a restore (got %d): the original's behaviour", b.WinningTicket)
	}
	if lt, _ := f.st.Ticket(p, 1); lt == nil || lt.FirstName != "Ann" {
		t.Fatalf("mirror lacks the saved ticket: %+v", lt)
	}

	// Keys through the proxy, then the prefix goes away.
	code, body := f.do("POST", "/api/auth", `{"description":"compat extra"}`, map[string]string{"TAM-PWD": passwordFor(t)})
	if code != 200 {
		t.Fatalf("create key: %d %s", code, body)
	}
	extra := decode[store.AuthKey](t, body)
	if extra.AuthKey == "" {
		t.Fatalf("created key = %s", body)
	}
	if code, _ := f.do("DELETE", "/api/auth?key_to_del="+extra.AuthKey, nil, map[string]string{"TAM-PWD": passwordFor(t)}); code != 200 {
		t.Fatalf("delete key = %d", code)
	}
	if code, _ := f.do("GET", "/api/auth", nil, map[string]string{"TAM-PWD": "wrong-" + passwordFor(t)}); code != 401 {
		t.Fatalf("wrong password = %d, want 401", code)
	}
	code, body = f.do("DELETE", "/api/prefixes?p="+url.QueryEscape(p), nil, nil)
	if code != 200 || !strings.Contains(string(body), p) {
		t.Fatalf("delete prefix = %d %s", code, body)
	}
	_, body = f.do("GET", "/api/prefixes", nil, nil)
	for _, x := range decode[[]store.Prefix](t, body) {
		if x.Prefix == p {
			t.Fatalf("prefix still listed after delete: %s", body)
		}
	}
}

func passwordFor(t *testing.T) string {
	t.Helper()
	if pw := os.Getenv("TAM_COMPAT_PASSWORD"); pw != "" {
		return pw
	}
	return "changeme"
}
