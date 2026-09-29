package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func recoveryToken(t *testing.T, a *api, key string) string {
	return recoveryTokenForClient(t, a, key, "test-client")
}

func recoveryTokenForClient(t *testing.T, a *api, key, client string) string {
	t.Helper()
	code, body := a.do("GET", "/api", nil, map[string]string{"TAM-KEY": key, "X-TAM-Client-Name": client})
	if code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", code, body)
	}
	var reply struct {
		Token string `json:"recovery_token"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Token
}

func TestRecoveryClientsSharingAKeyIncludingLateOfflineClient(t *testing.T) {
	a := newAPI(t)
	for i, client := range []string{"desk-one", "desk-two", "late-offline-desk"} {
		// Restart after each partial contribution and after all currently
		// known clients have answered; an unknown offline client still counts.
		a.url = newTestServer(t, NewHandler(a.st, FixedPassword("secret"))).URL
		token := recoveryTokenForClient(t, a, a.key, client)
		if token == "" {
			t.Fatalf("no recovery request for shared-key client %s", client)
		}
		data := store.BackupFile{Tickets: []store.Ticket{{Prefix: "A", TID: i + 1, FirstName: client}}}
		code, body := a.do("POST", "/api/recovery", map[string]any{"token": token, "data": nativeTestSnapshot(data)}, map[string]string{"TAM-KEY": a.key, "X-TAM-Client-Name": client})
		if code != http.StatusOK {
			t.Fatalf("recover %s: %d %s", client, code, body)
		}
		if recoveryTokenForClient(t, a, a.key, client) != "" {
			t.Fatalf("acknowledged client %s requested twice", client)
		}
	}
	got, err := a.st.AllTickets()
	if err != nil || len(got) != 3 {
		t.Fatalf("shared-key contributions: %+v, %v", got, err)
	}
}

func TestRecoveryRequestsAgainAfterLaterLossOfOrdinaryWrites(t *testing.T) {
	a := newAPI(t)
	client := "desk"
	token := recoveryTokenForClient(t, a, a.key, client)
	code, body := a.do("POST", "/api/recovery", map[string]any{"token": token, "data": nativeTestSnapshot(store.NewBackupFile())}, map[string]string{"TAM-KEY": a.key, "X-TAM-Client-Name": client})
	if code != http.StatusOK {
		t.Fatalf("acknowledge empty copy: %d %s", code, body)
	}
	// No recovery snapshot has populated the server: an ordinary event
	// write must also mark the round as populated before a later loss.
	if err := a.st.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.sqldb.Exec(`DELETE FROM tickets`); err != nil {
		t.Fatal(err)
	}
	a.url = newTestServer(t, NewHandler(a.st, FixedPassword("secret"))).URL
	next := recoveryTokenForClient(t, a, a.key, client)
	if next == "" || next == token {
		t.Fatalf("later loss did not start a fresh round: %q -> %q", token, next)
	}
	code, _ = a.do("POST", "/api/recovery", map[string]any{"token": token, "data": nativeTestSnapshot(store.NewBackupFile())}, map[string]string{"TAM-KEY": a.key, "X-TAM-Client-Name": client})
	if code != http.StatusConflict {
		t.Fatalf("old generation accepted after later loss: %d", code)
	}
}

func TestRecoveryRequestsSurvivePartialRestart(t *testing.T) {
	a := newAPI(t)
	other, err := a.st.CreateKey("offline client")
	if err != nil {
		t.Fatal(err)
	}
	a.url = newTestServer(t, NewHandler(a.st, FixedPassword("secret"))).URL
	token := recoveryToken(t, a, a.key)
	otherToken := recoveryToken(t, a, other.AuthKey)
	if token == "" || otherToken == "" {
		t.Fatal("empty server did not request both saved copies")
	}
	if got := recoveryToken(t, a, ""); got != "" {
		t.Fatal("anonymous heartbeat exposes recovery token")
	}
	if got := recoveryToken(t, a, "invalid"); got != "" {
		t.Fatal("invalid key exposes recovery token")
	}
	a.url = newTestServer(t, NewHandler(a.st, FixedPassword("secret"))).URL
	if got := recoveryToken(t, a, a.key); got != token {
		t.Fatal("empty restart changed outstanding token")
	}
	first := store.BackupFile{
		Prefixes: []store.Prefix{{Prefix: "A", Color: "blue", Weight: 1}},
		Tickets:  []store.Ticket{{Prefix: "A", TID: 1, FirstName: "first"}},
		Baskets:  []store.Basket{{Prefix: "A", BID: 1, Description: "first", WinningTicket: 1}},
	}
	code, body := a.do("POST", "/api/recovery", map[string]any{"token": token, "data": nativeTestSnapshot(first)}, map[string]string{"TAM-KEY": a.key})
	if code != http.StatusOK {
		t.Fatalf("recover first: %d %s", code, body)
	}
	// A normal save made during recovery must win over a later snapshot.
	if err := a.st.UpsertTickets([]store.Ticket{{Prefix: "A", TID: 1, FirstName: "new edit"}}); err != nil {
		t.Fatal(err)
	}
	a.url = newTestServer(t, NewHandler(a.st, FixedPassword("secret"))).URL
	if got := recoveryToken(t, a, a.key); got != "" {
		t.Fatal("acknowledged client requested again")
	}
	if got := recoveryToken(t, a, other.AuthKey); got != otherToken {
		t.Fatal("partial restart forgot offline client")
	}
	second := store.BackupFile{
		Prefixes: []store.Prefix{{Prefix: "A", Color: "red", Weight: 9}, {Prefix: "B", Color: "green"}},
		Tickets:  []store.Ticket{{Prefix: "A", TID: 1, FirstName: "old"}, {Prefix: "B", TID: 2, FirstName: "offline"}},
		Baskets:  []store.Basket{{Prefix: "A", BID: 1, Description: "old", WinningTicket: 2}, {Prefix: "B", BID: 2, WinningTicket: 2}},
	}
	code, body = a.do("POST", "/api/recovery", map[string]any{"token": otherToken, "data": nativeTestSnapshot(second)}, map[string]string{"TAM-KEY": other.AuthKey})
	if code != http.StatusOK {
		t.Fatalf("recover second: %d %s", code, body)
	}
	got, err := a.st.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Prefixes) != 2 || len(got.Tickets) != 2 || len(got.Baskets) != 2 {
		t.Fatalf("missing recovered rows: %+v", got)
	}
	for _, p := range got.Prefixes {
		if p.Prefix == "A" && (p.Color != "blue" || p.Weight != 1) {
			t.Fatalf("overwrote prefix: %+v", p)
		}
	}
	if got.Tickets[0].FirstName != "new edit" || got.Baskets[0].Description != "first" || got.Baskets[0].WinningTicket != 1 {
		t.Fatalf("overwrote current event rows: %+v", got)
	}
	code, _ = a.do("POST", "/api/recovery", map[string]any{"token": otherToken, "data": nativeTestSnapshot(second)}, map[string]string{"TAM-KEY": other.AuthKey})
	if code != http.StatusOK {
		t.Fatalf("acknowledged retry = %d, want idempotent 200", code)
	}
	afterRetry, err := a.st.Export()
	if err != nil || !reflect.DeepEqual(got, afterRetry) {
		t.Fatalf("repeated contribution changed the recovered event: %+v, %v", afterRetry, err)
	}
	a.url = newTestServer(t, NewHandler(a.st, FixedPassword("secret"))).URL
	if recoveryToken(t, a, a.key) != "" || recoveryToken(t, a, other.AuthKey) != "" {
		t.Fatal("nonempty server started another recovery")
	}
}

func TestRecoveryRejectsUnauthenticatedObsoleteAndInvalidSnapshots(t *testing.T) {
	a := newAPI(t)
	token := recoveryToken(t, a, a.key)
	if token == "" {
		t.Fatal("missing recovery request")
	}
	for _, tc := range []struct {
		name, key, token string
		data             any
		status           int
	}{
		{"anonymous", "", token, store.NewBackupFile(), http.StatusUnauthorized},
		{"obsolete", a.key, "wrong", nativeTestSnapshot(store.NewBackupFile()), http.StatusConflict},
		{"invalid", a.key, token, store.BackupFile{Tickets: []store.Ticket{{Prefix: "A", TID: -1}}}, http.StatusUnprocessableEntity},
		{"missing data", a.key, token, nil, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := a.do("POST", "/api/recovery", map[string]any{"token": tc.token, "data": tc.data}, map[string]string{"TAM-KEY": tc.key})
			if code != tc.status {
				t.Fatalf("got %d %s, want %d", code, body, tc.status)
			}
			if recoveryToken(t, a, a.key) != token {
				t.Fatal("refused snapshot acknowledged request")
			}
		})
	}
}

func TestRecoveryNewlyPairedKeyReceivesRequest(t *testing.T) {
	a := newAPI(t)
	code, body := a.do("POST", "/api/auth", map[string]string{"description": "new"}, map[string]string{"TAM-PW": "secret"})
	if code != http.StatusOK {
		t.Fatalf("pair: %d %s", code, body)
	}
	var key store.AuthKey
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatal(err)
	}
	if recoveryToken(t, a, key.AuthKey) == "" {
		t.Fatal("newly paired key did not receive recovery request")
	}
}

func TestRecoveryNewKeyJoinsPartlyPopulatedGeneration(t *testing.T) {
	a := newAPI(t)
	token := recoveryToken(t, a, a.key)
	data := store.BackupFile{Tickets: []store.Ticket{{Prefix: "A", TID: 1}}}
	code, body := a.do("POST", "/api/recovery", map[string]any{"token": token, "data": nativeTestSnapshot(data)}, map[string]string{"TAM-KEY": a.key})
	if code != http.StatusOK {
		t.Fatalf("initial contribution: %d %s", code, body)
	}
	code, body = a.do("POST", "/api/auth", map[string]string{"description": "replacement key"}, map[string]string{"TAM-PW": "secret"})
	if code != http.StatusOK {
		t.Fatalf("new key: %d %s", code, body)
	}
	var key store.AuthKey
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatal(err)
	}
	if got := recoveryToken(t, a, key.AuthKey); got == "" {
		t.Fatal("new key missed existing partly populated recovery")
	}
}

func nativeTestSnapshot(bf store.BackupFile) store.RecoverySnapshot {
	snapshot := store.RecoverySnapshot{BackupFile: bf, Format: store.NativeBackupFormat, BasketComponents: []store.BasketComponents{}}
	for _, row := range bf.Baskets {
		snapshot.BasketComponents = append(snapshot.BasketComponents, store.BasketComponents{Prefix: row.Prefix, BID: row.BID, Metadata: true, Drawing: true})
	}
	return snapshot
}
