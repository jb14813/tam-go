// Package sync keeps a tam-client useful while its server comes and goes.
// It watches the connection with a heartbeat, replays the saves the server
// has not taken yet. Request handlers feed it what they see and ask whether
// a call to the server is worth trying.
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
)

// State is where the client stands with its server.
type State string

const (
	// Connected: the last call to the server worked.
	Connected State = "connected"
	// Reconnecting: the server stopped answering a moment ago.
	Reconnecting State = "reconnecting"
	// Offline: the server has not answered for a while.
	Offline State = "offline"
	// Unauthenticated: the server answers but refuses this client's key.
	Unauthenticated State = "unauthenticated"
	// Certificate: the server's certificate is not the one pinned when the
	// client was paired; pairing again with the server trusts the new one.
	Certificate State = "certificate"
)

// Status is what GET /api/status answers in remote mode.
type Status struct {
	Mode       string `json:"mode"`
	State      State  `json:"state"`
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
	Pending    int    `json:"pending"`
	Failed     int    `json:"failed"`
	// Recovering means the server has requested this client's saved data;
	// transport may be connected before that upload is acknowledged.
	Recovering bool   `json:"recovering"`
	Conflicts  int    `json:"conflicts"`
	LastOK     string `json:"last_ok"`
	// SettingsError says why settings.json could not be used, in either
	// mode; see config.File.Problem.
	SettingsError string `json:"settings_error,omitempty"`
}

// Timings are the delays the syncer works with; tests shorten them.
type Timings struct {
	Heartbeat    time.Duration   // how often the server is pinged
	PingTimeout  time.Duration   // how long a ping may take
	OfflineAfter time.Duration   // reconnecting becomes offline after this
	Backoff      []time.Duration // waits between failed replay attempts
}

// DefaultTimings are the production delays.
func DefaultTimings() Timings {
	return Timings{
		Heartbeat:    5 * time.Second,
		PingTimeout:  2 * time.Second,
		OfflineAfter: 30 * time.Second,
		Backoff:      []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second},
	}
}

// Syncer owns the connection state, recovery uploads and outbox replay.
type Syncer struct {
	st     *store.Store
	cfg    *config.File
	client func(config.Settings) *remote.Client
	t      Timings
	host   string // machine name used by this data folder's save-order identity

	mu            sync.Mutex
	state         State
	label         string    // the server as named in log lines
	since         time.Time // when the current run of failures began
	lastOK        time.Time
	retries       int
	retryAt       time.Time
	nextPing      time.Time
	kick          chan struct{}
	recoveryToken string          // authenticated request to help refill an empty server
	conflicts     int             // server entries requiring an operator's review
	reviewToken   string          // last server-side resolution generation seen
	reviewApplied string          // generation durably retained in matching own rows
	work          sync.Mutex      // a sync round and a settings change cannot overlap
	running       context.Context // Run's context; the replay stops between saves when it ends

	// numbering is held while a save is numbered and then sent or queued,
	// so saves are numbered, queued and sent directly in one order (see
	// Numbering). sending is held for each numbered request on its way to
	// the server, by the replay and by a page sending a save directly, so
	// the server sees a client's saves one at a time, in the order of their
	// numbers, which its check of the numbers relies on (see store.InOrder).
	// Lock numbering before sending, never the other way.
	numbering sync.Mutex
	sending   sync.Mutex
}

// New returns a syncer over the client's store and settings. client builds
// (or reuses) the remote client for the given settings and returns nil in
// standalone mode.
func New(st *store.Store, cfg *config.File, client func(config.Settings) *remote.Client, t Timings) *Syncer {
	host, _ := os.Hostname()
	if host == "" {
		host = "client"
	}
	return &Syncer{st: st, cfg: cfg, client: client, t: t, host: host, kick: make(chan struct{}, 1)}
}

// Reconfigure changes the connection between sync rounds and saves. The
// queue belongs to this event, so changing its server does not discard it.
func (s *Syncer) Reconfigure(change func() error) error {
	s.work.Lock()
	defer s.work.Unlock()
	defer s.Numbering()()
	defer s.Sending()()
	if err := change(); err != nil {
		return err
	}
	s.Reset()
	return nil
}

