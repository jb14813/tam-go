package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
}
