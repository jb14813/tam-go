//go:build linux

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPortsStayReserved(t *testing.T) {
	reservations, err := reservePorts(13)
	if err != nil {
		t.Fatal(err)
	}
	defer reservations.close()
	for i := 0; i < 12; i++ {
		r, err := newRelay("127.0.0.1:1", 0, uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		defer r.close()
		for _, port := range reservations.ports {
			if r.port() == port {
				t.Errorf("relay took reserved control port %s", port)
			}
		}
	}
	for _, port := range reservations.ports {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err == nil {
			ln.Close()
			t.Errorf("control port %s was available before its child started", port)
		}
	}
}

func socketDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		target, _ := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if strings.HasPrefix(target, "socket:") {
			n++
		}
	}
	return n
}

func TestStartupErrorReleasesReservationsAndAllowsCleanup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"test"}`))
	}))
	defer srv.Close()
	bin, work := t.TempDir(), t.TempDir()
	for _, name := range []string{"tam-server", "tam-client"} {
		if err := os.WriteFile(filepath.Join(bin, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "client-01"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := socketDescriptors(t)
	run := newTest(options{bin: bin, server: srv.URL, clients: 3}, work)
	if err := run.start(); err == nil {
		t.Fatal("expected client directory creation to fail")
	}
	web.CloseIdleConnections()
	deadline := time.Now().Add(time.Second)
	for socketDescriptors(t) != before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if after := socketDescriptors(t); after != before {
		t.Fatalf("startup failure leaked socket descriptors: before %d, after %d", before, after)
	}
	run.stopAll()
}

func TestReleasingControlPortLeavesOtherReservationsHeld(t *testing.T) {
	reservations, err := reservePorts(3)
	if err != nil {
		t.Fatal(err)
	}
	defer reservations.close()
	reservations.release(1)
	for i, port := range reservations.ports {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err == nil {
			ln.Close()
		}
		if (err == nil) != (i == 1) {
			t.Errorf("control port %d: bind error %v; only the released port should be available", i, err)
		}
	}
	reservations.close()
	for _, port := range reservations.ports {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			t.Errorf("unreleased control port %s leaked after cleanup: %v", port, err)
			continue
		}
		ln.Close()
	}
}

// The test subprocess has no network listeners. Its lifetime lets the readiness
// checks distinguish a live child from another HTTP responder on the same URL.
func TestLoadProgramChild(t *testing.T) {
	mode := os.Getenv("LOADTEST_CHILD_MODE")
	if mode == "" {
		return
	}
	if mode == "exit-after-probe" {
		for {
			if _, err := os.Stat(os.Getenv("LOADTEST_CHILD_SIGNAL")); err == nil {
				os.Exit(1)
			}
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(time.Hour)
	os.Exit(0)
}

func readinessChild(t *testing.T, name, mode string) *program {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := newProgram(name, executable, t.TempDir(), "1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.args = []string{"-test.run=^TestLoadProgramChild$"}
	p.env = append(p.env, "LOADTEST_CHILD_MODE="+mode, "LOADTEST_CHILD_SIGNAL="+filepath.Join(p.dir, "probe"))
	t.Cleanup(p.kill)
	return p
}

func TestReadinessUsesRoleControlRoute(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"tam-server", "/api"},
		{"client-01", "/api/status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := readinessChild(t, tc.name, "exit-after-probe")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					os.WriteFile(filepath.Join(p.dir, "probe"), nil, 0o600)
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			p.url = srv.URL
			if err := p.startOnce(); err != nil {
				t.Fatalf("live %s should answer on %s: %v", tc.name, tc.path, err)
			}
		})
	}
}

func TestExitedChildCannotBeReadyFromAnotherResponder(t *testing.T) {
	p := readinessChild(t, "client-01", "exit-after-probe")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := os.WriteFile(filepath.Join(p.dir, "probe"), nil, 0o600); err != nil {
			t.Error(err)
		}
		_, exited := p.current()
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Error("child did not exit during the readiness request")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p.url = srv.URL
	if err := p.startOnce(); err == nil {
		t.Fatal("accepted HTTP 200 from another responder after the child exited")
	}
}
