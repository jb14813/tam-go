package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPasswordFromEnvironment(t *testing.T) {
	pw, err := Load(t.TempDir(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !pw.IsSet() || !pw.FromEnv() {
		t.Fatal("a password from the environment is set and comes from the environment")
	}
	if !pw.Check("secret") || pw.Check("Secret") || pw.Check("secret ") || pw.Check("") {
		t.Fatal("Check against the environment value misbehaves")
	}
}

func TestPasswordUnset(t *testing.T) {
	pw, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if pw.IsSet() || pw.FromEnv() || pw.Check("") || pw.Check("anything") {
		t.Fatal("with neither file nor environment nothing matches")
	}
}

func TestPasswordSetWritesFileThatWins(t *testing.T) {
	dir := t.TempDir()
	pw, err := Load(dir, "env")
	if err != nil {
		t.Fatal(err)
	}
	if err := pw.Set("from the file"); err != nil {
		t.Fatal(err)
	}
	if !pw.Check("from the file") || pw.Check("env") || pw.Check("") || pw.FromEnv() || !pw.IsSet() {
		t.Fatal("after Set only the new password matches")
	}

	path := filepath.Join(dir, "server.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"password_hash": "$2a$`) {
		t.Fatalf("server.json = %s", data)
	}
	if strings.Contains(string(data), "from the file") {
		t.Fatal("server.json must not hold the plain password")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("server.json mode = %o, want 600", st.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the temporary file must be gone after Set")
	}

	// A fresh start reads the file and ignores the environment.
	again, err := Load(dir, "env")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Check("from the file") || again.Check("env") || again.FromEnv() {
		t.Fatal("the stored hash must win over the environment after a restart")
	}

	// Changing it again replaces the file.
	if err := again.Set("second"); err != nil {
		t.Fatal(err)
	}
	third, _ := Load(dir, "")
	if !third.Check("second") || third.Check("from the file") {
		t.Fatal("the second Set did not replace the first")
	}
}

func TestPasswordSetRejectsUnusableValues(t *testing.T) {
	dir := t.TempDir()
	pw, _ := Load(dir, "")
	if err := pw.Set(""); err == nil {
		t.Fatal("an empty password must be refused")
	}
	if err := pw.Set(strings.Repeat("x", MaxPasswordLen+1)); err == nil {
		t.Fatal("a password longer than bcrypt can hash must be refused")
	}
	if pw.IsSet() {
		t.Fatal("a refused Set must leave the password unset")
	}
	if _, err := os.Stat(filepath.Join(dir, "server.json")); !os.IsNotExist(err) {
		t.Fatal("a refused Set must not write server.json")
	}
	if err := pw.Set(strings.Repeat("x", MaxPasswordLen)); err != nil {
		t.Fatalf("a password of exactly %d bytes must work: %v", MaxPasswordLen, err)
	}
}

func TestPasswordLoadBadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	for _, bad := range []string{"not json", `{"password_hash": "garbage"}`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir, "env"); err == nil {
			t.Fatalf("Load over %q should fail rather than fall back to the environment", bad)
		}
	}
	// An empty hash is the same as no file.
	if err := os.WriteFile(path, []byte(`{"password_hash": ""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pw, err := Load(dir, "env")
	if err != nil {
		t.Fatal(err)
	}
	if !pw.FromEnv() || !pw.Check("env") {
		t.Fatal("an empty password_hash should fall back to the environment")
	}
	if _, err := Load(filepath.Join(dir, "missing", "dir"), ""); err != nil {
		t.Fatalf("a missing data directory is not an error at Load: %v", err)
	}
}

func TestFormatting(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                  "0 s",
		45 * time.Second:                   "45 s",
		90 * time.Second:                   "1 min 30 s",
		3 * time.Hour:                      "3 h 0 min",
		26*time.Hour + 5*time.Minute:       "1 d 2 h 5 min",
		-5 * time.Second:                   "0 s",
		2*time.Hour + 59*time.Minute:       "2 h 59 min",
		time.Minute + 999*time.Millisecond: "1 min 1 s",
	} {
		if got := formatUptime(d); got != want {
			t.Errorf("formatUptime(%v) = %q, want %q", d, got, want)
		}
	}
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if got := formatSeen("", now); got != "never" {
		t.Errorf("formatSeen(empty) = %q", got)
	}
	if got := formatSeen("garbage", now); got != "garbage" {
		t.Errorf("formatSeen(garbage) = %q", got)
	}
	for seen, want := range map[string]string{
		"2026-09-25T11:59:30Z": "just now",
		"2026-09-25T11:55:00Z": "5 min ago",
		"2026-09-25T09:00:00Z": "3 h ago",
		"2026-09-20T12:00:00Z": "5 d ago",
	} {
		if got := formatSeen(seen, now); !strings.HasSuffix(got, "("+want+")") {
			t.Errorf("formatSeen(%s) = %q, want suffix (%s)", seen, got, want)
		}
	}
}
