package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Native backup values are typed records, even though conflict alternatives
// travel as RawMessage. A JSON editor or browser may change their encoding.
func encodedConflict(t *testing.T, kind string, values ...any) RecordConflict {
	t.Helper()
	id := 1
	if kind == "prefix" {
		id = 0
	}
	conflict := RecordConflict{Kind: kind, Prefix: "A", ID: id}
	for i, value := range values {
		raw, err := json.Marshal(value)
		must(t, err)
		actor := "store:" + string(rune('a'+i))
		dot := operationKey(actor, 1)
		hash := valueHash(raw)
		conflict.Candidates = append(conflict.Candidates, RecordCandidate{
			Revision: RecordRevision{Kind: kind, Prefix: "A", ID: id, Hash: hash, Vector: map[string]int64{actor: 1}, Operations: map[string]string{dot: hash}, Heads: []string{dot}},
			Value:    raw,
		})
	}
	return conflict
}

func rewriteConflictJSON(t *testing.T, snapshot RecoverySnapshot, mode string) RecoverySnapshot {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	must(t, err)
	if mode == "pretty" {
		var pretty bytes.Buffer
		must(t, json.Indent(&pretty, raw, "", "  "))
		raw = pretty.Bytes()
	} else {
		// Maps reorder object members; the encoder emits HTML characters
		// literally, as JSON.stringify does. Unescape Unicode too.
		var document any
		must(t, json.Unmarshal(raw, &document))
		var changed bytes.Buffer
		encoder := json.NewEncoder(&changed)
		encoder.SetEscapeHTML(false)
		must(t, encoder.Encode(document))
		raw = []byte(strings.ReplaceAll(strings.ReplaceAll(changed.String(), `\u2028`, "\u2028"), `\u2029`, "\u2029"))
	}
	var result RecoverySnapshot
	must(t, json.Unmarshal(raw, &result))
	return result
}

func TestConflictBackupJSONFormattingPreservesValuesAndReview(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		values     []any
	}{
		{"ticket", "ticket", []any{Ticket{Prefix: "A", TID: 1, FirstName: "Café & <林>\u2028\u2029"}, Ticket{Prefix: "A", TID: 1, FirstName: "Other"}}},
		{"metadata", "metadata", []any{[]string{"Café & <林>\u2028\u2029", "Donor"}, []string{"Other", "Donor"}}},
		{"drawing", "drawing", []any{41, 42}},
		{"prefix", "prefix", []any{Prefix{Prefix: "A", Color: "blue", Weight: 1}, Prefix{Prefix: "A", Color: "red", Weight: 2}}},
		{"deleted prefix", "prefix", []any{nil, Prefix{Prefix: "A", Color: "blue", Weight: 1}}},
	} {
		for _, mode := range []string{"pretty", "reordered and unescaped"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				conflict := encodedConflict(t, tc.kind, tc.values...)
				original := RecoverySnapshot{BackupFile: NewBackupFile(), Conflicts: []RecordConflict{conflict}}
				reformatted := rewriteConflictJSON(t, original, mode)
				must(t, ValidateRecoverySnapshot(&reformatted))
				if !reflect.DeepEqual(reformatted.Conflicts, original.Conflicts) {
					t.Fatal("JSON formatting changed canonical values or revision metadata")
				}
				for i, candidate := range reformatted.Conflicts[0].Candidates {
					if candidateKey(candidate) != candidateKey(conflict.Candidates[i]) {
						t.Fatal("JSON formatting changed a reviewed candidate's identity")
					}
				}
				// Check each import boundary, including a receipt whose candidates
				// have not first passed through snapshot validation.
				for _, boundary := range []string{"recovery", "restore", "client backup", "receipt"} {
					t.Run(boundary, func(t *testing.T) {
						incoming := rewriteConflictJSON(t, original, mode)
						server, _ := recoveryStore(t)
						key, token := requestedKey(t, server)
						switch boundary {
						case "recovery":
							must(t, server.RecoverSnapshot(key, "first", token, incoming))
						case "restore":
							must(t, server.RestoreSnapshot(incoming))
						case "client backup":
							must(t, server.ImportClientBackup(incoming))
						case "receipt":
							must(t, server.ApplyReceipt(SaveReceipt{Conflicts: incoming.Conflicts}))
						}
						stored, err := server.Conflicts()
						must(t, err)
						if len(stored) != 1 || len(stored[0].Candidates) != 2 {
							t.Fatalf("lost conflict alternatives: %+v", stored)
						}
						for _, candidate := range stored[0].Candidates {
							if valueHash(candidate.Value) != candidate.Revision.Hash {
								t.Fatal("stored a noncanonical candidate payload")
							}
						}
						must(t, server.ResolveConflict(tc.kind, "A", conflict.ID, conflict.Candidates[0].Revision.Hash))
						must(t, server.RecoverSnapshot(key, "late", token, rewriteConflictJSON(t, original, mode)))
						must(t, server.CheckConflicts())
					})
				}
			})
		}
	}
}

func TestConflictBackupEncodingCannotHideChangedValues(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		value, bad any
	}{
		{"ticket", "ticket", Ticket{Prefix: "A", TID: 1, FirstName: "Original"}, Ticket{Prefix: "A", TID: 1, FirstName: "Changed"}},
		{"metadata", "metadata", []string{"Original", "Donor"}, []string{"Changed", "Donor"}},
		{"drawing", "drawing", 41, 42},
		{"prefix", "prefix", Prefix{Prefix: "A", Color: "blue"}, Prefix{Prefix: "A", Color: "red"}},
		{"deleted prefix", "prefix", nil, Prefix{Prefix: "A", Color: "blue"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conflict := encodedConflict(t, tc.kind, tc.value, tc.value)
			changed, err := json.Marshal(tc.bad)
			must(t, err)
			conflict.Candidates[1].Value = changed
			snapshot := RecoverySnapshot{BackupFile: NewBackupFile(), Conflicts: []RecordConflict{conflict}}
			snapshot.Tickets = []Ticket{{Prefix: "A", TID: 10, FirstName: "Must not commit"}}
			if err := ValidateRecoverySnapshot(&snapshot); err == nil {
				t.Fatal("changed value was accepted under the original digest")
			}
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			if err := server.RecoverSnapshot(key, "bad", token, snapshot); err == nil {
				t.Fatal("tampered recovery was accepted")
			}
			backup, err := server.Export()
			must(t, err)
			if len(backup.Tickets) != 0 {
				t.Fatal("tampered recovery partially committed")
			}
		})
	}
}
