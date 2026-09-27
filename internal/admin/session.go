package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// cookieName is the session cookie. Its path is /admin so the API never
	// sees it.
	cookieName = "tam_admin"
	// sessionLife is how long a login lasts.
	sessionLife = 12 * time.Hour
	// anonymousLife is how long a login form can sit open before its token
	// expires. Anyone can open the form, so these sessions are short.
	anonymousLife = time.Hour
	// maxFailures is how many wrong passwords an address may send before it
	// has to wait failureWait.
	maxFailures = 5
	failureWait = 30 * time.Second
)

// session is one browser's state: an id in the cookie, a token every form
// of that session carries, and whether the password was given.
type session struct {
	id       string
	csrf     string
	loggedIn bool
	expires  time.Time
	flash    string // a message shown once on the next page
}

// sessions is the in-memory session table plus the login rate limit. Both
// are lost on restart, which just means logging in again.
type sessions struct {
	mu       sync.Mutex
	byID     map[string]*session
	failures map[string]*failure // by remote address
	now      func() time.Time
}

type failure struct {
	count int
	until time.Time // when count reached maxFailures: the end of the wait
}

func newSessions() *sessions {
	return &sessions{byID: map[string]*session{}, failures: map[string]*failure{}, now: time.Now}
}

func randomHex() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// create starts a session. It sweeps expired sessions on the way so the
// table stays bounded by the number of visitors in the last hour.
func (ss *sessions) create(loggedIn bool) *session {
	s := &session{id: randomHex(), csrf: randomHex(), loggedIn: loggedIn}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := ss.now()
	for id, old := range ss.byID {
		if !old.expires.After(now) {
			delete(ss.byID, id)
		}
	}
	if loggedIn {
		s.expires = now.Add(sessionLife)
	} else {
		s.expires = now.Add(anonymousLife)
	}
	ss.byID[s.id] = s
	return s
}

// get returns a copy of the live session with that id, or nil.
func (ss *sessions) get(id string) *session {
	if id == "" {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.byID[id]
	if !ok {
		return nil
	}
	if !s.expires.After(ss.now()) {
		delete(ss.byID, id)
		return nil
	}
	copy := *s
	return &copy
}

// delete ends a session.
func (ss *sessions) delete(id string) {
	ss.mu.Lock()
	delete(ss.byID, id)
	ss.mu.Unlock()
}

// setFlash stores a message the next page of the session shows once.
func (ss *sessions) setFlash(id, msg string) {
	ss.mu.Lock()
	if s, ok := ss.byID[id]; ok {
		s.flash = msg
	}
	ss.mu.Unlock()
}

// takeFlash returns the session's pending message and clears it.
func (ss *sessions) takeFlash(id string) string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.byID[id]
	if !ok {
		return ""
	}
	msg := s.flash
	s.flash = ""
	return msg
}

// validToken reports whether token is the session's form token.
func (s *session) validToken(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.csrf)) == 1
}

// --- login rate limit ---

// remoteIP is the address part of r.RemoteAddr.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginAllowed reports whether addr may try a password now. Once the wait
// after maxFailures wrong passwords is over the count starts again.
func (ss *sessions) loginAllowed(addr string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	f, ok := ss.failures[addr]
	if !ok || f.count < maxFailures {
		return true
	}
	if ss.now().Before(f.until) {
		return false
	}
	delete(ss.failures, addr)
	return true
}

// noteFailure counts a wrong password from addr.
func (ss *sessions) noteFailure(addr string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	f, ok := ss.failures[addr]
	if !ok {
		f = &failure{}
		ss.failures[addr] = f
	}
	f.count++
	if f.count >= maxFailures {
		f.until = ss.now().Add(failureWait)
	}
}

// noteSuccess forgets addr's wrong passwords.
func (ss *sessions) noteSuccess(addr string) {
	ss.mu.Lock()
	delete(ss.failures, addr)
	ss.mu.Unlock()
}

// --- cookies ---

// setCookie sends the session cookie: HttpOnly, SameSite=Strict, Secure
// when the request came over TLS, and only for the /admin pages.
func setCookie(w http.ResponseWriter, r *http.Request, s *session, now time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.id,
		Path:     "/admin",
		MaxAge:   int(s.expires.Sub(now).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearCookie tells the browser to drop the session cookie.
func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// cookieID returns the session id the request carries, or "".
func cookieID(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
