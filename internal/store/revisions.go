package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
)

// RecordRevision belongs to one exact value. Vectors carry causal history
// across server replacements; operation hashes make a lost acknowledgement
// distinguishable from a reused client/save number with different content.
type RecordRevision struct {
	Kind               string              `json:"kind"`
	Prefix             string              `json:"prefix"`
	ID                 int                 `json:"id"`
	Vector             map[string]int64    `json:"vector"`
	Hash               string              `json:"hash"`
	Operations         map[string]string   `json:"operations,omitempty"`
	Heads              []string            `json:"heads,omitempty"`
	Reviewed           []string            `json:"reviewed,omitempty"`
	ReviewedOperations map[string][]string `json:"reviewed_operations,omitempty"`
}

func (r *RecordRevision) UnmarshalJSON(b []byte) error {
	type fields RecordRevision
	var value fields
	raw := struct {
		*fields
		ID *Int `json:"id"`
	}{fields: &value}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.ID == nil {
		return errors.New("revision: id is required")
	}
	value.ID = int(*raw.ID)
	*r = RecordRevision(value)
	return nil
}

type RecordCandidate struct {
	Revision RecordRevision  `json:"revision"`
	Value    json.RawMessage `json:"value"`
}

type RecordConflict struct {
	Kind       string            `json:"kind"`
	Prefix     string            `json:"prefix"`
	ID         int               `json:"id"`
	Candidates []RecordCandidate `json:"candidates"`
}

func (c *RecordConflict) UnmarshalJSON(b []byte) error {
	type fields RecordConflict
	var value fields
	raw := struct {
		*fields
		ID *Int `json:"id"`
	}{fields: &value}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.ID == nil {
		return errors.New("conflict: id is required")
	}
	value.ID = int(*raw.ID)
	*c = RecordConflict(value)
	return nil
}

type SaveReceipt struct {
	Revisions []RecordRevision `json:"revisions"`
	Conflicts []RecordConflict `json:"conflicts,omitempty"`
}

type ConflictError struct{ Conflicts []RecordConflict }

func (e *ConflictError) Error() string {
	return "Conflicting saved entries need review before these results can be used."
}

func (s *Store) view(tx *sql.Tx) *Store {
	return &Store{db: s.db, in: tx, operation: s.operation, receipt: s.receipt, withoutRevisions: s.withoutRevisions, touched: s.touched, readGuarded: s.readGuarded}
}

// Checking conflicts and selecting data share a transaction, so a recovery
// arriving between the two cannot expose an arbitrary unresolved winner.
// A read-only transaction keeps that snapshot without joining the writer
// queue or reserving SQLite's write lock while reports are being read.
func reviewedRead[T any](s *Store, read func(*Store) (T, error)) (T, error) {
	var value T
	tx := s.in
	if tx == nil {
		var err error
		tx, err = s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return value, err
		}
		defer tx.Rollback()
	}
	view := s.view(tx)
	if err := view.CheckConflicts(); err != nil {
		return value, err
	}
	view.readGuarded = true
	value, err := read(view)
	if err == nil && s.in == nil {
		err = tx.Commit()
	}
	return value, err
}

func (s *Store) WithoutRevisions(write func(*Store) error) error {
	return s.tx(func(tx *sql.Tx) error { v := s.view(tx); v.withoutRevisions = true; return write(v) })
}

func (s *Store) WithLocalOperation(order Order, write func(*Store) error) error {
	return s.tx(func(tx *sql.Tx) error {
		v := s.view(tx)
		if order.Client != "" && order.Save > 0 {
			v.operation = &order
			v.touched = map[string]bool{}
		}
		return write(v)
	})
}

// Renumbering changes the identity of a journaled save, never its retained
// values. Link its replacement identity only when the old operation is still
// a current head; later local edits must remain untouched.
func renumberLocalOperation(tx *sql.Tx, request *Outbox, next Order) error {
	// An online intent has not changed this client's entries yet. A copied
	// counter can reuse an accepted head with the same payload; renumbering
	// the new request must not relabel that older accepted history.
	if !request.LocalApplied {
		return nil
	}
	old := request.Order
	if old.Client == "" || old.Save <= 0 || next.Client == "" || next.Save <= 0 || old == next {
		return nil
	}
	edits, err := recoveryIntentEdits(request)
	if err != nil {
		return err
	}
	affected := map[string]string{}
	for _, edit := range edits {
		affected[edit.key] = edit.hash
	}
	oldDot := operationKey("client:"+old.Client, old.Save)
	nextActor := "client:" + next.Client
	nextDot := operationKey(nextActor, next.Save)
	revisions, err := allRevisions(tx)
	if err != nil {
		return err
	}
	for _, r := range revisions {
		if affected[recordKey(r.Kind, r.Prefix, r.ID)] != r.Hash {
			continue
		}
		if !containsHash(r.Heads, oldDot) {
			continue
		}
		raw, exists, err := currentValue(tx, r.Kind, r.Prefix, r.ID)
		if err != nil {
			return err
		}
		if !exists || valueHash(raw) != r.Hash {
			continue
		}
		if previous, seen := r.Operations[nextDot]; seen && previous != r.Hash {
			return errors.New("replacement save number already identifies another value")
		}
		r.Operations[nextDot] = r.Hash
		r.Vector = joinVector(r.Vector, map[string]int64{nextActor: next.Save})
		heads := []string{}
		for _, dot := range r.Heads {
			if dot != oldDot && dot != nextDot {
				heads = append(heads, dot)
			}
		}
		r.Heads = append(heads, nextDot)
		sort.Strings(r.Heads)
		if err := saveRevision(tx, r); err != nil {
			return err
		}
		held, err := loadRecoveryHoldback(tx, r)
		if err != nil {
			return err
		}
		if held != nil && held.Current.Hash == r.Hash && containsHash(held.Current.Heads, oldDot) {
			held.Current = r
			if err := saveRecoveryHoldback(tx, *held); err != nil {
				return err
			}
		}
	}
	return nil
}

