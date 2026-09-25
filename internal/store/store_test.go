package store

import (
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	return New(sqldb)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrefixes(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertPrefixes([]Prefix{{"B", "blue", 2}, {"A", "red", 1}}))

	got, err := s.ListPrefixes()
	must(t, err)
	want := []Prefix{{"A", "red", 1}, {"B", "blue", 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPrefixes = %v, want %v", got, want)
	}

	must(t, s.UpsertPrefixes([]Prefix{{"A", "green", 1}}))
	got, _ = s.ListPrefixes()
	if got[0].Color != "green" {
		t.Fatalf("upsert did not update colour: %v", got)
	}

	n, err := s.DeletePrefix("A")
	must(t, err)
	if n != 1 {
		t.Fatalf("DeletePrefix affected %d rows, want 1", n)
	}
	n, _ = s.DeletePrefix("A")
	if n != 0 {
		t.Fatalf("second DeletePrefix affected %d rows, want 0", n)
	}

	empty := newTestStore(t)
	list, err := empty.ListPrefixes()
	must(t, err)
	if list == nil || len(list) != 0 {
		t.Fatalf("empty list should be a non-nil empty slice, got %#v", list)
	}
}

func TestTickets(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{
		{"A", 3, "Cara", "Lee", "555-0003", "TEXT"},
		{"A", 1, "Joan", "Smith", "555-0001", "CALL"},
		{"B", 1, "Bob", "Jones", "555-0100", "CALL"},
		{"A", 2, "Jo", "Kim", "555-0002", "CALL"},
	}))

	all, err := s.AllTickets()
	must(t, err)
	if len(all) != 4 || all[0].TID != 1 || all[0].Prefix != "A" || all[3].Prefix != "B" {
		t.Fatalf("AllTickets order wrong: %v", all)
	}

	byPrefix, _ := s.TicketsByPrefix("A")
	if len(byPrefix) != 3 || byPrefix[2].TID != 3 {
		t.Fatalf("TicketsByPrefix = %v", byPrefix)
	}

	one, err := s.Ticket("A", 2)
	must(t, err)
	if one == nil || one.FirstName != "Jo" {
		t.Fatalf("Ticket(A,2) = %v", one)
	}
	missing, err := s.Ticket("A", 9)
	must(t, err)
	if missing != nil {
		t.Fatalf("Ticket(A,9) should be nil, got %v", missing)
	}

	rng, _ := s.TicketRange("A", 2, 3)
	if len(rng) != 2 || rng[0].TID != 2 || rng[1].TID != 3 {
		t.Fatalf("TicketRange = %v", rng)
	}

	must(t, s.UpsertTickets([]Ticket{{"A", 1, "Joan", "Smith-Ng", "555-0001", "TEXT"}}))
	one, _ = s.Ticket("A", 1)
	if one.LastName != "Smith-Ng" || one.Pref != "TEXT" {
		t.Fatalf("upsert did not update: %v", one)
	}

	found, err := s.SearchTickets("jo", "", "")
	must(t, err)
	if len(found) != 2 {
		t.Fatalf("SearchTickets(jo) = %v, want Joan and Jo", found)
	}
	found, _ = s.SearchTickets("", "", "0100")
	if len(found) != 1 || found[0].Prefix != "B" {
		t.Fatalf("SearchTickets(phone) = %v", found)
	}
	found, _ = s.SearchTickets("", "", "")
	if len(found) != 4 {
		t.Fatalf("empty search should match everything, got %d", len(found))
	}
}

func TestBasketsAndDrawing(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{{"A", 5, "Winnie", "Won", "555-0005", "CALL"}}))
	must(t, s.UpsertBaskets([]Basket{
		{"A", 1, "Wine", "The Smiths", 0},
		{"A", 2, "Spa day", "", 0},
	}))

	// The drawing form only sets winning tickets, and may name a basket that
	// does not exist yet.
	must(t, s.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 5}, {Prefix: "A", BID: 3, WinningTicket: 7}}))

	line, err := s.DrawingLine("A", 1)
	must(t, err)
	if line == nil || line.WinningTicket != 5 || line.LastName != "Won" || line.Description != "Wine" {
		t.Fatalf("DrawingLine(A,1) = %+v", line)
	}
	unclaimed, _ := s.DrawingLine("A", 2)
	if unclaimed == nil || unclaimed.LastName != "" || unclaimed.WinningTicket != 0 {
		t.Fatalf("DrawingLine(A,2) = %+v", unclaimed)
	}
	created, _ := s.Basket("A", 3)
	if created == nil || created.Description != "" || created.WinningTicket != 7 {
		t.Fatalf("basket created by the drawing form = %+v", created)
	}

	// Saving the baskets form again must not clobber a drawn winner.
	must(t, s.UpsertBaskets([]Basket{{"A", 1, "Red wine", "The Smiths", 0}}))
	b, _ := s.Basket("A", 1)
	if b.Description != "Red wine" || b.WinningTicket != 5 {
		t.Fatalf("UpsertBaskets must keep the winning ticket: %+v", b)
	}

	rng, _ := s.DrawingRange("A", 1, 3)
	if len(rng) != 3 {
		t.Fatalf("DrawingRange = %v", rng)
	}
	all, _ := s.AllDrawing()
	byPrefix, _ := s.DrawingByPrefix("A")
	if len(all) != 3 || len(byPrefix) != 3 {
		t.Fatalf("AllDrawing=%d DrawingByPrefix=%d", len(all), len(byPrefix))
	}
	if missing, _ := s.Basket("Z", 1); missing != nil {
		t.Fatal("Basket(Z,1) should be nil")
	}
	brange, _ := s.BasketRange("A", 2, 3)
	if len(brange) != 2 {
		t.Fatalf("BasketRange = %v", brange)
	}
	if bp, _ := s.BasketsByPrefix("A"); len(bp) != 3 {
		t.Fatalf("BasketsByPrefix = %v", bp)
	}
	if ab, _ := s.AllBaskets(); len(ab) != 3 {
		t.Fatalf("AllBaskets = %v", ab)
	}
}

