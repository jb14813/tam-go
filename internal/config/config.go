// Package config reads and writes the client's settings.json.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Settings is the settings.json document. The JSON names are shared with
// the original Ticket Auction Manager.
type Settings struct {
	RemoteServer  string `json:"remote_server"`
	RemoteKey     string `json:"remote_key"`
	RemotePort    string `json:"remote_port"`
	RemoteTLS     bool   `json:"remote_tls"`
	DefaultPref   string `json:"default_pref"`
	VenueName     string `json:"venue_name"`
	DisableAttrib bool   `json:"disable_attrib"`
}

// Defaults returns the settings of a fresh installation.
func Defaults() Settings {
	return Settings{
		RemotePort:  "8000",
		DefaultPref: "CALL",
		VenueName:   "Test Venue",
	}
}

// LoadError reports a settings file that could not be used. Load returns it
// together with default settings so the application keeps working.
type LoadError struct {
	Path string
	Err  error
}

func (e *LoadError) Error() string { return fmt.Sprintf("settings file %s: %v", e.Path, e.Err) }
func (e *LoadError) Unwrap() error { return e.Err }

// Load reads the settings file. A missing file is created with defaults. An
// unreadable or malformed file yields defaults and a *LoadError; the file is
// left untouched so a hand edit can be fixed.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s := Defaults()
		if err := Save(path, s); err != nil {
			return s, &LoadError{Path: path, Err: err}
		}
		return s, nil
	}
	if err != nil {
		return Defaults(), &LoadError{Path: path, Err: err}
	}
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		return Defaults(), &LoadError{Path: path, Err: err}
	}
	return s, nil
}

// Save writes the settings file. The document goes to a temporary file
// that then replaces the real one, so a crash mid-write leaves the old
// file intact. Windows refuses the replace while another process holds the
// file open; the document is then written in place.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return os.WriteFile(path, data, 0o644)
	}
	return nil
}

// File is the settings document with an in-memory copy. Readers never see
// a half-written file: saves happen under the write lock, and the file is
// re-read only when its modification time or size changed, which is how a
// hand edit is picked up while the daemon runs.
type File struct {
	path   string
	mu     sync.RWMutex
	cur    Settings
	loaded bool
	mtime  time.Time
	size   int64
}

// Open loads the settings file (creating it with defaults when missing)
// and returns the live document. A broken file is logged; defaults are used
// until it is fixed.
func Open(path string) *File {
	f := &File{path: path}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.reload(); err != nil {
		log.Printf("%v (using defaults)", err)
	}
	return f
}

// reload reads the file and remembers its stat. The caller holds mu for
// writing. When the file cannot be used, the last good settings stay.
func (f *File) reload() error {
	s, err := Load(f.path)
	if info, statErr := os.Stat(f.path); statErr == nil {
		f.mtime, f.size = info.ModTime(), info.Size()
	}
	if err != nil {
		if !f.loaded {
			f.cur = s
		}
		return err
	}
	f.cur, f.loaded = s, true
	return nil
}

// changedOnDisk reports whether the file differs from what was last read.
func (f *File) changedOnDisk() bool {
	info, err := os.Stat(f.path)
	if err != nil {
		return true
	}
	return !info.ModTime().Equal(f.mtime) || info.Size() != f.size
}

// Get returns the current settings.
func (f *File) Get() Settings {
	f.mu.RLock()
	if !f.changedOnDisk() {
		s := f.cur
		f.mu.RUnlock()
		return s
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changedOnDisk() {
		if err := f.reload(); err != nil {
			log.Printf("%v (keeping the last good settings)", err)
		}
	}
	return f.cur
}

// Update applies fn to the current settings and saves the result. fn runs
// under the lock, so concurrent updates never lose each other's fields.
func (f *File) Update(fn func(Settings) (Settings, error)) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changedOnDisk() {
		if err := f.reload(); err != nil {
			log.Printf("%v (keeping the last good settings)", err)
		}
	}
	next, err := fn(f.cur)
	if err != nil {
		return f.cur, err
	}
	if err := Save(f.path, next); err != nil {
		return f.cur, err
	}
	f.cur, f.loaded = next, true
	if info, err := os.Stat(f.path); err == nil {
		f.mtime, f.size = info.ModTime(), info.Size()
	}
	return next, nil
}

var knownKeys = map[string]bool{
	"remote_server": true, "remote_key": true, "remote_port": true, "remote_tls": true,
	"default_pref": true, "venue_name": true, "disable_attrib": true,
}

// Merge applies the keys present in patch onto current. Keys that are not
// settings, or values of the wrong type, are rejected.
func Merge(current Settings, patch map[string]json.RawMessage) (Settings, error) {
	for k := range patch {
		if !knownKeys[k] {
			return current, fmt.Errorf("unknown setting %q", k)
		}
	}
	base, err := json.Marshal(current)
	if err != nil {
		return current, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return current, err
	}
	for k, v := range patch {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return current, err
	}
	var s Settings
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return current, fmt.Errorf("invalid settings: %w", err)
	}
	return s, nil
}

// Normalize trims the text fields so a stray space never ends up in a URL.
func Normalize(s Settings) Settings {
	s.RemoteServer = strings.TrimSpace(s.RemoteServer)
	s.RemoteKey = strings.TrimSpace(s.RemoteKey)
	s.RemotePort = strings.TrimSpace(s.RemotePort)
	s.DefaultPref = strings.TrimSpace(s.DefaultPref)
	s.VenueName = strings.TrimSpace(s.VenueName)
	return s
}

// Validate checks the values that other code relies on.
func Validate(s Settings) error {
	if host := s.RemoteServer; host != "" {
		if strings.ContainsAny(host, `/\?#@ `) || strings.Contains(host, "://") {
			return errors.New("remote_server must be a host name or IP address, without scheme, port or path")
		}
		if s.RemotePort == "" {
			return errors.New("remote_port is required when remote_server is set")
		}
	}
	if p := s.RemotePort; p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("remote_port must be a number between 1 and 65535")
		}
	}
	if s.DefaultPref != "CALL" && s.DefaultPref != "TEXT" {
		return errors.New("default_pref must be CALL or TEXT")
	}
	return nil
}

// RemoteURL returns the base URL of the remote server, or "" in standalone
// mode. IPv6 addresses are bracketed.
func (s Settings) RemoteURL() string {
	host := strings.TrimSpace(s.RemoteServer)
	if host == "" {
		return ""
	}
	scheme := "http"
	if s.RemoteTLS {
		scheme = "https"
	}
	port := strings.TrimSpace(s.RemotePort)
	if port == "" {
		port = "8000"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
