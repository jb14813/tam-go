package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStatusSeparatesClientsSharingAKey(t *testing.T) {
	s := newSite(t, "secret")
	s.login("secret")
	key, err := s.st.CreateKey("Shared")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.h.ss.now = func() time.Time { return now }
	s.reg.Heartbeat(key.AuthKey, "desk-a", "tam-client", 9, 2, true)
	now = now.Add(time.Minute)
	s.reg.Heartbeat(key.AuthKey, "desk-b", "tam-client", 0, 0, false)
	res, text := s.getJSON("/admin/status")
	var status statusJSON
	if res.StatusCode != 200 || json.Unmarshal([]byte(text), &status) != nil || len(status.Clients) != 2 {
		t.Fatalf("status did not separate workstations: %d %s", res.StatusCode, text)
	}
	a, b := status.Clients[0], status.Clients[1]
	if a.Name != "desk-a" || !strings.HasPrefix(a.State, "away") || a.Queued == nil || *a.Queued != 9 || a.Failed == nil || *a.Failed != 2 || a.Recovering == nil || !*a.Recovering {
		t.Fatalf("outstanding workstation work was hidden: %+v", a)
	}
	if b.Name != "desk-b" || b.State != "connected" || b.Queued == nil || *b.Queued != 0 || b.Recovering == nil || *b.Recovering {
		t.Fatalf("healthy workstation state changed: %+v", b)
	}
	body, _ := s.page("/admin/status")
	if !strings.Contains(body, "<th>Refused</th>") || !strings.Contains(body, "<th>Recovering</th>") || !strings.Contains(body, "desk-a") || !strings.Contains(body, "desk-b") {
		t.Fatalf("HTML status omitted client work: %s", body)
	}
}

func TestAdminRejectsOriginalBackupWithoutChangingRecords(t *testing.T) {
	s := newSite(t, "secret")
	seed(t, s.st)
	s.login("secret")
	_, token := s.page("/admin/backup")
	for _, file := range []string{
		`{}`,
		`{"prefixes":[],"tickets":[],"baskets":[]}`,
		`{"prefixes":[],"tickets":[{"prefix":"A","t_id":1,"first_name":"Overwrite"}],"baskets":[]}`,
	} {
		res, body := s.upload(token, []byte(file), true)
		if res.StatusCode != 400 {
			t.Fatalf("non-native backup accepted: %d %s", res.StatusCode, body)
		}
		row, err := s.st.Ticket("A", 1)
		if err != nil || row == nil || row.FirstName != "Amy" {
			t.Fatalf("rejected backup changed records: %+v %v", row, err)
		}
	}
}
