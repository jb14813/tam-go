package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// RecoveryHoldback distinguishes an entered value awaiting acceptance (or
// refused/discarded) from its last recoverable predecessor. It belongs to an
// exact operation, so a later intentional save of the same value supersedes it.
// Native client backups carry this distinction even without the delivery queue.
type RecoveryHoldback struct {
	Current RecordRevision   `json:"current"`
	Prior   *RecordCandidate `json:"prior,omitempty"`
}

func hasRecoveryHoldbacks(tx *sql.Tx) (bool, error) {
	var has bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='recovery_holdbacks')`).Scan(&has)
	return has, err
}

func heldRevision(current, held RecordRevision) bool {
	if current.Hash != held.Hash {
		return false
	}
	if len(held.Heads) == 0 {
		// Headless legacy copies have no safe operation identity. Keep their
		// complete revision identity; never infer provenance from the value.
		a, _ := json.Marshal(current)
		b, _ := json.Marshal(held)
		return string(a) == string(b)
	}
	for _, dot := range held.Heads {
		if containsHash(current.Heads, dot) && current.Operations[dot] == held.Operations[dot] {
			return true
		}
	}
	return false
}

func loadRecoveryHoldback(tx *sql.Tx, r RecordRevision) (*RecoveryHoldback, error) {
	var raw string
	err := tx.QueryRow(`SELECT holdback FROM recovery_holdbacks WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var held RecoveryHoldback
	err = json.Unmarshal([]byte(raw), &held)
	return &held, err
}

