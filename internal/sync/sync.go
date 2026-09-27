// Package sync keeps a tam-client useful while its server comes and goes.
// It watches the connection with a heartbeat, replays the saves the server
// has not taken yet, and refreshes the local mirror when the server is
// back. Request handlers feed it what they see and ask it whether a call
// to the server is worth trying.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
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
	// Unauthenticated: the server answers but refuses this laptop's key.
	Unauthenticated State = "unauthenticated"
)

// Status is what GET /api/status answers in remote mode.
type Status struct {
	Mode       string `json:"mode"`
	State      State  `json:"state"`
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
	Pending    int    `json:"pending"`
	Failed     int    `json:"failed"`
	LastOK     string `json:"last_ok"`
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

// Syncer owns the connection state, the outbox replay and the mirror pull.
type Syncer struct {
	st     *store.Store
	cfg    *config.File
	client func(config.Settings) *remote.Client
	t      Timings

	mu         sync.Mutex
	state      State
	label      string    // the server as named in log lines
	since      time.Time // when the current run of failures began
	lastOK     time.Time
	pullNeeded bool
	retries    int
	retryAt    time.Time
	nextPing   time.Time
	kick       chan struct{}
	touched    map[string]bool // rows saved since the running pull began; nil when none runs
	pulling    func()          // tests: runs as a pull starts

	// saving is held for reading by every page save (BeginSave) and for
	// writing by the mirror pull while it starts noting saved rows and while
	// it imports, so the pull knows which rows a page saved meanwhile.
	saving sync.RWMutex
}

// New returns a syncer over the client's store and settings. client builds
// (or reuses) the remote client for the given settings and returns nil in
// standalone mode.
func New(st *store.Store, cfg *config.File, client func(config.Settings) *remote.Client, t Timings) *Syncer {
	return &Syncer{st: st, cfg: cfg, client: client, t: t, kick: make(chan struct{}, 1)}
}

// Online reports whether a call to the server is worth trying: the server
// answered last time, or nothing is known yet.
func (s *Syncer) Online() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "" || s.state == Connected
}

