// Package presence tracks each authenticated client independently, including
// clients sharing an access key. Records describe the last reported state.
package presence

import (
	"sync"
	"time"
)

// Identity separates a workstation from other clients using the same key.
type Identity struct {
	Key  string
	Name string
}

// Record is what is known about one client.
type Record struct {
	Seen       time.Time // the last keyed request
	Updated    time.Time // the last accepted write; zero when none
	Pending    int       // queued saves, from the last heartbeat
	HasPending bool      // whether a heartbeat ever reported Pending
	Failed     int       // refused saves, from the last heartbeat
	Recovering bool      // recovery work, from the last heartbeat
	Client     string    // the program, as its last request named it
}

// Registry is the thread-safe table of records.
type Registry struct {
	now func() time.Time

	mu       sync.Mutex
	byClient map[Identity]Record
}

// New returns an empty registry that reads the time from now; nil means
// the wall clock.
func New(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{now: now, byClient: map[Identity]Record{}}
}

// Seen records a request with a stable identity. Unnamed API reads cannot
// stand in for a workstation's presence or heartbeat.
func (r *Registry) Seen(key, identity, client string) {
	if identity == "" {
		return
	}
	r.mu.Lock()
	r.see(Identity{key, identity}, client)
	r.mu.Unlock()
}

// Heartbeat records a keyed request that reports pending queued saves.
func (r *Registry) Heartbeat(key, identity, client string, pending, failed int, recovering bool) {
	if identity == "" || pending < 0 || failed < 0 {
		return
	}
	r.mu.Lock()
	id := Identity{key, identity}
	rec := r.see(id, client)
	rec.Pending, rec.HasPending = pending, true
	rec.Failed, rec.Recovering = failed, recovering
	r.byClient[id] = rec
	r.mu.Unlock()
}

// Updated records an accepted write, which also counts as seeing the key.
func (r *Registry) Updated(key, identity string) {
	if identity == "" {
		return
	}
	r.mu.Lock()
	id := Identity{key, identity}
	rec := r.see(id, "")
	rec.Updated = rec.Seen
	r.byClient[id] = rec
	r.mu.Unlock()
}

// Forget drops the key's record, for when the key is deleted.
func (r *Registry) Forget(key string) {
	r.mu.Lock()
	for id := range r.byClient {
		if id.Key == key {
			delete(r.byClient, id)
		}
	}
	r.mu.Unlock()
}

// Snapshot returns a copy of every record by authenticated identity.
func (r *Registry) Snapshot() map[Identity]Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[Identity]Record, len(r.byClient))
	for k, rec := range r.byClient {
		out[k] = rec
	}
	return out
}

// see stamps the key as seen now, stores the record and returns it. The
// caller holds the lock.
func (r *Registry) see(id Identity, client string) Record {
	rec := r.byClient[id]
	rec.Seen = r.now()
	if client != "" {
		rec.Client = client
	}
	r.byClient[id] = rec
	return rec
}
