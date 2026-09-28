package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func TestAdminCreatedKeyRecovery(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty event"
		if populated {
			name = "existing event"
		}
		t.Run(name, func(t *testing.T) {
			s := newSite(t, "secret")
			if populated {
				if err := s.st.UpsertPrefixes([]store.Prefix{{Prefix: "A", Color: "blue"}}); err != nil {
					t.Fatal(err)
				}
			}
			// Keep this API handler running before the admin creates the key:
			// starting a new handler afterwards would mask missed enrollment.
			api := server.NewHandler(s.st, s.pw)
			s.login("secret")
			_, csrf := s.page("/admin/keys")
			res, body := s.post("/admin/keys", url.Values{"csrf": {csrf}, "description": {"desk"}})
			if res.StatusCode != http.StatusOK {
				t.Fatalf("create key: %d %s", res.StatusCode, body)
			}
			keys, err := s.st.ListKeys()
			if err != nil || len(keys) != 1 {
				t.Fatalf("created keys = %+v, %v", keys, err)
			}
			req := httptest.NewRequest("GET", "/api", nil)
			req.Header.Set("TAM-KEY", keys[0].AuthKey)
			w := httptest.NewRecorder()
			api.ServeHTTP(w, req)
			var reply struct {
				Authenticated bool   `json:"authenticated"`
				Token         string `json:"recovery_token"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || !reply.Authenticated {
				t.Fatalf("heartbeat: %d %s", w.Code, w.Body.String())
			}
			if (reply.Token != "") == populated {
				t.Fatalf("recovery token = %q for populated=%v", reply.Token, populated)
			}
		})
	}
}
