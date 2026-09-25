// Package server is the HTTP API of tam-server, the shared database that
// several tam-client installations talk to in remote mode.
package server

import (
	"crypto/subtle"
	"net/http"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

type handler struct {
	st       *store.Store
	password string
}

// NewHandler returns the server API. Data routes require a TAM-KEY header
// that matches a stored access key; key management requires a TAM-PW header
// equal to password. Unknown paths and wrong methods under /api answer
// {"detail": ...} like the original.
func NewHandler(st *store.Store, password string) http.Handler {
	h := &handler{st: st, password: password}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api", h.root)
	mux.HandleFunc("GET /api/{$}", h.root)

	mux.Handle("GET /api/auth", h.requirePassword(h.listKeys))
	mux.Handle("POST /api/auth", h.requirePassword(h.createKey))
	mux.Handle("DELETE /api/auth", h.requirePassword(h.deleteKey))

	key := h.requireKey
	mux.Handle("GET /api/prefixes", key(h.listPrefixes))
	mux.Handle("POST /api/prefixes", key(h.postPrefixes))
	mux.Handle("DELETE /api/prefixes", key(h.deletePrefix))

	mux.Handle("GET /api/tickets", key(h.allTickets))
	mux.Handle("GET /api/tickets/{prefix}", key(h.ticketsByPrefix))
	mux.Handle("GET /api/tickets/{prefix}/{id}", key(h.singleTicket))
	mux.Handle("GET /api/tickets/{prefix}/{from}/{to}", key(h.ticketRange))
	mux.Handle("POST /api/tickets", key(h.postTickets))

	mux.Handle("GET /api/baskets", key(h.allBaskets))
	mux.Handle("GET /api/baskets/{prefix}", key(h.basketsByPrefix))
	mux.Handle("GET /api/baskets/{prefix}/{id}", key(h.singleBasket))
	mux.Handle("GET /api/baskets/{prefix}/{from}/{to}", key(h.basketRange))
	mux.Handle("POST /api/baskets", key(h.postBaskets))

	mux.Handle("GET /api/drawing", key(h.allDrawing))
	mux.Handle("GET /api/drawing/{prefix}", key(h.drawingByPrefix))
	mux.Handle("GET /api/drawing/{prefix}/{id}", key(h.singleDrawing))
	mux.Handle("GET /api/drawing/{prefix}/{from}/{to}", key(h.drawingRange))
	mux.Handle("POST /api/drawing", key(h.postDrawing))

	mux.Handle("GET /api/reports/byname/{prefix}", key(h.reportByName))
	mux.Handle("GET /api/reports/bybasket/{prefix}", key(h.reportByBasket))
	mux.Handle("GET /api/reports/counts", key(h.reportCounts))

	mux.Handle("GET /api/search/tickets", key(h.searchTickets))
	mux.Handle("POST /api/search/tickets", key(h.postTickets))

	mux.Handle("GET /api/backuprestore", key(h.exportBackup))
	mux.Handle("POST /api/backuprestore", key(h.importBackup))

	return httpx.JSONErrors(mux, "/api")
}

func (h *handler) requireKey(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, err := h.st.KeyExists(r.Header.Get("TAM-KEY"))
		if err != nil {
			httpx.WriteInternal(w, err)
			return
		}
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Key")
			return
		}
		next(w, r)
	})
}

func (h *handler) requirePassword(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		given := r.Header.Get("TAM-PW")
		if given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(h.password)) != 1 {
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Password")
			return
		}
		next(w, r)
	})
}

func (h *handler) root(w http.ResponseWriter, r *http.Request) {
	authed, err := h.st.KeyExists(r.Header.Get("TAM-KEY"))
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"whoami": "TAM Server", "authenticated": authed, "healthy": true})
}

