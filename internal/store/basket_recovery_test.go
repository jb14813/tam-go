package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/db"
)

func TestRecoveryBasketMetadataAndDrawingFromDifferentClients(t *testing.T) {
	for _, drawingFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("drawingFirst=%v", drawingFirst), func(t *testing.T) {
			metadata := newTestStore(t)
			drawing := newTestStore(t)
			must(t, metadata.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Gift basket", Donors: "Shop"}}))
			must(t, drawing.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
			first, err := metadata.ExportRecovery()
			must(t, err)
			second, err := drawing.ExportRecovery()
			must(t, err)
			if drawingFirst {
				first, second = second, first
			}
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			must(t, server.RecoverSnapshot(key, "first", token, first))
			must(t, server.RecoverSnapshot(key, "second", token, second))
			basket, err := server.Basket("A", 1)
			must(t, err)
			if basket == nil || basket.Description != "Gift basket" || basket.Donors != "Shop" || basket.WinningTicket != 42 {
				t.Fatalf("split contributions lost: %+v", basket)
			}
		})
	}
}

func TestRecoveryBasketsInitialWinnerIsOwnedOnlyWhenAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   bool
		rows   []Basket
		winner int
		owned  bool
	}{
		{"new accepted winner", false, []Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}, 42, true},
		{"existing ignored winner", true, []Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}, 0, false},
		{"duplicate ignored winner", false, []Basket{{Prefix: "A", BID: 1}, {Prefix: "A", BID: 1, WinningTicket: 42}}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newTestStore(t)
			if tc.seed {
				must(t, source.UpsertBaskets([]Basket{{Prefix: "A", BID: 1}}))
			}
			must(t, source.UpsertBaskets(tc.rows))
			snapshot, err := source.ExportRecovery()
			must(t, err)
			if snapshot.Baskets[0].WinningTicket != tc.winner || snapshot.BasketComponents[0].Drawing != tc.owned {
				t.Fatalf("accepted winner/provenance mismatch: %+v %+v", snapshot.Baskets, snapshot.BasketComponents)
			}
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			must(t, server.RecoverSnapshot(key, "client", token, snapshot))
			got, err := server.Basket("A", 1)
			must(t, err)
			if got.WinningTicket != tc.winner {
				t.Fatalf("recovery lost accepted winner: %+v", got)
			}
		})
	}
}

func TestRecoveryTracksOriginalApplicationBasketUpdates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		drawingFirst bool
		update       string
		winner       int
	}{
		{"original winner", false, `UPDATE baskets SET winning_ticket = 42 WHERE prefix = 'A' AND b_id = 1`, 42},
		{"original winner clear", false, `UPDATE baskets SET winning_ticket = 0 WHERE prefix = 'A' AND b_id = 1`, 0},
		{"original metadata clear", true, `UPDATE baskets SET description = '', donors = '' WHERE prefix = 'A' AND b_id = 1`, 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client.db")
			sqldb, err := db.Open(path)
			must(t, err)
			must(t, db.Migrate(sqldb))
			source := New(sqldb)
			if tc.drawingFirst {
				must(t, source.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
			} else {
				must(t, source.UpsertBaskets([]Basket{{Prefix: "A", BID: 1}}))
			}
			// The original app writes its shared baskets table directly.
			_, err = sqldb.Exec(tc.update)
			must(t, err)
			must(t, sqldb.Close())
			sqldb, err = db.Open(path)
			must(t, err)
			t.Cleanup(func() { sqldb.Close() })
			must(t, db.Migrate(sqldb))
			source = New(sqldb)
			snapshot, err := source.ExportRecovery()
			must(t, err)
			if !snapshot.BasketComponents[0].Metadata || !snapshot.BasketComponents[0].Drawing {
				t.Fatalf("original update lost ownership after reopen: %+v", snapshot.BasketComponents)
			}
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			must(t, server.RecoverSnapshot(key, "client", token, snapshot))
			got, err := server.Basket("A", 1)
			must(t, err)
			if got.Description != "" || got.Donors != "" || got.WinningTicket != tc.winner {
				t.Fatalf("original update lost in recovery: %+v", got)
			}
		})
	}
}

func TestRecoveryBasketComponentsPreserveExplicitClears(t *testing.T) {
	metadata, drawing := newTestStore(t), newTestStore(t)
	must(t, metadata.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "old", Donors: "old"}}))
	must(t, metadata.UpsertBaskets([]Basket{{Prefix: "A", BID: 1}}))
	must(t, drawing.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 42}}))
	must(t, drawing.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 0}}))
	server, _ := recoveryStore(t)
	key, token := requestedKey(t, server)
	for i, source := range []*Store{metadata, drawing} {
		snapshot, err := source.ExportRecovery()
		must(t, err)
		must(t, server.RecoverSnapshot(key, fmt.Sprint(i), token, snapshot))
	}
	// A later complete stale copy must not interpret empty strings or zero
	// as unentered fields and bring the old values back.
	must(t, server.Recover(key, "stale", token, BackupFile{Baskets: []Basket{{Prefix: "A", BID: 1, Description: "stale", Donors: "stale", WinningTicket: 99}}}))
	basket, err := server.Basket("A", 1)
	must(t, err)
	if basket == nil || basket.Description != "" || basket.Donors != "" || basket.WinningTicket != 0 {
		t.Fatalf("explicit clears overwritten: %+v", basket)
	}
}

