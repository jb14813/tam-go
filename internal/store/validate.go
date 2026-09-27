package store

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Colors is the palette the pages know how to render.
var Colors = []string{"white", "blue", "yellow", "green", "orange", "purple", "red"}

// maxPrefixLen matches the VARCHAR(100) of the first schema.
const maxPrefixLen = 100

func validColor(c string) bool {
	for _, x := range Colors {
		if x == c {
			return true
		}
	}
	return false
}

// ValidatePrefixName trims a new prefix name and rejects names that cannot
// be used in a URL path segment or a sheet header. The names . and .. are
// path segments that browsers and Go's ServeMux resolve away before any
// handler sees them. It applies when prefixes are created; tickets and
// baskets only need a non-empty prefix, so data from an original database
// with an unusual prefix stays writable.
func ValidatePrefixName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("prefix name must not be empty")
	}
	if len(name) > maxPrefixLen {
		return "", fmt.Errorf("prefix name must be at most %d characters", maxPrefixLen)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", errors.New(`prefix name must not contain / or \`)
	}
	if name == "." || name == ".." {
		return "", errors.New("prefix name must not be . or ..")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("prefix name must not contain control characters")
		}
	}
	return name, nil
}

// ValidatePrefixes trims names and rejects empty or unusable names, unknown
// colours and negative weights. Errors name the offending row.
func ValidatePrefixes(ps []Prefix) error {
	for i := range ps {
		name, err := ValidatePrefixName(ps[i].Prefix)
		if err != nil {
			return fmt.Errorf("prefix %d: %w", i+1, err)
		}
		ps[i].Prefix = name
		if !validColor(ps[i].Color) {
			return fmt.Errorf("prefix %d (%s): color %q is not one of %s", i+1, name, ps[i].Color, strings.Join(Colors, ", "))
		}
		if ps[i].Weight < 0 {
			return fmt.Errorf("prefix %d (%s): weight must be zero or more", i+1, name)
		}
	}
	return nil
}

// ValidateTickets rejects tickets without a prefix or with a negative id.
// The contact preference is free text, as in the original; the pages send
// CALL or TEXT.
func ValidateTickets(ts []Ticket) error {
	for i := range ts {
		ts[i].Prefix = strings.TrimSpace(ts[i].Prefix)
		if ts[i].Prefix == "" {
			return fmt.Errorf("ticket %d: prefix must not be empty", i+1)
		}
		if ts[i].TID < 0 {
			return fmt.Errorf("ticket %d (%s/%d): id must be zero or more", i+1, ts[i].Prefix, ts[i].TID)
		}
		ts[i].Pref = strings.TrimSpace(ts[i].Pref)
	}
	return nil
}

// ValidateBaskets rejects baskets without a prefix or with negative ids.
func ValidateBaskets(bs []Basket) error {
	for i := range bs {
		bs[i].Prefix = strings.TrimSpace(bs[i].Prefix)
		if bs[i].Prefix == "" {
			return fmt.Errorf("basket %d: prefix must not be empty", i+1)
		}
		if bs[i].BID < 0 {
			return fmt.Errorf("basket %d (%s/%d): id must be zero or more", i+1, bs[i].Prefix, bs[i].BID)
		}
		if bs[i].WinningTicket < 0 {
			return fmt.Errorf("basket %d (%s/%d): winning ticket must be zero or more", i+1, bs[i].Prefix, bs[i].BID)
		}
	}
	return nil
}

// ValidateBackup validates every list of a backup file and replaces nil
// lists with empty ones. Backups written by the original app may carry
// colours outside the palette; those are shown as white rather than
// rejected, so a restore at an event never fails over a colour.
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
	for i := range bf.Prefixes {
		if !validColor(bf.Prefixes[i].Color) {
			bf.Prefixes[i].Color = "white"
		}
	}
	if err := ValidatePrefixes(bf.Prefixes); err != nil {
		return err
	}
	if err := ValidateBaskets(bf.Baskets); err != nil {
		return err
	}
	return ValidateTickets(bf.Tickets)
}
