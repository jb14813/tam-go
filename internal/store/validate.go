package store

import (
	"errors"
	"fmt"
	"strings"
)

// Colors is the palette the pages know how to render.
var Colors = []string{"white", "blue", "yellow", "green", "orange", "purple", "red"}

func validColor(c string) bool {
	for _, x := range Colors {
		if x == c {
			return true
		}
	}
	return false
}

// ValidatePrefixes trims names and rejects empty names, unknown colours and
// negative weights.
func ValidatePrefixes(ps []Prefix) error {
	for i := range ps {
		ps[i].Prefix = strings.TrimSpace(ps[i].Prefix)
		if ps[i].Prefix == "" {
			return errors.New("prefix name must not be empty")
		}
		if !validColor(ps[i].Color) {
			return fmt.Errorf("color %q is not one of %s", ps[i].Color, strings.Join(Colors, ", "))
		}
		if ps[i].Weight < 0 {
			return errors.New("weight must be zero or more")
		}
	}
	return nil
}

// ValidateTickets rejects tickets without a prefix, with negative ids or
// with a contact preference other than CALL, TEXT or empty.
func ValidateTickets(ts []Ticket) error {
	for i := range ts {
		ts[i].Prefix = strings.TrimSpace(ts[i].Prefix)
		if ts[i].Prefix == "" {
			return errors.New("ticket prefix must not be empty")
		}
		if ts[i].TID < 0 {
			return errors.New("ticket id must be zero or more")
		}
		switch ts[i].Pref {
		case "", "CALL", "TEXT":
		default:
			return fmt.Errorf("pref %q must be CALL or TEXT", ts[i].Pref)
		}
	}
	return nil
}

// ValidateBaskets rejects baskets without a prefix or with negative ids.
func ValidateBaskets(bs []Basket) error {
	for i := range bs {
		bs[i].Prefix = strings.TrimSpace(bs[i].Prefix)
		if bs[i].Prefix == "" {
			return errors.New("basket prefix must not be empty")
		}
		if bs[i].BID < 0 {
			return errors.New("basket id must be zero or more")
		}
		if bs[i].WinningTicket < 0 {
			return errors.New("winning ticket must be zero or more")
		}
	}
	return nil
}

// ValidateBackup validates every list of a backup file and replaces nil
// lists with empty ones.
func ValidateBackup(bf *BackupFile) error {
	if bf.Prefixes == nil {
		bf.Prefixes = []Prefix{}
	}
	if bf.Baskets == nil {
		bf.Baskets = []Basket{}
	}
	if bf.Tickets == nil {
		bf.Tickets = []Ticket{}
	}
	if err := ValidatePrefixes(bf.Prefixes); err != nil {
		return err
	}
	if err := ValidateBaskets(bf.Baskets); err != nil {
		return err
	}
	return ValidateTickets(bf.Tickets)
}
