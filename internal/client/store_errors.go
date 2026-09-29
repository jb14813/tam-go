package client

import (
	"errors"
	"net/http"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

func writeStoreError(w http.ResponseWriter, err error) {
	var conflict *store.ConflictError
	if errors.As(err, &conflict) {
		httpx.WriteError(w, http.StatusConflict, "Conflicting saved entries need review on the server's Data review page before these results can be used.")
		return
	}
	httpx.WriteInternal(w, err)
}
