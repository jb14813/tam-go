package store

import (
	"strings"
	"testing"
)

// TestBackupKeepsPrefixesAsTheyAre: the rules for a new prefix name (no /
// or \, not . or .., at most 100 characters, trimmed) are for names typed
// now. A backup carries the names a database already has, possibly from the
// original app, which allowed any: restoring it, or a client copying its
// server's data, takes them as they are. Renaming one would cut it off from
// its tickets and baskets, and refusing it would refuse the whole backup.
func TestBackupKeepsPrefixesAsTheyAre(t *testing.T) {
	legacy := []string{"A/B", `C\D`, ".", "..", " E ", strings.Repeat("F", 120)}
	bf := NewBackupFile()
	for i, name := range legacy {
		bf.Prefixes = append(bf.Prefixes, Prefix{Prefix: name, Color: "red", Weight: i - 1})
	}
	if err := ValidateBackup(&bf); err != nil {
		t.Fatalf("a backup with the names %q was refused: %v", legacy, err)
	}
	for i, p := range bf.Prefixes {
		if p.Prefix != legacy[i] || p.Weight != i-1 {
			t.Fatalf("prefix %d came out as %+v, want %q with weight %d", i, p, legacy[i], i-1)
		}
	}

	empty := NewBackupFile()
	empty.Prefixes = []Prefix{{Prefix: "", Color: "red"}}
	if err := ValidateBackup(&empty); err == nil {
		t.Fatal("a prefix without a name must still be refused")
	}
}

// A restored prefix is an identity, not new text to normalize. Editing its
// tickets, baskets or drawing must update those rows instead of creating
// disconnected rows under a trimmed name.
func TestFormSavesKeepRestoredPrefixIdentity(t *testing.T) {
	for _, prefix := range []string{" E ", "\u00a0E\u00a0"} {
		t.Run(prefix, func(t *testing.T) {
			s := newTestStore(t)
			bf := NewBackupFile()
			bf.Prefixes = []Prefix{{Prefix: prefix, Color: "red"}}
			bf.Tickets = []Ticket{{Prefix: prefix, TID: 7, FirstName: "Before", Pref: "CALL"}}
			bf.Baskets = []Basket{{Prefix: prefix, BID: 3, Description: "Before"}}
			must(t, ValidateBackup(&bf))
			must(t, s.Import(bf))

			tickets, err := s.TicketsByPrefix(prefix)
			must(t, err)
			tickets[0].FirstName = "After"
			must(t, ValidateTickets(tickets))
			must(t, s.UpsertTickets(tickets))
			ticket, err := s.Ticket(prefix, 7)
			must(t, err)
			if ticket == nil || ticket.FirstName != "After" {
				t.Errorf("ticket save did not update the restored row: %+v", ticket)
			}

			baskets, err := s.BasketsByPrefix(prefix)
			must(t, err)
			baskets[0].Description = "After"
			must(t, ValidateBaskets(baskets))
			must(t, s.UpsertBaskets(baskets))
			basket, err := s.Basket(prefix, 3)
			must(t, err)
			if basket == nil || basket.Description != "After" {
				t.Errorf("basket save did not update the restored row: %+v", basket)
			}

			winning := []Basket{{Prefix: prefix, BID: 3, WinningTicket: 7}}
			must(t, ValidateBaskets(winning))
			must(t, s.UpsertWinning(winning))
			drawing, err := s.DrawingLine(prefix, 3)
			must(t, err)
			if drawing == nil || drawing.WinningTicket != 7 || drawing.FirstName != "After" || drawing.Description != "After" {
				t.Errorf("drawing save did not keep the restored basket and ticket together: %+v", drawing)
			}

			strayTicket, err := s.Ticket("E", 7)
			must(t, err)
			strayBasket, err := s.Basket("E", 3)
			must(t, err)
			if strayTicket != nil || strayBasket != nil {
				t.Errorf("saves created rows under a different prefix: ticket %+v, basket %+v", strayTicket, strayBasket)
			}
		})
	}
}

func TestPrefixMetadataChangesKeepRestoredNames(t *testing.T) {
	for _, prefix := range []string{" E ", "A/B", ".", strings.Repeat("L", 101)} {
		t.Run(prefix, func(t *testing.T) {
			s := newTestStore(t)
			bf := NewBackupFile()
			bf.Prefixes = []Prefix{{Prefix: prefix, Color: "red", Weight: 1}}
			must(t, ValidateBackup(&bf))
			must(t, s.Import(bf))
			existing, err := s.ListPrefixes()
			must(t, err)
			changes := []Prefix{{Prefix: prefix, Color: "blue", Weight: 2}}
			must(t, ValidatePrefixChanges(changes, existing))
			must(t, s.UpsertPrefixes(changes))
			got, err := s.ListPrefixes()
			must(t, err)
			if len(got) != 1 || got[0] != (Prefix{Prefix: prefix, Color: "blue", Weight: 2}) {
				t.Fatalf("metadata save changed the restored prefix identity: %+v", got)
			}
		})
	}
}

func TestPrefixChangesStillValidateNewNames(t *testing.T) {
	existing := []Prefix{{Prefix: " E ", Color: "red", Weight: 1}}
	for _, name := range []string{"", " ", "A/B", ".", strings.Repeat("L", 101)} {
		if err := ValidatePrefixChanges([]Prefix{{Prefix: name, Color: "blue"}}, existing); err == nil {
			t.Errorf("new prefix %q should be refused", name)
		}
	}
	newPrefix := []Prefix{{Prefix: " N ", Color: "blue"}}
	must(t, ValidatePrefixChanges(newPrefix, existing))
	if newPrefix[0].Prefix != "N" {
		t.Fatalf("new prefix = %q, want normalized N", newPrefix[0].Prefix)
	}
	for _, change := range []Prefix{{Prefix: " E ", Color: "unknown"}, {Prefix: " E ", Color: "blue", Weight: -1}} {
		if err := ValidatePrefixChanges([]Prefix{change}, existing); err == nil {
			t.Errorf("existing prefix's invalid metadata should be refused: %+v", change)
		}
	}
}