// Online reports whether a call to the server is worth trying: the server
// answered last time, or nothing is known yet.
func (s *Syncer) Online() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "" || s.state == Connected
}

// InStep reports whether the pages should work through the server now: it
// answers, and every save queued on this client has reached it. Until then
// the pages keep working from the client's own copy and new saves queue
// behind the old ones, so the server takes a client's saves in the order
// they were made and a sheet opened meanwhile shows what the client saved,
// not the server's older copy.
func (s *Syncer) InStep() bool {
	s.mu.Lock()
	recovering := s.recoveryToken != ""
	s.mu.Unlock()
	if recovering {
		return false
	}
	if !s.Online() {
		return false
	}
	waiting, err := s.st.OutboxWaiting()
	if err != nil {
		log.Printf("outbox: %v", err)
		return false
	}
	return !waiting
}

// Numbering holds this client's saves in line while the caller numbers one
// and then queues it, or sends it (taking Sending as well). While it is
// held no other save is numbered or queued, so a caller that finds the
// queue empty (InStep) can count on it staying empty. done lets the next go.
func (s *Syncer) Numbering() (done func()) {
	s.numbering.Lock()
	return s.numbering.Unlock
}

// Sending takes the way to the server for one numbered request: the replay
// holds it for each queued save it sends, and a page for a save it sends
// directly. A page whose save goes to the queue does not wait for it, so a
// replay held up by a dead link does not hold up the pages. done lets the
// next go.
func (s *Syncer) Sending() (done func()) {
	s.sending.Lock()
	return s.sending.Unlock
}

// State returns the current state ("" before the first contact).
func (s *Syncer) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// labelOf names a server the way log lines refer to it.
func labelOf(settings config.Settings) string {
	if settings.RemoteName != "" {
		return settings.RemoteName
	}
	return net.JoinHostPort(settings.RemoteServer, settings.RemotePort)
}

// NoteSuccess records that the server answered.
func (s *Syncer) NoteSuccess() {
	label := labelOf(s.cfg.Get())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = label
	s.lastOK = time.Now()
	s.retries = 0
	s.retryAt = time.Time{}
	if s.state == Connected {
		return
	}
	if s.state != "" {
		log.Printf("server %s: connected again", s.label)
	} else {
		log.Printf("server %s: connected", s.label)
	}
	s.state = Connected
	s.wake()
}

// NoteFailure records that the server could not be reached or was unwell,
// or that its certificate is no longer the one pinned at pairing.
func (s *Syncer) NoteFailure(err error) {
	label := labelOf(s.cfg.Get())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = label
	if errors.Is(err, remote.ErrCertificateChanged) {
		if s.state != Certificate {
			log.Printf("server %s: its certificate changed (%v); pair with it again in Settings", s.label, err)
			s.state = Certificate
		}
		return
	}
	now := time.Now()
	switch s.state {
	case Certificate:
		// The certificate is refused before anything else; this failure is
		// the same one seen through a page, so the state stays.
		return
	case Reconnecting:
		if now.Sub(s.since) >= s.t.OfflineAfter {
			s.state = Offline
			log.Printf("server %s: offline", s.label)
		}
	case Offline:
	default:
		s.state = Reconnecting
		s.since = now
		log.Printf("server %s: unreachable (%v), reconnecting", s.label, err)
	}
}

// NoteUnauthorized records that the server refused this client's key.
func (s *Syncer) NoteUnauthorized() {
	label := labelOf(s.cfg.Get())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = label
	if s.state != Unauthenticated {
		log.Printf("server %s: rejected this client's key", s.label)
		s.state = Unauthenticated
	}
}

