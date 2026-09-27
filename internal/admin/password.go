// Package admin is the server's login page: status, paired clients and
// keys, backup and restore, and the server password. It also owns the
// password itself, which the API's key routes check through server.Password.
package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// passwordFile is the name of the file that holds the bcrypt hash.
const passwordFile = "server.json"

// MaxPasswordLen is the longest password bcrypt can hash.
const MaxPasswordLen = 72

// Password is the server password. A bcrypt hash stored in
// <dataDir>/server.json wins over the plain value given in the environment
// (TAM_PWD); with neither the password is unset and the server is in setup
// mode until Set is called from the admin page.
type Password struct {
	path string
	env  string

	mu   sync.RWMutex
	hash []byte // the stored bcrypt hash, nil when there is no file
}

type passwordDoc struct {
	PasswordHash string `json:"password_hash"`
}

// Load reads <dataDir>/server.json when it exists and remembers envValue
// for when it does not. A file that cannot be parsed is an error rather than
// a silent fallback to the environment.
func Load(dataDir, envValue string) (*Password, error) {
	p := &Password{path: filepath.Join(dataDir, passwordFile), env: envValue}
	data, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.path, err)
	}
	var doc passwordDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.path, err)
	}
	if doc.PasswordHash != "" {
		if _, err := bcrypt.Cost([]byte(doc.PasswordHash)); err != nil {
			return nil, fmt.Errorf("parse %s: password_hash is not a bcrypt hash: %w", p.path, err)
		}
		p.hash = []byte(doc.PasswordHash)
	}
	return p, nil
}

// IsSet reports whether there is a password at all: a stored hash or a
// non-empty environment value.
func (p *Password) IsSet() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hash != nil || p.env != ""
}

// FromEnv reports whether the password in use is the environment value,
// which is the case until Set writes server.json.
func (p *Password) FromEnv() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hash == nil && p.env != ""
}

// Check reports whether plain is the password. The stored hash is compared
// with bcrypt, the environment value in constant time; an empty plain never
// matches.
func (p *Password) Check(plain string) bool {
	if plain == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.hash != nil {
		return bcrypt.CompareHashAndPassword(p.hash, []byte(plain)) == nil
	}
	if p.env == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(plain), []byte(p.env)) == 1
}

// Set hashes plain, writes it to server.json (readable by the owner only)
// and makes it the password from now on, over any environment value.
func (p *Password) Set(plain string) error {
	if err := ValidatePassword(plain); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(passwordDoc{PasswordHash: string(hash)}, "", "  ")
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Write next to the file and rename, so a crash mid-write cannot leave
	// a half-written file that Load would then refuse.
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", p.path, err)
	}
	p.hash = hash
	return nil
}

// ValidatePassword rejects an empty password and one longer than bcrypt
// can hash.
func ValidatePassword(plain string) error {
	if plain == "" {
		return errors.New("the password must not be empty")
	}
	if len(plain) > MaxPasswordLen {
		return fmt.Errorf("the password must be at most %d bytes", MaxPasswordLen)
	}
	return nil
}
