// Package httpx holds the small helpers every handler uses.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
)

// MaxBody is the largest request body accepted (backup files can be big).
const MaxBody = 64 << 20

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

// WriteError writes the {"detail": msg} error document the original uses.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"detail": msg})
}

// DecodeJSON decodes exactly one JSON document from the request body into v.
// The request must declare application/json.
func DecodeJSON(r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxBody))
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain a single JSON document")
	}
	return nil
}

// SameSite reports whether a request may perform a write. Browsers send
// Sec-Fetch-Site on every request; a cross-site value means another web page
// is trying to use this API. Non-browser clients send no header.
func SameSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// IntParam reads an integer path parameter.
func IntParam(r *http.Request, name string) (int, error) {
	n, err := strconv.Atoi(r.PathValue(name))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return n, nil
}
