package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLoadCreatesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s != Defaults() {
		t.Fatalf("Load = %+v, want defaults", s)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("defaults were not written: %v", err)
	}
}

func TestLoadMalformedReturnsDefaultsAndLeavesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	bad := []byte(`{"venue_name": "Typo Venue",}`)
	os.WriteFile(path, bad, 0o644)

	s, err := Load(path)
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("want *LoadError, got %v", err)
	}
	if s != Defaults() {
		t.Fatalf("Load = %+v, want defaults", s)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(bad) {
		t.Fatal("a malformed file must not be overwritten")
	}
}

func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"venue_name": "Hall"}`), 0o644)
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.VenueName != "Hall" || s.RemotePort != "8000" || s.DefaultPref != "CALL" {
		t.Fatalf("Load = %+v", s)
	}
}

func TestSaveThenLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := Settings{RemoteServer: "srv", RemoteKey: "K", RemotePort: "8443", RemoteTLS: true, DefaultPref: "TEXT", VenueName: "V", DisableAttrib: true}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestMerge(t *testing.T) {
	cur := Defaults()
	got, err := Merge(cur, map[string]json.RawMessage{"venue_name": json.RawMessage(`"Y"`)})
	if err != nil {
		t.Fatal(err)
	}
	if got.VenueName != "Y" || got.RemotePort != "8000" || got.DefaultPref != "CALL" {
		t.Fatalf("Merge = %+v", got)
	}
	if _, err := Merge(cur, map[string]json.RawMessage{"colour": json.RawMessage(`"x"`)}); err == nil {
		t.Fatal("unknown keys must be rejected")
	}
	if _, err := Merge(cur, map[string]json.RawMessage{"remote_tls": json.RawMessage(`"yes"`)}); err == nil {
		t.Fatal("wrong types must be rejected")
	}
}

func TestValidate(t *testing.T) {
	ok := Defaults()
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Settings{
		{RemotePort: "abc", DefaultPref: "CALL"},
		{RemotePort: "70000", DefaultPref: "CALL"},
		{RemotePort: "8000", DefaultPref: "PHONE"},
		{RemotePort: "8000", DefaultPref: ""},
		{RemoteServer: "http://tam.lan", RemotePort: "8000", DefaultPref: "CALL"},
		{RemoteServer: "tam.lan/api", RemotePort: "8000", DefaultPref: "CALL"},
		{RemoteServer: "tam lan", RemotePort: "8000", DefaultPref: "CALL"},
		{RemoteServer: "user@tam.lan", RemotePort: "8000", DefaultPref: "CALL"},
		{RemoteServer: "tam.lan", RemotePort: "", DefaultPref: "CALL"},
	} {
		if err := Validate(bad); err == nil {
			t.Errorf("Validate(%+v) should fail", bad)
		}
	}
	for _, good := range []string{"tam.lan", "192.168.1.10", "localhost", ""} {
		s := Defaults()
		s.RemoteServer = good
		if err := Validate(s); err != nil {
			t.Errorf("Validate(remote_server=%q) = %v", good, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	s := Normalize(Settings{RemoteServer: " tam.lan ", RemotePort: " 8443 ", VenueName: " Hall ", DefaultPref: "CALL", RemoteKey: " K "})
	if s.RemoteServer != "tam.lan" || s.RemotePort != "8443" || s.VenueName != "Hall" || s.RemoteKey != "K" {
		t.Fatalf("Normalize = %+v", s)
	}
}

func TestRemoteURL(t *testing.T) {
	if got := Defaults().RemoteURL(); got != "" {
		t.Fatalf("standalone RemoteURL = %q", got)
	}
	s := Settings{RemoteServer: "srv", RemotePort: "8443", RemoteTLS: true}
	if got := s.RemoteURL(); got != "https://srv:8443" {
		t.Fatalf("RemoteURL = %q", got)
	}
	s = Settings{RemoteServer: " srv ", RemotePort: ""}
	if got := s.RemoteURL(); got != "http://srv:8000" {
		t.Fatalf("RemoteURL = %q", got)
	}
	s = Settings{RemoteServer: "::1", RemotePort: "8101"}
	if got := s.RemoteURL(); got != "http://[::1]:8101" {
		t.Fatalf("RemoteURL with IPv6 = %q", got)
	}
}

func TestFileConcurrentGetAndUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	f := Open(path)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var broken atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Every version ever saved has a venue name and a port.
				if s := f.Get(); s.VenueName == "" || s.RemotePort == "" {
					broken.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		want := fmt.Sprintf("Venue %d", i)
		got, err := f.Update(func(s Settings) (Settings, error) {
			s.VenueName = want
			return s, nil
		})
		if err != nil || got.VenueName != want {
			t.Fatalf("update %d: %+v %v", i, got, err)
		}
	}
	close(stop)
	wg.Wait()
	if n := broken.Load(); n > 0 {
		t.Fatalf("%d reads saw broken settings during saves", n)
	}
	if s := f.Get(); s.VenueName != "Venue 199" {
		t.Fatalf("last update lost in memory: %+v", s)
	}
	if s, err := Load(path); err != nil || s.VenueName != "Venue 199" {
		t.Fatalf("last update lost on disk: %+v %v", s, err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestFilePicksUpHandEditsAndKeepsLastGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	f := Open(path)
	if f.Get() != Defaults() {
		t.Fatalf("fresh file = %+v", f.Get())
	}
	edited := Defaults()
	edited.VenueName = "Edited by hand"
	if err := Save(path, edited); err != nil {
		t.Fatal(err)
	}
	if s := f.Get(); s.VenueName != "Edited by hand" {
		t.Fatalf("hand edit not picked up: %+v", s)
	}
	os.WriteFile(path, []byte(`{"venue_name": "Typo",}`), 0o644)
	if s := f.Get(); s.VenueName != "Edited by hand" {
		t.Fatalf("a broken file must keep the last good settings, got %+v", s)
	}
	_, err := f.Update(func(s Settings) (Settings, error) { return s, errors.New("rejected") })
	if err == nil {
		t.Fatal("Update must return fn's error")
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "Typo") {
		t.Fatal("a rejected update must not write the file")
	}
}