// InStep reports whether the pages should work through the server now: it
// answers, and every save queued on this laptop has reached it. Until then
// the pages keep working from the laptop's own copy and new saves queue
// behind the old ones, so the server takes a laptop's saves in the order
// they were made and a sheet opened meanwhile shows what the laptop saved,
// not the server's older copy.
func (s *Syncer) InStep() bool {
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

// Row names a row for BeginSave: its table, prefix and id (0 for a prefix).
func Row(table, prefix string, id int) string {
	return table + "\x00" + prefix + "\x00" + strconv.Itoa(id)
}

// BeginSave tells the syncer that a page is saving the named rows and
// returns the function that ends the save. A mirror pull under way leaves
// those rows as the page saved them: what it downloaded may be older.
func (s *Syncer) BeginSave(rows []string) (end func()) {
	s.saving.RLock()
	s.mu.Lock()
	if s.touched != nil {
		for _, r := range rows {
			s.touched[r] = true
		}
	}
	s.mu.Unlock()
	return s.saving.RUnlock
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
	s.pullNeeded = true
	s.wake()
}

// NoteFailure records that the server could not be reached or was unwell.
func (s *Syncer) NoteFailure(err error) {
	label := labelOf(s.cfg.Get())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = label
	now := time.Now()
	switch s.state {
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

// NoteUnauthorized records that the server refused this laptop's key.
func (s *Syncer) NoteUnauthorized() {
	label := labelOf(s.cfg.Get())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = label
	if s.state != Unauthenticated {
		log.Printf("server %s: rejected this laptop's key", s.label)
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
	s.pullNeeded = false
	s.retries = 0
	s.retryAt = time.Time{}
	s.nextPing = time.Time{}
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

// SaveQueued writes a save to the laptop's copy and queues its request
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
	if settings.RemoteURL() == "" {
		return Status{Mode: "standalone"}
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
		LastOK:     lastOK,
	}
}

// Run pings the server, replays the outbox and refreshes the mirror until
// ctx ends. It is the only goroutine that talks to the server on its own.
func (s *Syncer) Run(ctx context.Context) {
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
// connected, a replay of the outbox and the mirror pull that a fresh
// connection asks for. It is what Run repeats and what tests call directly.
func (s *Syncer) Tick() {
	settings := s.cfg.Get()
	rc := s.client(settings)
	if rc == nil {
		s.mu.Lock()
		s.state = ""
		s.pullNeeded = false
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
	if _, ok := s.drain(rc); !ok {
		return
	}
	s.mu.Lock()
	pull := s.pullNeeded
	s.mu.Unlock()
	if pull {
		s.pull(rc)
	}
}

func (s *Syncer) ping(rc *remote.Client, haveKey bool) {
	res, err := rc.WithTimeout(s.t.PingTimeout).Get("/api")
	switch {
	case err != nil:
		s.NoteFailure(err)
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
		s.NoteUnauthorized()
	case !res.OK():
		s.NoteFailure(fmt.Errorf("server answered %d", res.Status))
	default:
		var doc struct {
			Authenticated bool `json:"authenticated"`
		}
		if json.Unmarshal(res.Body, &doc) == nil && haveKey && !doc.Authenticated {
			s.NoteUnauthorized()
			return
		}
		s.NoteSuccess()
	}
}

// drain sends queued requests in order and returns how many it took off
// the queue (sent, or refused by the server and kept in the failed list).
// ok is false when the server stopped taking them, so the caller does not
// pull a mirror it cannot trust.
func (s *Syncer) drain(rc *remote.Client) (handled int, ok bool) {
	for ; ; handled++ {
		o, err := s.st.NextOutbox()
		if err != nil {
			log.Printf("outbox: %v", err)
			return handled, false
		}
		if o == nil {
			return handled, true
		}
		var body any
		if len(o.Body) > 0 {
			body = json.RawMessage(o.Body)
		}
		res, err := rc.Do(o.Method, o.Path, OrderHeaders(o.Order), body)
		switch {
		case err != nil:
			s.noteAttempt(o.ID, err.Error())
			s.NoteFailure(err)
			s.backOff()
			return handled, false
		case res.OK():
			if err := s.st.DeleteOutbox(o.ID); err != nil {
				log.Printf("outbox: %v", err)
				return handled, false
			}
			s.NoteSuccess()
			log.Printf("server %s: took a queued %s %s", s.name(), o.Method, o.Path)
		case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
			s.noteAttempt(o.ID, detail(res))
			s.NoteUnauthorized()
			return handled, false
		case res.Retryable():
			s.noteAttempt(o.ID, detail(res))
			s.NoteFailure(fmt.Errorf("server answered %d", res.Status))
			s.backOff()
			return handled, false
		default:
			reason := detail(res)
			if err := s.st.FailOutbox(o.ID, reason); err != nil {
				log.Printf("outbox: %v", err)
				return handled, false
			}
			log.Printf("server %s: refused a queued %s %s (%s); kept in the failed list", s.name(), o.Method, o.Path, reason)
		}
	}
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

// pull copies the server's data into the mirror, so the pages have it when
// the server goes away again. Rows a page saves while the download is on
// its way keep what the page saved: the download may be older.
func (s *Syncer) pull(rc *remote.Client) {
	if s.pulling != nil {
		s.pulling()
	}
	// Saves already under way finish first, so the download includes them.
	// A save queued after the replay found nothing left to send is not on
	// the server yet: the next tick sends it, then pulls. From here on the
	// rows of every save are noted until the import.
	s.saving.Lock()
	waiting, err := s.st.OutboxWaiting()
	if err != nil || waiting {
		s.saving.Unlock()
		if err != nil {
			log.Printf("outbox: %v", err)
		}
		return
	}
	s.mu.Lock()
	s.touched = map[string]bool{}
	s.mu.Unlock()
	s.saving.Unlock()
	defer func() {
		s.mu.Lock()
		s.touched = nil
		s.mu.Unlock()
	}()

	res, err := rc.Get("/api/backuprestore")
	if err != nil {
		s.NoteFailure(err)
		return
	}
	if !res.OK() {
		if res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden {
			s.NoteUnauthorized()
		} else {
			s.NoteFailure(fmt.Errorf("server answered %d to the backup download", res.Status))
		}
		return
	}
	var bf store.BackupFile
	if err := res.JSON(&bf); err != nil {
		log.Printf("server %s: sent an unreadable backup: %v", s.name(), err)
		return
	}
	if err := store.ValidateBackup(&bf); err != nil {
		log.Printf("server %s: sent an invalid backup: %v", s.name(), err)
		return
	}
	// Import only adds and updates: a laptop's own standalone rows are never
	// deleted by a pull, so pairing (and unpairing) never loses local data.
	s.saving.Lock()
	s.mu.Lock()
	saved := s.touched
	s.mu.Unlock()
	err = s.st.Import(without(bf, saved))
	s.saving.Unlock()
	if err != nil {
		log.Printf("mirror: %v", err)
		return
	}
	s.mu.Lock()
	s.pullNeeded = false
	s.mu.Unlock()
	log.Printf("mirror refreshed from %s: %d prefixes, %d tickets, %d baskets", s.name(), len(bf.Prefixes), len(bf.Tickets), len(bf.Baskets))
}

// OrderHeaders are the headers that name and number a save for the server
// (see store.InOrder); an unnumbered save has none.
func OrderHeaders(o store.Order) map[string]string {
	if o.Save <= 0 {
		return nil
	}
	return map[string]string{"X-TAM-Laptop": o.Laptop, "X-TAM-Save": strconv.FormatInt(o.Save, 10)}
}

// without returns the backup minus the named rows.
func without(bf store.BackupFile, rows map[string]bool) store.BackupFile {
	if len(rows) == 0 {
		return bf
	}
	out := store.NewBackupFile()
	for _, p := range bf.Prefixes {
		if !rows[Row("prefixes", p.Prefix, 0)] {
			out.Prefixes = append(out.Prefixes, p)
		}
	}
	for _, b := range bf.Baskets {
		if !rows[Row("baskets", b.Prefix, b.BID)] {
			out.Baskets = append(out.Baskets, b)
		}
	}
	for _, t := range bf.Tickets {
		if !rows[Row("tickets", t.Prefix, t.TID)] {
			out.Tickets = append(out.Tickets, t)
		}
	}
	return out
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
