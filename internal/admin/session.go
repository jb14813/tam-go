package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
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

// sessions is the in-memory table of logged-in sessions. The anonymous
// session of a visitor who has not logged in is not kept: its id carries
// its end and its form token is a MAC of the id (see visit), so anyone can
// open the login form as often as they like without adding to the table.
// All of it is lost on restart, which just means logging in again.
type sessions struct {
	mu   sync.Mutex
	byID map[string]*session // logged-in sessions
	key  []byte              // signs the form tokens of anonymous sessions
	now  func() time.Time
}

func newSessions() *sessions {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return &sessions{byID: map[string]*session{}, key: key, now: time.Now}
}

func randomHex() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// create starts a logged-in session. It sweeps expired sessions on the way,
// so the table stays bounded by the logins of the last sessionLife; only
// the password makes one.
func (ss *sessions) create() *session {
	s := &session{id: randomHex(), csrf: randomHex(), loggedIn: true}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := ss.now()
	for id, old := range ss.byID {
		if !old.expires.After(now) {
			delete(ss.byID, id)
		}
	}
	s.expires = now.Add(sessionLife)
	ss.byID[s.id] = s
	return s
}

// visit starts the anonymous session of a visitor of the login or setup
// form, and keeps nothing: the id is 24 random bytes followed by the second
// the session ends, and the form token is a MAC of the id with the table's
// key. A form posted with that token thus comes from the browser that holds
// the cookie, as with a kept session.
func (ss *sessions) visit() *session {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw[:24]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	expires := ss.now().Add(anonymousLife).Truncate(time.Second)
	binary.BigEndian.PutUint64(raw[24:], uint64(expires.Unix()))
	return &session{id: hex.EncodeToString(raw), csrf: ss.sign(raw), expires: expires}
}

// anonymous returns the anonymous session id names, or nil when id is not
// one that visit could have handed out within the last anonymousLife.
func (ss *sessions) anonymous(id string) *session {
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 32 {
		return nil
	}
	expires := time.Unix(int64(binary.BigEndian.Uint64(raw[24:])), 0)
	if now := ss.now(); !expires.After(now) || expires.After(now.Add(anonymousLife)) {
		return nil
	}
	return &session{id: id, csrf: ss.sign(raw), expires: expires}
}

// sign is the form token of an anonymous session's id.
func (ss *sessions) sign(raw []byte) string {
	mac := hmac.New(sha256.New, ss.key)
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

// visitor returns the session a cookie names: a logged-in one, or else an
// anonymous one, or nil.
func (ss *sessions) visitor(id string) *session {
	if s := ss.get(id); s != nil {
		return s
	}
	return ss.anonymous(id)
}

// get returns a copy of the live logged-in session with that id, or nil.
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
