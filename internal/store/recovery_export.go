package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ExportRecoveryForSync offers only retained history, leaving journaled edits
// to ordinary ordered replay. Their current values have not necessarily been
// accepted by any server and must not be presented as competing saved copies.
// Native backups use ExportRecovery so pending and rejected work stays available.
func (s *Store) ExportRecoveryForSync() (RecoverySnapshot, error) {
	var snapshot RecoverySnapshot
	err := s.tx(func(tx *sql.Tx) error {
		var err error
		snapshot, err = s.view(tx).ExportRecovery()
		if err != nil {
			return err
		}
		for i := range snapshot.WithheldRecords {
			held := &snapshot.WithheldRecords[i]
			pending, err := journalOwnsRevision(tx, held.Current, true)
			if err != nil {
				return err
			}
			if pending {
				// Its ordered replay may already be an ancestor of another
				// client's correction. Offering an independent pre-Push copy
				// before that replay would manufacture a recovery conflict.
				held.Prior = nil
			}
		}
		if err := applyRecoveryHoldbacks(&snapshot); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT ` + outboxCols + `, rejected <> '' FROM outbox UNION ALL SELECT ` + outboxCols + `, 1 FROM outbox_failed`)
		if err != nil {
			return err
		}
		type journalEntry struct {
			request *Outbox
			failed  bool
		}
		var requests []journalEntry
		for rows.Next() {
			var failed bool
			o, err := scanOutbox(recoveryOutboxScanner{rows, &failed})
			if err != nil {
				rows.Close()
				return err
			}
			requests = append(requests, journalEntry{o, failed})
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		candidates, err := snapshotCandidates(snapshot)
		if err != nil {
			return err
		}
		current := map[string]RecordRevision{}
		for _, c := range candidates {
			r := c.Revision
			current[recordKey(r.Kind, r.Prefix, r.ID)] = r
		}
		omit := map[string]bool{}
		for _, entry := range requests {
			request := entry.request
			// A rejected online intent may never have changed the retained copy.
			if !request.LocalApplied {
				continue
			}
			edits, err := recoveryIntentEdits(request)
			if err != nil {
				return fmt.Errorf("inspect queued save %d: %w", request.ID, err)
			}
			for _, edit := range edits {
				r, exists := current[edit.key]
				if !exists || r.Hash != edit.hash {
					continue
				}
				if entry.failed && len(r.Heads) > 0 && !containsHash(r.Heads, operationKey("client:"+request.Order.Client, request.Order.Save)) {
					// A restored or standalone copy may share a rejected payload
					// while belonging to completely different accepted history.
					continue
				}
				if request.Order.Client != "" && request.Order.Save > 0 && len(r.Operations) > 0 {
					actor := "client:" + request.Order.Client
					dot := operationKey(actor, request.Order.Save)
					if len(r.Heads) > 0 && !containsHash(r.Heads, dot) {
						// A later standalone or copied-client save can have another
						// actor. Its explicit head supersedes this failed operation.
						continue
					}
					// A later retained save can have the same payload as an old
					// failed request; the failed request does not own that revision.
					if r.Vector[actor] > request.Order.Save {
						continue
					}
					// The baskets endpoint ignores winners on updates. Only an
					// operation actually recorded on drawing owns its insert winner.
					if edit.insertWinner && r.Operations[dot] != edit.hash {
						continue
					}
				}
				omit[edit.key] = true
			}
		}
		filterRecoverySnapshot(&snapshot, omit)
		return nil
	})
	return snapshot, err
}

type recoveryOutboxScanner struct {
	row    rowScanner
	failed *bool
}

func (s recoveryOutboxScanner) Scan(dest ...any) error {
	return s.row.Scan(append(dest, s.failed)...)
}

type recoveryIntentEdit struct {
	key, hash    string
	insertWinner bool
	candidate    RecordCandidate
}

// Request values, rather than just row IDs, keep an older failed request from
// hiding a later corrected copy. Basket metadata and drawing remain separate.
func recoveryIntentEdits(o *Outbox) ([]recoveryIntentEdit, error) {
	var edits []recoveryIntentEdit
	add := func(kind, prefix string, id int, value any, insertWinner bool) {
		raw, _ := json.Marshal(value)
		r := RecordRevision{Kind: kind, Prefix: prefix, ID: id, Hash: valueHash(raw)}
		edits = append(edits, recoveryIntentEdit{recordKey(kind, prefix, id), r.Hash, insertWinner, RecordCandidate{Revision: r, Value: raw}})
	}
	u, err := url.Parse(o.Path)
	if err != nil {
		return nil, err
	}
	if o.Method == http.MethodDelete && u.Path == "/api/prefixes" {
		add("prefix", u.Query().Get("p"), 0, nil, false)
		return edits, nil
	}
	if o.Method != http.MethodPost {
		return nil, fmt.Errorf("unsupported method %q", o.Method)
	}
	switch u.Path {
	case "/api/backuprestore":
		var backup BackupFile
		if err := json.Unmarshal(o.Body, &backup); err != nil {
			return nil, err
		}
		if len(backup.Tickets) != 0 || len(backup.Baskets) != 0 {
			return nil, fmt.Errorf("unsupported non-prefix backup intent")
		}
		for _, p := range backup.Prefixes {
			add("prefix", p.Prefix, 0, p, false)
		}
	case "/api/prefixes":
		var rows []Prefix
		if err := json.Unmarshal(o.Body, &rows); err != nil {
			return nil, err
		}
		for _, p := range rows {
			add("prefix", p.Prefix, 0, p, false)
		}
	case "/api/tickets", "/api/search/tickets":
		var rows []Ticket
		if err := json.Unmarshal(o.Body, &rows); err != nil {
			return nil, err
		}
		for _, t := range rows {
			add("ticket", t.Prefix, t.TID, t, false)
		}
	case "/api/baskets", "/api/drawing":
		var rows []Basket
		if err := json.Unmarshal(o.Body, &rows); err != nil {
			return nil, err
		}
		seen := map[basketID]bool{}
		for _, b := range rows {
			if u.Path == "/api/drawing" {
				add("drawing", b.Prefix, b.BID, b.WinningTicket, false)
				continue
			}
			add("metadata", b.Prefix, b.BID, []string{b.Description, b.Donors}, false)
			id := basketID{b.Prefix, b.BID}
			if !seen[id] && b.WinningTicket != 0 {
				add("drawing", b.Prefix, b.BID, b.WinningTicket, true)
			}
			seen[id] = true
		}
	default:
		return nil, fmt.Errorf("unsupported save path %q", o.Path)
	}
	return edits, nil
}

func filterRecoverySnapshot(snapshot *RecoverySnapshot, omit map[string]bool) {
	prefixes := snapshot.Prefixes[:0]
	for _, p := range snapshot.Prefixes {
		if !omit[recordKey("prefix", p.Prefix, 0)] {
			prefixes = append(prefixes, p)
		}
	}
	snapshot.Prefixes = prefixes
	tickets := snapshot.Tickets[:0]
	for _, t := range snapshot.Tickets {
		if !omit[recordKey("ticket", t.Prefix, t.TID)] {
			tickets = append(tickets, t)
		}
	}
	snapshot.Tickets = tickets
	components := map[basketID]BasketComponents{}
	for _, c := range snapshot.BasketComponents {
		components[basketID{c.Prefix, c.BID}] = c
	}
	baskets := snapshot.Baskets[:0]
	keptComponents := snapshot.BasketComponents[:0]
	for _, b := range snapshot.Baskets {
		c := components[basketID{b.Prefix, b.BID}]
		c.Metadata = c.Metadata && !omit[recordKey("metadata", b.Prefix, b.BID)]
		c.Drawing = c.Drawing && !omit[recordKey("drawing", b.Prefix, b.BID)]
		if !c.Metadata && !c.Drawing {
			continue
		}
		if !c.Metadata {
			b.Description, b.Donors = "", ""
		}
		if !c.Drawing {
			b.WinningTicket = 0
		}
		baskets = append(baskets, b)
		keptComponents = append(keptComponents, c)
	}
	snapshot.Baskets, snapshot.BasketComponents = baskets, keptComponents
	revisions := snapshot.Revisions[:0]
	for _, r := range snapshot.Revisions {
		if !omit[recordKey(r.Kind, r.Prefix, r.ID)] {
			revisions = append(revisions, r)
		}
	}
	snapshot.Revisions = revisions
	conflicts := snapshot.Conflicts[:0]
	for _, c := range snapshot.Conflicts {
		if !omit[recordKey(c.Kind, c.Prefix, c.ID)] {
			conflicts = append(conflicts, c)
		}
	}
	snapshot.Conflicts = conflicts
	deleted := snapshot.DeletedPrefixes[:0]
	for _, prefix := range snapshot.DeletedPrefixes {
		if !omit[recordKey("prefix", prefix, 0)] {
			deleted = append(deleted, prefix)
		}
	}
	snapshot.DeletedPrefixes = deleted
}
