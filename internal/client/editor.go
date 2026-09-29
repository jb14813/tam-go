package client

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"ticket-auction-manager/tam-go/internal/store"
)

type editorStamp struct {
	session  string
	sequence int64
}

func readEditorStamp(r *http.Request) (editorStamp, error) {
	sessions, sequences := r.Header.Values("X-TAM-Edit-Session"), r.Header.Values("X-TAM-Edit-Sequence")
	if len(sessions) == 0 && len(sequences) == 0 {
		return editorStamp{}, nil
	}
	invalid := errors.New("X-TAM-Edit-Session must be 32 hexadecimal characters and X-TAM-Edit-Sequence must be a positive integer; supply both headers together")
	if len(sessions) != 1 || len(sequences) != 1 || len(sessions[0]) != 32 {
		return editorStamp{}, invalid
	}
	if _, err := hex.DecodeString(sessions[0]); err != nil {
		return editorStamp{}, invalid
	}
	for _, digit := range sequences[0] {
		if digit < '0' || digit > '9' {
			return editorStamp{}, invalid
		}
	}
	sequence, err := strconv.ParseInt(sequences[0], 10, 64)
	if err != nil || sequence <= 0 {
		return editorStamp{}, invalid
	}
	return editorStamp{strings.ToLower(sessions[0]), sequence}, nil
}

func filterEditor[T any](st *store.Store, stamp editorStamp, path string, items []T) ([]T, *store.EditorReservation, error) {
	if stamp.session == "" {
		return items, nil, nil
	}
	records := make([]store.EditorRecord, len(items))
	for i, item := range items {
		switch row := any(item).(type) {
		case store.Ticket:
			records[i] = store.EditorRecord{Kind: "ticket", Prefix: row.Prefix, ID: row.TID}
		case store.Prefix:
			records[i] = store.EditorRecord{Kind: "prefix", Prefix: row.Prefix}
		case store.Basket:
			kind := "metadata"
			if path == "/api/drawing" {
				kind = "drawing"
			}
			records[i] = store.EditorRecord{Kind: kind, Prefix: row.Prefix, ID: row.BID}
		default:
			return nil, nil, fmt.Errorf("unsupported editor record on %s", path)
		}
	}
	keep, reservation, err := st.PrepareEditor(stamp.session, stamp.sequence, records)
	if err != nil {
		return nil, nil, err
	}
	filtered := make([]T, 0, len(items))
	for i, item := range items {
		if keep[i] {
			filtered = append(filtered, item)
		}
	}
	return filtered, reservation, nil
}
