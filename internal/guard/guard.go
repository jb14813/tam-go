// Package guard limits how fast one address can guess the server password.
// tam-server's admin login and its API's key routes share one Limiter, so
// guesses cannot be split between the two to get more of them.
package guard

import (
	"log"
	"sync"
	"time"
)

const (
	// MaxFailures is how many wrong passwords an address may send before it
	// has to wait.
	MaxFailures = 5
	// Wait is how long it then waits. Wrong passwords older than that are
	// forgotten.
	Wait = 30 * time.Second
)

// Limiter counts wrong passwords per address. It lives in memory; a restart
// forgets the counts, as it forgets the admin sessions. Use New.
type Limiter struct {
	// Now is the limiter's clock: time.Now, unless a test moves it.
	Now func() time.Time

	mu     sync.Mutex
	ended  *sync.Cond // broadcast whenever a check ends
	byAddr map[string]*record
	swept  time.Time // when byAddr was last cleared of records that hold nothing back
}

// record is what the limiter knows of one address.
type record struct {
	failures int       // wrong passwords in a row
	checking int       // passwords of the address being checked right now
	last     time.Time // when the last wrong password was found out
	until    time.Time // after MaxFailures wrong passwords, the end of the wait
	told     bool      // a password refused during this wait was logged
}

// New returns a Limiter that knows no address yet.
func New() *Limiter {
	l := &Limiter{Now: time.Now, byAddr: map[string]*record{}}
	l.ended = sync.NewCond(&l.mu)
	return l
}

// Check runs check, which compares a password sent from addr, and reports
// whether the password was right. While addr is waiting after MaxFailures
// wrong passwords, check does not run, and wait is how long addr still has
// to wait.
//
// A password is counted before it is checked: no more passwords of an
// address are checked at once than it has tries left, and one arriving
// beyond that waits until a check ends. So guesses sent all at once cannot
// slip past the limit, while right passwords sent together, as by clients
// behind one address pairing at the same time, just take turns.
//
// Every wrong password is logged with its address, never the password, and
// so is the wait that follows the last try. Of the passwords refused during
// a wait only the first is logged: an address that keeps sending thousands a
// second would otherwise fill the disk. via, where the password came in
// (api, admin), starts each line.
func (l *Limiter) Check(addr, via string, check func() bool) (right bool, wait time.Duration) {
	l.mu.Lock()
	var r *record
	for {
		now := l.Now()
		r = l.record(addr, now)
		if r.failures >= MaxFailures {
			if left := r.until.Sub(now); left > 0 {
				first := !r.told
				r.told = true
				l.mu.Unlock()
				if first {
					log.Printf("%s: refusing passwords from %s for %d s more (logged once per wait)", via, addr, Seconds(left))
				}
				return false, left
			}
			r.failures = 0 // the wait is over: the count starts again
		} else if r.failures > 0 && now.Sub(r.last) >= Wait {
			r.failures = 0 // the last wrong password is old: forgotten
		}
		if r.failures+r.checking < MaxFailures {
			break
		}
		l.ended.Wait()
	}
	r.checking++
	l.mu.Unlock()

	// Done also when check panics, so the attempts waiting behind this one
	// are not stuck; it then counts as wrong.
	defer func() {
		l.mu.Lock()
		r.checking--
		lastTry := false
		if right {
			r.failures = 0
		} else {
			now := l.Now()
			r.failures++
			r.last = now
			if r.failures == MaxFailures {
				r.until, r.told, lastTry = now.Add(Wait), false, true
			}
		}
		if r.failures == 0 && r.checking == 0 {
			delete(l.byAddr, addr)
		}
		l.mu.Unlock()
		l.ended.Broadcast()
		switch {
		case right:
		case lastTry:
			log.Printf("%s: wrong password from %s, %d in a row: its passwords are refused for the next %s", via, addr, MaxFailures, Wait)
		default:
			log.Printf("%s: wrong password from %s", via, addr)
		}
	}()
	return check(), 0
}

// record returns the record of addr, making one when there is none. At most
// once per Wait it first drops the records that hold nothing back any more,
// so the table stays as small as the number of addresses that sent a wrong
// password lately. l.mu is held.
func (l *Limiter) record(addr string, now time.Time) *record {
	if now.Sub(l.swept) >= Wait {
		for a, r := range l.byAddr {
			if r.checking == 0 && now.Sub(r.last) >= Wait && !now.Before(r.until) {
				delete(l.byAddr, a)
			}
		}
		l.swept = now
	}
	r := l.byAddr[addr]
	if r == nil {
		r = &record{}
		l.byAddr[addr] = r
	}
	return r
}

// Seconds rounds a wait up to whole seconds, as Retry-After gives it.
func Seconds(wait time.Duration) int {
	return int((wait + time.Second - 1) / time.Second)
}
