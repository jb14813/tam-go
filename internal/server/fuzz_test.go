package server

import (
	"encoding/json"
	"fmt"
	"testing"
)

// serverPostRoutes are the POST routes of tam-server.
var serverPostRoutes = []string{
	"/api/auth", "/api/prefixes", "/api/tickets", "/api/baskets", "/api/drawing", "/api/search/tickets", "/api/backuprestore",
}

// FuzzServerPostRoutes: no body and no Content-Type makes a POST route of
// tam-server answer 500 or panic. The requests carry a valid key and the
// password, so every body reaches the handler behind them. Every refusal
// carries the original's {"detail": ...} body and every success is JSON.
// go test runs the seeds; each input gets a server of its own.
func FuzzServerPostRoutes(f *testing.F) {
	type seed struct {
		route uint8
		ct    string
		body  string
	}
	const js = "application/json"
	for _, s := range []seed{
		{0, js, `{"description":"tablet"}`},
		{0, js, `{"description":5}`},
		{0, js, `null`},
		{1, js, `[{"prefix":"A","color":"red","weight":"3"},{"prefix":" A ","color":"blue","weight":4.0}]`},
		{1, js, `[{"prefix":".","color":"red","weight":1}]`},
		{1, js, `[{"prefix":"A","color":"chartreuse","weight":1}]`},
		{2, js, `[{"prefix":"A","t_id":"12","first_name":"Ann","pref":"CALL","changed":true}]`},
		{2, js, `[{"prefix":"A\u0000B","t_id":9223372036854775807,"first_name":"\ud800"}]`},
		{2, js, `[{"prefix":"A","t_id":9223372036854775808}]`},
		{2, js, `[{"prefix":"A","t_id":1e400}]`},
		{2, js, `[{"prefix":"A"}]`},
		{2, js, `[null]`},
		{2, js, `[[]]`},
		{2, js, `{}`},
		{3, js, `[{"prefix":"A","b_id":1,"description":"Wine","winning_ticket":"7"}]`},
		{3, js, `[{"prefix":"A","b_id":1,"winning_ticket":-1}]`},
		{4, js, `[{"prefix":"A","b_id":1,"winning_ticket":2,"last_name":"","changed":true}]`},
		{5, js, `[{"prefix":"A","t_id":2,"first_name":"Bo"}]`},
		{6, js, `{"prefixes":[{"prefix":"OLD","color":"gray","weight":1}],"baskets":[{"prefix":"OLD","b_id":1,"winning_ticket":1}],"tickets":[{"prefix":"OLD","t_id":1}]}`},
		{6, js, `{"prefixes":null,"baskets":null,"tickets":null}`},
		{6, js, `{"prefixes":[{"prefix":"","color":"red","weight":1}]}`},
		{6, js, `[]`},
		{2, "application/json; charset=utf-8", `[]`},
		{2, "APPLICATION/JSON", `null`},
		{2, "text/plain", `[]`},
		{2, "", `[]`},
		{2, "application/json;;", `[]`},
		{2, "multipart/form-data; boundary=x", "--x\r\n"},
		{2, js, `[] []`},
		{2, js, "\xef\xbb\xbf[]"},
		{2, js, "[\"\xff\"]"},
		{2, js, `{bad`},
		{2, js, ``},
	} {
		f.Add(s.route, s.ct, []byte(s.body))
	}
	f.Fuzz(func(t *testing.T, route uint8, contentType string, body []byte) {
		a := newAPI(t)
		path := serverPostRoutes[int(route)%len(serverPostRoutes)]
		headers := map[string]string{"Content-Type": headerValue(contentType), "TAM-KEY": a.key, "TAM-PW": "secret"}
		code, resp := a.do("POST", path, string(body), headers)
		what := fmt.Sprintf("POST %s (Content-Type %q) %q", path, headers["Content-Type"], body)
		switch {
		case code == 500:
			t.Fatalf("%s = 500 %s", what, resp)
		case code >= 400:
			var doc map[string]json.RawMessage
			if json.Unmarshal(resp, &doc) != nil || doc["detail"] == nil {
				t.Fatalf("%s = %d %s, want a {\"detail\": ...} body", what, code, resp)
			}
		case code/100 == 2:
			if !json.Valid(resp) {
				t.Fatalf("%s = %d %s, want JSON", what, code, resp)
			}
		}
	})
}

// headerValue drops the bytes net/http refuses to send in a header value.
func headerValue(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f || c == '\t' {
			b = append(b, c)
		}
	}
	return string(b)
}
