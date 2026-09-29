package sync

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"time"

	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
)

// recover responds only to an authenticated server's recovery request.
// The client offers its locally entered rows and retained history; the server
// reconciles those copies before the ordinary ordered queue follows.
func (s *Syncer) recover(rc *remote.Client) bool {
	s.mu.Lock()
	token := s.recoveryToken
	s.mu.Unlock()
	client, err := s.st.ClientName(s.host)
	if err != nil {
		s.NoteFailure(fmt.Errorf("client identity: %w", err))
		return false
	}
	settings := s.cfg.Get()
	// Do not send a previous event's unfinished contribution to a newly
	// configured server. Store only a digest, never another copy of its key.
	target := fmt.Sprintf("%x", sha256.Sum256([]byte(settings.RemoteURL()+"\x00"+settings.RemoteKey)))
	done := s.Numbering()
	contribution, err := s.st.PrepareRecoveryContribution(target, token)
	done()
	if err != nil {
		s.NoteFailure(fmt.Errorf("export recovery copy: %w", err))
		return false
	}
	if contribution.Token == "" {
		s.mu.Lock()
		s.recoveryToken = ""
		s.mu.Unlock()
		return true
	}
	token = contribution.Token
	bf := contribution.Data
	s.mu.Lock()
	s.recoveryToken = token
	s.mu.Unlock()
	defer s.Sending()()
	res, err := rc.WithTimeout(2*time.Minute).Do(http.MethodPost, "/api/recovery", map[string]string{"X-TAM-Client-Name": client, "X-TAM-Receipts": "1"}, struct {
		Token string                 `json:"token"`
		Data  store.RecoverySnapshot `json:"data"`
	}{token, bf})
	if err != nil {
		s.NoteFailure(err)
		return false
	}
	if res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden {
		s.NoteUnauthorized()
		return false
	}
	if res.Status == http.StatusConflict {
		// A stale generation may reject a repeat upload.
		// Suppress that exact token durably and keep ordinary replay moving.
		if err := s.st.RejectRecoveryContribution(target, token); err != nil {
			s.NoteFailure(fmt.Errorf("retain rejected recovery generation: %w", err))
			return false
		}
		s.mu.Lock()
		s.recoveryToken = ""
		s.nextPing = time.Time{}
		s.mu.Unlock()
		s.wake()
		return true
	}
	if !res.OK() {
		s.NoteFailure(fmt.Errorf("server answered %d to recovery", res.Status))
		return false
	}
	if err := s.st.CompleteRecoveryContribution(target, contribution, res.Receipt); err != nil {
		s.NoteFailure(fmt.Errorf("retain recovery receipt: %w", err))
		return false
	}
	s.mu.Lock()
	s.recoveryToken = ""
	s.mu.Unlock()
	log.Printf("recovery copy sent to %s: %d prefixes, %d tickets, %d baskets", s.name(), len(bf.Prefixes), len(bf.Tickets), len(bf.Baskets))
	return true
}

// refreshReviewed retains a reviewed result's history only where this
// workstation already owns that exact value. It never downloads someone
// else's ticket or basket, and runs only after a new review generation.
func (s *Syncer) refreshReviewed(rc *remote.Client) bool {
	s.mu.Lock()
	token, applied := s.reviewToken, s.reviewApplied
	s.mu.Unlock()
	if token == "" || token == applied {
		return true
	}
	done := s.Numbering()
	snapshot, err := s.st.ExportRecovery()
	done()
	if err != nil {
		s.NoteFailure(fmt.Errorf("export receipt request: %w", err))
		return false
	}
	client, err := s.st.ClientName(s.host)
	if err != nil {
		s.NoteFailure(err)
		return false
	}
	res, err := rc.WithTimeout(2*time.Minute).Do(http.MethodPost, "/api/recovery/receipts", map[string]string{"X-TAM-Receipts": "1", "X-TAM-Client-Name": client}, snapshot)
	if err != nil {
		s.NoteFailure(err)
		return false
	}
	if res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden {
		s.NoteUnauthorized()
		return false
	}
	if !res.OK() || res.Receipt == nil {
		s.NoteFailure(fmt.Errorf("server answered %d without reviewed receipts", res.Status))
		return false
	}
	if err := s.st.ApplyReceipt(*res.Receipt); err != nil {
		s.NoteFailure(fmt.Errorf("retain reviewed receipt: %w", err))
		return false
	}
	s.mu.Lock()
	s.reviewApplied = token
	s.mu.Unlock()
	return true
}
