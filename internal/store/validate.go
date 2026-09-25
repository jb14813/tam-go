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

// ValidatePrefixName trims a prefix name and rejects names that cannot be
// used in a URL path segment or a sheet header.
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
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("prefix name must not contain control characters")
		}
	}
	return name, nil
}

// ValidatePrefixes trims names and rejects empty or unusable names, unknown
// colours and negative weights.
func ValidatePrefixes(ps []Prefix) error {
	for i := range ps {
		name, err := ValidatePrefixName(ps[i].Prefix)
		if err != nil {
			return err
		}
		ps[i].Prefix = name
		if !validColor(ps[i].Color) {
			return fmt.Errorf("color %q is not one of %s", ps[i].Color, strings.Join(Colors, ", "))
		}
		if ps[i].Weight < 0 {
			return errors.New("weight must be zero or more")
		}
	}
	return nil
}

// ValidateTickets rejects tickets with an unusable prefix or a negative id.
// The contact preference is free text, as in the original; the pages send
// CALL or TEXT.
func ValidateTickets(ts []Ticket) error {
	for i := range ts {
		name, err := ValidatePrefixName(ts[i].Prefix)
		if err != nil {
			return fmt.Errorf("ticket: %w", err)
		}
		ts[i].Prefix = name
		if ts[i].TID < 0 {
			return errors.New("ticket id must be zero or more")
		}
		ts[i].Pref = strings.TrimSpace(ts[i].Pref)
	}
	return nil
}

// ValidateBaskets rejects baskets with an unusable prefix or negative ids.
func ValidateBaskets(bs []Basket) error {
	for i := range bs {
		name, err := ValidatePrefixName(bs[i].Prefix)
		if err != nil {
			return fmt.Errorf("basket: %w", err)
		}
		bs[i].Prefix = name
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
