package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
)

// rangeLimit caps how many ids one range request may cover, as the pages do.
const rangeLimit = 300

// forward relays a remote error response to the browser.
func forward(w http.ResponseWriter, res *remote.Response) {
	var doc map[string]any
	if json.Unmarshal(res.Body, &doc) == nil {
		if _, ok := doc["detail"]; ok {
			httpx.WriteJSON(w, res.Status, doc)
			return
		}
	}
	httpx.WriteError(w, res.Status, http.StatusText(res.Status))
}

func unreachable(w http.ResponseWriter, err error) {
	httpx.WriteError(w, http.StatusBadGateway, "Remote server unreachable: "+err.Error())
}

// listOr answers with a list from the remote server (empty on any failure,
// as the original does) or from the local store.
func listOr[T any](w http.ResponseWriter, rc *remote.Client, remotePath string, local func() ([]T, error)) {
	if rc != nil {
		out := []T{}
		res, err := rc.Get(remotePath)
		if err != nil || !res.OK() || res.JSON(&out) != nil || out == nil {
			out = []T{}
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	out, err := local()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// singleOr answers with one row, or a placeholder when it does not exist.
func singleOr[T any](w http.ResponseWriter, rc *remote.Client, remotePath string, placeholder T, local func() (*T, error)) {
	if rc != nil {
		var rows []T
		res, err := rc.Get(remotePath)
		if err == nil && res.OK() && res.JSON(&rows) == nil && len(rows) > 0 {
			httpx.WriteJSON(w, http.StatusOK, rows[0])
			return
		}
		httpx.WriteJSON(w, http.StatusOK, placeholder)
		return
	}
	row, err := local()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if row == nil {
		httpx.WriteJSON(w, http.StatusOK, placeholder)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, *row)
}

// rangeOr answers with one entry per id in [from, to]: existing rows where
// they exist, placeholders elsewhere. In remote mode a failure answers [].
func rangeOr[T any](w http.ResponseWriter, r *http.Request, rc *remote.Client, remotePath func(from, to int) string,
	placeholder func(id int) T, idOf func(T) int, local func(from, to int) ([]T, error)) {
	from, err := httpx.IntParam(r, "from")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := httpx.IntParam(r, "to")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if from > to {
		from, to = to, from
	}
	if to-from > rangeLimit {
		to = from + rangeLimit
	}
	var rows []T
	if rc != nil {
		res, err := rc.Get(remotePath(from, to))
		if err != nil || !res.OK() || res.JSON(&rows) != nil {
			httpx.WriteJSON(w, http.StatusOK, []T{})
			return
		}
	} else {
		rows, err = local(from, to)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	byID := make(map[int]T, len(rows))
	for _, row := range rows {
		byID[idOf(row)] = row
	}
	out := make([]T, 0, to-from+1)
	for id := from; id <= to; id++ {
		if row, ok := byID[id]; ok {
			out = append(out, row)
		} else {
			out = append(out, placeholder(id))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// writeThrough decodes and validates a list, sends it to the remote server
// when configured, then writes it locally, as the original client does.
func writeThrough[T any](w http.ResponseWriter, r *http.Request, rc *remote.Client, remotePath string,
	validate func([]T) error, local func([]T) error) {
	var items []T
	if err := httpx.DecodeJSON(r, &items); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if items == nil {
		items = []T{}
	}
	if err := validate(items); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rc != nil {
		res, err := rc.Post(remotePath, items)
		if err != nil {
			unreachable(w, err)
			return
		}
		if !res.OK() {
			forward(w, res)
			return
		}
	}
	if err := local(items); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

// --- root and settings ---

func (h *handler) root(w http.ResponseWriter, r *http.Request) {
	s := h.settings()
	rc := h.remote(s)
	if rc == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"whoami": "TAM Client"})
		return
	}
	var doc map[string]any
	res, err := rc.Get("/api")
	if err != nil || !res.OK() || res.JSON(&doc) != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"whoami": "TAM Server", "authenticated": false, "healthy": false})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, doc)
}

func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, h.settings())
}

func (h *handler) postSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if err := httpx.DecodeJSON(r, &patch); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	merged, err := config.Merge(h.settings(), patch)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	merged = config.Normalize(merged)
	if err := config.Validate(merged); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.Save(h.settingsPath, merged); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not write settings: "+err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, merged)
}

// proxyAuth relays key management to the server, translating the page's
// TAM-PWD header into the server's TAM-PW.
func (h *handler) proxyAuth(w http.ResponseWriter, r *http.Request) {
	s := h.settings()
	rc := h.remote(s)
	if rc == nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Not configured.")
		return
	}
	headers := map[string]string{"TAM-PW": r.Header.Get("TAM-PWD")}
	var body any
	if r.Method == http.MethodPost {
		var req struct {
			Description string `json:"description"`
		}
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		body = req
	}
	p := "/api/auth"
	if r.Method == http.MethodDelete {
		p += "?key_to_del=" + url.QueryEscape(r.URL.Query().Get("key_to_del"))
	}
	res, err := rc.Do(r.Method, p, headers, body)
	if err != nil {
		unreachable(w, err)
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.Status)
	w.Write(res.Body)
}

