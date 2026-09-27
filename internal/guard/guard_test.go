package guard

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a time a test moves by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newLimiter() (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	l := New()
	l.Now = c.now
	return l, c
}

func wrong() bool { return false }
func right() bool { return true }

func TestFiveWrongPasswordsThenAWait(t *testing.T) {
	l, c := newLimiter()
	for i := 0; i < MaxFailures; i++ {
		if ok, wait := l.Check("10.0.0.2", "test", wrong); ok || wait != 0 {
			t.Fatalf("wrong password %d = %v, wait %v; want checked and wrong", i+1, ok, wait)
		}
	}
	ran := false
	ok, wait := l.Check("10.0.0.2", "test", func() bool { ran = true; return true })
	if ok || ran || wait != Wait {
		t.Fatalf("after %d wrong passwords: right=%v, checked=%v, wait %v; want refused unchecked for %v", MaxFailures, ok, ran, wait, Wait)
	}
	c.add(Wait - time.Second)
	if _, wait = l.Check("10.0.0.2", "test", right); wait != time.Second {
		t.Fatalf("a second before the end of the wait: wait %v", wait)
	}
	c.add(time.Second)
	if ok, wait = l.Check("10.0.0.2", "test", right); !ok || wait != 0 {
		t.Fatalf("after the wait the right password = %v, wait %v", ok, wait)
	}
}

func TestARightPasswordForgetsTheWrongOnes(t *testing.T) {
	l, _ := newLimiter()
	for round := 0; round < 3; round++ {
		for i := 0; i < MaxFailures-1; i++ {
			if _, wait := l.Check("10.0.0.2", "test", wrong); wait != 0 {
				t.Fatalf("round %d, wrong password %d was refused", round, i+1)
			}
		}
		if ok, wait := l.Check("10.0.0.2", "test", right); !ok || wait != 0 {
			t.Fatalf("round %d: the right password = %v, wait %v", round, ok, wait)
		}
	}
	if len(l.byAddr) != 0 {
		t.Fatalf("an address whose last password was right is still kept: %+v", l.byAddr)
	}
}

func TestOldWrongPasswordsAreForgotten(t *testing.T) {
	l, c := newLimiter()
	for i := 0; i < MaxFailures-1; i++ {
		l.Check("10.0.0.2", "test", wrong)
	}
	c.add(Wait)
	for i := 0; i < MaxFailures-1; i++ {
		if _, wait := l.Check("10.0.0.2", "test", wrong); wait != 0 {
			t.Fatalf("wrong password %d after a quiet %v was refused", i+1, Wait)
		}
	}
}

func TestAddressesAreCountedApart(t *testing.T) {
	l, _ := newLimiter()
	for i := 0; i < MaxFailures; i++ {
		l.Check("10.0.0.2", "test", wrong)
	}
	if _, wait := l.Check("10.0.0.2", "test", right); wait == 0 {
		t.Fatal("the address that guessed must wait")
	}
	if ok, wait := l.Check("10.0.0.3", "test", right); !ok || wait != 0 {
		t.Fatalf("another address = %v, wait %v; want it checked", ok, wait)
	}
}

// slowly returns a check that takes a while, as bcrypt does, and counts how
// many checks ran and how many ran at the same time at most.
func slowly(result bool, ran, most *int64) func() bool {
	var now int64
	return func() bool {
		n := atomic.AddInt64(&now, 1)
		defer atomic.AddInt64(&now, -1)
		for {
			m := atomic.LoadInt64(most)
			if n <= m || atomic.CompareAndSwapInt64(most, m, n) {
				break
			}
		}
		atomic.AddInt64(ran, 1)
		time.Sleep(10 * time.Millisecond)
		return result
	}
}

func atOnce(n int, attempt func()) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			attempt()
		}()
	}
	close(start)
	wg.Wait()
}