func saveRecoveryHoldback(tx *sql.Tx, held RecoveryHoldback) error {
	if err := observeLocalHistory(tx, held.Current); err != nil {
		return err
	}
	if held.Prior != nil {
		if err := observeLocalHistory(tx, held.Prior.Revision); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(held)
	if err != nil {
		return err
	}
	r := held.Current
	_, err = tx.Exec(`INSERT INTO recovery_holdbacks(kind,prefix,record_id,holdback) VALUES(?,?,?,?)
		ON CONFLICT(kind,prefix,record_id) DO UPDATE SET holdback=excluded.holdback`, r.Kind, r.Prefix, r.ID, string(raw))
	return err
}

func clearRecoveryHoldback(tx *sql.Tx, r RecordRevision) error {
	has, err := hasRecoveryHoldbacks(tx)
	if err != nil || !has {
		return err
	}
	_, err = tx.Exec(`DELETE FROM recovery_holdbacks WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID)
	return err
}

func ownedRecoveryCandidate(tx *sql.Tx, r RecordRevision) (*RecordCandidate, error) {
	raw, exists, err := currentValue(tx, r.Kind, r.Prefix, r.ID)
	if err != nil || !exists {
		return nil, err
	}
	if r.Kind == "metadata" || r.Kind == "drawing" {
		var owned bool
		if err := tx.QueryRow(`SELECT coalesce((SELECT `+r.Kind+` FROM basket_components WHERE prefix=? AND b_id=?),1)`, r.Prefix, r.ID).Scan(&owned); err != nil || !owned {
			return nil, err
		}
	}
	r, err = loadRevision(tx, r.Kind, r.Prefix, r.ID, valueHash(raw))
	if err != nil {
		return nil, err
	}
	return &RecordCandidate{Revision: r, Value: raw}, nil
}

func journalOwnsRevision(tx *sql.Tx, r RecordRevision, pendingOnly bool) (bool, error) {
	for _, dot := range r.Heads {
		cut := strings.LastIndexByte(dot, '/')
		if cut < 0 || !strings.HasPrefix(dot, "client:") {
			continue
		}
		number, err := strconv.ParseInt(dot[cut+1:], 10, 64)
		if err != nil {
			return false, err
		}
		var held bool
		query := `SELECT EXISTS(SELECT 1 FROM outbox WHERE client=? AND save_number=? AND local_applied=1 AND rejected='')`
		args := []any{dot[7:cut], number}
		if !pendingOnly {
			query = `SELECT EXISTS(SELECT 1 FROM outbox WHERE client=? AND save_number=? AND local_applied=1 UNION ALL SELECT 1 FROM outbox_failed WHERE client=? AND save_number=? AND local_applied=1)`
			args = append(args, dot[7:cut], number)
		}
		err = tx.QueryRow(query, args...).Scan(&held)
		if err != nil || held {
			return held, err
		}
	}
	if len(r.Heads) == 0 {
		query := `SELECT ` + outboxCols + ` FROM outbox WHERE local_applied=1 AND rejected=''`
		if !pendingOnly {
			query = `SELECT ` + outboxCols + ` FROM outbox WHERE local_applied=1 UNION ALL SELECT ` + outboxCols + ` FROM outbox_failed WHERE local_applied=1`
		}
		rows, err := tx.Query(query)
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			request, err := scanOutbox(rows)
			if err != nil {
				return false, err
			}
			edits, err := recoveryIntentEdits(request)
			if err != nil {
				return false, err
			}
			for _, edit := range edits {
				if edit.key == recordKey(r.Kind, r.Prefix, r.ID) && edit.hash == r.Hash {
					return true, nil
				}
			}
		}
		if err := rows.Err(); err != nil {
			return false, err
		}
	}
	return false, nil
}

// A new uncertain write retains its predecessor and marker in the same
// transaction as the local row and journal entry. A second queued edit keeps
// the predecessor of the first, until an acknowledgement advances it.
func (s *Store) retainQueuedRecovery(request *Outbox, write func(*Store) error) error {
	return s.tx(func(tx *sql.Tx) error {
		edits, err := recoveryIntentEdits(request)
		if err != nil {
			return err
		}
		priors := map[string]*RecordCandidate{}
		for _, edit := range edits {
			if _, seen := priors[edit.key]; seen {
				continue
			}
			prior, err := ownedRecoveryCandidate(tx, edit.candidate.Revision)
			if err != nil {
				return err
			}
			if prior != nil {
				held, err := loadRecoveryHoldback(tx, prior.Revision)
				if err != nil {
					return err
				}
				if held != nil && heldRevision(prior.Revision, held.Current) {
					prior = held.Prior
				} else if pending, err := journalOwnsRevision(tx, prior.Revision, false); err != nil {
					return err
				} else if pending {
					// Older journals did not retain the overwritten payload. Its
					// hash cannot reconstruct it, so never guess a predecessor.
					prior = nil
				}
			}
			priors[edit.key] = prior
		}
		if err := s.view(tx).WithLocalOperation(request.Order, write); err != nil {
			return err
		}
		for _, edit := range edits {
			current, err := ownedRecoveryCandidate(tx, edit.candidate.Revision)
			if err != nil {
				return err
			}
			if current == nil || current.Revision.Hash != edit.hash {
				continue
			}
			if request.Order.Client != "" && request.Order.Save > 0 && !containsHash(current.Revision.Heads, operationKey("client:"+request.Order.Client, request.Order.Save)) {
				continue
			}
			if err := saveRecoveryHoldback(tx, RecoveryHoldback{Current: current.Revision, Prior: priors[edit.key]}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Keep the accepted request's actual payload, even when a newer local queued
// edit means ApplyReceipt cannot attach this acknowledgement to the live row.
func acknowledgeRecoveryHoldback(tx *sql.Tx, request *Outbox, receipts ...*SaveReceipt) error {
	edits, err := recoveryIntentEdits(request)
	if err != nil {
		return err
	}
	for _, edit := range edits {
		accepted := edit.candidate
		r := &accepted.Revision
		var haveReceipt, found bool
		for _, receipt := range receipts {
			if receipt == nil {
				continue
			}
			haveReceipt = true
			for _, revision := range receipt.Revisions {
				if recordKey(revision.Kind, revision.Prefix, revision.ID) == edit.key && revision.Hash == edit.hash {
					*r, found = revision, true
				}
			}
		}
		if haveReceipt && !found {
			// A baskets update does not accept the request's insert-only
			// winner. Never manufacture ownership for an ignored component.
			continue
		}
		if !found && request.Order.Client != "" && request.Order.Save > 0 {
			actor := "client:" + request.Order.Client
			dot := operationKey(actor, request.Order.Save)
			r.Vector, r.Operations, r.Heads = map[string]int64{actor: request.Order.Save}, map[string]string{dot: r.Hash}, []string{dot}
		}
		current, err := ownedRecoveryCandidate(tx, *r)
		if err != nil {
			return err
		}
		dot := operationKey("client:"+request.Order.Client, request.Order.Save)
		if edit.insertWinner && !found && (current == nil || current.Revision.Operations[dot] != edit.hash) {
			continue
		}
		held, err := loadRecoveryHoldback(tx, *r)
		if err != nil {
			return err
		}
		if held == nil || current == nil || !heldRevision(current.Revision, held.Current) {
			continue
		}
		if current.Revision.Hash == edit.hash && containsHash(current.Revision.Heads, dot) {
			if err := clearRecoveryHoldback(tx, *r); err != nil {
				return err
			}
			continue
		}
		held.Prior = &accepted
		if err := saveRecoveryHoldback(tx, *held); err != nil {
			return err
		}
	}
	return nil
}

func exportRecoveryHoldbacks(tx *sql.Tx, snapshot *RecoverySnapshot) error {
	has, err := hasRecoveryHoldbacks(tx)
	if err != nil || !has {
		return err
	}
	candidates, err := snapshotCandidates(*snapshot)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		held, err := loadRecoveryHoldback(tx, candidate.Revision)
		if err != nil {
			return err
		}
		if held != nil && heldRevision(candidate.Revision, held.Current) {
			snapshot.WithheldRecords = append(snapshot.WithheldRecords, *held)
		} else if pending, err := journalOwnsRevision(tx, candidate.Revision, false); err != nil {
			return err
		} else if pending {
			snapshot.WithheldRecords = append(snapshot.WithheldRecords, RecoveryHoldback{Current: candidate.Revision})
		}
	}
	return nil
}

// A journal created by an older release may be the only remaining evidence
// that its live value was not accepted. Preserve that evidence before Retry
// changes the operation number or Discard removes the failed request.
func (s *Store) preserveJournalHoldbacks() error {
	snapshot, err := s.ExportRecovery()
	if err != nil {
		return err
	}
	for _, held := range snapshot.WithheldRecords {
		if err := saveRecoveryHoldback(s.in, held); err != nil {
			return err
		}
	}
	return nil
}

func validateRecoveryHoldbacks(snapshot *RecoverySnapshot) error {
	candidates, err := snapshotCandidates(*snapshot)
	if err != nil {
		return err
	}
	current := map[string]RecordRevision{}
	for _, c := range candidates {
		r := c.Revision
		current[recordKey(r.Kind, r.Prefix, r.ID)] = r
	}
	seen := map[string]bool{}
	for i := range snapshot.WithheldRecords {
		held := &snapshot.WithheldRecords[i]
		r := held.Current
		if err := validateRevision(r); err != nil {
			return err
		}
		key := recordKey(r.Kind, r.Prefix, r.ID)
		if seen[key] || !heldRevision(current[key], r) {
			return errors.New("withheld record does not match its saved operation")
		}
		seen[key] = true
		if held.Prior != nil {
			if err := validateCandidate(held.Prior); err != nil {
				return err
			}
			p := held.Prior.Revision
			if recordKey(p.Kind, p.Prefix, p.ID) != key {
				return errors.New("withheld predecessor has another record identity")
			}
		}
	}
	return nil
}

func applyRecoveryHoldbacks(snapshot *RecoverySnapshot) error {
	if len(snapshot.WithheldRecords) == 0 {
		return nil
	}
	if err := validateRecoveryHoldbacks(snapshot); err != nil {
		return err
	}
	// Recovery accepts snapshots by value. Filtering must not replace values
	// in a caller's retained native backup through shared slice storage.
	snapshot.Prefixes = slices.Clone(snapshot.Prefixes)
	snapshot.Tickets = slices.Clone(snapshot.Tickets)
	snapshot.Baskets = slices.Clone(snapshot.Baskets)
	snapshot.BasketComponents = slices.Clone(snapshot.BasketComponents)
	snapshot.Revisions = slices.Clone(snapshot.Revisions)
	snapshot.Conflicts = slices.Clone(snapshot.Conflicts)
	snapshot.DeletedPrefixes = slices.Clone(snapshot.DeletedPrefixes)
	if snapshot.BasketComponents == nil {
		components, err := recoveryComponents(*snapshot)
		if err != nil {
			return err
		}
		for _, row := range snapshot.Baskets {
			snapshot.BasketComponents = append(snapshot.BasketComponents, components[basketID{row.Prefix, row.BID}])
		}
	}
	omit := map[string]bool{}
	for _, held := range snapshot.WithheldRecords {
		r := held.Current
		omit[recordKey(r.Kind, r.Prefix, r.ID)] = true
	}
	filterRecoverySnapshot(snapshot, omit)
	for _, held := range snapshot.WithheldRecords {
		if held.Prior == nil {
			continue
		}
		c := held.Prior
		r := c.Revision
		snapshot.Revisions = append(snapshot.Revisions, r)
		switch r.Kind {
		case "ticket":
			var row Ticket
			if err := json.Unmarshal(c.Value, &row); err != nil {
				return err
			}
			snapshot.Tickets = append(snapshot.Tickets, row)
		case "prefix":
			if string(c.Value) == "null" {
				snapshot.DeletedPrefixes = append(snapshot.DeletedPrefixes, r.Prefix)
				continue
			}
			var row Prefix
			if err := json.Unmarshal(c.Value, &row); err != nil {
				return err
			}
			snapshot.Prefixes = append(snapshot.Prefixes, row)
		case "metadata", "drawing":
			i := -1
			for j, row := range snapshot.Baskets {
				if row.Prefix == r.Prefix && row.BID == r.ID {
					i = j
					break
				}
			}
			if i == -1 {
				i = len(snapshot.Baskets)
				snapshot.Baskets = append(snapshot.Baskets, Basket{Prefix: r.Prefix, BID: r.ID})
				snapshot.BasketComponents = append(snapshot.BasketComponents, BasketComponents{Prefix: r.Prefix, BID: r.ID})
			}
			if r.Kind == "metadata" {
				var value []string
				if err := json.Unmarshal(c.Value, &value); err != nil {
					return err
				}
				snapshot.Baskets[i].Description, snapshot.Baskets[i].Donors = value[0], value[1]
			} else if err := json.Unmarshal(c.Value, &snapshot.Baskets[i].WinningTicket); err != nil {
				return err
			}
			for j := range snapshot.BasketComponents {
				component := &snapshot.BasketComponents[j]
				if component.Prefix == r.Prefix && component.BID == r.ID {
					component.Metadata = component.Metadata || r.Kind == "metadata"
					component.Drawing = component.Drawing || r.Kind == "drawing"
				}
			}
		}
	}
	snapshot.WithheldRecords = nil
	sort.Slice(snapshot.Prefixes, func(i, j int) bool { return snapshot.Prefixes[i].Prefix < snapshot.Prefixes[j].Prefix })
	sort.Slice(snapshot.Tickets, func(i, j int) bool {
		a, b := snapshot.Tickets[i], snapshot.Tickets[j]
		return a.Prefix < b.Prefix || a.Prefix == b.Prefix && a.TID < b.TID
	})
	sort.Slice(snapshot.Baskets, func(i, j int) bool {
		a, b := snapshot.Baskets[i], snapshot.Baskets[j]
		return a.Prefix < b.Prefix || a.Prefix == b.Prefix && a.BID < b.BID
	})
	return nil
}
