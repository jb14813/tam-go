package store

import (
	"strings"
	"testing"
)

// TestBackupKeepsPrefixesAsTheyAre: the rules for a new prefix name (no /
// or \, not . or .., at most 100 characters, trimmed) are for names typed
// now. A backup carries the names a database already has, possibly from the
// original app, which allowed any: restoring it, or a client copying its
// server's data, takes them as they are. Renaming one would cut it off from
// its tickets and baskets, and refusing it would refuse the whole backup.
func TestBackupKeepsPrefixesAsTheyAre(t *testing.T) {
	legacy := []string{"A/B", `C\D`, ".", "..", " E ", strings.Repeat("F", 120)}
	bf := NewBackupFile()
	for i, name := range legacy {
		bf.Prefixes = append(bf.Prefixes, Prefix{Prefix: name, Color: "red", Weight: i - 1})
	}
	if err := ValidateBackup(&bf); err != nil {
		t.Fatalf("a backup with the names %q was refused: %v", legacy, err)
	}
	for i, p := range bf.Prefixes {
		if p.Prefix != legacy[i] || p.Weight != i-1 {
			t.Fatalf("prefix %d came out as %+v, want %q with weight %d", i, p, legacy[i], i-1)
		}
	}

	empty := NewBackupFile()
	empty.Prefixes = []Prefix{{Prefix: "", Color: "red"}}
	if err := ValidateBackup(&empty); err == nil {
		t.Fatal("a prefix without a name must still be refused")
	}
}
