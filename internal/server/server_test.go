package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/store"
)

type api struct {
	t   *testing.T
	url string
	st  *store.Store
	key string
}

func newAPI(t *testing.T) *api {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqldb)
	k, err := st.CreateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(st, "secret"))
	t.Cleanup(ts.Close)
	return &api{t: t, url: ts.URL, st: st, key: k.AuthKey}
}

// do sends a request. body may be nil, a string (sent verbatim as JSON) or
// any value (marshalled).
func (a *api) do(method, path string, body any, headers map[string]string) (int, []byte) {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			data, err := json.Marshal(b)
			if err != nil {
				a.t.Fatal(err)
			}
			rdr = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequest(method, a.url+path, rdr)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func (a *api) keyed(method, path string, body any) (int, []byte) {
	return a.do(method, path, body, map[string]string{"TAM-KEY": a.key})
}

func (a *api) pw(method, path string, body any) (int, []byte) {
	return a.do(method, path, body, map[string]string{"TAM-PW": "secret"})
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

func TestKeyRequiredOnDataRoutes(t *testing.T) {
	a := newAPI(t)
	if code, body := a.do("GET", "/api/prefixes", nil, nil); code != 401 || !strings.Contains(string(body), "Invalid Key") {
		t.Fatalf("no key: %d %s", code, body)
	}
	if code, _ := a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": "WRONG"}); code != 401 {
		t.Fatalf("bad key: %d", code)
	}
	if code, _ := a.keyed("GET", "/api/prefixes", nil); code != 200 {
		t.Fatalf("good key: %d", code)
	}
	if code, _ := a.do("POST", "/api/tickets", `[]`, nil); code != 401 {
		t.Fatalf("POST without key: %d", code)
	}
}

func TestPasswordProtectsKeyManagement(t *testing.T) {
	a := newAPI(t)
	if code, body := a.do("GET", "/api/auth", nil, nil); code != 401 || !strings.Contains(string(body), "Invalid Password") {
		t.Fatalf("no password: %d %s", code, body)
	}
	if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": "nope"}); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-KEY": a.key}); code != 401 {
		t.Fatalf("a data key must not open key management: %d", code)
	}
}

func TestKeyLifecycle(t *testing.T) {
	a := newAPI(t)
	code, body := a.pw("POST", "/api/auth", map[string]string{"description": "laptop"})
	if code != 200 {
		t.Fatalf("create key: %d %s", code, body)
	}
	k := decode[store.AuthKey](t, body)
	if len(k.AuthKey) != 32 || k.Description != "laptop" {
		t.Fatalf("created key = %+v", k)
	}

	code, body = a.do("GET", "/api", nil, map[string]string{"TAM-KEY": k.AuthKey})
	root := decode[map[string]any](t, body)
	if code != 200 || root["whoami"] != "TAM Server" || root["authenticated"] != true || root["healthy"] != true {
		t.Fatalf("root with key = %d %v", code, root)
	}
	_, body = a.do("GET", "/api", nil, nil)
	if root = decode[map[string]any](t, body); root["authenticated"] != false {
		t.Fatalf("root without key = %v", root)
	}

	_, body = a.pw("GET", "/api/auth", nil)
	if list := decode[[]store.AuthKey](t, body); len(list) != 2 {
		t.Fatalf("ListKeys = %v", list)
	}

	if code, _ = a.pw("DELETE", "/api/auth?key_to_del="+k.AuthKey, nil); code != 200 {
		t.Fatalf("delete key: %d", code)
	}
	if code, _ = a.pw("DELETE", "/api/auth?key_to_del="+k.AuthKey, nil); code != 404 {
		t.Fatalf("delete missing key: %d, want 404", code)
	}
	if code, _ = a.do("GET", "/api/prefixes", nil, map[string]string{"TAM-KEY": k.AuthKey}); code != 401 {
		t.Fatalf("deleted key still works: %d", code)
	}
}

