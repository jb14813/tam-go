package store

import (
	"encoding/json"
	"testing"
)

func TestClientBackupRoundTripPreservesComponentOwnership(t *testing.T) {
	for _, drawingOnly := range []bool{false, true} {
		name := "metadata-only"
		if drawingOnly {
			name = "drawing-only"
		}
		t.Run(name, func(t *testing.T) {
			source := newTestStore(t)
			if drawingOnly {
				must(t, source.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
			} else {
				must(t, source.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Gift", Donors: "Sponsor"}}))
			}
			backup, err := source.ExportClientBackup()
			must(t, err)
			encoded, err := json.Marshal(backup)
			must(t, err)
			var parsed RecoverySnapshot
			must(t, json.Unmarshal(encoded, &parsed))
			destination := newTestStore(t)
			must(t, destination.ImportClientBackup(parsed))
			got, err := destination.ExportRecovery()
			must(t, err)
			if len(got.BasketComponents) != 1 || got.BasketComponents[0].Drawing != drawingOnly || got.BasketComponents[0].Metadata == drawingOnly {
				t.Fatalf("round trip changed ownership: %+v", got.BasketComponents)
			}
		})
	}
}

func TestClientBackupPartialRestorePreservesOtherComponent(t *testing.T) {
	source, destination := newTestStore(t), newTestStore(t)
	must(t, source.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Restored gift"}}))
	must(t, destination.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	backup, err := source.ExportClientBackup()
	must(t, err)
	must(t, destination.ImportClientBackup(backup))
	got, err := destination.Basket("A", 1)
	must(t, err)
	if got.Description != "Restored gift" || got.WinningTicket != 42 {
		t.Fatalf("partial restore destroyed another form: %+v", got)
	}
}

func TestLegacyClientBackupRestoresExplicitEmptyComponents(t *testing.T) {
	destination := newTestStore(t)
	must(t, destination.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Old gift", WinningTicket: 42}}))
	legacy := RecoverySnapshot{BackupFile: NewBackupFile()}
	legacy.Baskets = []Basket{{Prefix: "A", BID: 1}}
	must(t, destination.ImportClientBackup(legacy))
	got, err := destination.Basket("A", 1)
	must(t, err)
	if got.Description != "" || got.WinningTicket != 0 {
		t.Fatalf("legacy full restore failed to clear: %+v", got)
	}
}
