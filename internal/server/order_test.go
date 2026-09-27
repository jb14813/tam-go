package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ticket-auction-manager/tam-go/internal/store"
)

// orderedSave sends a keyed save of ticket A 1 with the given phone number,
// numbered as tam-client numbers its saves when laptop is not empty.
func (a *api) orderedSave(laptop, save, phone string) (int, http.Header) {
	a.t.Helper()
	body, _ := json.Marshal([]store.Ticket{{Prefix: "A", TID: 1, PhoneNumber: phone}})
	req, _ := http.NewRequest("POST", a.url+"/api/tickets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("TAM-KEY", a.key)
	if laptop != "" {
		req.Header.Set("X-TAM-Laptop", laptop)
		req.Header.Set("X-TAM-Save", save)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header
}

func (a *api) phone() string {
	a.t.Helper()
	tk, err := a.st.Ticket("A", 1)
	if err != nil || tk == nil {
		a.t.Fatalf("ticket A 1: %+v, %v", tk, err)
	}
	return tk.PhoneNumber
}

// TestSavesFromALaptopApplyInOrder: a save the network delivers late (the
// laptop gave up on it, queued it and sent it again) must not undo what
// came after it. The server applies a numbered save only when it is newer
// than the last one applied from that laptop and answers the others with
// X-TAM-Stale; saves without numbers, as the original client sends them,
// apply as they come.
func TestSavesFromALaptopApplyInOrder(t *testing.T) {
	a := newAPI(t)
	if code, h := a.orderedSave("L1", "7", "seventh"); code != 200 || h.Get("X-TAM-Stale") != "" {
		t.Fatalf("a new save = %d, stale %q", code, h.Get("X-TAM-Stale"))
	}
	for _, again := range []string{"6", "7"} {
		code, h := a.orderedSave("L1", again, "late copy "+again)
		if code != 200 || h.Get("X-TAM-Stale") != "1" {
			t.Fatalf("save %s after save 7 = %d, stale %q; want 200 and marked stale", again, code, h.Get("X-TAM-Stale"))
		}
		if a.phone() != "seventh" {
			t.Fatalf("save %s after save 7 changed the ticket to %q", again, a.phone())
		}
	}
	if code, _ := a.orderedSave("L1", "8", "eighth"); code != 200 || a.phone() != "eighth" {
		t.Fatalf("a newer save = %d, the ticket reads %q", code, a.phone())
	}
	if code, _ := a.orderedSave("", "", "unnumbered"); code != 200 || a.phone() != "unnumbered" {
		t.Fatalf("an unnumbered save = %d, the ticket reads %q", code, a.phone())
	}
	for _, bad := range []string{"0", "-1", "x"} {
		if code, _ := a.orderedSave("L1", bad, "bad"); code != 400 {
			t.Fatalf("save number %q = %d, want 400", bad, code)
		}
	}
	if code, _ := a.orderedSave(strings.Repeat("L", 65), "9", "long name"); code != 400 {
		t.Fatalf("a laptop name of 65 characters = %d, want 400", code)
	}
}

// TestStaleDeleteAnswersAsDone: a numbered prefix delete that is stale is
// skipped and answered 200, not 404: the laptop replaying it must not file
// it as refused.
func TestStaleDeleteAnswersAsDone(t *testing.T) {
	a := newAPI(t)
	a.st.UpsertPrefixes([]store.Prefix{{Prefix: "Z", Color: "red", Weight: 1}})
	del := func(save string) (int, http.Header) {
		req, _ := http.NewRequest("DELETE", a.url+"/api/prefixes?p=Z", nil)
		req.Header.Set("TAM-KEY", a.key)
		req.Header.Set("X-TAM-Laptop", "L1")
		req.Header.Set("X-TAM-Save", save)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode, res.Header
	}
	if code, _ := del("3"); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if code, h := del("3"); code != 200 || h.Get("X-TAM-Stale") != "1" {
		t.Fatalf("the same delete again = %d, stale %q; want 200 and marked stale", code, h.Get("X-TAM-Stale"))
	}
}
