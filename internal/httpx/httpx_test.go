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
	req := func(ct, body string) (*httptest.ResponseRecorder, *http.Request) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		return httptest.NewRecorder(), r
	}
	var p payload
	if w, r := req("application/json", `{"name":"a"}`); DecodeJSON(w, r, &p) != nil || p.Name != "a" {
		t.Fatalf("plain json: %+v", p)
	}
	if w, r := req("application/json; charset=utf-8", `{"name":"b"}`); DecodeJSON(w, r, &p) != nil || p.Name != "b" {
		t.Fatal("json with charset")
	}
	if w, r := req("text/plain", `{"name":"c"}`); DecodeJSON(w, r, &p) == nil {
		t.Fatal("text/plain must be rejected")
	}
	if w, r := req("", `{"name":"c"}`); DecodeJSON(w, r, &p) == nil {
		t.Fatal("missing content type must be rejected")
	}
	if w, r := req("application/json", `{"name":"d"} {"name":"e"}`); DecodeJSON(w, r, &p) == nil {
		t.Fatal("trailing documents must be rejected")
	}
	if w, r := req("application/json", `{bad`); DecodeJSON(w, r, &p) == nil {
		t.Fatal("malformed json must be rejected")
	}

	old := MaxBody
	MaxBody = 32
	defer func() { MaxBody = old }()
	w, r := req("application/json", `{"name":"`+strings.Repeat("x", 100)+`"}`)
	err := DecodeJSON(w, r, &p)
	if err == nil {
		t.Fatal("oversized body must be rejected")
	}
	rec := httptest.NewRecorder()
	WriteDecodeError(rec, err)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rec.Code)
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

func TestJSONErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/things", func(w http.ResponseWriter, r *http.Request) { WriteJSON(w, 200, []int{}) })
	mux.HandleFunc("GET /web/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) })
	h := JSONErrors(mux, "/api")

	get := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	if w := get("GET", "/api/things"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("matched route = %d %q", w.Code, w.Body.String())
	}
	if w := get("GET", "/api/nope"); w.Code != 404 || strings.TrimSpace(w.Body.String()) != `{"detail":"Not Found"}` {
		t.Fatalf("unknown api path = %d %q", w.Code, w.Body.String())
	}
	if w := get("DELETE", "/api/things"); w.Code != 405 || strings.TrimSpace(w.Body.String()) != `{"detail":"Method Not Allowed"}` || w.Header().Get("Allow") == "" {
		t.Fatalf("wrong method = %d %q allow=%q", w.Code, w.Body.String(), w.Header().Get("Allow"))
	}
	if w := get("GET", "/nope"); w.Code != 404 || strings.Contains(w.Body.String(), "detail") {
		t.Fatalf("outside the prefix stays plain: %d %q", w.Code, w.Body.String())
	}
	if w := get("GET", "/api//things"); w.Code/100 != 3 || w.Header().Get("Location") == "" {
		t.Fatalf("path cleaning redirects pass through: %d %q", w.Code, w.Header().Get("Location"))
	}
}
