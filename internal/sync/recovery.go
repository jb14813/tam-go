package sync

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
)

// recover responds only to an authenticated server's recovery request.
// The client offers its locally entered rows; the server adds missing
// rows, then the ordinary ordered queue follows afterwards.
func (s *Syncer) recover(rc *remote.Client) bool {
	s.mu.Lock()
	token := s.recoveryToken
	s.mu.Unlock()
	if token == "" {
		return true
	}
	client, err := s.st.ClientName(s.host)
	if err != nil {
		s.NoteFailure(fmt.Errorf("client identity: %w", err))
		return false
	}
	done := s.Numbering()
	bf, err := s.st.ExportRecovery()
	done()
	if err != nil {
		s.NoteFailure(fmt.Errorf("export recovery copy: %w", err))
		return false
	}
	defer s.Sending()()
	res, err := rc.WithTimeout(2*time.Minute).Do(http.MethodPost, "/api/recovery", map[string]string{"X-TAM-Client-Name": client}, struct {
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
		// The acknowledgement may have been lost after the server committed.
		// Ask it again; never turn that ambiguity into a discarded local copy.
		s.mu.Lock()
		s.recoveryToken = ""
		s.nextPing = time.Time{}
		s.mu.Unlock()
		s.wake()
		return false
	}
	if !res.OK() {
		s.NoteFailure(fmt.Errorf("server answered %d to recovery", res.Status))
		return false
	}
	s.mu.Lock()
	s.recoveryToken = ""
	s.mu.Unlock()
	log.Printf("recovery copy sent to %s: %d prefixes, %d tickets, %d baskets", s.name(), len(bf.Prefixes), len(bf.Tickets), len(bf.Baskets))
	return true
}
