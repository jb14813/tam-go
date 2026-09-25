package store

// The JSON names below are the wire format shared with the original
// Ticket Auction Manager; do not rename them.

// Prefix is a ticket series (for example "CALL" or "A") with a display colour
// and a sort weight.
type Prefix struct {
	Prefix string `json:"prefix"`
	Color  string `json:"color"`
	Weight int    `json:"weight"`
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

// Basket is one auction item within a prefix.
type Basket struct {
	Prefix        string `json:"prefix"`
	BID           int    `json:"b_id"`
	Description   string `json:"description"`
	Donors        string `json:"donors"`
	WinningTicket int    `json:"winning_ticket"`
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

// AuthKey is a server access key.
type AuthKey struct {
	AuthKey     string `json:"auth_key"`
	Description string `json:"description"`
}

// BackupFile is the backup and restore document.
type BackupFile struct {
	Prefixes []Prefix `json:"prefixes"`
	Baskets  []Basket `json:"baskets"`
	Tickets  []Ticket `json:"tickets"`
}
