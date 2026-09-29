package store

import "database/sql"

const basketCols = `prefix, b_id, description, donors, winning_ticket`

// upsertBasketSQL is what the baskets form saves: it never touches the
// winning ticket of an existing basket.
const upsertBasketSQL = `INSERT INTO baskets (prefix, b_id, description, donors, winning_ticket) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT (prefix, b_id) DO UPDATE SET description = EXCLUDED.description, donors = EXCLUDED.donors`

// upsertWinningSQL is what the drawing form saves: only the winning ticket.
const upsertWinningSQL = `INSERT INTO baskets (prefix, b_id, winning_ticket) VALUES (?, ?, ?)
	ON CONFLICT (prefix, b_id) DO UPDATE SET winning_ticket = EXCLUDED.winning_ticket`

// restoreBasketSQL overwrites every field; used by Import.
const restoreBasketSQL = `INSERT INTO baskets (prefix, b_id, description, donors, winning_ticket) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT (prefix, b_id) DO UPDATE SET description = EXCLUDED.description, donors = EXCLUDED.donors,
	winning_ticket = EXCLUDED.winning_ticket`

func (s *Store) queryBaskets(query string, args ...any) ([]Basket, error) {
	if !s.readGuarded {
		return reviewedRead(s, func(v *Store) ([]Basket, error) { return v.queryBaskets(query, args...) })
	}
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Basket{}
	for rows.Next() {
		var b Basket
		var id, winning sql.NullInt64
		var desc, donors sql.NullString
		if err := rows.Scan(&b.Prefix, &id, &desc, &donors, &winning); err != nil {
			return nil, err
		}
		b.BID, b.WinningTicket = nint(id), nint(winning)
		b.Description, b.Donors = nstr(desc), nstr(donors)
		out = append(out, b)
	}
	return out, rows.Err()
}

// AllBaskets returns every basket ordered by prefix, then id.
func (s *Store) AllBaskets() ([]Basket, error) {
	return s.queryBaskets(`SELECT ` + basketCols + ` FROM baskets ORDER BY prefix, b_id`)
}

// BasketsByPrefix returns the baskets of one prefix ordered by id.
func (s *Store) BasketsByPrefix(prefix string) ([]Basket, error) {
	return s.queryBaskets(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? ORDER BY b_id`, prefix)
}

// Basket returns one basket, or nil when it does not exist.
func (s *Store) Basket(prefix string, id int) (*Basket, error) {
	bs, err := s.queryBaskets(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? AND b_id = ?`, prefix, id)
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return &bs[0], nil
}

// BasketRange returns the existing baskets with ids between from and to inclusive.
func (s *Store) BasketRange(prefix string, from, to int) ([]Basket, error) {
	return s.queryBaskets(`SELECT `+basketCols+` FROM baskets WHERE prefix = ? AND b_id BETWEEN ? AND ? ORDER BY b_id`, prefix, from, to)
}

// UpsertBaskets inserts baskets or updates their description and donors.
func (s *Store) UpsertBaskets(bs []Basket) error {
	return s.tx(func(tx *sql.Tx) error {
		if len(bs) == 0 {
			return nil
		}
		components, err := tx.Prepare(markBasketComponentSQL)
		if err != nil {
			return err
		}
		defer components.Close()
		baskets, err := tx.Prepare(upsertBasketSQL)
		if err != nil {
			return err
		}
		defer baskets.Close()
		for _, b := range bs {
			apply, err := s.prepareRecord(tx, "metadata", b.Prefix, b.BID, []string{b.Description, b.Donors})
			if err != nil {
				return err
			}
			if !apply {
				continue
			}
			var exists bool
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM baskets WHERE prefix=? AND b_id=?)`, b.Prefix, b.BID).Scan(&exists); err != nil {
				return err
			}
			if !exists && b.WinningTicket != 0 {
				if _, err = s.prepareRecord(tx, "drawing", b.Prefix, b.BID, b.WinningTicket); err != nil {
					return err
				}
			}
			// Mark immediately before each write: a repeated basket in one
			// batch is an update after the first entry inserted it.
			if _, err := components.Exec(basketComponentArgs(b, true, false)...); err != nil {
				return err
			}
			if _, err := baskets.Exec(b.Prefix, b.BID, b.Description, b.Donors, b.WinningTicket); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertWinning sets the winning ticket of each basket, creating the basket
// when it does not exist yet.
func (s *Store) UpsertWinning(bs []Basket) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := markBasketComponents(tx, bs, false, true); err != nil {
			return err
		}
		for _, b := range bs {
			apply, err := s.prepareRecord(tx, "drawing", b.Prefix, b.BID, b.WinningTicket)
			if err != nil {
				return err
			}
			if !apply {
				continue
			}
			if _, err = tx.Exec(upsertWinningSQL, b.Prefix, b.BID, b.WinningTicket); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- drawing view ---

const drawingCols = `prefix, b_id, description, winning_ticket, last_name, first_name, phone_number`

func (s *Store) queryDrawing(query string, args ...any) ([]DrawingLine, error) {
	if !s.readGuarded {
		return reviewedRead(s, func(v *Store) ([]DrawingLine, error) { return v.queryDrawing(query, args...) })
	}
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DrawingLine{}
	for rows.Next() {
		var d DrawingLine
		var id, winning sql.NullInt64
		var desc, last, first, phone sql.NullString
		if err := rows.Scan(&d.Prefix, &id, &desc, &winning, &last, &first, &phone); err != nil {
			return nil, err
		}
		d.BID, d.WinningTicket = nint(id), nint(winning)
		d.Description, d.LastName, d.FirstName, d.PhoneNumber = nstr(desc), nstr(last), nstr(first), nstr(phone)
		out = append(out, d)
	}
	return out, rows.Err()
}

// AllDrawing returns every basket joined with its winner.
func (s *Store) AllDrawing() ([]DrawingLine, error) {
	return s.queryDrawing(`SELECT ` + drawingCols + ` FROM drawing ORDER BY prefix, b_id`)
}

// DrawingByPrefix returns the drawing lines of one prefix.
func (s *Store) DrawingByPrefix(prefix string) ([]DrawingLine, error) {
	return s.queryDrawing(`SELECT `+drawingCols+` FROM drawing WHERE prefix = ? ORDER BY b_id`, prefix)
}

// DrawingLine returns one drawing line, or nil when the basket does not exist.
func (s *Store) DrawingLine(prefix string, id int) (*DrawingLine, error) {
	ds, err := s.queryDrawing(`SELECT `+drawingCols+` FROM drawing WHERE prefix = ? AND b_id = ?`, prefix, id)
	if err != nil || len(ds) == 0 {
		return nil, err
	}
	return &ds[0], nil
}

// DrawingRange returns the existing drawing lines with ids between from and to inclusive.
func (s *Store) DrawingRange(prefix string, from, to int) ([]DrawingLine, error) {
	return s.queryDrawing(`SELECT `+drawingCols+` FROM drawing WHERE prefix = ? AND b_id BETWEEN ? AND ? ORDER BY b_id`, prefix, from, to)
}
