package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/guard"
)

// slowPassword takes about as long to check as a bcrypt hash, so guesses
// sent at the same time are checked at the same time.
type slowPassword struct{ Password }

func (p slowPassword) Check(plain string) bool {
	time.Sleep(20 * time.Millisecond)
	return p.Password.Check(plain)
}

// logged collects what the program logs while a test runs.
type logged struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logged) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logged) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLog(t *testing.T) *logged {
	t.Helper()
	l := &logged{}
	old := log.Writer()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(old) })
	return l
}

// atOnce sends n requests at the same moment, the i-th with the headers
// headers(i), and counts the answers by status.
func (a *api) atOnce(n int, method, path string, headers func(i int) map[string]string) map[int]int {
	a.t.Helper()
	var (
		mu    sync.Mutex
		count = map[int]int{}
		errs  []error
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	for i := 0; i < n; i++ {
		req, err := http.NewRequest(method, a.url+path, nil)
		if err != nil {
			a.t.Fatal(err)
		}
		for k, v := range headers(i) {
			req.Header.Set(k, v)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := http.DefaultClient.Do(req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			count[res.StatusCode]++
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		a.t.Fatalf("%d of %d requests failed, the first: %v", len(errs), n, errs[0])
	}
	return count
}

// TestFiveWrongPasswordsWaitThirtySeconds: after five wrong TAM-PW values
// an address waits 30 s, told so with 429 and Retry-After, even with the
// right password; then the right one works again. A request without TAM-PW
// guesses nothing and is not counted.
func TestFiveWrongPasswordsWaitThirtySeconds(t *testing.T) {
	guesses := guard.New()
	a := newAPI(t, WithGuesses(guesses))
	for i := 0; i < 10; i++ {
		if code, _ := a.do("GET", "/api/auth", nil, nil); code != 401 {
			t.Fatalf("no password = %d, want 401", code)
		}
	}
	for i := 0; i < 5; i++ {
		if code, body := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": "wrong"}); code != 401 || !strings.Contains(string(body), "Invalid Password") {
			t.Fatalf("wrong password %d = %d %s, want 401", i+1, code, body)
		}
	}
	req, _ := http.NewRequest("POST", a.url+"/api/auth", strings.NewReader(`{"description":"client"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("TAM-PW", "secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 429 || res.Header.Get("Retry-After") != "30" || strings.TrimSpace(string(body)) != `{"detail":"Too many wrong passwords. Wait 30 seconds and try again."}` {
		t.Fatalf("the right password after five wrong ones = %d, Retry-After %q, %s", res.StatusCode, res.Header.Get("Retry-After"), body)
	}
	if keys, _ := a.st.ListKeys(); len(keys) != 1 {
		t.Fatalf("a refused request created a key: %v", keys)
	}
	if code, _ := a.keyed("GET", "/api/prefixes", nil); code != 200 {
		t.Fatalf("the data routes do not take the password and must keep working: %d", code)
	}

	guesses.Now = func() time.Time { return time.Now().Add(guard.Wait) }
	if code, _ := a.pw("GET", "/api/auth", nil); code != 200 {
		t.Fatalf("the right password after the wait = %d, want 200", code)
	}
}

// TestPasswordGuessesSentAtOnceAreLimited: the admin login lets an address
// try five wrong passwords, then makes it wait; TAM-PW on the key routes is
// held to the same limit, also when the guesses arrive all at once.
func TestPasswordGuessesSentAtOnceAreLimited(t *testing.T) {
	a := newAPIWithPassword(t, slowPassword{FixedPassword("secret")})
	const n = 40
	count := a.atOnce(n, "GET", "/api/auth", func(i int) map[string]string {
		return map[string]string{"TAM-PW": fmt.Sprintf("guess-%d", i)}
	})
	if count[401] != 5 || count[429] != n-5 {
		t.Fatalf("%d wrong passwords sent at once were answered %v; want 5 checked (401) and %d refused (429)", n, count, n-5)
	}
}

// TestRightPasswordsSentAtOnceAllGetThrough: counting a password before it
// is checked must not refuse right ones that arrive together, as when the
// clients of an event behind one address (or the load test's) pair at the
// same moment; they take turns instead.
func TestRightPasswordsSentAtOnceAllGetThrough(t *testing.T) {
	a := newAPIWithPassword(t, slowPassword{FixedPassword("secret")})
	const n = 30
	count := a.atOnce(n, "GET", "/api/auth", func(int) map[string]string {
		return map[string]string{"TAM-PW": "secret"}
	})
	if count[200] != n {
		t.Fatalf("%d right passwords sent at once were answered %v; want all 200", n, count)
	}
}

// TestWrongPasswordsAreLoggedWithTheAddress: every wrong TAM-PW is in the
// log with the address it came from, and so are the wait that follows the
// fifth and the first password that wait refuses; the passwords themselves
// never are.
func TestWrongPasswordsAreLoggedWithTheAddress(t *testing.T) {
	out := captureLog(t)
	a := newAPI(t)
	for i := 0; i < 5; i++ {
		if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": fmt.Sprintf("hunter%d", i)}); code != 401 {
			t.Fatalf("wrong password %d = %d, want 401", i+1, code)
		}
	}
	for i := 0; i < 3; i++ {
		if code, _ := a.do("GET", "/api/auth", nil, map[string]string{"TAM-PW": "hunter-late"}); code != 429 {
			t.Fatalf("password %d during the wait = %d, want 429", i+1, code)
		}
	}
	text := out.String()
	if n := strings.Count(text, "api: wrong password from 127.0.0.1"); n != 5 {
		t.Fatalf("the log names the address of %d of the 5 wrong passwords:\n%s", n, text)
	}
	if !strings.Contains(text, "api: wrong password from 127.0.0.1, 5 in a row: its passwords are refused for the next 30s") {
		t.Fatalf("the log does not say the address has to wait now:\n%s", text)
	}
	if n := strings.Count(text, "api: refusing passwords from 127.0.0.1"); n != 1 {
		t.Fatalf("the first password refused in the wait is logged %d times, want once:\n%s", n, text)
	}
	if strings.Contains(text, "hunter") {
		t.Fatalf("a password is in the log:\n%s", text)
	}
}
