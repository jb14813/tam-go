package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestCausalReceiptRecoveryAndConflictReadBarrier(t *testing.T) {
	original := newAPI(t)
	old := []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Old buyer"}}
	corrected := []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Correct buyer"}}
	var receipts []store.SaveReceipt
	for i, rows := range [][]store.Ticket{old, corrected} {
		client := []string{"a", "b"}[i]
		code, body := original.do("POST", "/api/tickets", rows, map[string]string{"TAM-KEY": original.key, "X-TAM-Client-Name": client, "X-TAM-Save": "1", "X-TAM-Receipts": "1"})
		if code != http.StatusOK {
			t.Fatalf("save: %d %s", code, body)
		}
		var envelope struct {
			Data    []store.Ticket    `json:"data"`
			Receipt store.SaveReceipt `json:"receipt"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data) != 1 || len(envelope.Receipt.Revisions) != 1 {
			t.Fatalf("missing modern receipt: %s", body)
		}
		receipts = append(receipts, envelope.Receipt)
	}
	if receipts[1].Revisions[0].Vector["client:a"] != 1 {
		t.Fatal("accepted correction lost the previous writer's ancestry")
	}
	replacement := newAPI(t)
	token := recoveryToken(t, replacement, replacement.key)
	for i, rows := range [][]store.Ticket{old, corrected} {
		snapshot := store.RecoverySnapshot{BackupFile: store.BackupFile{Tickets: rows}, Revisions: receipts[i].Revisions}
		code, body := replacement.do("POST", "/api/recovery", map[string]any{"token": token, "data": snapshot}, map[string]string{"TAM-KEY": replacement.key, "X-TAM-Client-Name": []string{"a", "b"}[i], "X-TAM-Receipts": "1"})
		if code != http.StatusOK {
			t.Fatalf("recover: %d %s", code, body)
		}
	}
	// The original save committed, but its response could have been lost.
	code, body := replacement.do("POST", "/api/tickets", old, map[string]string{"TAM-KEY": replacement.key, "X-TAM-Client-Name": "a", "X-TAM-Save": "1", "X-TAM-Receipts": "1"})
	if code != http.StatusOK {
		t.Fatalf("lost ack retry: %d %s", code, body)
	}
	code, body = replacement.keyed("GET", "/api/tickets/A/1", nil)
	if code != http.StatusOK || decode[[]store.Ticket](t, body)[0].FirstName != "Correct buyer" {
		t.Fatalf("retry undid correction: %d %s", code, body)
	}
	unknown := store.RecoverySnapshot{BackupFile: store.BackupFile{Tickets: []store.Ticket{{Prefix: "A", TID: 1, FirstName: "Unknown legacy buyer"}}}}
	code, body = replacement.do("POST", "/api/recovery", map[string]any{"token": token, "data": unknown}, map[string]string{"TAM-KEY": replacement.key, "X-TAM-Client-Name": "legacy"})
	if code != http.StatusOK {
		t.Fatalf("conflict must be retained: %d %s", code, body)
	}
	for _, path := range []string{"/api/tickets/A/1", "/api/drawing/A", "/api/reports/counts", "/api/prefixes"} {
		code, body = replacement.keyed("GET", path, nil)
		if code != http.StatusConflict {
			t.Fatalf("unreviewed result leaked at %s: %d %s", path, code, body)
		}
	}
	code, body = replacement.keyed("GET", "/api", nil)
	heartbeat := decode[map[string]any](t, body)
	if heartbeat["conflicts"] != float64(1) || heartbeat["backup_metadata"] != true {
		t.Fatalf("heartbeat hides review/capability: %d %s", code, body)
	}
	code, body = replacement.do("POST", "/api/recovery/receipts", unknown, map[string]string{"TAM-KEY": replacement.key, "X-TAM-Receipts": "1"})
	if code != http.StatusOK {
		t.Fatalf("metadata refresh: %d %s", code, body)
	}
	var receiptEnvelope struct {
		Receipt store.SaveReceipt `json:"receipt"`
	}
	if err := json.Unmarshal(body, &receiptEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(receiptEnvelope.Receipt.Revisions) != 0 {
		t.Fatal("ambiguous row received authoritative receipt")
	}
	code, body = replacement.do("GET", "/api/backuprestore", nil, map[string]string{"TAM-KEY": replacement.key, "X-TAM-Receipts": "1"})
	if code != http.StatusOK {
		t.Fatalf("conflicts prevented backup: %d %s", code, body)
	}
	snapshot := decode[store.RecoverySnapshot](t, body)
	if len(snapshot.Conflicts) != 1 {
		t.Fatal("native server backup lost conflict candidates")
	}
	third := newAPI(t)
	code, body = third.keyed("POST", "/api/backuprestore", snapshot)
	if code != http.StatusOK {
		t.Fatalf("native restore: %d %s", code, body)
	}
	code, body = third.keyed("GET", "/api/tickets/A/1", nil)
	if code != http.StatusConflict {
		t.Fatalf("native restore hid unresolved conflict: %d %s", code, body)
	}
}

func TestNumberedPrefixBackupPreservesLegacyNamesAndRecoveryOrdering(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "newer correction", true: "newer deletion"}[deleted], func(t *testing.T) {
			original := newAPI(t)
			old := store.NewBackupFile()
			old.Prefixes = []store.Prefix{{Prefix: "A/B", Color: "blue", Weight: -3}, {Prefix: " E ", Color: "gray", Weight: -5}}
			headers := map[string]string{"TAM-KEY": original.key, "X-TAM-Client-Name": "old", "X-TAM-Save": "1", "X-TAM-Receipts": "1"}
			code, body := original.do("POST", "/api/backuprestore", old, headers)
			if code != http.StatusOK {
				t.Fatalf("legacy prefix Push: %d %s", code, body)
			}
			var envelope struct {
				Receipt store.SaveReceipt `json:"receipt"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Receipt.Revisions) != 2 {
				t.Fatalf("missing prefix receipts: %s", body)
			}
			prefixes, err := original.st.ListPrefixes()
			if err != nil {
				t.Fatal(err)
			}
			if len(prefixes) != 2 || prefixes[0].Prefix != " E " || prefixes[0].Color != "white" || prefixes[0].Weight != -5 || prefixes[1].Prefix != "A/B" {
				t.Fatalf("legacy prefix values changed: %+v", prefixes)
			}
			headers["X-TAM-Client-Name"] = "newer"
			latest := store.RecoverySnapshot{BackupFile: store.NewBackupFile()}
			if deleted {
				code, body = original.do("DELETE", "/api/prefixes?p="+url.QueryEscape(" E "), nil, headers)
				latest.DeletedPrefixes = []string{" E "}
			} else {
				latest.Prefixes = []store.Prefix{{Prefix: " E ", Color: "red", Weight: 9}}
				code, body = original.do("POST", "/api/backuprestore", latest.BackupFile, headers)
			}
			if code != http.StatusOK {
				t.Fatalf("newer edit: %d %s", code, body)
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatal(err)
			}
			latest.Revisions = envelope.Receipt.Revisions
			replacement := newAPI(t)
			token := recoveryToken(t, replacement, replacement.key)
			if err := replacement.st.RecoverSnapshot(replacement.key, "newer", token, latest); err != nil {
				t.Fatal(err)
			}
			if deleted {
				if _, err := replacement.st.DeletePrefix(" E "); err != nil {
					t.Fatal(err)
				}
			}
			// Original Push committed, its acknowledgement was lost, and a later
			// operator corrected/deleted this prefix before the server disappeared.
			headers["TAM-KEY"] = replacement.key
			headers["X-TAM-Client-Name"] = "old"
			code, body = replacement.do("POST", "/api/backuprestore", old, headers)
			if code != http.StatusOK {
				t.Fatalf("lost-ack prefix retry: %d %s", code, body)
			}
			prefixes, err = replacement.st.ListPrefixes()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, prefix := range prefixes {
				if prefix.Prefix == "E" {
					t.Fatal("whitespace identity was silently trimmed")
				}
				if prefix.Prefix == " E " {
					found = true
					if deleted || prefix.Color != "red" || prefix.Weight != 9 {
						t.Fatalf("old retry undid newer state: %+v", prefix)
					}
				}
			}
			if found == deleted {
				t.Fatalf("wrong deleted state: deleted=%v prefixes=%+v", deleted, prefixes)
			}
			if deleted {
				var tombstones int
				if err := replacement.sqldb.QueryRow(`SELECT count(*) FROM recovery_deleted_prefixes WHERE prefix=' E '`).Scan(&tombstones); err != nil {
					t.Fatal(err)
				}
				if tombstones != 1 {
					t.Fatal("skipped old Push removed current deletion tombstone")
				}
			}
		})
	}
}

