package store

import "database/sql"

const ticketCols = `prefix, t_id, first_name, last_name, phone_number, pref`

const upsertTicketSQL = `INSERT INTO tickets (prefix, t_id, first_name, last_name, phone_number, pref) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT (prefix, t_id) DO UPDATE SET first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name,
	phone_number = EXCLUDED.phone_number, pref = EXCLUDED.pref`

func (s *Store) queryTickets(query string, args ...any) ([]Ticket, error) {
	if !s.readGuarded {
		return reviewedRead(s, func(v *Store) ([]Ticket, error) { return v.queryTickets(query, args...) })
	}
	rows, err := s.query(query, args...)
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
	for _, ticket := range ts {
		if err := validateIdentity(ticket.TID); err != nil {
			return err
		}
	}
	return s.tx(func(tx *sql.Tx) error {
		for _, t := range ts {
			apply, err := s.prepareRecord(tx, "ticket", t.Prefix, t.TID, t)
			if err != nil {
				return err
			}
			if !apply {
				continue
			}
			if _, err = tx.Exec(upsertTicketSQL, t.Prefix, t.TID, t.FirstName, t.LastName, t.PhoneNumber, t.Pref); err != nil {
				return err
			}
		}
		return nil
	})
}

// SearchTickets finds tickets whose fields contain the given fragments,
// ignoring the case of ASCII letters. Empty fragments match everything, and
// %, _ and \ are ordinary characters. instr compares the whole field and
// fragment, where LIKE stops at a NUL character and refuses patterns over
// 50,000 bytes; lower changes ASCII letters only, as LIKE compares them.
func (s *Store) SearchTickets(first, last, phone string) ([]Ticket, error) {
	return s.queryTickets(`SELECT `+ticketCols+` FROM tickets
		WHERE instr(lower(first_name), lower(?)) > 0 AND instr(lower(last_name), lower(?)) > 0
		AND instr(lower(phone_number), lower(?)) > 0
		ORDER BY prefix, t_id`,
		first, last, phone)
}