// respond writes a value (or a generic error) produced by a store call.
func respond[T any](w http.ResponseWriter, v T, err error) {
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

// decodeList reads a JSON list body, validates it and answers the error
// itself when something is wrong. A JSON null becomes an empty list.
func decodeList[T any](w http.ResponseWriter, r *http.Request, validate func([]T) error) ([]T, bool) {
	var items []T
	if err := httpx.DecodeJSON(w, r, &items); err != nil {
		httpx.WriteDecodeError(w, err)
		return nil, false
	}
	if items == nil {
		items = []T{}
	}
	if err := validate(items); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return items, true
}

// asList mirrors the original single-item endpoints, which answer with a
// list holding zero or one row.
func asList[T any](item *T, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	if item == nil {
		return []T{}, nil
	}
	return []T{*item}, nil
}

// rangeParams reads from and to, swapping them when reversed.
func rangeParams(r *http.Request) (int, int, error) {
	from, err := httpx.IntParam(r, "from")
	if err != nil {
		return 0, 0, err
	}
	to, err := httpx.IntParam(r, "to")
	if err != nil {
		return 0, 0, err
	}
	if from > to {
		from, to = to, from
	}
	return from, to, nil
}

// --- auth keys ---

func (h *handler) listKeys(w http.ResponseWriter, r *http.Request) {
	ks, err := h.st.ListKeys()
	respond(w, ks, err)
}

func (h *handler) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string `json:"description"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	k, err := h.st.CreateKey(req.Description)
	respond(w, k, err)
}

func (h *handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key_to_del")
	if key == "" {
		httpx.WriteError(w, http.StatusBadRequest, "key_to_del is required")
		return
	}
	gone, err := h.st.DeleteKey(key)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if gone == nil {
		httpx.WriteError(w, http.StatusNotFound, "Key not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, gone)
}

// --- prefixes ---

func (h *handler) listPrefixes(w http.ResponseWriter, r *http.Request) {
	ps, err := h.st.ListPrefixes()
	respond(w, ps, err)
}

func (h *handler) postPrefixes(w http.ResponseWriter, r *http.Request) {
	ps, ok := decodeList(w, r, store.ValidatePrefixes)
	if !ok {
		return
	}
	if err := h.st.UpsertPrefixes(ps); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ps)
}

func (h *handler) deletePrefix(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("p")
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "p is required")
		return
	}
	gone, err := h.st.DeletePrefix(name)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if gone == nil {
		httpx.WriteError(w, http.StatusNotFound, "Prefix not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, gone)
}

// --- tickets ---

func (h *handler) allTickets(w http.ResponseWriter, r *http.Request) {
	ts, err := h.st.AllTickets()
	respond(w, ts, err)
}

func (h *handler) ticketsByPrefix(w http.ResponseWriter, r *http.Request) {
	ts, err := h.st.TicketsByPrefix(r.PathValue("prefix"))
	respond(w, ts, err)
}

func (h *handler) singleTicket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ts, err := asList(h.st.Ticket(r.PathValue("prefix"), id))
	respond(w, ts, err)
}

func (h *handler) ticketRange(w http.ResponseWriter, r *http.Request) {
	from, to, err := rangeParams(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ts, err := h.st.TicketRange(r.PathValue("prefix"), from, to)
	respond(w, ts, err)
}

func (h *handler) postTickets(w http.ResponseWriter, r *http.Request) {
	ts, ok := decodeList(w, r, store.ValidateTickets)
	if !ok {
		return
	}
	if err := h.st.UpsertTickets(ts); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ts)
}

func (h *handler) searchTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ts, err := h.st.SearchTickets(q.Get("first_name"), q.Get("last_name"), q.Get("phone_number"))
	respond(w, ts, err)
}

// --- baskets ---

func (h *handler) allBaskets(w http.ResponseWriter, r *http.Request) {
	bs, err := h.st.AllBaskets()
	respond(w, bs, err)
}

func (h *handler) basketsByPrefix(w http.ResponseWriter, r *http.Request) {
	bs, err := h.st.BasketsByPrefix(r.PathValue("prefix"))
	respond(w, bs, err)
}

func (h *handler) singleBasket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	bs, err := asList(h.st.Basket(r.PathValue("prefix"), id))
	respond(w, bs, err)
}

func (h *handler) basketRange(w http.ResponseWriter, r *http.Request) {
	from, to, err := rangeParams(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	bs, err := h.st.BasketRange(r.PathValue("prefix"), from, to)
	respond(w, bs, err)
}

func (h *handler) postBaskets(w http.ResponseWriter, r *http.Request) {
	bs, ok := decodeList(w, r, store.ValidateBaskets)
	if !ok {
		return
	}
	if err := h.st.UpsertBaskets(bs); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bs)
}

// --- drawing ---

func (h *handler) allDrawing(w http.ResponseWriter, r *http.Request) {
	ds, err := h.st.AllDrawing()
	respond(w, ds, err)
}

func (h *handler) drawingByPrefix(w http.ResponseWriter, r *http.Request) {
	ds, err := h.st.DrawingByPrefix(r.PathValue("prefix"))
	respond(w, ds, err)
}

func (h *handler) singleDrawing(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ds, err := asList(h.st.DrawingLine(r.PathValue("prefix"), id))
	respond(w, ds, err)
}

func (h *handler) drawingRange(w http.ResponseWriter, r *http.Request) {
	from, to, err := rangeParams(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ds, err := h.st.DrawingRange(r.PathValue("prefix"), from, to)
	respond(w, ds, err)
}

func (h *handler) postDrawing(w http.ResponseWriter, r *http.Request) {
	bs, ok := decodeList(w, r, store.ValidateBaskets)
	if !ok {
		return
	}
	if err := h.st.UpsertWinning(bs); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bs)
}

// --- reports ---

func (h *handler) reportByName(w http.ResponseWriter, r *http.Request) {
	ls, err := h.st.ReportByName(r.PathValue("prefix"))
	respond(w, ls, err)
}

func (h *handler) reportByBasket(w http.ResponseWriter, r *http.Request) {
	ls, err := h.st.ReportByBasket(r.PathValue("prefix"))
	respond(w, ls, err)
}

func (h *handler) reportCounts(w http.ResponseWriter, r *http.Request) {
	ls, err := h.st.ReportCounts()
	respond(w, ls, err)
}

// --- backup and restore ---

func (h *handler) exportBackup(w http.ResponseWriter, r *http.Request) {
	bf, err := h.st.Export()
	respond(w, bf, err)
}

func (h *handler) importBackup(w http.ResponseWriter, r *http.Request) {
	var bf store.BackupFile
	if err := httpx.DecodeJSON(w, r, &bf); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	if err := store.ValidateBackup(&bf); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.st.Import(bf); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Backup file imported successfully."})
}
