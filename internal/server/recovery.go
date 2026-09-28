package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

// recoveryClient is the stable save-order identity, not the auth key or
// host name. Older clients can omit it and use the legacy empty identity.
func recoveryClient(r *http.Request) (string, error) {
	client := r.Header.Get("X-TAM-Client-Name")
	if len(client) > 64 || strings.IndexFunc(client, unicode.IsControl) >= 0 {
		return "", errors.New("X-TAM-Client-Name must be at most 64 characters without control characters")
	}
	return client, nil
}

func (h *handler) recoverEvent(w http.ResponseWriter, r *http.Request) {
	client, err := recoveryClient(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var request struct {
		Token string                  `json:"token"`
		Data  *store.RecoverySnapshot `json:"data"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	if request.Data == nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "data is required")
		return
	}
	if err := store.ValidateRecoverySnapshot(request.Data); err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.st.RecoverSnapshot(keyOf(r), client, request.Token, *request.Data); err != nil {
		if errors.Is(err, store.ErrRecoveryToken) {
			httpx.WriteError(w, http.StatusConflict, err.Error())
		} else {
			httpx.WriteInternal(w, err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"recovered": true})
}
