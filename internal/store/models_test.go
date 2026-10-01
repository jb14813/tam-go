package store

import (
	"encoding/json"
	"testing"
)

func TestIntAcceptsTheOriginalSpellings(t *testing.T) {
	for in, want := range map[string]int{`4`: 4, `4.0`: 4, `"4"`: 4, `" 07 "`: 7, `"+2"`: 2, `null`: 0, `1099511627776`: 1099511627776} {
		var n Int
		if err := json.Unmarshal([]byte(in), &n); err != nil || int(n) != want {
			t.Errorf("Int(%s) = %d, %v; want %d", in, n, err, want)
		}
	}
	for _, bad := range []string{`1.5`, `"abc"`, `""`, `true`, `[1]`} {
		var n Int
		if err := json.Unmarshal([]byte(bad), &n); err == nil {
			t.Errorf("Int(%s) should fail", bad)
		}
	}
}

func TestModelsDecodeLikeTheOriginal(t *testing.T) {
	var ts []Ticket
	// The original client sends the id of a placeholder ticket as a string.
	if err := json.Unmarshal([]byte(`[{"prefix":"A","t_id":"4","first_name":"S","pref":"CALL","changed":true}]`), &ts); err != nil {
		t.Fatal(err)
	}
	if len(ts) != 1 || ts[0].TID != 4 || ts[0].FirstName != "S" {
		t.Fatalf("ticket = %+v", ts)
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","first_name":"no id"}]`), &ts); err == nil {
		t.Fatal("a ticket without t_id must be rejected instead of becoming ticket 0")
	}
	if err := json.Unmarshal([]byte(`[null]`), &ts); err == nil {
		t.Fatal("a null ticket must be rejected")
	}

	var bs []Basket
	if err := json.Unmarshal([]byte(`[{"prefix":"A","b_id":2.0,"winning_ticket":"5"}]`), &bs); err != nil || bs[0].BID != 2 || bs[0].WinningTicket != 5 {
		t.Fatalf("basket = %+v, %v", bs, err)
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","description":"no id"}]`), &bs); err == nil {
		t.Fatal("a basket without b_id must be rejected")
	}

	var ps []Prefix
	if err := json.Unmarshal([]byte(`[{"prefix":"A","color":"red","weight":"3"}]`), &ps); err != nil || ps[0].Weight != 3 {
		t.Fatalf("prefix = %+v, %v", ps, err)
	}
	if err := json.Unmarshal([]byte(`[{"prefix":"A","color":"red","weight":"heavy"}]`), &ps); err == nil {
		t.Fatal("a non-numeric weight must be rejected")
	}

	out, _ := json.Marshal(Ticket{Prefix: "A", TID: 4})
	if string(out) != `{"prefix":"A","t_id":4,"first_name":"","last_name":"","phone_number":"","pref":""}` {
		t.Fatalf("ticket marshals as numbers: %s", out)
	}
}