// --- prefixes ---

func (h *handler) listPrefixes(w http.ResponseWriter, r *http.Request) {
	listOr(w, h.remote(h.settings()), "/api/prefixes", h.st.ListPrefixes)
}

func (h *handler) postPrefixes(w http.ResponseWriter, r *http.Request) {
	writeThrough(w, r, h.remote(h.settings()), "/api/prefixes", store.ValidatePrefixes, h.st.UpsertPrefixes)
}

func (h *handler) deletePrefix(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("p")
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "p is required")
		return
	}
	rc := h.remote(h.settings())
	if rc != nil {
		res, err := rc.Delete("/api/prefixes?p=" + url.QueryEscape(name))
		if err != nil {
			unreachable(w, err)
			return
		}
		if !res.OK() {
			forward(w, res)
			return
		}
	}
	n, err := h.st.DeletePrefix(name)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 && rc == nil {
		httpx.WriteError(w, http.StatusNotFound, "Prefix not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, store.Prefix{Prefix: name})
}

// --- tickets ---

func ticketPlaceholder(prefix string, pref string) func(id int) store.Ticket {
	return func(id int) store.Ticket { return store.Ticket{Prefix: prefix, TID: id, Pref: pref} }
}

func (h *handler) allTickets(w http.ResponseWriter, r *http.Request) {
	listOr(w, h.remote(h.settings()), "/api/tickets", h.st.AllTickets)
}

func (h *handler) ticketsByPrefix(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(w, h.remote(h.settings()), "/api/tickets/"+url.PathEscape(prefix), func() ([]store.Ticket, error) {
		return h.st.TicketsByPrefix(prefix)
	})
}

func (h *handler) singleTicket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	s := h.settings()
	prefix := r.PathValue("prefix")
	singleOr(w, h.remote(s), fmt.Sprintf("/api/tickets/%s/%d", url.PathEscape(prefix), id),
		ticketPlaceholder(prefix, s.DefaultPref)(id), func() (*store.Ticket, error) { return h.st.Ticket(prefix, id) })
}

func (h *handler) ticketRange(w http.ResponseWriter, r *http.Request) {
	s := h.settings()
	prefix := r.PathValue("prefix")
	rangeOr(w, r, h.remote(s),
		func(from, to int) string {
			return fmt.Sprintf("/api/tickets/%s/%d/%d", url.PathEscape(prefix), from, to)
		},
		ticketPlaceholder(prefix, s.DefaultPref),
		func(t store.Ticket) int { return t.TID },
		func(from, to int) ([]store.Ticket, error) { return h.st.TicketRange(prefix, from, to) })
}

func (h *handler) postTickets(w http.ResponseWriter, r *http.Request) {
	writeThrough(w, r, h.remote(h.settings()), "/api/tickets", store.ValidateTickets, h.st.UpsertTickets)
}

func (h *handler) searchTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := url.Values{}
	for _, k := range []string{"first_name", "last_name", "phone_number"} {
		params.Set(k, q.Get(k))
	}
	listOr(w, h.remote(h.settings()), "/api/search/tickets?"+params.Encode(), func() ([]store.Ticket, error) {
		return h.st.SearchTickets(q.Get("first_name"), q.Get("last_name"), q.Get("phone_number"))
	})
}

func (h *handler) postSearch(w http.ResponseWriter, r *http.Request) {
	writeThrough(w, r, h.remote(h.settings()), "/api/search/tickets", store.ValidateTickets, h.st.UpsertTickets)
}

// --- baskets ---

func basketPlaceholder(prefix string) func(id int) store.Basket {
	return func(id int) store.Basket { return store.Basket{Prefix: prefix, BID: id} }
}

func (h *handler) allBaskets(w http.ResponseWriter, r *http.Request) {
	listOr(w, h.remote(h.settings()), "/api/baskets", h.st.AllBaskets)
}

func (h *handler) basketsByPrefix(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(w, h.remote(h.settings()), "/api/baskets/"+url.PathEscape(prefix), func() ([]store.Basket, error) {
		return h.st.BasketsByPrefix(prefix)
	})
}

func (h *handler) singleBasket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	prefix := r.PathValue("prefix")
	singleOr(w, h.remote(h.settings()), fmt.Sprintf("/api/baskets/%s/%d", url.PathEscape(prefix), id),
		basketPlaceholder(prefix)(id), func() (*store.Basket, error) { return h.st.Basket(prefix, id) })
}

func (h *handler) basketRange(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	rangeOr(w, r, h.remote(h.settings()),
		func(from, to int) string {
			return fmt.Sprintf("/api/baskets/%s/%d/%d", url.PathEscape(prefix), from, to)
		},
		basketPlaceholder(prefix),
		func(b store.Basket) int { return b.BID },
		func(from, to int) ([]store.Basket, error) { return h.st.BasketRange(prefix, from, to) })
}

