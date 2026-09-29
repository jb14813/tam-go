package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrRecoveryToken means this key has no outstanding request with that token.
var ErrRecoveryToken = errors.New("recovery request is no longer current")

const eventEmptySQL = `NOT EXISTS (SELECT 1 FROM prefixes)
	AND NOT EXISTS (SELECT 1 FROM tickets) AND NOT EXISTS (SELECT 1 FROM baskets)`

// BeginRecovery asks every existing key for its local copy when the event
// is empty. Requests and per-client receipts survive repeated starts, and a
// partly recovered event keeps waiting even for clients not seen before.
// Call on server startup and after pairing a new key into an empty event.
func (s *Store) BeginRecovery() error {
	token, err := generateKey()
	if err != nil {
		return err
	}
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM recovery_requests WHERE auth_key NOT IN (SELECT auth_key FROM auth_keys)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM recovery_receipts WHERE auth_key NOT IN (SELECT auth_key FROM auth_keys)`); err != nil {
			return err
		}
		var empty bool
		if err := tx.QueryRow(`SELECT ` + eventEmptySQL).Scan(&empty); err != nil {
			return err
		}
		var current string
		var populated bool
		err := tx.QueryRow(`SELECT token, populated FROM recovery_state WHERE id = 1`).Scan(&current, &populated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !empty && current == "" {
			return nil
		}
		if empty && (current == "" || populated) {
			current = token
			if _, err := tx.Exec(`INSERT INTO recovery_state (id, token, populated) VALUES (1, ?, 0)
				ON CONFLICT (id) DO UPDATE SET token = excluded.token, populated = 0`, current); err != nil {
				return err
			}
			for _, table := range []string{"recovery_requests", "recovery_receipts", "recovery_deleted_prefixes"} {
				if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(`INSERT INTO recovery_requests (auth_key, token)
			SELECT auth_key, ? FROM auth_keys WHERE true
			ON CONFLICT (auth_key) DO NOTHING`, current)
		return err
	})
}

// RecoveryToken returns this client's request, or "" after its receipt.
// A key's generation stays available to late clients sharing that key.
func (s *Store) RecoveryToken(key, client string) (string, error) {
	var token string
	err := s.db.QueryRow(`SELECT r.token FROM recovery_requests r
		JOIN auth_keys k ON k.auth_key = r.auth_key
		WHERE r.auth_key = ? AND NOT EXISTS (SELECT 1 FROM recovery_receipts a
			WHERE a.auth_key = r.auth_key AND a.client = ? AND a.token = r.token)`, key, client).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return token, err
}

// Recover accepts the legacy full-basket snapshot format.
func (s *Store) Recover(key, client, token string, bf BackupFile) error {
	return s.RecoverSnapshot(key, client, token, RecoverySnapshot{BackupFile: bf})
}

// RecoverSnapshot merges owned components by their causal receipts, retaining
// incomparable values as conflicts, and acknowledges the client atomically.
// It never imports keys or replaces the event with one client's snapshot.
func (s *Store) RecoverSnapshot(key, client, token string, snapshot RecoverySnapshot) error {
	if err := ValidateRecoverySnapshot(&snapshot); err != nil {
		return err
	}
	return s.tx(func(tx *sql.Tx) error {
		var valid bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM recovery_requests r
			JOIN auth_keys k ON k.auth_key = r.auth_key
			WHERE r.auth_key = ? AND r.token = ? AND NOT EXISTS (SELECT 1 FROM recovery_receipts a
				WHERE a.auth_key = r.auth_key AND a.client = ? AND a.token = r.token))`, key, token, client).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrRecoveryToken
		}
		candidates, err := snapshotCandidates(snapshot)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.Revision.Kind == "prefix" {
				var deleted bool
				if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM recovery_deleted_prefixes WHERE prefix=?)`, candidate.Revision.Prefix).Scan(&deleted); err != nil {
					return err
				}
				if deleted && len(candidate.Revision.Vector) == 0 {
					continue
				}
			}
			if err := mergeRecovered(tx, candidate, func() error { return applyCandidate(tx, candidate) }); err != nil {
				return fmt.Errorf("recover %s: %w", candidate.Revision.Kind, err)
			}
		}
		for _, conflict := range snapshot.Conflicts {
			for _, candidate := range conflict.Candidates {
				if err := mergeRecovered(tx, candidate, func() error { return applyCandidate(tx, candidate) }); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(`INSERT INTO recovery_receipts (auth_key, client, token) VALUES (?, ?, ?)
			ON CONFLICT (auth_key, client) DO UPDATE SET token = excluded.token`, key, client, token)
		return err
	})
}

// hasRecovery is false in ordinary client databases, which share prefix
// write methods but do not have the server's recovery schema.
func hasRecovery(tx *sql.Tx) (bool, error) {
	var exists bool
	err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master
		WHERE type = 'table' AND name = 'recovery_requests')`).Scan(&exists)
	return exists, err
}

func recoveryPrefixDeleted(tx *sql.Tx, name string, deleted bool) error {
	has, err := hasRecovery(tx)
	if err != nil || !has {
		return err
	}
	_, err = tx.Exec(`INSERT INTO recovery_deleted_prefixes (prefix)
		SELECT ? WHERE EXISTS (SELECT 1 FROM recovery_requests)
		ON CONFLICT (prefix) DO NOTHING`, name)
	if err == nil && deleted {
		// Removing the last menu prefix through the normal API is an
		// intentional empty event, not a new loss. Keep this generation's
		// receipts and tombstones so a late copy cannot undo that deletion.
		_, err = tx.Exec(`UPDATE recovery_state SET populated = 0 WHERE id = 1 AND ` + eventEmptySQL)
	}
	return err
}

func recoveryPrefixesSaved(tx *sql.Tx, ps []Prefix) error {
	has, err := hasRecovery(tx)
	if err != nil || !has {
		return err
	}
	return execEach(tx, `DELETE FROM recovery_deleted_prefixes WHERE prefix = ?`, len(ps), func(i int) []any {
		return []any{ps[i].Prefix}
	})
}
