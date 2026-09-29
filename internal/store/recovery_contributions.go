package store

import (
	"database/sql"
	"errors"
)

// RecoveryContribution names exactly the eligibility state captured for an
// upload. A later refused save advances Epoch, so an older acknowledgement
// cannot erase its need to offer the newly available accepted predecessor.
type RecoveryContribution struct {
	Token string
	Epoch int64
	Data  RecoverySnapshot
}

// PrepareRecoveryContribution persists an authenticated request before export
// and retries unfinished contributions only to the same remote URL and key.
// A rejected generation is suppressed until the server requests a new token;
// older recovery servers reject repeats after their first acknowledgement.
func (s *Store) PrepareRecoveryContribution(target, requested string) (RecoveryContribution, error) {
	var result RecoveryContribution
	err := s.tx(func(tx *sql.Tx) error {
		var savedTarget, token string
		var epoch int64
		var needed, rejected bool
		err := tx.QueryRow(`SELECT target,token,epoch,needed,rejected FROM client_recovery_contribution WHERE id=1`).Scan(&savedTarget, &token, &epoch, &needed, &rejected)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if requested != "" {
			if target != savedTarget || requested != token {
				epoch++
				rejected = false
			}
			if rejected {
				return nil
			}
			savedTarget, token, needed = target, requested, true
			if _, err := tx.Exec(`INSERT INTO client_recovery_contribution(id,target,token,epoch,needed,rejected) VALUES(1,?,?,?,1,0)
				ON CONFLICT(id) DO UPDATE SET target=excluded.target,token=excluded.token,epoch=excluded.epoch,needed=1,rejected=0`, target, token, epoch); err != nil {
				return err
			}
		}
		if savedTarget != target || !needed || rejected {
			return nil
		}
		result.Token, result.Epoch = token, epoch
		result.Data, err = s.view(tx).ExportRecoveryForSync()
		return err
	})
	return result, err
}

// CompleteRecoveryContribution retains the receipt and clears only this
// snapshot's retry flag in the same transaction.
func (s *Store) CompleteRecoveryContribution(target string, sent RecoveryContribution, receipt *SaveReceipt) error {
	return s.tx(func(tx *sql.Tx) error {
		if receipt != nil {
			if err := s.view(tx).ApplyReceipt(*receipt); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`UPDATE client_recovery_contribution SET needed=0 WHERE id=1 AND target=? AND token=? AND epoch=?`, target, sent.Token, sent.Epoch)
		return err
	})
}

// RejectRecoveryContribution stops retrying a stale or unsupported generation.
// The full predecessor remains in the native backup and is offered when a
// later generation requests recovery; this does not block ordinary replay.
func (s *Store) RejectRecoveryContribution(target, token string) error {
	_, err := s.exec(`UPDATE client_recovery_contribution SET needed=0,rejected=1 WHERE id=1 AND target=? AND token=?`, target, token)
	return err
}

func markRecoveryReoffer(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE client_recovery_contribution SET needed=1,epoch=epoch+1 WHERE id=1 AND rejected=0`)
	return err
}