func (h *handler) postBaskets(w http.ResponseWriter, r *http.Request) {
	writeThrough(w, r, h.remote(h.settings()), "/api/baskets", store.ValidateBaskets, h.st.UpsertBaskets)
}

// --- drawing ---

func drawingPlaceholder(prefix string) func(id int) store.DrawingLine {
	return func(id int) store.DrawingLine { return store.DrawingLine{Prefix: prefix, BID: id} }
}

func (h *handler) allDrawing(w http.ResponseWriter, r *http.Request) {
	listOr(w, h.remote(h.settings()), "/api/drawing", h.st.AllDrawing)
}

func (h *handler) drawingByPrefix(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(w, h.remote(h.settings()), "/api/drawing/"+url.PathEscape(prefix), func() ([]store.DrawingLine, error) {
		return h.st.DrawingByPrefix(prefix)
	})
}

func (h *handler) singleDrawing(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.IntParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	prefix := r.PathValue("prefix")
	singleOr(w, h.remote(h.settings()), fmt.Sprintf("/api/drawing/%s/%d", url.PathEscape(prefix), id),
		drawingPlaceholder(prefix)(id), func() (*store.DrawingLine, error) { return h.st.DrawingLine(prefix, id) })
}

func (h *handler) drawingRange(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	rangeOr(w, r, h.remote(h.settings()),
		func(from, to int) string {
			return fmt.Sprintf("/api/drawing/%s/%d/%d", url.PathEscape(prefix), from, to)
		},
		drawingPlaceholder(prefix),
		func(d store.DrawingLine) int { return d.BID },
		func(from, to int) ([]store.DrawingLine, error) { return h.st.DrawingRange(prefix, from, to) })
}

func (h *handler) postDrawing(w http.ResponseWriter, r *http.Request) {
	writeThrough(w, r, h.remote(h.settings()), "/api/drawing", store.ValidateBaskets, h.st.UpsertWinning)
}

// --- reports ---

func (h *handler) reportByName(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(w, h.remote(h.settings()), "/api/reports/byname/"+url.PathEscape(prefix), func() ([]store.ReportByNameLine, error) {
		return h.st.ReportByName(prefix)
	})
}

func (h *handler) reportByBasket(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(w, h.remote(h.settings()), "/api/reports/bybasket/"+url.PathEscape(prefix), func() ([]store.ReportByBasketLine, error) {
		return h.st.ReportByBasket(prefix)
	})
}

func (h *handler) reportCounts(w http.ResponseWriter, r *http.Request) {
	listOr(w, h.remote(h.settings()), "/api/reports/counts", h.st.ReportCounts)
}

// --- backup and restore ---

func (h *handler) exportLocal(w http.ResponseWriter, r *http.Request) {
	bf, err := h.st.Export()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bf)
}

func decodeBackup(w http.ResponseWriter, r *http.Request) (store.BackupFile, bool) {
	var bf store.BackupFile
	if err := httpx.DecodeJSON(r, &bf); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return bf, false
	}
	if err := store.ValidateBackup(&bf); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return bf, false
	}
	return bf, true
}

func (h *handler) importLocal(w http.ResponseWriter, r *http.Request) {
	bf, ok := decodeBackup(w, r)
	if !ok {
		return
	}
	if err := h.st.Import(bf); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Data loaded successfully."})
}

func (h *handler) exportRemote(w http.ResponseWriter, r *http.Request) {
	rc := h.remote(h.settings())
	if rc == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}
	var bf store.BackupFile
	res, err := rc.Get("/api/backuprestore")
	if err != nil {
		unreachable(w, err)
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	if err := res.JSON(&bf); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "Remote server sent an invalid backup")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bf)
}

func (h *handler) importRemote(w http.ResponseWriter, r *http.Request) {
	rc := h.remote(h.settings())
	if rc == nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Server not set.")
		return
	}
	bf, ok := decodeBackup(w, r)
	if !ok {
		return
	}
	res, err := rc.Post("/api/backuprestore", bf)
	if err != nil {
		unreachable(w, err)
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Backup file imported successfully."})
}

// push sends one local table to the server.
func (h *handler) push(w http.ResponseWriter, r *http.Request) {
	target := r.PathValue("target")
	var bf store.BackupFile
	var err error
	switch target {
	case "prefixes":
		bf.Prefixes, err = h.st.ListPrefixes()
	case "tickets":
		bf.Tickets, err = h.st.AllTickets()
	case "baskets":
		bf.Baskets, err = h.st.AllBaskets()
	default:
		httpx.WriteError(w, http.StatusBadRequest, "Can only push prefixes, tickets, or baskets.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rc := h.remote(h.settings())
	if rc == nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Server not set.")
		return
	}
	res, err := rc.Post("/api/backuprestore", bf)
	if err != nil {
		unreachable(w, err)
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	label := strings.ToUpper(target[:1]) + target[1:]
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": label + " pushed successfully."})
}