func valueHash(value []byte) string { hash := sha256.Sum256(value); return hex.EncodeToString(hash[:]) }
func candidateKey(c RecordCandidate) string {
	c.Revision.Reviewed = nil
	raw, _ := json.Marshal(c)
	return valueHash(raw)
}
func joinReviewed(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range [][]string{a, b} {
		for _, key := range list {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}
func recordKey(kind, prefix string, id int) string {
	b, _ := json.Marshal([]any{kind, prefix, id})
	return string(b)
}
func operationKey(actor string, n int64) string { return actor + "/" + strconv.FormatInt(n, 10) }
func cloneVector(v map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for a, n := range v {
		out[a] = n
	}
	return out
}
func joinVector(a, b map[string]int64) map[string]int64 {
	if len(a) == 0 && len(b) == 0 {
		return a
	}
	out := cloneVector(a)
	for actor, n := range b {
		if out[actor] < n {
			out[actor] = n
		}
	}
	return out
}
func dominates(a, b map[string]int64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for actor, n := range b {
		if a[actor] < n {
			return false
		}
	}
	return true
}
func revisionDominates(a, b RecordRevision) bool {
	// A matching frontier operation proves its earlier ancestry even when a
	// replacement first learned that operation through queue replay. Counters
	// alone cannot do this: copied clients may skip different operations.
	if len(a.Heads) > 0 && len(b.Heads) > 0 {
		for _, dot := range b.Heads {
			hash := b.Operations[dot]
			if a.Operations[dot] != hash && !containsHash(a.ReviewedOperations[dot], hash) {
				return false
			}
		}
		return true
	}
	if !dominates(a.Vector, b.Vector) || len(a.Operations) == 0 || len(b.Operations) == 0 {
		return false
	}
	for dot, hash := range b.Operations {
		if a.Operations[dot] != hash && !containsHash(a.ReviewedOperations[dot], hash) {
			return false
		}
	}
	for dot, hashes := range b.ReviewedOperations {
		for _, hash := range hashes {
			if a.Operations[dot] != hash && !containsHash(a.ReviewedOperations[dot], hash) {
				return false
			}
		}
	}
	return true
}

func mergeHeads(a, b RecordRevision) []string {
	// A headless versioned receipt has no explicit frontier. Keep the strict
	// legacy comparison until a genuine new write supplies one.
	if len(a.Heads) == 0 && len(a.Operations) > 0 || len(b.Heads) == 0 && len(b.Operations) > 0 {
		return nil
	}
	out := []string{}
	for _, pair := range [][2]RecordRevision{{a, b}, {b, a}} {
		from, other := pair[0], pair[1]
		for _, dot := range from.Heads {
			hash := from.Operations[dot]
			known := other.Operations[dot] == hash || containsHash(other.ReviewedOperations[dot], hash)
			if known && !containsHash(other.Heads, dot) {
				continue
			}
			if !containsHash(out, dot) {
				out = append(out, dot)
			}
		}
	}
	sort.Strings(out)
	return out
}

func joinRevision(head, older RecordRevision) (RecordRevision, error) {
	ops, variants, err := mergeHistory(head, older)
	if err != nil {
		return head, err
	}
	head.Heads = mergeHeads(head, older)
	head.Vector = joinVector(head.Vector, older.Vector)
	head.Operations, head.ReviewedOperations = ops, variants
	head.Reviewed = joinReviewed(head.Reviewed, older.Reviewed)
	return head, nil
}
func containsHash(hashes []string, hash string) bool {
	for _, h := range hashes {
		if h == hash {
			return true
		}
	}
	return false
}
func mergeVariants(a, b map[string][]string) map[string][]string {
	out := map[string][]string{}
	for dot, hashes := range a {
		out[dot] = append([]string(nil), hashes...)
	}
	for dot, hashes := range b {
		for _, hash := range hashes {
			if !containsHash(out[dot], hash) {
				out[dot] = append(out[dot], hash)
			}
		}
	}
	for dot := range out {
		sort.Strings(out[dot])
	}
	return out
}
func historyVariants(r RecordRevision) map[string][]string {
	variants := mergeVariants(nil, r.ReviewedOperations)
	for dot, hash := range r.Operations {
		if !containsHash(variants[dot], hash) {
			variants[dot] = append(variants[dot], hash)
		}
	}
	return variants
}
func mergeHistory(a, b RecordRevision) (map[string]string, map[string][]string, error) {
	variants := mergeVariants(a.ReviewedOperations, b.ReviewedOperations)
	ops := map[string]string{}
	for dot, hash := range a.Operations {
		ops[dot] = hash
	}
	for dot, hash := range b.Operations {
		if old, ok := ops[dot]; ok && old != hash && !(containsHash(variants[dot], old) && containsHash(variants[dot], hash)) {
			return nil, nil, fmt.Errorf("operation %s has conflicting values", dot)
		}
		ops[dot] = hash
	}
	return ops, variants, nil
}
func loadRevision(tx *sql.Tx, kind, prefix string, id int, hash string) (RecordRevision, error) {
	r := RecordRevision{Kind: kind, Prefix: prefix, ID: id, Hash: hash}
	var raw string
	err := tx.QueryRow(`SELECT revision FROM record_revisions WHERE kind=? AND prefix=? AND record_id=?`, kind, prefix, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	var found RecordRevision
	if err = json.Unmarshal([]byte(raw), &found); err != nil {
		return r, err
	}
	// Direct writes by an older application cannot inherit a modern receipt.
	if found.Hash == hash {
		return found, nil
	}
	return r, nil
}
func saveRevision(tx *sql.Tx, r RecordRevision) error {
	if err := observeLocalHistory(tx, r); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO record_revisions(kind,prefix,record_id,revision) VALUES(?,?,?,?) ON CONFLICT(kind,prefix,record_id) DO UPDATE SET revision=excluded.revision`, r.Kind, r.Prefix, r.ID, string(raw))
	return err
}

// Imported history may come from a newer copy of this same database. Never
// allocate an operation number already used by that actor, even for another row.
func observeLocalHistory(tx *sql.Tx, r RecordRevision) error {
	var actor string
	err := tx.QueryRow(`SELECT actor FROM causal_identity WHERE id=1`).Scan(&actor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if n := r.Vector[actor]; n > 0 {
		if _, err = tx.Exec(`UPDATE causal_identity SET counter=max(counter,?) WHERE id=1`, n); err != nil {
			return err
		}
	}
	// Paired and offline client saves have a separate actor and allocator.
	// Server-only databases have no save_order table.
	var hasClientOrder bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='save_order')`).Scan(&hasClientOrder); err != nil || !hasClientOrder {
		return err
	}
	var client string
	if err := tx.QueryRow(`SELECT client FROM save_order WHERE id=1`).Scan(&client); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if n := r.Vector["client:"+client]; n > 0 {
		_, err := tx.Exec(`UPDATE save_order SET last_save=max(last_save,?) WHERE id=1`, n)
		return err
	}
	return nil
}

func currentValue(tx *sql.Tx, kind, prefix string, id int) ([]byte, bool, error) {
	var value any
	switch kind {
	case "ticket":
		var first, last, phone, pref sql.NullString
		err := tx.QueryRow(`SELECT first_name,last_name,phone_number,pref FROM tickets WHERE prefix=? AND t_id=?`, prefix, id).Scan(&first, &last, &phone, &pref)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		value = Ticket{prefix, id, nstr(first), nstr(last), nstr(phone), nstr(pref)}
	case "metadata":
		var description, donors sql.NullString
		err := tx.QueryRow(`SELECT description,donors FROM baskets WHERE prefix=? AND b_id=?`, prefix, id).Scan(&description, &donors)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		value = []string{nstr(description), nstr(donors)}
	case "drawing":
		var winner sql.NullInt64
		err := tx.QueryRow(`SELECT winning_ticket FROM baskets WHERE prefix=? AND b_id=?`, prefix, id).Scan(&winner)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		value = nint(winner)
	case "prefix":
		var color sql.NullString
		var weight sql.NullInt64
		err := tx.QueryRow(`SELECT color,weight FROM prefixes WHERE prefix=?`, prefix).Scan(&color, &weight)
		if errors.Is(err, sql.ErrNoRows) {
			var encoded string
			if e := tx.QueryRow(`SELECT revision FROM record_revisions WHERE kind='prefix' AND prefix=? AND record_id=0`, prefix).Scan(&encoded); e == nil {
				var r RecordRevision
				if json.Unmarshal([]byte(encoded), &r) == nil && r.Hash == valueHash([]byte("null")) {
					return []byte("null"), true, nil
				}
			}
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		value = Prefix{prefix, nstr(color), nint(weight)}
	default:
		return nil, false, fmt.Errorf("unknown record kind %q", kind)
	}
	raw, err := json.Marshal(value)
	return raw, true, err
}

func (s *Store) nextDot(tx *sql.Tx) (string, int64, error) {
	if s.operation != nil {
		return "client:" + s.operation.Client, s.operation.Save, nil
	}
	var actor string
	var counter int64
	err := tx.QueryRow(`SELECT actor,counter FROM causal_identity WHERE id=1`).Scan(&actor, &counter)
	if errors.Is(err, sql.ErrNoRows) {
		name, e := newClientName()
		if e != nil {
			return "", 0, e
		}
		actor = "store:" + name
		_, err = tx.Exec(`INSERT INTO causal_identity(id,actor,counter) VALUES(1,?,0)`, actor)
	}
	if err != nil {
		return "", 0, err
	}
	counter++
	_, err = tx.Exec(`UPDATE causal_identity SET counter=? WHERE id=1`, counter)
	return actor, counter, err
}

// prepareRecord is called before a form writes its component. A causal retry
// already present in a later recovered version is acknowledged without replay.
func (s *Store) prepareRecord(tx *sql.Tx, kind, prefix string, id int, value any) (bool, error) {
	if s.withoutRevisions {
		return true, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	hash := valueHash(raw)
	old, exists, err := currentValue(tx, kind, prefix, id)
	if err != nil {
		return false, err
	}
	r, err := loadRevision(tx, kind, prefix, id, valueHash(old))
	if err != nil {
		return false, err
	}
	actor, n, err := s.nextDot(tx)
	if err != nil {
		return false, err
	}
	dot := operationKey(actor, n)
	if prior, seen := r.Operations[dot]; seen && !s.touched[recordKey(kind, prefix, id)] {
		if prior != hash && !containsHash(r.ReviewedOperations[dot], hash) {
			return false, fmt.Errorf("client save number already identifies a different %s value", kind)
		}
		if s.receipt != nil {
			s.receipt.Revisions = append(s.receipt.Revisions, RecordRevision{Kind: kind, Prefix: prefix, ID: id, Hash: hash, Vector: map[string]int64{actor: n}, Operations: map[string]string{dot: hash}, Heads: []string{dot}})
		}
		return false, nil
	}
	if conflicts, err := listConflicts(tx, kind, prefix, id); err != nil {
		return false, err
	} else if len(conflicts) > 0 {
		return false, &ConflictError{conflicts}
	}
	if !exists {
		r = RecordRevision{Kind: kind, Prefix: prefix, ID: id}
	}
	r.Vector = joinVector(r.Vector, map[string]int64{actor: n})
	r.Hash = hash
	if r.Operations == nil {
		r.Operations = map[string]string{}
	}
	r.Operations[dot] = hash
	r.Heads = []string{dot}
	if s.touched != nil {
		s.touched[recordKey(kind, prefix, id)] = true
	}
	if err = saveRevision(tx, r); err != nil {
		return false, err
	}
	if s.receipt != nil {
		replaced := false
		for i, previous := range s.receipt.Revisions {
			if previous.Kind == kind && previous.Prefix == prefix && previous.ID == id {
				s.receipt.Revisions[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			s.receipt.Revisions = append(s.receipt.Revisions, r)
		}
	}
	return true, nil
}

func (s *Store) ApplyReceipt(receipt SaveReceipt) error {
	return s.tx(func(tx *sql.Tx) error {
		for _, incoming := range receipt.Revisions {
			if err := validateRevision(incoming); err != nil {
				return err
			}
			raw, exists, err := currentValue(tx, incoming.Kind, incoming.Prefix, incoming.ID)
			if err != nil {
				return err
			}
			if !exists || valueHash(raw) != incoming.Hash {
				if !exists && incoming.Kind == "prefix" && incoming.Hash == valueHash([]byte("null")) {
					raw = []byte("null")
					exists = true
				} else {
					continue
				}
			}
			current, err := loadRevision(tx, incoming.Kind, incoming.Prefix, incoming.ID, incoming.Hash)
			if err != nil {
				return err
			}
			incoming, err = joinRevision(incoming, current)
			if err != nil {
				return err
			}
			if err = saveRevision(tx, incoming); err != nil {
				return err
			}
			conflicts, err := listConflicts(tx, incoming.Kind, incoming.Prefix, incoming.ID)
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				if err = mergeConflictSet(tx, RecordCandidate{Revision: incoming, Value: raw}, conflicts, func(RecordCandidate) error { return nil }); err != nil {
					return err
				}
			}
		}
		for _, conflict := range receipt.Conflicts {
			for _, candidate := range conflict.Candidates {
				if err := validateCandidate(&candidate); err != nil {
					return err
				}
				if err := mergeRecovered(tx, candidate, func() error { return applyCandidate(tx, candidate) }); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// MatchingReceipt returns metadata only for the caller's exact offered values.
// No other workstation's ticket or basket data is copied back to this client.
func (s *Store) MatchingReceipt(snapshot RecoverySnapshot) (SaveReceipt, error) {
	receipt := SaveReceipt{Revisions: []RecordRevision{}}
	err := s.tx(func(tx *sql.Tx) error {
		candidates, err := snapshotCandidates(snapshot)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			r := candidate.Revision
			conflicts, err := listConflicts(tx, r.Kind, r.Prefix, r.ID)
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				continue
			}
			raw, exists, err := currentValue(tx, r.Kind, r.Prefix, r.ID)
			if err != nil {
				return err
			}
			if !exists || valueHash(raw) != r.Hash {
				continue
			}
			r, err = loadRevision(tx, r.Kind, r.Prefix, r.ID, r.Hash)
			if err != nil {
				return err
			}
			receipt.Revisions = append(receipt.Revisions, r)
		}
		return nil
	})
	return receipt, err
}

// RestoreSnapshot is an operator's deliberate restore, hence a new accepted
// value. It retains both restored and current ancestry instead of relabelling
// an older receipt as the new head. Native unresolved conflicts stay unresolved.
func (s *Store) RestoreSnapshot(snapshot RecoverySnapshot) error {
	if err := ValidateRecoverySnapshot(&snapshot); err != nil {
		return err
	}
	// Numbered requests are durable prefix Push operations. Use ordinary
	// per-record replay protection while retaining the backup API's legacy
	// prefix identities. General operator restores remain unnumbered.
	if s.operation != nil {
		if err := ValidateNumberedRestore(snapshot); err != nil {
			return err
		}
		return s.UpsertPrefixes(snapshot.Prefixes)
	}
	return s.tx(func(tx *sql.Tx) error {
		candidates, err := snapshotCandidates(snapshot)
		if err != nil {
			return err
		}
		unresolved := map[string]bool{}
		for _, conflict := range snapshot.Conflicts {
			unresolved[recordKey(conflict.Kind, conflict.Prefix, conflict.ID)] = true
		}
		for _, candidate := range candidates {
			r := candidate.Revision
			r.Operations = maps.Clone(r.Operations)
			if unresolved[recordKey(r.Kind, r.Prefix, r.ID)] {
				// The file explicitly retains alternatives. Reinstating its value
				// and provenance must not invent a review of those alternatives.
				if err := applyCandidate(tx, candidate); err != nil {
					return err
				}
				if err := saveRevision(tx, r); err != nil {
					return err
				}
				if _, err := tx.Exec(`DELETE FROM record_conflicts WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID); err != nil {
					return err
				}
				continue
			}
			old, exists, err := currentValue(tx, r.Kind, r.Prefix, r.ID)
			if err != nil {
				return err
			}
			if exists {
				current, err := loadRevision(tx, r.Kind, r.Prefix, r.ID, valueHash(old))
				if err != nil {
					return err
				}
				r.Vector = joinVector(current.Vector, r.Vector)
				r.ReviewedOperations = mergeVariants(r.ReviewedOperations, historyVariants(current))
				r.ReviewedOperations = mergeVariants(r.ReviewedOperations, historyVariants(r))
				if r.Operations == nil {
					r.Operations = map[string]string{}
				}
				for dot, hash := range current.Operations {
					if _, known := r.Operations[dot]; !known {
						r.Operations[dot] = hash
					}
				}
				r.Reviewed = joinReviewed(r.Reviewed, append(current.Reviewed, candidateKey(RecordCandidate{Revision: current, Value: old})))
			}
			conflicts, err := listConflicts(tx, r.Kind, r.Prefix, r.ID)
			if err != nil {
				return err
			}
			for _, conflict := range conflicts {
				for _, c := range conflict.Candidates {
					r.Vector = joinVector(r.Vector, c.Revision.Vector)
					r.ReviewedOperations = mergeVariants(r.ReviewedOperations, historyVariants(c.Revision))
					r.Reviewed = joinReviewed(r.Reviewed, append(c.Revision.Reviewed, candidateKey(c)))
					if r.Operations == nil {
						r.Operations = map[string]string{}
					}
					for dot, hash := range c.Revision.Operations {
						if _, known := r.Operations[dot]; !known {
							r.Operations[dot] = hash
						}
					}
				}
			}
			if err := observeLocalHistory(tx, r); err != nil {
				return err
			}
			actor, n, err := s.nextDot(tx)
			if err != nil {
				return err
			}
			r.Vector = joinVector(r.Vector, map[string]int64{actor: n})
			if r.Operations == nil {
				r.Operations = map[string]string{}
			}
			r.Operations[operationKey(actor, n)] = r.Hash
			r.Heads = []string{operationKey(actor, n)}
			candidate.Revision = r
			if err = applyCandidate(tx, candidate); err != nil {
				return err
			}
			if err = saveRevision(tx, r); err != nil {
				return err
			}
			if _, err = tx.Exec(`DELETE FROM record_conflicts WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID); err != nil {
				return err
			}
		}
		for _, conflict := range snapshot.Conflicts {
			for _, candidate := range conflict.Candidates {
				if err := addCandidate(tx, candidate); err != nil {
					return err
				}
			}
		}
		return setReviewToken(tx)
	})
}

func ValidateNumberedRestore(snapshot RecoverySnapshot) error {
	if len(snapshot.Tickets) > 0 || len(snapshot.Baskets) > 0 || len(snapshot.BasketComponents) > 0 || len(snapshot.Revisions) > 0 || len(snapshot.Conflicts) > 0 || len(snapshot.DeletedPrefixes) > 0 || len(snapshot.WithheldRecords) > 0 {
		return errors.New("numbered backup requests support prefix Push only; use an unnumbered request for an operator backup restore")
	}
	return nil
}

type revisionQuery interface {
	Query(string, ...any) (*sql.Rows, error)
}

func listConflicts(q revisionQuery, kind, prefix string, id int) ([]RecordConflict, error) {
	query := `SELECT kind,prefix,record_id,candidate FROM record_conflicts WHERE 1=1`
	args := []any{}
	if kind != "" {
		query += ` AND kind=?`
		args = append(args, kind)
	}
	if prefix != "" {
		query += ` AND prefix=?`
		args = append(args, prefix)
	}
	if id >= 0 {
		query += ` AND record_id=?`
		args = append(args, id)
	}
	query += ` ORDER BY kind,prefix,record_id,candidate_id`
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecordConflict{}
	var last string
	for rows.Next() {
		var k, p, raw string
		var n int
		if err = rows.Scan(&k, &p, &n, &raw); err != nil {
			return nil, err
		}
		key := recordKey(k, p, n)
		if key != last {
			out = append(out, RecordConflict{Kind: k, Prefix: p, ID: n, Candidates: []RecordCandidate{}})
			last = key
		}
		var c RecordCandidate
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		out[len(out)-1].Candidates = append(out[len(out)-1].Candidates, c)
	}
	return out, rows.Err()
}
func (s *Store) Conflicts() ([]RecordConflict, error) {
	if s.in != nil {
		return listConflicts(s.in, "", "", -1)
	}
	return listConflicts(s.db, "", "", -1)
}
func (s *Store) CheckConflicts() error {
	cs, err := s.Conflicts()
	if err != nil {
		return err
	}
	if len(cs) > 0 {
		return &ConflictError{cs}
	}
	return nil
}
func addCandidate(tx *sql.Tx, c RecordCandidate) error {
	if err := observeLocalHistory(tx, c.Revision); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO record_conflicts(kind,prefix,record_id,candidate_id,candidate) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, c.Revision.Kind, c.Revision.Prefix, c.Revision.ID, valueHash(raw), string(raw))
	return err
}

func allRevisions(tx *sql.Tx) ([]RecordRevision, error) {
	rows, err := tx.Query(`SELECT revision FROM record_revisions ORDER BY kind,prefix,record_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecordRevision{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r RecordRevision
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Receipt(order Order) (SaveReceipt, error) {
	var result SaveReceipt
	var raw string
	err := s.db.QueryRow(`SELECT receipt FROM operation_receipts WHERE client=? AND save=?`, order.Client, order.Save).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	err = json.Unmarshal([]byte(raw), &result)
	return result, err
}

// mergeRecovered compares only genuine causal receipts. Missing receipts do
// not acquire invented timestamps; differing unversioned values are conflicts.
func mergeRecovered(tx *sql.Tx, incoming RecordCandidate, apply func() error) error {
	r := incoming.Revision
	old, exists, err := currentValue(tx, r.Kind, r.Prefix, r.ID)
	if err != nil {
		return err
	}
	if r.Kind == "metadata" || r.Kind == "drawing" {
		var owned bool
		column := r.Kind
		if column == "metadata" {
			column = "metadata"
		}
		err = tx.QueryRow(`SELECT coalesce((SELECT `+column+` FROM basket_components WHERE prefix=? AND b_id=?),1)`, r.Prefix, r.ID).Scan(&owned)
		if err != nil {
			return err
		}
		exists = exists && owned
	}
	if !exists {
		if err = apply(); err != nil {
			return err
		}
		return saveRevision(tx, r)
	}
	current, err := loadRevision(tx, r.Kind, r.Prefix, r.ID, valueHash(old))
	if err != nil {
		return err
	}
	conflicts, err := listConflicts(tx, r.Kind, r.Prefix, r.ID)
	if err != nil {
		return err
	}
	for _, reviewed := range current.Reviewed {
		if reviewed == candidateKey(incoming) {
			return nil
		}
	}
	if len(conflicts) > 0 {
		return mergeConflictSet(tx, incoming, conflicts, func(candidate RecordCandidate) error { return applyCandidate(tx, candidate) })
	}
	if containsHash(r.Reviewed, candidateKey(RecordCandidate{Revision: current, Value: old})) {
		if err = apply(); err != nil {
			return err
		}
		return saveRevision(tx, r)
	}
	if current.Hash == r.Hash {
		joined, e := joinRevision(current, r)
		if e == nil {
			return saveRevision(tx, joined)
		}
	}
	_, _, compatible := mergeHistory(current, r)
	if compatible == nil && revisionDominates(current, r) && !revisionDominates(r, current) {
		current, err = joinRevision(current, r)
		if err != nil {
			return err
		}
		return saveRevision(tx, current)
	}
	if compatible == nil && revisionDominates(r, current) && !revisionDominates(current, r) {
		if err = apply(); err != nil {
			return err
		}
		r, err = joinRevision(r, current)
		if err != nil {
			return err
		}
		return saveRevision(tx, r)
	}
	if err = addCandidate(tx, RecordCandidate{Revision: current, Value: old}); err != nil {
		return err
	}
	return addCandidate(tx, incoming)
}

func candidateCovers(head, older RecordCandidate) bool {
	if containsHash(head.Revision.Reviewed, candidateKey(older)) {
		return true
	}
	if _, _, err := mergeHistory(head.Revision, older.Revision); err != nil {
		return false
	}
	return revisionDominates(head.Revision, older.Revision) && !revisionDominates(older.Revision, head.Revision)
}

// A late resolution can arrive after both old conflicting copies. Retire only
// the candidates it actually covers; a genuinely new branch still needs review.
// Local receipt application supplies a no-op write, retaining only local data.
func mergeConflictSet(tx *sql.Tx, head RecordCandidate, conflicts []RecordConflict, write func(RecordCandidate) error) error {
	remaining := []RecordCandidate{}
	for _, conflict := range conflicts {
		for _, candidate := range conflict.Candidates {
			if head.Revision.Hash == candidate.Revision.Hash {
				joined, err := joinRevision(head.Revision, candidate.Revision)
				if err == nil {
					head.Revision = joined
					continue
				}
			}
			if candidateCovers(candidate, head) {
				return nil
			}
			remaining = append(remaining, candidate)
		}
	}
	keep := []RecordCandidate{}
	for _, candidate := range remaining {
		if !candidateCovers(head, candidate) {
			keep = append(keep, candidate)
		} else if joined, err := joinRevision(head.Revision, candidate.Revision); err == nil {
			head.Revision = joined
		}
	}
	r := head.Revision
	if len(keep) == 0 {
		if err := write(head); err != nil {
			return err
		}
		if err := saveRevision(tx, r); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM record_conflicts WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID)
		return err
	}
	if _, err := tx.Exec(`DELETE FROM record_conflicts WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID); err != nil {
		return err
	}
	if err := addCandidate(tx, head); err != nil {
		return err
	}
	for _, candidate := range keep {
		if err := addCandidate(tx, candidate); err != nil {
			return err
		}
	}
	return nil
}

func validateRevision(r RecordRevision) error {
	if r.Kind != "ticket" && r.Kind != "metadata" && r.Kind != "drawing" && r.Kind != "prefix" {
		return fmt.Errorf("unknown revision kind %q", r.Kind)
	}
	if strings.TrimSpace(r.Prefix) == "" || r.ID < 0 || int64(r.ID) > SafeIntegerMax || len(r.Hash) != 64 {
		return errors.New("invalid record revision")
	}
	for actor, n := range r.Vector {
		if actor == "" || len(actor) > 128 || n <= 0 {
			return errors.New("invalid revision vector")
		}
	}
	if _, err := hex.DecodeString(r.Hash); err != nil {
		return errors.New("invalid revision hash")
	}
	if len(r.Vector) > 0 && len(r.Operations) == 0 {
		return errors.New("revision vector is missing operation ancestry")
	}
	for dot, hash := range r.Operations {
		cut := strings.LastIndexByte(dot, '/')
		if cut < 1 {
			return errors.New("invalid operation identity")
		}
		n, err := strconv.ParseInt(dot[cut+1:], 10, 64)
		if err != nil || n <= 0 || r.Vector[dot[:cut]] < n || len(hash) != 64 {
			return errors.New("operation is outside its revision vector")
		}
		if _, err = hex.DecodeString(hash); err != nil {
			return errors.New("invalid operation hash")
		}
	}
	for dot, hashes := range r.ReviewedOperations {
		if _, exists := r.Operations[dot]; !exists || len(hashes) == 0 {
			return errors.New("reviewed operation is missing its canonical identity")
		}
		for _, hash := range hashes {
			if len(hash) != 64 {
				return errors.New("invalid reviewed operation hash")
			}
			if _, err := hex.DecodeString(hash); err != nil {
				return errors.New("invalid reviewed operation hash")
			}
		}
	}
	for _, head := range r.Heads {
		if r.Operations[head] != r.Hash {
			return errors.New("revision head must identify its current value")
		}
	}
	for _, reviewed := range r.Reviewed {
		if len(reviewed) != 64 {
			return errors.New("invalid reviewed candidate hash")
		}
		if _, err := hex.DecodeString(reviewed); err != nil {
			return errors.New("invalid reviewed candidate hash")
		}
	}
	return nil
}

// validateCandidate restores the record's original typed JSON encoding before
// comparing its digest. Whitespace, object order and equivalent string escapes
// are transport details; the digest and all reviewed candidate identities still
// belong to the same exact typed value. Retain that canonical payload so later
// comparisons, review keys and database writes cannot depend on the transport.
func validateCandidate(c *RecordCandidate) error {
	r := c.Revision
	if err := validateRevision(r); err != nil {
		return err
	}
	var canonical any
	switch r.Kind {
	case "ticket":
		var value Ticket
		if err := json.Unmarshal(c.Value, &value); err != nil {
			return err
		}
		if value.Prefix != r.Prefix || value.TID != r.ID {
			return errors.New("ticket candidate identity mismatch")
		}
		if err := ValidateTickets([]Ticket{value}); err != nil {
			return err
		}
		canonical = value
	case "prefix":
		if r.ID != 0 {
			return errors.New("invalid prefix candidate id")
		}
		if strings.TrimSpace(string(c.Value)) == "null" {
			break
		}
		var value Prefix
		if err := json.Unmarshal(c.Value, &value); err != nil {
			return err
		}
		if value.Prefix != r.Prefix {
			return errors.New("prefix candidate identity mismatch")
		}
		canonical = value
	case "metadata":
		var value []string
		if err := json.Unmarshal(c.Value, &value); err != nil {
			return err
		}
		if len(value) != 2 {
			return errors.New("metadata candidate must contain description and donors")
		}
		canonical = value
	case "drawing":
		if strings.TrimSpace(string(c.Value)) == "null" {
			return errors.New("drawing candidate cannot be null")
		}
		var value int
		if err := json.Unmarshal(c.Value, &value); err != nil {
			return err
		}
		if err := ValidateBaskets([]Basket{{Prefix: r.Prefix, BID: r.ID, WinningTicket: value}}); err != nil {
			return err
		}
		canonical = value
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	if valueHash(raw) != r.Hash {
		return errors.New("candidate payload hash mismatch")
	}
	c.Value = raw
	return nil
}

var ErrConflictChanged = errors.New("conflict changed; review the current copies before choosing")

func ConflictToken(conflict RecordConflict) string {
	raw, _ := json.Marshal(conflict)
	return valueHash(raw)
}

// ResolveConflict explicitly chooses one candidate, retaining every reviewed
// candidate's ancestry so another recovery cannot reopen that same dispute.
func (s *Store) ResolveConflict(kind, prefix string, id int, hash string) error {
	return s.ResolveConflictIfCurrent(kind, prefix, id, hash, "")
}
func (s *Store) ResolveConflictIfCurrent(kind, prefix string, id int, hash, expectedToken string) error {
	return s.tx(func(tx *sql.Tx) error {
		cs, err := listConflicts(tx, kind, prefix, id)
		if err != nil {
			return err
		}
		if len(cs) != 1 {
			return ErrConflictChanged
		}
		if expectedToken != "" && ConflictToken(cs[0]) != expectedToken {
			return ErrConflictChanged
		}
		var chosen *RecordCandidate
		vector := map[string]int64{}
		operations := map[string]string{}
		reviewed := []string{}
		variants := map[string][]string{}
		for i := range cs[0].Candidates {
			c := &cs[0].Candidates[i]
			if c.Revision.Hash == hash {
				chosen = c
			}
			vector = joinVector(vector, c.Revision.Vector)
			variants = mergeVariants(variants, historyVariants(c.Revision))
			reviewed = joinReviewed(reviewed, append(c.Revision.Reviewed, candidateKey(*c)))
			for dot, h := range c.Revision.Operations {
				operations[dot] = h
			}
		}
		if chosen == nil {
			return errors.New("selected candidate no longer exists")
		}
		// Preserve the chosen branch's canonical hashes and both reviewed forks.
		for dot, hash := range chosen.Revision.Operations {
			operations[dot] = hash
		}
		actor, n, err := s.nextDot(tx)
		if err != nil {
			return err
		}
		vector[actor] = n
		operations[operationKey(actor, n)] = hash
		chosen.Revision.Vector = vector
		chosen.Revision.Operations = operations
		chosen.Revision.Heads = []string{operationKey(actor, n)}
		chosen.Revision.ReviewedOperations = variants
		chosen.Revision.Reviewed = reviewed
		if err = applyCandidate(tx, *chosen); err != nil {
			return err
		}
		if err = saveRevision(tx, chosen.Revision); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM record_conflicts WHERE kind=? AND prefix=? AND record_id=?`, kind, prefix, id)
		if err != nil {
			return err
		}
		return setReviewToken(tx)
	})
}

func setReviewToken(tx *sql.Tx) error {
	has, err := hasRecovery(tx)
	if err != nil || !has {
		return err
	}
	token, err := newClientName()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO causal_review(id,token) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET token=excluded.token`, token)
	return err
}
func (s *Store) ReviewToken() (string, error) {
	var token string
	err := s.db.QueryRow(`SELECT token FROM causal_review WHERE id=1`).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return token, err
}

func applyCandidate(tx *sql.Tx, c RecordCandidate) error {
	r := c.Revision
	switch r.Kind {
	case "ticket":
		var t Ticket
		if err := json.Unmarshal(c.Value, &t); err != nil {
			return err
		}
		_, err := tx.Exec(upsertTicketSQL, t.Prefix, t.TID, t.FirstName, t.LastName, t.PhoneNumber, t.Pref)
		return err
	case "prefix":
		if strings.TrimSpace(string(c.Value)) == "null" {
			_, err := tx.Exec(`DELETE FROM prefixes WHERE prefix=?`, r.Prefix)
			return err
		}
		var p Prefix
		if err := json.Unmarshal(c.Value, &p); err != nil {
			return err
		}
		_, err := tx.Exec(upsertPrefixSQL, p.Prefix, p.Color, p.Weight)
		return err
	case "metadata", "drawing":
		if _, err := tx.Exec(`INSERT INTO baskets(prefix,b_id) VALUES(?,?) ON CONFLICT DO NOTHING`, r.Prefix, r.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO basket_components(prefix,b_id) VALUES(?,?) ON CONFLICT DO NOTHING`, r.Prefix, r.ID); err != nil {
			return err
		}
		if r.Kind == "metadata" {
			var v []string
			if err := json.Unmarshal(c.Value, &v); err != nil {
				return err
			}
			if len(v) != 2 {
				return errors.New("invalid metadata")
			}
			_, err := tx.Exec(`UPDATE baskets SET description=?,donors=? WHERE prefix=? AND b_id=?`, v[0], v[1], r.Prefix, r.ID)
			return err
		}
		var winner int
		if err := json.Unmarshal(c.Value, &winner); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE baskets SET winning_ticket=? WHERE prefix=? AND b_id=?`, winner, r.Prefix, r.ID)
		return err
	}
	return errors.New("unknown candidate kind")
}

func snapshotCandidates(snapshot RecoverySnapshot) ([]RecordCandidate, error) {
	components, err := recoveryComponents(snapshot)
	if err != nil {
		return nil, err
	}
	values := map[string]RecordCandidate{}
	add := func(kind, prefix string, id int, value any) {
		raw, _ := json.Marshal(value)
		values[recordKey(kind, prefix, id)] = RecordCandidate{Revision: RecordRevision{Kind: kind, Prefix: prefix, ID: id, Hash: valueHash(raw)}, Value: raw}
	}
	for _, p := range snapshot.Prefixes {
		add("prefix", p.Prefix, 0, p)
	}
	for _, prefix := range snapshot.DeletedPrefixes {
		if prefix == "" {
			return nil, errors.New("empty deleted prefix")
		}
		if _, exists := values[recordKey("prefix", prefix, 0)]; exists {
			return nil, errors.New("prefix is both present and deleted")
		}
		add("prefix", prefix, 0, nil)
	}
	for _, t := range snapshot.Tickets {
		add("ticket", t.Prefix, t.TID, t)
	}
	for _, b := range snapshot.Baskets {
		c := components[basketID{b.Prefix, b.BID}]
		if c.Metadata {
			add("metadata", b.Prefix, b.BID, []string{b.Description, b.Donors})
		}
		if c.Drawing {
			add("drawing", b.Prefix, b.BID, b.WinningTicket)
		}
	}
	seen := map[string]bool{}
	for _, r := range snapshot.Revisions {
		if err = validateRevision(r); err != nil {
			return nil, err
		}
		key := recordKey(r.Kind, r.Prefix, r.ID)
		c, ok := values[key]
		if !ok || seen[key] || r.Hash != c.Revision.Hash {
			return nil, errors.New("revision does not match its saved value")
		}
		c.Revision = r
		values[key] = c
		seen[key] = true
	}
	for _, conflict := range snapshot.Conflicts {
		if len(conflict.Candidates) < 2 {
			return nil, errors.New("conflict needs at least two candidates")
		}
		for i := range conflict.Candidates {
			c := &conflict.Candidates[i]
			if err := validateCandidate(c); err != nil {
				return nil, err
			}
			if c.Revision.Kind != conflict.Kind || c.Revision.Prefix != conflict.Prefix || c.Revision.ID != conflict.ID || valueHash(c.Value) != c.Revision.Hash {
				return nil, errors.New("conflict candidate does not match its identity or hash")
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]RecordCandidate, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out, nil
}