// Reset forgets the connection state, for when the settings point at a
// different server (or none). The next tick starts from scratch.
func (s *Syncer) Reset() {
	s.mu.Lock()
	s.state = ""
	s.since = time.Time{}
	s.lastOK = time.Time{}
	s.retries = 0
	s.retryAt = time.Time{}
	s.nextPing = time.Time{}
	s.recoveryToken = ""
	s.conflicts = 0
	s.reviewToken, s.reviewApplied = "", ""
	s.mu.Unlock()
	s.wake()
}

// Enqueue stores a request for the server to take later and wakes the
// worker.
func (s *Syncer) Enqueue(method, path string, body []byte) error {
	if _, err := s.st.EnqueueOutbox(method, path, body); err != nil {
		return err
	}
	s.wake()
	return nil
}

// SaveQueued writes a save to the client's copy and queues its request
// for the server in one transaction (see store.SaveQueued), under the name
// and number it was first sent with, then wakes the worker.
func (s *Syncer) SaveQueued(method, path string, body []byte, order store.Order, write func(*store.Store) error) error {
	if _, err := s.st.SaveQueued(method, path, body, order, write); err != nil {
		return err
	}
	s.wake()
	return nil
}

// Kick asks the worker to look at the server now.
func (s *Syncer) Kick() {
	s.mu.Lock()
	s.nextPing = time.Time{}
	s.mu.Unlock()
	s.wake()
}

func (s *Syncer) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Status answers the status route. In standalone mode only Mode is set.
func (s *Syncer) Status() Status {
	settings := s.cfg.Get()
	problem := s.cfg.Problem()
	if settings.RemoteURL() == "" {
		return Status{Mode: "standalone", SettingsError: problem}
	}
	pending, failed, err := s.st.OutboxCounts()
	if err != nil {
		log.Printf("outbox: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	if st == "" {
		st = Reconnecting
	}
	name := settings.RemoteName
	if name == "" {
		name = settings.RemoteServer
	}
	lastOK := ""
	if !s.lastOK.IsZero() {
		lastOK = s.lastOK.UTC().Format(time.RFC3339)
	}
	return Status{
		Mode:       "remote",
		State:      st,
		Server:     net.JoinHostPort(settings.RemoteServer, settings.RemotePort),
		ServerName: name,
		Pending:    pending,
		Failed:     failed,
		Recovering: s.recoveryToken != "",
		Conflicts:  s.conflicts,
		LastOK:     lastOK,

		SettingsError: problem,
	}
}

// Run pings the server, offers local recovery data and replays the outbox
// until ctx ends. It is the only goroutine that talks to the server on its own.
func (s *Syncer) Run(ctx context.Context) {
	s.mu.Lock()
	s.running = ctx
	s.mu.Unlock()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.Tick()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.kick:
		}
	}
}