func TestRecoveryBasketComponentsRespectOrdinaryServerWrites(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(fmt.Sprintf("liveBeforeRecovery=%v", before), func(t *testing.T) {
			server, _ := recoveryStore(t)
			key, token := requestedKey(t, server)
			stale := BackupFile{Baskets: []Basket{{Prefix: "A", BID: 1, Description: "stale", Donors: "stale", WinningTicket: 99}}}
			write := func() {
				must(t, server.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "live", Donors: ""}}))
				must(t, server.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 0}}))
			}
			if before {
				write()
			}
			must(t, server.Recover(key, "first", token, stale))
			if !before {
				write()
			}
			must(t, server.Recover(key, "late", token, stale))
			basket, err := server.Basket("A", 1)
			must(t, err)
			if basket == nil || basket.Description != "live" || basket.Donors != "" || basket.WinningTicket != 0 {
				t.Fatalf("ordinary server write lost: %+v", basket)
			}
		})
	}
}

func TestRecoveryBasketComponentsFillAroundOneOrdinaryServerWrite(t *testing.T) {
	for _, liveDrawing := range []bool{true, false} {
		server, _ := recoveryStore(t)
		key, token := requestedKey(t, server)
		if liveDrawing {
			must(t, server.UpsertWinning([]Basket{{Prefix: "A", BID: 1, WinningTicket: 0}}))
		} else {
			must(t, server.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "", Donors: ""}}))
		}
		must(t, server.Recover(key, "client", token, BackupFile{Baskets: []Basket{{Prefix: "A", BID: 1, Description: "recovered", Donors: "donor", WinningTicket: 42}}}))
		basket, err := server.Basket("A", 1)
		must(t, err)
		if liveDrawing && (basket.Description != "recovered" || basket.Donors != "donor" || basket.WinningTicket != 0) {
			t.Fatalf("live drawing blocked complementary metadata or lost clear: %+v", basket)
		}
		if !liveDrawing && (basket.Description != "" || basket.Donors != "" || basket.WinningTicket != 42) {
			t.Fatalf("live metadata blocked complementary drawing or lost clear: %+v", basket)
		}
	}
}

func TestRecoveryBasketComponentsPreserveLegacyRowsAndBackupFormat(t *testing.T) {
	legacy := newTestStore(t)
	_, err := legacy.db.Exec(`INSERT INTO baskets VALUES ('A', 1, '', '', 0)`)
	must(t, err)
	snapshot, err := legacy.ExportRecovery()
	must(t, err)
	if len(snapshot.BasketComponents) != 1 || !snapshot.BasketComponents[0].Metadata || !snapshot.BasketComponents[0].Drawing {
		t.Fatalf("legacy provenance was guessed from empty values: %+v", snapshot.BasketComponents)
	}
	backup, err := legacy.Export()
	must(t, err)
	raw, err := json.Marshal(backup)
	must(t, err)
	if strings.Contains(string(raw), "basket_components") {
		t.Fatal("ordinary backup format changed")
	}
	must(t, legacy.Import(BackupFile{Baskets: []Basket{{Prefix: "B", BID: 2}}}))
	snapshot, err = legacy.ExportRecovery()
	must(t, err)
	if !snapshot.BasketComponents[1].Metadata || !snapshot.BasketComponents[1].Drawing {
		t.Fatal("explicit restore did not own both components")
	}
	// A missing basket must not inherit orphan provenance after data loss.
	_, err = legacy.db.Exec(`DELETE FROM baskets WHERE prefix = 'B'`)
	must(t, err)
	var n int
	must(t, legacy.db.QueryRow(`SELECT count(*) FROM basket_components WHERE prefix = 'B'`).Scan(&n))
	if n != 0 {
		t.Fatal("basket deletion retained orphan component flags")
	}
}

func TestRecoverySnapshotRejectsIncompleteOrDuplicateComponents(t *testing.T) {
	for _, components := range [][]BasketComponents{
		{},
		{{Prefix: "A", BID: 1}},
		{{Prefix: "other", BID: 1, Metadata: true}},
		{{Prefix: "A", BID: 1, Metadata: true}, {Prefix: "A", BID: 1, Drawing: true}},
	} {
		snapshot := RecoverySnapshot{BackupFile: BackupFile{Baskets: []Basket{{Prefix: "A", BID: 1}}}, BasketComponents: components}
		if err := ValidateRecoverySnapshot(&snapshot); err == nil {
			t.Fatalf("accepted invalid component map: %+v", components)
		}
	}
}
