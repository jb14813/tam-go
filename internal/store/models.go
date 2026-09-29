package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SafeIntegerMax is the largest integer represented exactly by browser JSON.
const SafeIntegerMax = 9007199254740991

// Int decodes a JSON integer within the exact range shared by Go and browsers.
type Int int

// UnmarshalJSON implements json.Unmarshaler.
func (n *Int) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	i, err := strconv.ParseInt(s, 10, strconv.IntSize)
	if err != nil || i < -SafeIntegerMax || i > SafeIntegerMax {
		return fmt.Errorf("%q must be a JSON integer between -%d and %d", s, SafeIntegerMax, SafeIntegerMax)
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

// UnmarshalJSON requires an exact integer for any supplied weight.
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

// UnmarshalJSON requires an exact integer t_id.
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

// UnmarshalJSON requires an exact integer b_id and winning_ticket.
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

// ReportCountLine distinguishes the aggregate from any real prefix named Total.
type ReportCountLine struct {
	Prefix       string `json:"prefix"`
	IsTotal      bool   `json:"is_total"`
	UniqueBuyers int    `json:"unique_buyers"`
	TotalBuys    int    `json:"total_buys"`
}

// AuthKey is a server access key. LastSeen is the time of the key's last
// authenticated request and LastUpdate that of its last accepted write, in
// RFC 3339, or "" when that never happened or the database has no
// auth_key_activity table. Absent timestamps are omitted from the response.
type AuthKey struct {
	AuthKey     string `json:"auth_key"`
	Description string `json:"description"`
	LastSeen    string `json:"last_seen,omitempty"`
	LastUpdate  string `json:"last_update,omitempty"`
}

// BackupFile holds event rows. External backups use RecoverySnapshot to include
// the ownership and history required to restore them safely.
type BackupFile struct {
	Prefixes []Prefix `json:"prefixes"`
	Baskets  []Basket `json:"baskets"`
	Tickets  []Ticket `json:"tickets"`
}

// NewBackupFile returns an empty backup with non-nil lists.
func NewBackupFile() BackupFile {
	return BackupFile{Prefixes: []Prefix{}, Baskets: []Basket{}, Tickets: []Ticket{}}
}
