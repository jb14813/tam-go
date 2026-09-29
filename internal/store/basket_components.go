package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// BasketComponents distinguishes a saved empty value from a part of a
// basket this client never entered. Metadata is description and donors;
// drawing is the winning ticket, including an explicit zero (clear).
type BasketComponents struct {
	Prefix   string `json:"prefix"`
	BID      int    `json:"b_id"`
	Metadata bool   `json:"metadata"`
	Drawing  bool   `json:"drawing"`
}

func (c *BasketComponents) UnmarshalJSON(b []byte) error {
	type fields BasketComponents
	var value fields
	raw := struct {
		*fields
		BID *Int `json:"b_id"`
	}{fields: &value}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.BID == nil {
		return errors.New("basket components: b_id is required")
	}
	value.BID = int(*raw.BID)
	*c = BasketComponents(value)
	return nil
}

const NativeBackupFormat = "tam-native-v1"

// RecoverySnapshot is the native backup and recovery document.
type RecoverySnapshot struct {
	BackupFile
	Format           string             `json:"format,omitempty"`
	BasketComponents []BasketComponents `json:"basket_components"`
	Revisions        []RecordRevision   `json:"revisions,omitempty"`
	Conflicts        []RecordConflict   `json:"conflicts,omitempty"`
	DeletedPrefixes  []string           `json:"deleted_prefixes,omitempty"`
	WithheldRecords  []RecoveryHoldback `json:"withheld_records,omitempty"`
}

type basketID struct {
	prefix string
	id     int
}

// markBasketComponents runs before the form's row write. A pre-existing
// basket without provenance is conservatively complete; a new row owns
// only the form's component. Existing component flags never infer ownership
// from a nonzero winner or nonempty description.
const markBasketComponentSQL = `INSERT INTO basket_components (prefix, b_id, metadata, drawing)
		VALUES (?, ?,
			CASE WHEN EXISTS (SELECT 1 FROM baskets WHERE prefix = ? AND b_id = ?) THEN 1 ELSE ? END,
			CASE WHEN EXISTS (SELECT 1 FROM baskets WHERE prefix = ? AND b_id = ?) THEN 1 ELSE ? END)
		ON CONFLICT (prefix, b_id) DO UPDATE SET
			metadata = max(basket_components.metadata, ?), drawing = max(basket_components.drawing, ?)`

func basketComponentArgs(b Basket, metadata, drawing bool) []any {
	// The baskets endpoint accepts an initial nonzero winner, but
	// ignores an incoming winner when it updates an existing basket. Only
	// the insert branch owns that additional accepted drawing component.
	insertDrawing := drawing || (metadata && b.WinningTicket != 0)
	return []any{b.Prefix, b.BID, b.Prefix, b.BID, metadata, b.Prefix, b.BID, insertDrawing, metadata, drawing}
}

func markBasketComponents(tx *sql.Tx, bs []Basket, metadata, drawing bool) error {
	return execEach(tx, markBasketComponentSQL, len(bs), func(i int) []any {
		return basketComponentArgs(bs[i], metadata, drawing)
	})
}

// ExportRecovery reads the rows and their provenance in one database
// transaction, including when a concurrent backup import is writing.
func (s *Store) ExportRecovery() (RecoverySnapshot, error) {
	snapshot := RecoverySnapshot{Format: NativeBackupFormat}
	err := s.tx(func(tx *sql.Tx) error {
		view := s.view(tx)
		var err error
		if snapshot.BackupFile, err = view.Export(); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT b.prefix, b.b_id, coalesce(c.metadata, 1), coalesce(c.drawing, 1)
			FROM baskets b LEFT JOIN basket_components c ON c.prefix = b.prefix AND c.b_id = b.b_id
			ORDER BY b.prefix, b.b_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		snapshot.BasketComponents = []BasketComponents{}
		for rows.Next() {
			var component BasketComponents
			if err := rows.Scan(&component.Prefix, &component.BID, &component.Metadata, &component.Drawing); err != nil {
				return err
			}
			snapshot.BasketComponents = append(snapshot.BasketComponents, component)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		all, err := allRevisions(tx)
		if err != nil {
			return err
		}
		// Never export a receipt attached to a different stored value.
		for _, revision := range all {
			raw, exists, err := currentValue(tx, revision.Kind, revision.Prefix, revision.ID)
			if err != nil {
				return err
			}
			if exists && valueHash(raw) == revision.Hash {
				snapshot.Revisions = append(snapshot.Revisions, revision)
				if revision.Kind == "prefix" && string(raw) == "null" {
					snapshot.DeletedPrefixes = append(snapshot.DeletedPrefixes, revision.Prefix)
				}
			}
		}
		snapshot.Conflicts, err = view.Conflicts()
		if err != nil {
			return err
		}
		return exportRecoveryHoldbacks(tx, &snapshot)
	})
	return snapshot, err
}

// ValidateNativeBackup validates an external file before any restore writes.
// Prior Go exports are recognized by their ownership or causal metadata; an
// ambiguous three-list file cannot establish native provenance.
func ValidateNativeBackup(snapshot *RecoverySnapshot) error {
	if snapshot.Format != "" && snapshot.Format != NativeBackupFormat {
		return fmt.Errorf("unsupported backup format %q", snapshot.Format)
	}
	if snapshot.Format == "" && snapshot.BasketComponents == nil && len(snapshot.Revisions) == 0 && len(snapshot.Conflicts) == 0 && len(snapshot.DeletedPrefixes) == 0 && len(snapshot.WithheldRecords) == 0 {
		return errors.New("a Go-native backup with ownership or history metadata is required")
	}
	if len(snapshot.Baskets) > 0 && snapshot.BasketComponents == nil {
		return errors.New("native backup requires basket_components for every basket")
	}
	return ValidateRecoverySnapshot(snapshot)
}

// ValidateRecoverySnapshot validates internally constructed event snapshots.
// External import boundaries additionally require ValidateNativeBackup.
func ValidateRecoverySnapshot(snapshot *RecoverySnapshot) error {
	if err := ValidateBackup(&snapshot.BackupFile); err != nil {
		return err
	}
	_, err := snapshotCandidates(*snapshot)
	if err != nil {
		return err
	}
	return validateRecoveryHoldbacks(snapshot)
}

func recoveryComponents(snapshot RecoverySnapshot) (map[basketID]BasketComponents, error) {
	components := make(map[basketID]BasketComponents, len(snapshot.Baskets))
	for _, b := range snapshot.Baskets {
		components[basketID{b.Prefix, b.BID}] = BasketComponents{Prefix: b.Prefix, BID: b.BID, Metadata: true, Drawing: true}
	}
	if snapshot.BasketComponents == nil {
		return components, nil
	}
	seen := make(map[basketID]bool, len(components))
	for _, c := range snapshot.BasketComponents {
		id := basketID{c.Prefix, c.BID}
		if _, exists := components[id]; !exists || seen[id] || (!c.Metadata && !c.Drawing) {
			return nil, fmt.Errorf("invalid basket components for %s/%d", c.Prefix, c.BID)
		}
		components[id], seen[id] = c, true
	}
	if len(seen) != len(components) {
		return nil, errors.New("basket_components must name every recovered basket")
	}
	return components, nil
}
