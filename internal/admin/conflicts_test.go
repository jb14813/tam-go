package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

func seedReviewConflict(t *testing.T, site *site) (store.AuthKey, string, store.RecordConflict) {
	t.Helper()
	key, err := site.st.CreateKey("event desks")
	if err != nil {
		t.Fatal(err)
	}
	if err = site.st.BeginRecovery(); err != nil {
		t.Fatal(err)
	}
	token, err := site.st.RecoveryToken(key.AuthKey, "first")
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Original buyer", "Corrected buyer"} {
		snapshot := store.RecoverySnapshot{BackupFile: store.NewBackupFile()}
		snapshot.Tickets = []store.Ticket{{Prefix: "A", TID: 42, FirstName: name}}
		if err = site.st.RecoverSnapshot(key.AuthKey, []string{"first", "second"}[i], token, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	conflicts, err := site.st.Conflicts()
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts=%+v err=%v", conflicts, err)
	}
	return key, token, conflicts[0]
}

func TestDataReviewRequiresLoginAndExplicitConfirmedChoice(t *testing.T) {
	site := newSite(t, "secret")
	_, _, conflict := seedReviewConflict(t, site)
	res, _ := site.get("/admin/conflicts")
	wantRedirect(t, res, "/admin/")
	site.login("secret")
	body, csrf := site.page("/admin/conflicts")
	if !strings.Contains(body, "Original buyer") || !strings.Contains(body, "Corrected buyer") {
		t.Fatalf("alternatives not visible: %s", body)
	}
	form := url.Values{"kind": {"ticket"}, "prefix": {"A"}, "id": {"42"}, "choice": {conflict.Candidates[0].Revision.Hash}, "expected": {store.ConflictToken(conflict)}}
	res, _ = site.post("/admin/conflicts/resolve", form)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", res.StatusCode)
	}
	form.Set("csrf", csrf)
	res, _ = site.post("/admin/conflicts/resolve", form)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed choice: %d", res.StatusCode)
	}
	form.Set("confirm", "yes")
	res, _ = site.post("/admin/conflicts/resolve", form)
	wantRedirect(t, res, "/admin/conflicts")
	remaining, err := site.st.Conflicts()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("review not resolved: %+v %v", remaining, err)
	}
}

func TestDataReviewRejectsChoiceBasedOnOutdatedAlternatives(t *testing.T) {
	site := newSite(t, "secret")
	key, token, conflict := seedReviewConflict(t, site)
	site.login("secret")
	_, csrf := site.page("/admin/conflicts")
	snapshot := store.RecoverySnapshot{BackupFile: store.NewBackupFile()}
	snapshot.Tickets = []store.Ticket{{Prefix: "A", TID: 42, FirstName: "Late third copy"}}
	if err := site.st.RecoverSnapshot(key.AuthKey, "third", token, snapshot); err != nil {
		t.Fatal(err)
	}
	res, body := site.post("/admin/conflicts/resolve", url.Values{"csrf": {csrf}, "confirm": {"yes"}, "kind": {"ticket"}, "prefix": {"A"}, "id": {"42"}, "choice": {conflict.Candidates[0].Revision.Hash}, "expected": {store.ConflictToken(conflict)}})
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Late third copy") {
		t.Fatalf("stale choice should show new alternatives: %d %s", res.StatusCode, body)
	}
	remaining, err := site.st.Conflicts()
	if err != nil || len(remaining) != 1 || len(remaining[0].Candidates) != 3 {
		t.Fatalf("stale choice mutated conflict: %+v %v", remaining, err)
	}
}