// Tick does one round of work: a heartbeat when one is due, then, while
// connected, a requested recovery upload and replay of the outbox. It is
// what Run repeats and what tests call directly.
func (s *Syncer) Tick() {
	s.work.Lock()
	defer s.work.Unlock()
	done := s.Numbering()
	err := s.st.MaterializeIntents()
	done()
	if err != nil {
		log.Printf("retain pending saves: %v", err)
		return
	}
	settings := s.cfg.Get()
	rc := s.client(settings)
	if rc == nil {
		s.mu.Lock()
		s.state = ""
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.label = labelOf(settings)
	pingDue := !time.Now().Before(s.nextPing)
	s.mu.Unlock()

	if pingDue {
		s.ping(rc, settings.RemoteKey != "")
		s.mu.Lock()
		s.nextPing = time.Now().Add(s.t.Heartbeat)
		s.mu.Unlock()
	}

	s.mu.Lock()
	ready := s.state == Connected && !time.Now().Before(s.retryAt)
	s.mu.Unlock()
	if !ready {
		return
	}
	if !s.recover(rc) {
		return
	}
	if !s.refreshReviewed(rc) {
		return
	}
	handled, ok := s.drain(rc)
	if !ok {
		return
	}
	if handled > 0 {
		// A definitive refusal may have exposed the last accepted local
		// value, omitted while this component still had an ordered replay.
		if !s.recover(rc) {
			return
		}
		// The server's admin page shows what each client has queued, from
		// the heartbeat: say at once that the queue is empty, not at the
		// next heartbeat.
		s.ping(rc, settings.RemoteKey != "")
		s.mu.Lock()
		s.nextPing = time.Now().Add(s.t.Heartbeat)
		s.mu.Unlock()
	}
}

// ping is the heartbeat. It tells the server how many saves are queued
// here (X-TAM-Pending), which its admin page shows; when the count cannot
// be read the header is left out rather than guessed.
func (s *Syncer) ping(rc *remote.Client, haveKey bool) {
	client, err := s.st.ClientName(s.host)
	if err != nil {
		s.NoteFailure(fmt.Errorf("client identity: %w", err))
		return
	}
	headers := map[string]string{"X-TAM-Client-Name": client}
	if pending, _, err := s.st.OutboxCounts(); err != nil {
		log.Printf("outbox: %v", err)
	} else {
		headers["X-TAM-Pending"] = strconv.Itoa(pending)
	}
	res, err := rc.WithTimeout(s.t.PingTimeout).Do(http.MethodGet, "/api", headers, nil)
	switch {
	case err != nil:
		s.NoteFailure(err)
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
		s.NoteUnauthorized()
	case !res.OK():
		s.NoteFailure(fmt.Errorf("server answered %d", res.Status))
	default:
		var doc struct {
			Authenticated bool   `json:"authenticated"`
			RecoveryToken string `json:"recovery_token"`
			Conflicts     int    `json:"conflicts"`
			ReviewToken   string `json:"review_token"`
		}
		if json.Unmarshal(res.Body, &doc) == nil && haveKey && !doc.Authenticated {
			s.NoteUnauthorized()
			return
		}
		s.mu.Lock()
		if haveKey && doc.Authenticated {
			s.recoveryToken = doc.RecoveryToken
			s.conflicts = doc.Conflicts
			s.reviewToken = doc.ReviewToken
		}
		s.mu.Unlock()
		s.NoteSuccess()
	}
}

// drain sends queued requests in order and returns how many it took off
// the queue (sent, or refused by the server and kept in the failed list).
// ok is false when the server stopped taking them.
func (s *Syncer) drain(rc *remote.Client) (handled int, ok bool) {
	for ; ; handled++ {
		if s.stopping() {
			return handled, false
		}
		took, ok := s.replayOne(rc)
		if !took || !ok {
			return handled, ok
		}
	}
}

// stopping reports that Run's context has ended: the program is shutting
// down, and the replay stops between saves so the database can close.
func (s *Syncer) stopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running != nil && s.running.Err() != nil
}

