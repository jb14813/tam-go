package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Int decodes a whole number sent as a JSON integer. null is zero, which is
// what the pages send for an empty number box. Strings and fractions are
// refused, and so is anything beyond what a browser holds exactly (2^53-1).
type Int int

// maxExact is the largest whole number a browser's numbers hold exactly.
const maxExact = 1<<53 - 1

// UnmarshalJSON implements json.Unmarshaler.
func (n *Int) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*n = 0
		return nil
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil || i > maxExact || i < -maxExact {
		return fmt.Errorf("%s is not a whole number from %d to %d", s, -maxExact, maxExact)
	}
	*n = Int(i)
	return nil
}

// Prefix is a ticket series (for example "CALL" or "A") with a display colour
// and a sort weight.
type Prefix struct {
	Prefix string `json:"prefix"`
	Color  string `json:"color"`
	Weight int    `json:"weight"`
}

// UnmarshalJSON reads weight as an Int.
func (p *Prefix) UnmarshalJSON(b []byte) error {
	var raw struct {
		Prefix string `json:"prefix"`
		Color  string `json:"color"`
		Weight Int    `json:"weight"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Prefix{Prefix: raw.Prefix, Color: raw.Color, Weight: int(raw.Weight)}
	return nil
}

// Ticket is one sold ticket within a prefix.
type Ticket struct {
	Prefix      string `json:"prefix"`
	TID         int    `json:"t_id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	PhoneNumber string `json:"phone_number"`
	Pref        string `json:"pref"`
}

// UnmarshalJSON requires t_id and reads it as an Int.
func (t *Ticket) UnmarshalJSON(b []byte) error {
	var raw struct {
		Prefix      string `json:"prefix"`
		TID         *Int   `json:"t_id"`
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name"`
		PhoneNumber string `json:"phone_number"`
		Pref        string `json:"pref"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.TID == nil {
		return errors.New("ticket: t_id is required")
	}
	*t = Ticket{Prefix: raw.Prefix, TID: int(*raw.TID), FirstName: raw.FirstName, LastName: raw.LastName, PhoneNumber: raw.PhoneNumber, Pref: raw.Pref}
	return nil
}

// Basket is one auction item within a prefix.
type Basket struct {
	Prefix        string `json:"prefix"`
	BID           int    `json:"b_id"`
	Description   string `json:"description"`
	Donors        string `json:"donors"`
	WinningTicket int    `json:"winning_ticket"`
}

// UnmarshalJSON requires b_id and reads the numbers as Ints.
func (bk *Basket) UnmarshalJSON(b []byte) error {
	var raw struct {
		Prefix        string `json:"prefix"`
		BID           *Int   `json:"b_id"`
		Description   string `json:"description"`
		Donors        string `json:"donors"`
		WinningTicket Int    `json:"winning_ticket"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.BID == nil {
		return errors.New("basket: b_id is required")
	}
	*bk = Basket{Prefix: raw.Prefix, BID: int(*raw.BID), Description: raw.Description, Donors: raw.Donors, WinningTicket: int(raw.WinningTicket)}
	return nil
}

// DrawingLine is a basket joined with the buyer of its winning ticket.
type DrawingLine struct {
	Prefix        string `json:"prefix"`
	BID           int    `json:"b_id"`
	Description   string `json:"description"`
	WinningTicket int    `json:"winning_ticket"`
	LastName      string `json:"last_name"`
	FirstName     string `json:"first_name"`
	PhoneNumber   string `json:"phone_number"`
}

// ReportByNameLine is a winners report row ordered by winner name.
type ReportByNameLine struct {
	LastName      string `json:"last_name"`
	FirstName     string `json:"first_name"`
	PhoneNumber   string `json:"phone_number"`
	Pref          string `json:"pref"`
	Prefix        string `json:"prefix"`
	BID           int    `json:"b_id"`
	Description   string `json:"description"`
	Donors        string `json:"donors"`
	WinningTicket int    `json:"winning_ticket"`
}

// ReportByBasketLine is a winners report row ordered by basket.
type ReportByBasketLine struct {
	Prefix        string `json:"prefix"`
	BID           int    `json:"b_id"`
	Description   string `json:"description"`
	Donors        string `json:"donors"`
	WinningTicket int    `json:"winning_ticket"`
	LastName      string `json:"last_name"`
	FirstName     string `json:"first_name"`
	PhoneNumber   string `json:"phone_number"`
	Pref          string `json:"pref"`
}

// ReportCountLine is one row of the ticket counts report. The last row has
// the prefix "Total".
type ReportCountLine struct {
	Prefix       string `json:"prefix"`
	UniqueBuyers int    `json:"unique_buyers"`
	TotalBuys    int    `json:"total_buys"`
}

// AuthKey is a server access key. LastSeen is the time of the key's last
// authenticated request and LastUpdate that of its last accepted write, in
// RFC 3339, or "" when that never happened or the database has no
// auth_key_activity table; both are left out of the JSON when empty.
type AuthKey struct {
	AuthKey     string `json:"auth_key"`
	Description string `json:"description"`
	LastSeen    string `json:"last_seen,omitempty"`
	LastUpdate  string `json:"last_update,omitempty"`
}

// BackupFile is the backup and restore document. Every list is always
// present on the wire.
type BackupFile struct {
	Prefixes []Prefix `json:"prefixes"`
	Baskets  []Basket `json:"baskets"`
	Tickets  []Ticket `json:"tickets"`
}

// NewBackupFile returns an empty backup with non-nil lists.
func NewBackupFile() BackupFile {
	return BackupFile{Prefixes: []Prefix{}, Baskets: []Basket{}, Tickets: []Ticket{}}
}
