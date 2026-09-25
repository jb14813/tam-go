package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameSite(t *testing.T) {
	for header, want := range map[string]bool{"": true, "same-origin": true, "none": true, "cross-site": false, "same-site": false} {
		r := httptest.NewRequest(http.MethodPost, "/api/x", nil)
		if header != "" {
			r.Header.Set("Sec-Fetch-Site", header)
		}
		if got := SameSite(r); got != want {
			t.Errorf("SameSite(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	req := func(ct, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		return r
	}
	var p payload
	if err := DecodeJSON(req("application/json", `{"name":"a"}`), &p); err != nil || p.Name != "a" {
		t.Fatalf("plain json: %v %+v", err, p)
	}
	if err := DecodeJSON(req("application/json; charset=utf-8", `{"name":"b"}`), &p); err != nil || p.Name != "b" {
		t.Fatalf("json with charset: %v", err)
	}
	if err := DecodeJSON(req("text/plain", `{"name":"c"}`), &p); err == nil {
		t.Fatal("text/plain must be rejected")
	}
	if err := DecodeJSON(req("", `{"name":"c"}`), &p); err == nil {
		t.Fatal("missing content type must be rejected")
	}
	if err := DecodeJSON(req("application/json", `{"name":"d"} {"name":"e"}`), &p); err == nil {
		t.Fatal("trailing documents must be rejected")
	}
	if err := DecodeJSON(req("application/json", `{bad`), &p); err == nil {
		t.Fatal("malformed json must be rejected")
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusBadRequest, "nope")
	if w.Code != 400 || w.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(w.Body.String()) != `{"detail":"nope"}` {
		t.Fatalf("WriteError = %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func TestIntParam(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x/12", nil)
	r.SetPathValue("id", "12")
	if n, err := IntParam(r, "id"); err != nil || n != 12 {
		t.Fatalf("IntParam = %d, %v", n, err)
	}
	r.SetPathValue("id", "twelve")
	if _, err := IntParam(r, "id"); err == nil {
		t.Fatal("non-integer must fail")
	}
}
