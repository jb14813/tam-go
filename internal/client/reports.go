package client

import (
	"net/http"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
)

// eventReport never substitutes a workstation's partial entries for the
// shared event. Local reporting is available when deliberately standalone.
func eventReport[T any](h *handler, w http.ResponseWriter, rc *remote.Client, path string, local func() ([]T, error)) {
	w.Header().Set("Cache-Control", "no-store")
	if rc == nil {
		rows, err := local()
		if err != nil {
			writeStoreError(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, rows)
		return
	}
	if !h.inStep() {
		httpx.WriteError(w, http.StatusServiceUnavailable, "Event reports are unavailable until this client reconnects and finishes synchronizing. Reconnect and refresh the report.")
		return
	}
	_, failed, err := h.st.OutboxCounts()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if failed > 0 {
		httpx.WriteError(w, http.StatusServiceUnavailable, "Event reports are unavailable while this client has refused saves. Review them in Settings, then refresh the report.")
		return
	}
	res, err := rc.WithTimeout(readTimeout).Get(path)
	h.observe(err, res)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "The shared server could not be reached. Event reports are unavailable; reconnect and refresh the report.")
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	rows := []T{}
	if err := res.JSON(&rows); err != nil || rows == nil {
		httpx.WriteError(w, http.StatusBadGateway, "The shared server returned an invalid report. Refresh the report or check the server.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rows)
}