func TestPrefixes(t *testing.T) {
	a := newAPI(t)
	code, body := a.keyed("POST", "/api/prefixes", []store.Prefix{{Prefix: "B", Color: "blue", Weight: 2}, {Prefix: " A ", Color: "red", Weight: 1}})
	if code != 200 {
		t.Fatalf("post: %d %s", code, body)
	}
	_, body = a.keyed("GET", "/api/prefixes", nil)
	ps := decode[[]store.Prefix](t, body)
	if len(ps) != 2 || ps[0].Prefix != "A" || ps[1].Prefix != "B" {
		t.Fatalf("list = %v", ps)
	}

	// Nothing is written when any item is invalid.
	for _, bad := range []string{
		`[{"prefix":"X","color":"red","weight":"heavy"}]`,
		`[{"prefix":"","color":"red","weight":1}]`,
		`[{"prefix":"   ","color":"red","weight":1}]`,
		`[{"prefix":"X","color":"chartreuse","weight":1}]`,
		`[{"prefix":"X","color":"red","weight":-1}]`,
		`{bad`,
	} {
		if code, _ := a.keyed("POST", "/api/prefixes", bad); code != 400 {
			t.Errorf("POST %s: %d, want 400", bad, code)
		}
	}
	_, body = a.keyed("GET", "/api/prefixes", nil)
	if ps = decode[[]store.Prefix](t, body); len(ps) != 2 {
		t.Fatalf("invalid posts must not write rows: %v", ps)
	}

	if code, _ = a.do("POST", "/api/prefixes", `[]`, map[string]string{"TAM-KEY": a.key, "Content-Type": "text/plain"}); code != 400 {
		t.Fatalf("text/plain body: %d, want 400", code)
	}

	if code, _ = a.keyed("DELETE", "/api/prefixes?p=A", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = a.keyed("DELETE", "/api/prefixes?p=A", nil); code != 404 {
		t.Fatalf("delete missing: %d, want 404", code)
	}
	if code, _ = a.keyed("DELETE", "/api/prefixes", nil); code != 400 {
		t.Fatalf("delete without p: %d, want 400", code)
	}
}

func TestTicketsBasketsDrawingReports(t *testing.T) {
	a := newAPI(t)
	code, body := a.keyed("POST", "/api/tickets", []store.Ticket{
		{Prefix: "A", TID: 1, FirstName: "Zed", LastName: "Young", PhoneNumber: "1", Pref: "CALL"},
		{Prefix: "A", TID: 2, FirstName: "Amy", LastName: "Adams", PhoneNumber: "2", Pref: "TEXT"},
	})
	if code != 200 {
		t.Fatalf("post tickets: %d %s", code, body)
	}

	_, body = a.keyed("GET", "/api/tickets", nil)
	if all := decode[[]store.Ticket](t, body); len(all) != 2 {
		t.Fatalf("all tickets = %v", all)
	}
	_, body = a.keyed("GET", "/api/tickets/A", nil)
	if byPrefix := decode[[]store.Ticket](t, body); len(byPrefix) != 2 {
		t.Fatalf("tickets by prefix = %v", byPrefix)
	}
	_, body = a.keyed("GET", "/api/tickets/A/2", nil)
	if single := decode[[]store.Ticket](t, body); len(single) != 1 || single[0].FirstName != "Amy" {
		t.Fatalf("single ticket = %v", single)
	}
	_, body = a.keyed("GET", "/api/tickets/A/9", nil)
	if missing := decode[[]store.Ticket](t, body); len(missing) != 0 {
		t.Fatalf("missing ticket = %v, want []", missing)
	}
	_, body = a.keyed("GET", "/api/tickets/A/2/1", nil) // reversed range is swapped
	if rng := decode[[]store.Ticket](t, body); len(rng) != 2 {
		t.Fatalf("range = %v", rng)
	}
	if code, _ = a.keyed("GET", "/api/tickets/A/x", nil); code != 400 {
		t.Fatalf("non-integer id: %d", code)
	}
	if code, _ = a.keyed("POST", "/api/tickets", `[{"prefix":"A/B","t_id":3,"pref":"CALL"}]`); code != 400 {
		t.Fatalf("prefix with a slash: %d, want 400", code)
	}
	if code, _ = a.keyed("POST", "/api/tickets", `[{"prefix":"A","t_id":-3,"pref":"CALL"}]`); code != 400 {
		t.Fatalf("negative id: %d, want 400", code)
	}

	code, body = a.keyed("POST", "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "Wine", Donors: "Smiths"}})
	if code != 200 {
		t.Fatalf("post baskets: %d %s", code, body)
	}
	_, body = a.keyed("GET", "/api/baskets/A/1", nil)
	if b := decode[[]store.Basket](t, body); len(b) != 1 || b[0].Description != "Wine" {
		t.Fatalf("single basket = %v", b)
	}

	// The drawing form sends the whole drawing line; extra fields are ignored.
	code, body = a.keyed("POST", "/api/drawing", `[{"prefix":"A","b_id":1,"description":"Wine","winning_ticket":2,"last_name":"","changed":true}]`)
	if code != 200 {
		t.Fatalf("post drawing: %d %s", code, body)
	}
	_, body = a.keyed("GET", "/api/drawing/A/1", nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 1 || d[0].WinningTicket != 2 || d[0].LastName != "Adams" {
		t.Fatalf("drawing line = %v", d)
	}
	_, body = a.keyed("GET", "/api/drawing/A/1/3", nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 1 {
		t.Fatalf("drawing range = %v", d)
	}
	_, body = a.keyed("GET", "/api/drawing", nil)
	if d := decode[[]store.DrawingLine](t, body); len(d) != 1 {
		t.Fatalf("all drawing = %v", d)
	}
	_, body = a.keyed("GET", "/api/baskets/A", nil)
	if b := decode[[]store.Basket](t, body); len(b) != 1 || b[0].Description != "Wine" || b[0].WinningTicket != 2 {
		t.Fatalf("baskets by prefix after drawing = %v", b)
	}

	_, body = a.keyed("GET", "/api/reports/byname/A", nil)
	if r := decode[[]store.ReportByNameLine](t, body); len(r) != 1 || r[0].LastName != "Adams" || r[0].Description != "Wine" {
		t.Fatalf("by name = %v", r)
	}
	_, body = a.keyed("GET", "/api/reports/bybasket/A", nil)
	if r := decode[[]store.ReportByBasketLine](t, body); len(r) != 1 || r[0].BID != 1 {
		t.Fatalf("by basket = %v", r)
	}
	_, body = a.keyed("GET", "/api/reports/counts", nil)
	counts := decode[[]store.ReportCountLine](t, body)
	if len(counts) != 2 || counts[1].Prefix != "Total" || counts[1].TotalBuys != 2 {
		t.Fatalf("counts = %v", counts)
	}

	_, body = a.keyed("GET", "/api/search/tickets?last_name=ada", nil)
	if found := decode[[]store.Ticket](t, body); len(found) != 1 || found[0].TID != 2 {
		t.Fatalf("search = %v", found)
	}
	code, _ = a.keyed("POST", "/api/search/tickets", []store.Ticket{{Prefix: "A", TID: 2, FirstName: "Amy", LastName: "Adams-Lee", PhoneNumber: "2", Pref: "TEXT"}})
	if code != 200 {
		t.Fatalf("search post: %d", code)
	}
	_, body = a.keyed("GET", "/api/tickets/A/2", nil)
	if single := decode[[]store.Ticket](t, body); single[0].LastName != "Adams-Lee" {
		t.Fatalf("search post did not update: %v", single)
	}
}

func TestBackupRoundTrip(t *testing.T) {
	a := newAPI(t)
	a.keyed("POST", "/api/prefixes", []store.Prefix{{Prefix: "A", Color: "red", Weight: 1}})
	a.keyed("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "F", LastName: "L", PhoneNumber: "P", Pref: "CALL"}})
	a.keyed("POST", "/api/baskets", []store.Basket{{Prefix: "A", BID: 1, Description: "D"}})

	_, body := a.keyed("GET", "/api/backuprestore", nil)
	bf := decode[store.BackupFile](t, body)
	if len(bf.Prefixes) != 1 || len(bf.Tickets) != 1 || len(bf.Baskets) != 1 {
		t.Fatalf("export = %+v", bf)
	}

	other := newAPI(t)
	code, body := other.keyed("POST", "/api/backuprestore", bf)
	if code != 200 || !strings.Contains(string(body), "imported successfully") {
		t.Fatalf("import: %d %s", code, body)
	}
	_, body = other.keyed("GET", "/api/backuprestore", nil)
	if bf2 := decode[store.BackupFile](t, body); len(bf2.Tickets) != 1 || bf2.Tickets[0].FirstName != "F" {
		t.Fatalf("import result = %+v", bf2)
	}
	if code, _ = other.keyed("POST", "/api/backuprestore", `{"prefixes":[{"prefix":"","color":"red","weight":1}]}`); code != 400 {
		t.Fatalf("invalid backup: %d", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	a := newAPI(t)
	if code, _ := a.keyed("DELETE", "/api/tickets", nil); code != 405 {
		t.Fatalf("DELETE /api/tickets = %d, want 405", code)
	}
	if code, _ := a.keyed("PUT", "/api/prefixes", `[]`); code != 405 {
		t.Fatalf("PUT /api/prefixes = %d, want 405", code)
	}
}
