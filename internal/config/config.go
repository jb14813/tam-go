// Package config reads and writes the client's settings.json.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
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

// Save writes the settings file, indented, with the usual permissions.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
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

// Validate checks the values that other code relies on.
func Validate(s Settings) error {
	if p := strings.TrimSpace(s.RemotePort); p != "" {
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
// mode.
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
	return scheme + "://" + host + ":" + port
}
