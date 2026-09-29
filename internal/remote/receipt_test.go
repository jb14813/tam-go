package remote

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSaveReceiptEnvelopePreservesLegacyResponseData(t *testing.T) {
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
