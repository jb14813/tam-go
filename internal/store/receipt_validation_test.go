package store

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSaveReceiptRequiresEveryAcceptedValueAndOperation(t *testing.T) {
	s := newOrderStore(t)
	rows := []Ticket{{Prefix: "A", TID: 1, FirstName: "First"}, {Prefix: "A", TID: 1, FirstName: "Last"}, {Prefix: "A", TID: 2, FirstName: "Other"}}
	body, err := json.Marshal(rows)
	must(t, err)
	order := Order{Client: "desk", Save: 3}
	_, _, err = s.InOrder(order.Client, order.Save, "ticket-save", func(st *Store) error { return st.UpsertTickets(rows) })
	must(t, err)
	receipt, err := s.Receipt(order)
	must(t, err)
	request := Outbox{Method: http.MethodPost, Path: "/api/tickets", Body: body, Order: order}
	must(t, ValidateSaveReceipt(request, &receipt))

	encoded, err := json.Marshal(receipt)
	must(t, err)
	for name, change := range map[string]func(*SaveReceipt){
		"missing list": func(r *SaveReceipt) { r.Revisions = nil },
		"empty list":   func(r *SaveReceipt) { r.Revisions = []RecordRevision{} },
		"missing row":  func(r *SaveReceipt) { r.Revisions = r.Revisions[:1] },
		"wrong operation": func(r *SaveReceipt) {
			r.Revisions[0].Operations = map[string]string{"client:other/3": r.Revisions[0].Hash}
			r.Revisions[0].Vector = map[string]int64{"client:other": 3}
			r.Revisions[0].Heads = []string{"client:other/3"}
		},
		"wrong value": func(r *SaveReceipt) {
			hash := valueHash([]byte(`"different"`))
			r.Revisions[0].Hash = hash
			r.Revisions[0].Operations["client:desk/3"] = hash
		},
		"invalid revision": func(r *SaveReceipt) { r.Revisions[0].Hash = "invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			var invalid SaveReceipt
			must(t, json.Unmarshal(encoded, &invalid))
			change(&invalid)
			if err := ValidateSaveReceipt(request, &invalid); err == nil {
				t.Fatal("unmatched receipt accepted")
			}
		})
	}
	if err := ValidateSaveReceipt(request, nil); err == nil {
		t.Fatal("absent receipt accepted")
	}
}

func TestSaveReceiptAllowsIgnoredInsertWinnerAndIdempotentDelete(t *testing.T) {
	s := newOrderStore(t)
	must(t, s.UpsertBaskets([]Basket{{Prefix: "A", BID: 1, Description: "Original", WinningTicket: 42}}))
	rows := []Basket{{Prefix: "A", BID: 1, Description: "Changed", WinningTicket: 99}}
	body, err := json.Marshal(rows)
	must(t, err)
	order := Order{Client: "desk", Save: 1}
	_, _, err = s.InOrder(order.Client, order.Save, "metadata", func(st *Store) error { return st.UpsertBaskets(rows) })
	must(t, err)
	receipt, err := s.Receipt(order)
	must(t, err)
	must(t, ValidateSaveReceipt(Outbox{Method: http.MethodPost, Path: "/api/baskets", Body: body, Order: order}, &receipt))
	order.Save++
	_, _, err = s.InOrder(order.Client, order.Save, "delete", func(st *Store) error { _, e := st.DeletePrefix("Absent"); return e })
	must(t, err)
	receipt, err = s.Receipt(order)
	must(t, err)
	must(t, ValidateSaveReceipt(Outbox{Method: http.MethodDelete, Path: "/api/prefixes?p=Absent", Order: order}, &receipt))
	must(t, ValidateSaveReceipt(Outbox{Method: http.MethodPost, Path: "/api/tickets", Body: []byte(`[]`), Order: order}, &SaveReceipt{Revisions: []RecordRevision{}}))
}