func TestNumberedBackupRejectsNonPrefixRestore(t *testing.T) {
	a := newAPI(t)
	code, body := a.do("POST", "/api/backuprestore", store.BackupFile{Tickets: []store.Ticket{{Prefix: "A", TID: 1}}}, map[string]string{"TAM-KEY": a.key, "X-TAM-Client-Name": "desk", "X-TAM-Save": "1"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("unsafe numbered restore accepted: %d %s", code, body)
	}
	backup, err := a.st.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.Tickets) != 0 {
		t.Fatal("rejected restore wrote event data")
	}
}

func TestOnlyLatestSaveReceiptIsRetained(t *testing.T) {
	a := newAPI(t)
	for _, save := range []string{"1", "2", "3"} {
		code, body := a.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: save}}, map[string]string{"TAM-KEY": a.key, "X-TAM-Client-Name": "desk", "X-TAM-Save": save, "X-TAM-Receipts": "1"})
		if code != http.StatusOK {
			t.Fatalf("save: %d %s", code, body)
		}
	}
	var count int
	if err := a.sqldb.QueryRow(`SELECT count(*) FROM operation_receipts WHERE client='desk'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("historical receipts retained: %d", count)
	}
	code, body := a.do("POST", "/api/tickets", []store.Ticket{{Prefix: "A", TID: 1, FirstName: "3"}}, map[string]string{"TAM-KEY": a.key, "X-TAM-Client-Name": "desk", "X-TAM-Save": "3", "X-TAM-Receipts": "1"})
	if code != http.StatusOK {
		t.Fatalf("repeat lost latest receipt: %d %s", code, body)
	}
}
