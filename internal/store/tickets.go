package store

import "database/sql"

const ticketCols = `prefix, t_id, first_name, last_name, phone_number, pref`

const upsertTicketSQL = `INSERT INTO tickets (prefix, t_id, first_name, last_name, phone_number, pref) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT (prefix, t_id) DO UPDATE SET first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name,
	phone_number = EXCLUDED.phone_number, pref = EXCLUDED.pref`

func (s *Store) queryTickets(query string, args ...any) ([]Ticket, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		var t Ticket
		var id sql.NullInt64
		var first, last, phone, pref sql.NullString
		if err := rows.Scan(&t.Prefix, &id, &first, &last, &phone, &pref); err != nil {
			return nil, err
		}
		t.TID = nint(id)
		t.FirstName, t.LastName, t.PhoneNumber, t.Pref = nstr(first), nstr(last), nstr(phone), nstr(pref)
		out = append(out, t)
	}
	return out, rows.Err()
}

// AllTickets returns every ticket ordered by prefix, then id.
func (s *Store) AllTickets() ([]Ticket, error) {
	return s.queryTickets(`SELECT ` + ticketCols + ` FROM tickets ORDER BY prefix, t_id`)
}

// TicketsByPrefix returns the tickets of one prefix ordered by id.
func (s *Store) TicketsByPrefix(prefix string) ([]Ticket, error) {
	return s.queryTickets(`SELECT `+ticketCols+` FROM tickets WHERE prefix = ? ORDER BY t_id`, prefix)
}

// Ticket returns one ticket, or nil when it does not exist.
func (s *Store) Ticket(prefix string, id int) (*Ticket, error) {
	ts, err := s.queryTickets(`SELECT `+ticketCols+` FROM tickets WHERE prefix = ? AND t_id = ?`, prefix, id)
	if err != nil || len(ts) == 0 {
		return nil, err
	}
	return &ts[0], nil
}

// TicketRange returns the existing tickets with ids between from and to inclusive.
func (s *Store) TicketRange(prefix string, from, to int) ([]Ticket, error) {
	return s.queryTickets(`SELECT `+ticketCols+` FROM tickets WHERE prefix = ? AND t_id BETWEEN ? AND ? ORDER BY t_id`, prefix, from, to)
}

// UpsertTickets inserts or updates tickets in one transaction.
func (s *Store) UpsertTickets(ts []Ticket) error {
	return s.tx(func(tx *sql.Tx) error {
		return execEach(tx, upsertTicketSQL, len(ts), func(i int) []any {
			t := ts[i]
			return []any{t.Prefix, t.TID, t.FirstName, t.LastName, t.PhoneNumber, t.Pref}
		})
	})
}

// SearchTickets finds tickets whose fields contain the given fragments. Empty
// fragments match everything.
func (s *Store) SearchTickets(first, last, phone string) ([]Ticket, error) {
	return s.queryTickets(`SELECT `+ticketCols+` FROM tickets
		WHERE first_name LIKE ? AND last_name LIKE ? AND phone_number LIKE ? ORDER BY prefix, t_id`,
		"%"+first+"%", "%"+last+"%", "%"+phone+"%")
}