// replayOne sends the oldest queued request, holding the way to the server
// meanwhile (see Sending). took reports that it left the queue (sent,
// or set aside in the failed list); ok is false when the server stopped
// taking requests or the queue could not be read.
func (s *Syncer) replayOne(rc *remote.Client) (took, ok bool) {
	// Finish any interrupted online save locally before another page can
	// write a newer value. Release Numbering before the network request so
	// ordinary offline entry can continue during a slow replay.
	done := s.Numbering()
	if err := s.st.MaterializeIntents(); err != nil {
		done()
		log.Printf("retain pending saves: %v", err)
		return false, false
	}
	defer s.Sending()()
	o, err := s.st.NextOutbox()
	done()
	if err != nil {
		log.Printf("outbox: %v", err)
		return false, false
	}
	if o == nil {
		return false, true
	}
	var body any
	if len(o.Body) > 0 {
		body = json.RawMessage(o.Body)
	}
	res, err := rc.Do(o.Method, o.Path, OrderHeaders(o.Order), body)
	last, behind := LastSave(res)
	switch {
	case err != nil:
		s.noteAttempt(o.ID, err.Error())
		s.NoteFailure(err)
		s.backOff()
		return false, false
	case res.OK():
		if err := s.st.AcknowledgeOutbox(o.ID, res.Receipt); err != nil {
			log.Printf("outbox: %v", err)
			return false, false
		}
		s.NoteSuccess()
		log.Printf("server %s: took a queued %s %s", s.name(), o.Method, o.Path)
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
		s.noteAttempt(o.ID, detail(res))
		s.NoteUnauthorized()
		return false, false
	case res.Retryable():
		s.noteAttempt(o.ID, detail(res))
		s.NoteFailure(fmt.Errorf("server answered %d", res.Status))
		s.backOff()
		return false, false
	case behind:
		// The server has applied a newer save from this client than this
		// one: the client's data folder was put back from a copy taken
		// before it. This save may have reached the server before the copy
		// was put back, or not; the volunteer decides in Settings (Retry
		// numbers it anew and sends it, Discard drops it). The client's own
		// count moves past the server's, so its next saves are taken.
		if err := s.st.SkipSavesTo(last); err != nil {
			log.Printf("outbox: %v", err)
			return false, false
		}
		reason := fmt.Sprintf("the server has newer saves from this client (up to its save %d), so this one may have reached it already: this client's data folder was put back from a copy; retry to send it again", last)
		if err := s.st.FailOutbox(o.ID, reason); err != nil {
			log.Printf("outbox: %v", err)
			return false, false
		}
		s.NoteSuccess()
		log.Printf("server %s: a queued %s %s is older than this client's save %d on the server; kept in the failed list", s.name(), o.Method, o.Path, last)
	case res.Status == http.StatusNotFound && o.Method == http.MethodDelete:
		// A prefix already gone from the server, which is what the delete
		// wanted; a delete made online takes the same answer as done.
		if err := s.st.AcknowledgeOutbox(o.ID, res.Receipt); err != nil {
			log.Printf("outbox: %v", err)
			return false, false
		}
		s.NoteSuccess()
		log.Printf("server %s: a queued %s %s found it gone already", s.name(), o.Method, o.Path)
	default:
		reason := detail(res)
		if err := s.st.FailOutbox(o.ID, reason); err != nil {
			log.Printf("outbox: %v", err)
			return false, false
		}
		log.Printf("server %s: refused a queued %s %s (%s); kept in the failed list", s.name(), o.Method, o.Path, reason)
	}
	return true, true
}

func (s *Syncer) noteAttempt(id int64, errText string) {
	if err := s.st.NoteOutboxAttempt(id, errText); err != nil {
		log.Printf("outbox: %v", err)
	}
}

func (s *Syncer) backOff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.t.Backoff) == 0 {
		return
	}
	i := s.retries
	if i >= len(s.t.Backoff) {
		i = len(s.t.Backoff) - 1
	}
	s.retryAt = time.Now().Add(s.t.Backoff[i])
	s.retries++
}

func (s *Syncer) name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.label
}

// LastSave reads the number of the last save the server applied from this
// client out of its answer to a save it did not apply as older than that
// (409, see store.InOrder). ok is false for any other answer.
func LastSave(res *remote.Response) (last int64, ok bool) {
	if res == nil || res.Status != http.StatusConflict {
		return 0, false
	}
	var doc struct {
		LastSave int64 `json:"last_save"`
	}
	if res.JSON(&doc) != nil || doc.LastSave <= 0 {
		return 0, false
	}
	return doc.LastSave, true
}

// OrderHeaders are the headers that name and number a save for the server
// (see store.InOrder); an unnumbered save has none.
func OrderHeaders(o store.Order) map[string]string {
	if o.Save <= 0 {
		return nil
	}
	return map[string]string{"X-TAM-Client-Name": o.Client, "X-TAM-Save": strconv.FormatInt(o.Save, 10), "X-TAM-Receipts": "1"}
}

// detail returns the server's {"detail": ...} message, or the status text.
func detail(res *remote.Response) string {
	var doc struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(res.Body, &doc) == nil && doc.Detail != "" {
		return fmt.Sprintf("%d: %s", res.Status, doc.Detail)
	}
	return fmt.Sprintf("%d: %s", res.Status, http.StatusText(res.Status))
}
