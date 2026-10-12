// Command digest_oracle prints vectors from the retained pinned Go protocol.
// Run from the checkout root: go run rust/tests/compatibility/digest_oracle.go.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"

	"ticket-auction-manager/tam-go/internal/store"
)

func printVector(label, method, path, query string, content any) {
	body, err := json.Marshal(content)
	if err != nil {
		panic(err)
	}
	values, _ := url.ParseQuery(query)
	sum := sha256.Sum256([]byte(method + " " + path + "?" + values.Encode() + "\n" + string(body)))
	fmt.Printf("%s-body: %s\n%s-query: %s\n%s-digest: %s\n", label, body, label, values.Encode(), label, hex.EncodeToString(sum[:]))
}

func main() {
	var tickets []store.TicketSave
	if err := json.Unmarshal([]byte(`[{"t_id":0,"prefix":" A ","pref":" CALL ","base":{"pref":" TEXT "}}]`), &tickets); err != nil {
		panic(err)
	}
	if err := store.ValidateTicketSaves(tickets); err != nil {
		panic(err)
	}
	printVector("delete", "DELETE", "/prefixes", "z=last&p=A+%26+B&p=C", nil)
	printVector("tickets", "POST", "/api/tickets", "b=2&a=first&a=second", tickets)
	var baskets []store.BasketSave
	if err := json.Unmarshal([]byte(`[{"prefix":" A &é ","b_id":3,"description":" <tea>&\u2028\u2029😀 ","donors":" Local ","winning_ticket":4,"rev":7,"win_rev":8,"base":{"description":" old ","donors":" Before "}},{"prefix":" A &é ","b_id":3}]`), &baskets); err != nil {
		panic(err)
	}
	if err := store.ValidateBasketSaves(baskets); err != nil {
		panic(err)
	}
	printVector("baskets", "POST", "/api/baskets", "z=last&a=two+words&a=one&%C3%A9=%2f%3f&b=~!*()&bad=%zz&semi=x;y&empty", baskets)
}
