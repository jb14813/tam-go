package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The JSON names below are the wire format shared with the original
// Ticket Auction Manager; do not rename them.

// Int decodes every spelling of a whole number the original accepted:
// 4, 4.0, "4" and null (zero). The original client sends ids from URL
// parameters as strings, so this keeps it compatible with tam-server.
type Int int

// UnmarshalJSON implements json.Unmarshaler.
func (n *Int) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		*n = Int(i)
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return fmt.Errorf("%q is not a whole number", s)
	}
	*n = Int(f)
	return nil
}

// Prefix is a ticket series (for example "CALL" or "A") with a display colour
// and a sort weight.
type Prefix struct {
	Prefix string `json:"prefix"`
	Color  string `json:"color"`
	Weight int    `json:"weight"`
}

// UnmarshalJSON accepts the numeric spellings of Int for weight.
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

// UnmarshalJSON requires t_id and accepts the numeric spellings of Int.
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

// UnmarshalJSON requires b_id and accepts the numeric spellings of Int.
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
// authenticated request in RFC 3339, or "" when the key has never been used
// or the database has no auth_key_activity table; it is left out of the
// JSON when empty so the wire format stays the original's.
type AuthKey struct {
	AuthKey     string `json:"auth_key"`
	Description string `json:"description"`
	LastSeen    string `json:"last_seen,omitempty"`
}

// BackupFile is the backup and restore document. Every list is always
// present on the wire, which the original server requires.
type BackupFile struct {
	Prefixes []Prefix `json:"prefixes"`
	Baskets  []Basket `json:"baskets"`
	Tickets  []Ticket `json:"tickets"`
}

// NewBackupFile returns an empty backup with non-nil lists.
func NewBackupFile() BackupFile {
	return BackupFile{Prefixes: []Prefix{}, Baskets: []Basket{}, Tickets: []Ticket{}}
}
