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

func TestClientBackupRejectsLegacyFilesWithoutChangingData(t *testing.T) {
	destination := newTestStore(t)
	must(t, destination.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Old gift", WinningTicket: 42}}))
	for _, raw := range []string{`{}`, `{"prefixes":[],"tickets":[],"baskets":[]}`, `{"prefixes":[],"tickets":[],"baskets":[{"prefix":"A","b_id":1}]}`} {
		var legacy RecoverySnapshot
		must(t, json.Unmarshal([]byte(raw), &legacy))
		if err := destination.ImportClientBackup(legacy); err == nil {
			t.Errorf("legacy backup accepted: %s", raw)
		}
	}
	got, err := destination.Basket("A", 1)
	must(t, err)
	if got.Description != "Old gift" || got.WinningTicket != 42 {
		t.Fatalf("rejected backup changed existing data: %+v", got)
	}
}

func TestEmptyNativeBackupRoundTrip(t *testing.T) {
	source := newTestStore(t)
	backup, err := source.ExportClientBackup()
	must(t, err)
	raw, err := json.Marshal(backup)
	must(t, err)
	var parsed RecoverySnapshot
	must(t, json.Unmarshal(raw, &parsed))
	must(t, newTestStore(t).ImportClientBackup(parsed))
}

func TestNativeBackupContract(t *testing.T) {
	for _, raw := range []string{
		`{"format":"tam-native-v1","prefixes":[],"tickets":[],"baskets":[],"basket_components":[]}`,
		`{"prefixes":[],"tickets":[],"baskets":[],"basket_components":[]}`,
	} {
		var snapshot RecoverySnapshot
		must(t, json.Unmarshal([]byte(raw), &snapshot))
		must(t, ValidateNativeBackup(&snapshot))
	}
	for _, raw := range []string{
		`{"format":"unsupported","basket_components":[]}`,
		`{"format":"tam-native-v1","baskets":[{"prefix":"A","b_id":1}]}`,
		`{"format":"tam-native-v1","tickets":[{"prefix":"A","t_id":9007199254740992}]}`,
		`{"format":"tam-native-v1","baskets":[{"prefix":"A","b_id":1,"winning_ticket":9007199254740992}],"basket_components":[{"prefix":"A","b_id":1,"drawing":true}]}`,
	} {
		var snapshot RecoverySnapshot
		err := json.Unmarshal([]byte(raw), &snapshot)
		if err == nil {
			err = ValidateNativeBackup(&snapshot)
		}
		if err == nil {
			t.Errorf("invalid native backup accepted: %s", raw)
		}
	}
}