func TestReports(t *testing.T) {
	s := newTestStore(t)
	must(t, s.UpsertTickets([]Ticket{
		{"A", 1, "Zed", "Young", "555-0001", "CALL"},
		{"A", 2, "Amy", "Adams", "555-0002", "TEXT"},
		{"A", 3, "Amy", "Adams", "555-0002", "TEXT"},
		{"B", 1, "Bea", "Brown", "555-0003", "CALL"},
	}))
	must(t, s.UpsertBaskets([]Basket{{"A", 1, "Wine", "", 1}, {"A", 2, "Spa", "", 2}, {"A", 3, "Books", "", 0}}))

	byName, err := s.ReportByName("A")
	must(t, err)
	if len(byName) != 3 {
		t.Fatalf("ReportByName = %v", byName)
	}
	// Undrawn basket sorts first (NULL name), then Adams, then Young.
	if byName[0].BID != 3 || byName[1].LastName != "Adams" || byName[2].LastName != "Young" {
		t.Fatalf("ReportByName order = %v", byName)
	}

	byBasket, _ := s.ReportByBasket("A")
	if len(byBasket) != 3 || byBasket[0].BID != 1 || byBasket[0].LastName != "Young" || byBasket[2].LastName != "" {
		t.Fatalf("ReportByBasket = %v", byBasket)
	}

	counts, _ := s.ReportCounts()
	got := map[string]ReportCountLine{}
	for _, c := range counts {
		got[c.Prefix] = c
	}
	if got["A"].TotalBuys != 3 || got["A"].UniqueBuyers != 2 || got["B"].TotalBuys != 1 {
		t.Fatalf("ReportCounts = %v", counts)
	}
	if got["Total"].TotalBuys != 4 || got["Total"].UniqueBuyers != 3 {
		t.Fatalf("ReportCounts total = %v", got["Total"])
	}
}

func TestAuthKeys(t *testing.T) {
	s := newTestStore(t)
	k, err := s.CreateKey("laptop 1")
	must(t, err)
	if !regexp.MustCompile(`^[A-Z0-9]{32}$`).MatchString(k.AuthKey) {
		t.Fatalf("key format: %q", k.AuthKey)
	}
	k2, _ := s.CreateKey("laptop 2")
	if k2.AuthKey == k.AuthKey {
		t.Fatal("keys must be unique")
	}
	ok, _ := s.KeyExists(k.AuthKey)
	if !ok {
		t.Fatal("KeyExists should be true")
	}
	if ok, _ := s.KeyExists(""); ok {
		t.Fatal("empty key must never exist")
	}
	list, _ := s.ListKeys()
	if len(list) != 2 || list[0].Description != "laptop 1" {
		t.Fatalf("ListKeys = %v", list)
	}
	n, _ := s.DeleteKey(k.AuthKey)
	if n != 1 {
		t.Fatalf("DeleteKey affected %d", n)
	}
	if ok, _ := s.KeyExists(k.AuthKey); ok {
		t.Fatal("deleted key still exists")
	}
}

func TestBackupRoundTrip(t *testing.T) {
	src := newTestStore(t)
	must(t, src.UpsertPrefixes([]Prefix{{"A", "red", 1}}))
	must(t, src.UpsertBaskets([]Basket{{"A", 1, "Wine", "Smiths", 0}}))
	must(t, src.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 2}}))
	must(t, src.UpsertTickets([]Ticket{{"A", 2, "Amy", "Adams", "555", "TEXT"}}))

	bf, err := src.Export()
	must(t, err)
	if len(bf.Prefixes) != 1 || len(bf.Baskets) != 1 || len(bf.Tickets) != 1 {
		t.Fatalf("Export = %+v", bf)
	}

	dst := newTestStore(t)
	must(t, dst.Import(bf))
	must(t, dst.Import(bf)) // restoring twice is harmless
	bf2, _ := dst.Export()
	if !reflect.DeepEqual(bf, bf2) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", bf, bf2)
	}
	if bf2.Baskets[0].WinningTicket != 2 {
		t.Fatal("restore must keep winning tickets")
	}

	empty := newTestStore(t)
	ebf, _ := empty.Export()
	if ebf.Prefixes == nil || ebf.Baskets == nil || ebf.Tickets == nil {
		t.Fatalf("Export of an empty store must use empty slices, got %+v", ebf)
	}
}
