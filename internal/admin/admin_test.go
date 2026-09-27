package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/store"
)

// site is one admin page under test with a browser-like client: a cookie
// jar and no automatic redirects, so every 303 can be checked.
type site struct {
	t   *testing.T
	url string
	dir string
	st  *store.Store
	pw  *Password
	h   *handler
	c   *http.Client
}

func newSite(t *testing.T, envPassword string) *site {
	t.Helper()
	dir := t.TempDir()
	sqldb, err := db.Open(filepath.Join(dir, "tam-remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateServer(sqldb); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqldb)
	pw, err := Load(dir, envPassword)
	if err != nil {
		t.Fatal(err)
	}
	hd := NewHandler(st, pw, Info{Addr: ":8000", DataDir: dir, Version: "0.0.1", Started: time.Now().Add(-90 * time.Second)})
	ts := httptest.NewServer(hd)
	t.Cleanup(ts.Close)
	return &site{t: t, url: ts.URL, dir: dir, st: st, pw: pw, h: hd.(*handler), c: newBrowser(t)}
}

func newBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *site) get(path string) (*http.Response, string) {
	s.t.Helper()
	res, err := s.c.Get(s.url + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func (s *site) post(path string, form url.Values) (*http.Response, string) {
	s.t.Helper()
	res, err := s.c.PostForm(s.url+path, form)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`)

// token returns the form token of a page.
func (s *site) token(page string) string {
	s.t.Helper()
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		s.t.Fatalf("no form token in page:\n%s", page)
	}
	return m[1]
}

// login opens the login form and posts the password, expecting success.
func (s *site) login(password string) {
	s.t.Helper()
	_, page := s.get("/admin/")
	res, body := s.post("/admin/login", url.Values{"csrf": {s.token(page)}, "password": {password}})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/admin/status" {
		s.t.Fatalf("login = %d %s\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
}

// page fetches a logged-in page and returns its body and form token.
func (s *site) page(path string) (string, string) {
	s.t.Helper()
	res, body := s.get(path)
	if res.StatusCode != 200 {
		s.t.Fatalf("GET %s = %d\n%s", path, res.StatusCode, body)
	}
	return body, s.token(body)
}

func wantRedirect(t *testing.T, res *http.Response, to string) {
	t.Helper()
	if res.StatusCode != 303 || res.Header.Get("Location") != to {
		t.Fatalf("got %d to %q, want 303 to %s", res.StatusCode, res.Header.Get("Location"), to)
	}
}

func TestSetupModeUntilThePasswordIsSet(t *testing.T) {
	s := newSite(t, "")
	res, page := s.get("/admin/")
	if res.StatusCode != 200 || !strings.Contains(page, `action="/admin/setup"`) || strings.Contains(page, `action="/admin/login"`) {
		t.Fatalf("without a password /admin/ must be the setup form: %d\n%s", res.StatusCode, page)
	}
	if strings.Contains(page, "/admin/status") {
		t.Fatal("the setup form must not show the navigation")
	}
	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	token := s.token(page)

	res, body := s.post("/admin/setup", url.Values{"csrf": {token}, "password": {"one"}, "confirm": {"two"}})
	if res.StatusCode != 400 || !strings.Contains(body, "do not match") {
		t.Fatalf("mismatched passwords = %d\n%s", res.StatusCode, body)
	}
	res, body = s.post("/admin/setup", url.Values{"csrf": {token}, "password": {""}, "confirm": {""}})
	if res.StatusCode != 400 || !strings.Contains(body, "must not be empty") {
		t.Fatalf("empty password = %d\n%s", res.StatusCode, body)
	}
	long := strings.Repeat("x", MaxPasswordLen+1)
	if res, _ = s.post("/admin/setup", url.Values{"csrf": {token}, "password": {long}, "confirm": {long}}); res.StatusCode != 400 {
		t.Fatalf("over-long password = %d", res.StatusCode)
	}
	if s.pw.IsSet() {
		t.Fatal("refused setups must not set a password")
	}
	if res, _ = s.post("/admin/setup", url.Values{"password": {"hunter2"}, "confirm": {"hunter2"}}); res.StatusCode != 403 {
		t.Fatalf("setup without the form token = %d, want 403", res.StatusCode)
	}

	res, _ = s.post("/admin/setup", url.Values{"csrf": {token}, "password": {"hunter2"}, "confirm": {"hunter2"}})
	wantRedirect(t, res, "/admin/status")
	if !s.pw.IsSet() || !s.pw.Check("hunter2") {
		t.Fatal("setup did not set the password")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "server.json")); err != nil {
		t.Fatalf("server.json after setup: %v", err)
	}
	// Setup logs the browser in and says so once.
	res, body = s.get("/admin/status")
	if res.StatusCode != 200 || !strings.Contains(body, "Password set.") || !strings.Contains(body, "Clients and keys") {
		t.Fatalf("status after setup = %d\n%s", res.StatusCode, body)
	}
	if _, body = s.get("/admin/status"); strings.Contains(body, "Password set.") {
		t.Fatal("the message must show only once")
	}
	res, _ = s.get("/admin/")
	wantRedirect(t, res, "/admin/status")

	// Setup is over: the form is the login form now, and a second setup
	// cannot replace the password.
	other := &site{t: t, url: s.url, c: newBrowser(t)}
	res, page = other.get("/admin/")
	if res.StatusCode != 200 || !strings.Contains(page, `action="/admin/login"`) {
		t.Fatalf("after setup /admin/ must be the login form: %d\n%s", res.StatusCode, page)
	}
	res, _ = other.post("/admin/setup", url.Values{"csrf": {other.token(page)}, "password": {"evil"}, "confirm": {"evil"}})
	wantRedirect(t, res, "/admin/")
	if s.pw.Check("evil") || !s.pw.Check("hunter2") {
		t.Fatal("a second setup must not change the password")
	}
}

func TestLoginLogoutAndPages(t *testing.T) {
	s := newSite(t, "secret")
	res, page := s.get("/admin/")
	if res.StatusCode != 200 || !strings.Contains(page, `action="/admin/login"`) {
		t.Fatalf("login form = %d\n%s", res.StatusCode, page)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "tam_admin" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/admin" || cookies[0].Secure || len(cookies[0].Value) != 64 {
		t.Fatalf("session cookie = %+v", cookies)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("headers = %v", res.Header)
	}
	if _, page2 := s.get("/admin/"); s.token(page2) != s.token(page) {
		t.Fatal("the same session must keep its form token")
	}

	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	res, _ = s.get("/admin/backup/download")
	wantRedirect(t, res, "/admin/")

	token := s.token(page)
	res, body := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"wrong"}})
	if res.StatusCode != 401 || !strings.Contains(body, "Wrong password.") || !strings.Contains(body, `action="/admin/login"`) {
		t.Fatalf("wrong password = %d\n%s", res.StatusCode, body)
	}
	res, _ = s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}})
	wantRedirect(t, res, "/admin/status")
	loggedIn := res.Cookies()
	if len(loggedIn) != 1 || loggedIn[0].Value == cookies[0].Value || loggedIn[0].MaxAge < 12*3600-5 || loggedIn[0].MaxAge > 12*3600 {
		t.Fatalf("a login must issue a fresh 12 h session cookie: %+v", loggedIn)
	}
	if s.h.ss.get(cookies[0].Value) != nil {
		t.Fatal("the pre-login session must be gone after the login")
	}

	body, _ = s.page("/admin/status")
	for _, want := range []string{":8000", "off", s.dir, "0.0.1", "1 min 30 s", "No client has paired yet", "Log out", `href="/admin/keys"`} {
		if !strings.Contains(body, want) {
			t.Errorf("status page lacks %q:\n%s", want, body)
		}
	}
	res, _ = s.get("/admin/")
	wantRedirect(t, res, "/admin/status")
	res, _ = s.get("/admin")
	wantRedirect(t, res, "/admin/status")

	_, token = s.page("/admin/keys")
	res, _ = s.post("/admin/logout", url.Values{"csrf": {token}})
	wantRedirect(t, res, "/admin/")
	if c := res.Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie: %+v", c)
	}
	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")

	// GET /admin/logout works too, for a plain link.
	s.login("secret")
	res, _ = s.get("/admin/logout")
	wantRedirect(t, res, "/admin/")
	res, _ = s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
}

func TestFormsNeedTheSessionToken(t *testing.T) {
	s := newSite(t, "secret")
	_, page := s.get("/admin/")
	for name, form := range map[string]url.Values{
		"no token":    {"password": {"secret"}},
		"wrong token": {"csrf": {strings.Repeat("0", 64)}, "password": {"secret"}},
	} {
		res, body := s.post("/admin/login", form)
		if res.StatusCode != 403 || !strings.Contains(body, "form token") {
			t.Fatalf("login with %s = %d\n%s", name, res.StatusCode, body)
		}
	}
	// A token from another browser's session does not work either.
	other := &site{t: t, url: s.url, c: newBrowser(t)}
	_, otherPage := other.get("/admin/")
	if res, _ := s.post("/admin/login", url.Values{"csrf": {other.token(otherPage)}, "password": {"secret"}}); res.StatusCode != 403 {
		t.Fatalf("login with another session's token = %d", res.StatusCode)
	}
	if res, _ := s.post("/admin/login", url.Values{"csrf": {s.token(page)}, "password": {"secret"}}); res.StatusCode != 303 {
		t.Fatalf("login with the right token = %d", res.StatusCode)
	}

	for _, path := range []string{"/admin/keys", "/admin/keys/delete", "/admin/password", "/admin/logout", "/admin/backup/restore"} {
		res, _ := s.post(path, url.Values{"description": {"x"}, "key": {"x"}, "confirm": {"yes"}})
		if res.StatusCode != 403 {
			t.Errorf("POST %s without a token = %d, want 403", path, res.StatusCode)
		}
	}
	if keys, _ := s.st.ListKeys(); len(keys) != 0 {
		t.Fatal("a refused form must not create a key")
	}
	if res, _ := s.get("/admin/status"); res.StatusCode != 200 {
		t.Fatal("a refused logout must keep the session")
	}
	// A POST without a body is refused as well, and never panics.
	req, _ := http.NewRequest("POST", s.url+"/admin/keys", nil)
	res, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("empty POST = %d, want 403", res.StatusCode)
	}
}

func TestFiveWrongPasswordsWaitThirtySeconds(t *testing.T) {
	s := newSite(t, "secret")
	_, page := s.get("/admin/")
	token := s.token(page)
	for i := 0; i < 5; i++ {
		if res, _ := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"wrong"}}); res.StatusCode != 401 {
			t.Fatalf("wrong password %d = %d, want 401", i+1, res.StatusCode)
		}
	}
	res, body := s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}})
	if res.StatusCode != 429 || res.Header.Get("Retry-After") != "30" || !strings.Contains(body, "Wait 30 seconds") {
		t.Fatalf("sixth attempt = %d %q\n%s", res.StatusCode, res.Header.Get("Retry-After"), body)
	}
	if res, _ := s.get("/admin/status"); res.StatusCode != 303 {
		t.Fatal("a refused login must not log in")
	}

	// Once the wait is over the right password works.
	s.h.ss.now = func() time.Time { return time.Now().Add(31 * time.Second) }
	res, _ = s.post("/admin/login", url.Values{"csrf": {token}, "password": {"secret"}})
	wantRedirect(t, res, "/admin/status")
}

func TestKeysPage(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	body, token := s.page("/admin/keys")
	if !strings.Contains(body, "No keys yet.") {
		t.Fatalf("keys page:\n%s", body)
	}

	res, body := s.post("/admin/keys", url.Values{"csrf": {token}, "description": {"  "}})
	if res.StatusCode != 400 || !strings.Contains(body, "Give the client a name.") {
		t.Fatalf("blank description = %d\n%s", res.StatusCode, body)
	}
	res, body = s.post("/admin/keys", url.Values{"csrf": {token}, "description": {" Laptop <A> "}})
	if res.StatusCode != 200 {
		t.Fatalf("create key = %d\n%s", res.StatusCode, body)
	}
	m := regexp.MustCompile(`<p class="key">([A-Z0-9]{32})</p>`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Laptop &lt;A&gt;") || !strings.Contains(body, "not shown again") {
		t.Fatalf("the new key must be shown once, escaped:\n%s", body)
	}
	key := m[1]
	if ok, _ := s.st.KeyExists(key); !ok {
		t.Fatal("the shown key is not in the store")
	}

	body, _ = s.page("/admin/keys")
	// The whole key appears once more, hidden in the delete form; the
	// visible cell shows the short form only.
	if strings.Count(body, key) != 1 || !strings.Contains(body, `name="key" value="`+key+`"`) || strings.Contains(body, ">"+key+"<") {
		t.Fatalf("the list must not display the whole key again:\n%s", body)
	}
	if !strings.Contains(body, "<code>"+key[:4]+"\u2026</code>") || !strings.Contains(body, "never") {
		t.Fatalf("the list lacks the short key or the last-seen time:\n%s", body)
	}
	if err := s.st.TouchKey(key); err != nil {
		t.Fatal(err)
	}
	body, _ = s.page("/admin/status")
	if !strings.Contains(body, "Laptop &lt;A&gt;") || !strings.Contains(body, "just now") {
		t.Fatalf("status must list the laptop with its last-seen time:\n%s", body)
	}

	// Deleting asks first, then deletes.
	res, body = s.post("/admin/keys/delete", url.Values{"csrf": {token}, "key": {key}})
	if res.StatusCode != 200 || !strings.Contains(body, "Delete the key for <strong>Laptop &lt;A&gt;</strong>") {
		t.Fatalf("delete without confirmation = %d\n%s", res.StatusCode, body)
	}
	if ok, _ := s.st.KeyExists(key); !ok {
		t.Fatal("the question must not delete anything")
	}
	res, _ = s.post("/admin/keys/delete", url.Values{"csrf": {token}, "key": {key}, "confirm": {"yes"}})
	wantRedirect(t, res, "/admin/keys")
	body, _ = s.page("/admin/keys")
	if !strings.Contains(body, "Deleted the key for Laptop &lt;A&gt;.") || !strings.Contains(body, "No keys yet.") {
		t.Fatalf("after the delete:\n%s", body)
	}
	if ok, _ := s.st.KeyExists(key); ok {
		t.Fatal("the key is still in the store")
	}
	res, _ = s.post("/admin/keys/delete", url.Values{"csrf": {token}, "key": {key}, "confirm": {"yes"}})
	wantRedirect(t, res, "/admin/keys")
	if body, _ = s.page("/admin/keys"); !strings.Contains(body, "already gone") {
		t.Fatalf("deleting a missing key:\n%s", body)
	}
	if res, body = s.post("/admin/keys/delete", url.Values{"csrf": {token}}); res.StatusCode != 400 || !strings.Contains(body, "Choose a key") {
		t.Fatalf("delete without a key = %d", res.StatusCode)
	}
}

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	for _, err := range []error{
		st.UpsertPrefixes([]store.Prefix{{Prefix: "A", Color: "red", Weight: 1}}),
		st.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1, FirstName: "Amy", LastName: "Adams", PhoneNumber: "555", Pref: "CALL"}}),
		st.UpsertBaskets([]store.Basket{{Prefix: "A", BID: 1, Description: "Wine", Donors: "Smiths"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// upload posts a multipart restore form.
func (s *site) upload(token string, file []byte, confirm bool) (*http.Response, string) {
	s.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("csrf", token)
	if confirm {
		mw.WriteField("confirm", "yes")
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "tam-backup.json")
		fw.Write(file)
	}
	mw.Close()
	res, err := s.c.Post(s.url+"/admin/backup/restore", mw.FormDataContentType(), &buf)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestBackupDownloadAndRestore(t *testing.T) {
	src := newSite(t, "secret")
	seed(t, src.st)
	src.login("secret")
	body, _ := src.page("/admin/backup")
	if !strings.Contains(body, "1 prefixes, 1 tickets and 1 baskets") || !strings.Contains(body, `href="/admin/backup/download"`) {
		t.Fatalf("backup page:\n%s", body)
	}

	res, file := src.get("/admin/backup/download")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Content-Disposition") != `attachment; filename="tam-backup.json"` {
		t.Fatalf("download = %d %v", res.StatusCode, res.Header)
	}
	var bf store.BackupFile
	if err := json.Unmarshal([]byte(file), &bf); err != nil || len(bf.Prefixes) != 1 || len(bf.Tickets) != 1 || len(bf.Baskets) != 1 || bf.Tickets[0].FirstName != "Amy" {
		t.Fatalf("download body = %s (%v)", file, err)
	}
	want, _ := src.st.Export()
	if wantJSON, _ := json.Marshal(want); strings.TrimSpace(file) != string(wantJSON) {
		t.Fatalf("the download must be store.Export():\n%s\n%s", file, wantJSON)
	}

	dst := newSite(t, "secret")
	dst.login("secret")
	_, token := dst.page("/admin/backup")
	res, body = dst.upload(token, []byte(file), false)
	if res.StatusCode != 400 || !strings.Contains(body, "Tick the box") {
		t.Fatalf("restore without confirmation = %d\n%s", res.StatusCode, body)
	}
	if _, tickets, _, _ := dst.st.Counts(); tickets != 0 {
		t.Fatal("an unconfirmed restore must not import")
	}
	res, body = dst.upload(token, nil, true)
	if res.StatusCode != 400 || !strings.Contains(body, "Choose a backup file.") {
		t.Fatalf("restore without a file = %d\n%s", res.StatusCode, body)
	}
	res, body = dst.upload(token, []byte("not json"), true)
	if res.StatusCode != 400 || !strings.Contains(body, "not a backup file") {
		t.Fatalf("restore of garbage = %d\n%s", res.StatusCode, body)
	}
	res, body = dst.upload(token, []byte(`{"prefixes":[{"prefix":"","color":"red","weight":1}]}`), true)
	if res.StatusCode != 400 || !strings.Contains(body, "cannot be restored") {
		t.Fatalf("restore of an invalid backup = %d\n%s", res.StatusCode, body)
	}

	res, _ = dst.upload(token, []byte(file), true)
	wantRedirect(t, res, "/admin/backup")
	body, _ = dst.page("/admin/backup")
	if !strings.Contains(body, "Restored 1 prefixes, 1 baskets and 1 tickets.") {
		t.Fatalf("after the restore:\n%s", body)
	}
	got, _ := dst.st.Export()
	if gotJSON, _ := json.Marshal(got); strings.TrimSpace(file) != string(gotJSON) {
		t.Fatalf("restored data differs:\n%s\n%s", gotJSON, file)
	}
}

func TestChangePassword(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	body, token := s.page("/admin/password")
	if !strings.Contains(body, "comes from TAM_PWD") {
		t.Fatalf("the page must say where the password comes from:\n%s", body)
	}
	for name, form := range map[string]url.Values{
		"wrong current": {"current": {"nope"}, "password": {"new"}, "confirm": {"new"}},
		"mismatch":      {"current": {"secret"}, "password": {"new"}, "confirm": {"newer"}},
		"empty":         {"current": {"secret"}, "password": {""}, "confirm": {""}},
	} {
		form.Set("csrf", token)
		if res, _ := s.post("/admin/password", form); res.StatusCode != 400 {
			t.Errorf("%s = %d, want 400", name, res.StatusCode)
		}
	}
	if !s.pw.Check("secret") {
		t.Fatal("refused changes must keep the password")
	}

	res, _ := s.post("/admin/password", url.Values{"csrf": {token}, "current": {"secret"}, "password": {"new one"}, "confirm": {"new one"}})
	wantRedirect(t, res, "/admin/password")
	body, _ = s.page("/admin/password")
	if !strings.Contains(body, "Password changed.") || strings.Contains(body, "comes from TAM_PWD") {
		t.Fatalf("after the change:\n%s", body)
	}
	if !s.pw.Check("new one") || s.pw.Check("secret") {
		t.Fatal("the password did not change")
	}
	reloaded, err := Load(s.dir, "secret")
	if err != nil || !reloaded.Check("new one") || reloaded.Check("secret") {
		t.Fatalf("server.json after the change: %v", err)
	}

	// The new password logs in; the session survives the change.
	if res, _ = s.get("/admin/status"); res.StatusCode != 200 {
		t.Fatal("the session must survive a password change")
	}
	fresh := &site{t: t, url: s.url, c: newBrowser(t)}
	_, page := fresh.get("/admin/")
	if res, _ = fresh.post("/admin/login", url.Values{"csrf": {fresh.token(page)}, "password": {"secret"}}); res.StatusCode != 401 {
		t.Fatalf("old password after the change = %d", res.StatusCode)
	}
	fresh.login("new one")
}

func TestUnknownPathsAndMethodsAreHTML(t *testing.T) {
	s := newSite(t, "secret")
	res, body := s.get("/admin/nope")
	if res.StatusCode != 404 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, "There is no such page.") {
		t.Fatalf("unknown path = %d %q\n%s", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	res, body = s.get("/admin/login")
	if res.StatusCode != 405 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, "not allowed") {
		t.Fatalf("GET on a POST route = %d\n%s", res.StatusCode, body)
	}
	s.login("secret")
	res, body = s.get("/admin/status/extra")
	if res.StatusCode != 404 || !strings.Contains(body, `href="/admin/keys"`) {
		t.Fatalf("unknown path while logged in = %d, want a 404 with the navigation\n%s", res.StatusCode, body)
	}
}

func TestSessionExpires(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	s.h.ss.now = func() time.Time { return time.Now().Add(sessionLife + time.Minute) }
	res, _ := s.get("/admin/status")
	wantRedirect(t, res, "/admin/")
	if len(s.h.ss.byID) != 0 {
		t.Fatalf("expired sessions must be dropped, %d left", len(s.h.ss.byID))
	}
}

func TestCookieIsSecureOverTLS(t *testing.T) {
	dir := t.TempDir()
	sqldb, err := db.Open(filepath.Join(dir, "tam-remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		t.Fatal(err)
	}
	pw, _ := Load(dir, "secret")
	ts := httptest.NewTLSServer(NewHandler(store.New(sqldb), pw, Info{TLS: true}))
	defer ts.Close()
	res, err := ts.Client().Get(ts.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if c := res.Cookies(); len(c) != 1 || !c[0].Secure {
		t.Fatalf("cookie over TLS = %+v, want Secure", c)
	}
}
