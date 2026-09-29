package remote

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSaveReceiptEnvelopePreservesResponseData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TAM-Receipts") != "1" {
			t.Error("receipt not requested")
		}
		w.Header().Set("X-TAM-Receipts", "1")
		w.Write([]byte(`{"data":[{"prefix":"A","t_id":42}],"receipt":{"revisions":[]}}`))
	}))
	defer server.Close()
	response, err := New(server.URL, "key", false).Do(http.MethodPost, "/api/tickets", map[string]string{"X-TAM-Receipts": "1"}, []any{})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK() || response.Receipt == nil || string(response.Body) != `[{"prefix":"A","t_id":42}]` {
		t.Fatalf("receipt envelope not decoded: %+v", response)
	}
}

func TestNumberedReceiptRequiredWithoutResponseHeader(t *testing.T) {
	for _, body := range []string{
		`<html>Wrong proxy</html>`, `[]`, `{"data":[],"receipt":{}}`,
		`{"data":[],"receipt":{"revisions":[]},"client":"other","save":1}`,
		`{"data":[],"receipt":{"revisions":[]},"client":"desk","save":2}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			res, err := New(server.URL, "key", false).Do("POST", "/api/tickets", map[string]string{"X-TAM-Client-Name": "desk", "X-TAM-Save": "1"}, []any{})
			if err == nil || res != nil {
				t.Fatalf("invalid numbered acknowledgement accepted: %+v %v", res, err)
			}
		})
	}
}

func TestMatchingNumberedReceiptAcceptedWithoutResponseHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[],"receipt":{"revisions":[]},"client":"desk","save":1}`))
	}))
	defer server.Close()
	res, err := New(server.URL, "key", false).Do("POST", "/api/tickets", map[string]string{"X-TAM-Client-Name": "desk", "X-TAM-Save": "1"}, []any{})
	if err != nil || res.Receipt == nil {
		t.Fatalf("valid envelope rejected: %+v %v", res, err)
	}
}

func TestMatchingEnvelopeWithMissingRecordReceiptCannotAcknowledgeWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[],"receipt":{"revisions":[]},"client":"desk","save":1}`))
	}))
	defer server.Close()
	res, err := New(server.URL, "key", false).Do("POST", "/api/tickets", map[string]string{"X-TAM-Client-Name": "desk", "X-TAM-Save": "1"}, []map[string]any{{"prefix": "A", "t_id": 1, "first_name": "Kept"}})
	if err == nil || res != nil {
		t.Fatalf("empty receipt acknowledged nonempty request: %+v %v", res, err)
	}
}

func TestMalformedSaveReceiptCannotAcknowledgeWrite(t *testing.T) {
	for _, body := range []string{`{`, `{"data":[]}`, `{"receipt":{"revisions":[]}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-TAM-Receipts", "1")
				w.Write([]byte(body))
			}))
			defer server.Close()
			if response, err := New(server.URL, "key", false).Get("/api"); err == nil || response != nil {
				t.Fatalf("malformed acknowledgement was accepted: response=%+v err=%v", response, err)
			}
		})
	}
}
