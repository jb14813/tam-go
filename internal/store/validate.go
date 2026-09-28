package store

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Colors is the palette the pages know how to render.
var Colors = []string{"white", "blue", "yellow", "green", "orange", "purple", "red"}

// maxPrefixLen matches the VARCHAR(100) of the first schema, which counts
// characters, not bytes.
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
	if utf8.RuneCountInString(name) > maxPrefixLen {
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
	return ValidatePrefixChanges(ps, nil)
}

// ValidatePrefixChanges preserves exact identities already in existing,
// including names restored from the original app that today's new-prefix
// rules reject. New names are normalized and validated as before; every
// row's color and weight must still be valid.
func ValidatePrefixChanges(ps, existing []Prefix) error {
	known := make(map[string]bool, len(existing))
	for _, p := range existing {
		known[p.Prefix] = true
	}
	for i := range ps {
		name := ps[i].Prefix
		if !known[name] {
			var err error
			name, err = ValidatePrefixName(name)
			if err != nil {
				return fmt.Errorf("prefix %d: %w", i+1, err)
			}
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
// Prefixes identify existing rows and must stay as restored, including any
// surrounding whitespace. The contact preference is free text, as in the
// original; the pages send CALL or TEXT.
func ValidateTickets(ts []Ticket) error {
	for i := range ts {
		if strings.TrimSpace(ts[i].Prefix) == "" {
			return fmt.Errorf("ticket %d: prefix must not be empty", i+1)
		}
		if ts[i].TID < 0 {
			return fmt.Errorf("ticket %d (%s/%d): id must be zero or more", i+1, ts[i].Prefix, ts[i].TID)
		}
		ts[i].Pref = strings.TrimSpace(ts[i].Pref)
	}
	return nil
}

// ValidateBaskets rejects baskets without a prefix or with negative ids,
// keeping the prefix identity unchanged as ValidateTickets does.
func ValidateBaskets(bs []Basket) error {
	for i := range bs {
		if strings.TrimSpace(bs[i].Prefix) == "" {
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

// ValidateBackup checks a backup file and replaces nil lists with empty
// ones. Its rows are taken as they are: the rules for a new prefix name
// (ValidatePrefixName) and the trimming of the save forms are for what is
// typed now, while a backup carries what a database already holds, possibly
// from the original app, which allowed any name. Renaming a prefix, or
// trimming the prefix of its tickets, would cut them apart, and refusing
// one row would refuse the whole backup. What must hold still holds: every
// row names a prefix, and no id is negative. Colours outside the palette,
// which the original also allowed, are shown as white.
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
		if bf.Prefixes[i].Prefix == "" {
			return fmt.Errorf("prefix %d: prefix name must not be empty", i+1)
		}
		if !validColor(bf.Prefixes[i].Color) {
			bf.Prefixes[i].Color = "white"
		}
	}
	for i, b := range bf.Baskets {
		switch {
		case b.Prefix == "":
			return fmt.Errorf("basket %d: prefix must not be empty", i+1)
		case b.BID < 0 || b.WinningTicket < 0:
			return fmt.Errorf("basket %d (%s/%d): ids must be zero or more", i+1, b.Prefix, b.BID)
		}
	}
	for i, t := range bf.Tickets {
		switch {
		case t.Prefix == "":
			return fmt.Errorf("ticket %d: prefix must not be empty", i+1)
		case t.TID < 0:
			return fmt.Errorf("ticket %d (%s/%d): id must be zero or more", i+1, t.Prefix, t.TID)
		}
	}
	return nil
}