func TestGuessesSentAtOnceAreCountedFirst(t *testing.T) {
	l, _ := newLimiter()
	var ran, most, refused int64
	check := slowly(false, &ran, &most)
	atOnce(100, func() {
		if _, wait := l.Check("10.0.0.2", "test", check); wait > 0 {
			atomic.AddInt64(&refused, 1)
		}
	})
	if ran != MaxFailures || refused != 100-MaxFailures {
		t.Fatalf("100 wrong passwords at once: %d checked, %d refused; want %d checked", ran, refused, MaxFailures)
	}
}

func TestRightPasswordsSentAtOnceTakeTurns(t *testing.T) {
	l, _ := newLimiter()
	var ran, most, refused, rights int64
	check := slowly(true, &ran, &most)
	atOnce(40, func() {
		ok, wait := l.Check("10.0.0.2", "test", check)
		if wait > 0 {
			atomic.AddInt64(&refused, 1)
		}
		if ok {
			atomic.AddInt64(&rights, 1)
		}
	})
	if rights != 40 || refused != 0 {
		t.Fatalf("40 right passwords at once: %d right, %d refused; want every one right", rights, refused)
	}
	if most > MaxFailures {
		t.Fatalf("%d checks of one address ran at the same time, more than its %d tries", most, MaxFailures)
	}
}

func TestACheckThatPanicsCountsAsWrong(t *testing.T) {
	l, _ := newLimiter()
	for i := 0; i < MaxFailures; i++ {
		func() {
			defer func() { recover() }()
			l.Check("10.0.0.2", "test", func() bool { panic("check failed") })
		}()
	}
	if _, wait := l.Check("10.0.0.2", "test", right); wait == 0 {
		t.Fatalf("%d checks that panicked must count as wrong passwords", MaxFailures)
	}
}

func TestTheTableKeepsOnlyRecentAddresses(t *testing.T) {
	l, c := newLimiter()
	for i := 0; i < 1000; i++ {
		l.Check(fmt.Sprintf("10.0.%d.%d", i/250, i%250), "test", wrong)
	}
	if len(l.byAddr) != 1000 {
		t.Fatalf("%d addresses kept, want 1000", len(l.byAddr))
	}
	c.add(Wait)
	l.Check("10.9.9.9", "test", wrong)
	if len(l.byAddr) != 1 {
		t.Fatalf("%d addresses kept once their wrong passwords are %v old, want only the new one", len(l.byAddr), Wait)
	}
}

// TestTheLogNamesEveryWrongPasswordAndEachWait: each wrong password is a
// line with its address, the last try says what follows, and a wait logs
// only the first password it refuses, however many come.
func TestTheLogNamesEveryWrongPasswordAndEachWait(t *testing.T) {
	var out strings.Builder
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	l, c := newLimiter()
	for round := 0; round < 2; round++ {
		for i := 0; i < MaxFailures+10; i++ {
			l.Check("10.0.0.2", "api", wrong)
		}
		c.add(Wait)
	}
	text := out.String()
	for line, want := range map[string]int{
		"api: wrong password from 10.0.0.2\n":                                                       2 * (MaxFailures - 1),
		"api: wrong password from 10.0.0.2, 5 in a row: its passwords are refused for the next 30s": 2,
		"api: refusing passwords from 10.0.0.2 for 30 s more (logged once per wait)":                2,
	} {
		if got := strings.Count(text, line); got != want {
			t.Errorf("%q is logged %d times, want %d:\n%s", line, got, want, text)
		}
	}
	if lines := strings.Count(text, "\n"); lines != 2*(MaxFailures+1) {
		t.Fatalf("%d lines for 2 rounds of %d passwords, want %d:\n%s", lines, MaxFailures+10, 2*(MaxFailures+1), text)
	}
}

func TestSeconds(t *testing.T) {
	for d, want := range map[time.Duration]int{Wait: 30, Wait - time.Millisecond: 30, time.Millisecond: 1, 1500 * time.Millisecond: 2} {
		if got := Seconds(d); got != want {
			t.Errorf("Seconds(%v) = %d, want %d", d, got, want)
		}
	}
}
