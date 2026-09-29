package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestRemoteImportUsesConnectionSelectedWhileBodyArrives(t *testing.T) {
	for _, unpair := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "standalone"}[unpair], func(t *testing.T) {
			f := newFixture(t)
			oldStore, _ := remoteFixture(t, f)
			bf := store.NewBackupFile()
			bf.Tickets = []store.Ticket{{Prefix: "A", TID: 22, FirstName: "Imported"}}
			data, err := json.Marshal(nativeFixtureBackup(bf))
			if err != nil {
				t.Fatal(err)
			}
			started, resume := make(chan struct{}), make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(resume) })
			body := &pausedSaveBody{started: started, resume: resume, reader: bytes.NewReader(data)}
			r := httptest.NewRequest("POST", "/api/backuprestore/remote", body)
			r.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { f.h.routes(testDist).ServeHTTP(response, r); close(done) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("import did not start reading its body")
			}
			newStore := newServerStore(t)
			if unpair {
				if code, b := f.do("POST", "/api/unpair", `{}`, nil); code != 200 {
					t.Fatalf("unpair: %d %s", code, b)
				}
			} else {
				key, err := newStore.CreateKey("replacement")
				if err != nil {
					t.Fatal(err)
				}
				rs := namedServer(t, newStore, "replacement")
				u, _ := url.Parse(rs.URL)
				if code, b := f.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": key.AuthKey}, nil); code != 200 {
					t.Fatalf("switch: %d %s", code, b)
				}
			}
			release.Do(func() { close(resume) })
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("import did not finish")
			}
			wantStatus := http.StatusOK
			if unpair {
				wantStatus = http.StatusInternalServerError
			}
			if response.Code != wantStatus {
				t.Errorf("import: %d %s, want %d", response.Code, response.Body.String(), wantStatus)
			}
			if row, err := oldStore.Ticket("A", 22); err != nil || row != nil {
				t.Errorf("import reached previous server: %+v, %v", row, err)
			}
			if !unpair {
				serverHas(t, newStore, 22, "Imported")
			}
		})
	}
}

func TestRemoteRestoreFinishesBeforeConnectionChange(t *testing.T) {
	for _, operation := range []string{"import", "push"} {
		for _, unpair := range []bool{false, true} {
			t.Run(operation+"/"+map[bool]string{false: "replacement", true: "standalone"}[unpair], func(t *testing.T) {
				f := newFixture(t)
				rst := newServerStore(t)
				key, err := rst.CreateKey("original")
				if err != nil {
					t.Fatal(err)
				}
				started, resume := make(chan struct{}), make(chan struct{})
				var release sync.Once
				inner := server.NewHandler(rst, server.FixedPassword("secret"))
				rs := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" && ((operation == "import" && r.URL.Path == "/api/backuprestore") || (operation == "push" && r.URL.Path == "/api/drawing")) {
						close(started)
						<-resume
					}
					inner.ServeHTTP(w, r)
				}))
				defer release.Do(func() { close(resume) })
				u, _ := url.Parse(rs.URL)
				if code, b := f.do("POST", "/api/settings", map[string]any{"remote_server": u.Hostname(), "remote_port": u.Port(), "remote_key": key.AuthKey}, nil); code != 200 {
					t.Fatalf("configure: %d %s", code, b)
				}
				bf := store.NewBackupFile()
				bf.Baskets = []store.Basket{{Prefix: "A", BID: 3, Description: "Prize", WinningTicket: 22}}
				path, payload := "/api/backuprestore/remote", any(nativeFixtureBackup(bf))
				if operation == "push" {
					if err := f.st.Import(bf); err != nil {
						t.Fatal(err)
					}
					path, payload = "/api/backuprestore/push/baskets", map[string]any{}
				}
				restored := make(chan int, 1)
				go func() { code, _ := f.do("POST", path, payload, nil); restored <- code }()
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("restore did not reach its native request")
				}
				changed := make(chan int, 1)
				go func() {
					if unpair {
						code, _ := f.do("POST", "/api/unpair", `{}`, nil)
						changed <- code
					} else {
						code, _ := f.do("POST", "/api/settings", map[string]any{"remote_server": "replacement.invalid"}, nil)
						changed <- code
					}
				}()
				var changeStatus int
				select {
				case changeStatus = <-changed:
					t.Error("connection change completed before the restore finished writing to the previous server")
				case <-time.After(150 * time.Millisecond):
				}
				release.Do(func() { close(resume) })
				select {
				case code := <-restored:
					if code != 200 {
						t.Errorf("restore: %d", code)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("restore did not finish")
				}
				if changeStatus == 0 {
					select {
					case changeStatus = <-changed:
					case <-time.After(3 * time.Second):
						t.Fatal("connection change did not finish after the restore")
					}
				}
				if changeStatus != 200 {
					t.Errorf("connection change: %d", changeStatus)
				}
				row, err := rst.Basket("A", 3)
				if err != nil || row == nil || row.Description != "Prize" || row.WinningTicket != 22 {
					t.Errorf("incomplete restore: %+v, %v", row, err)
				}
			})
		}
	}
}
