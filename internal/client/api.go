package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
	tamsync "ticket-auction-manager/tam-go/internal/sync"
)

// rangeLimit caps how many ids one range request may cover, as the pages do.
const rangeLimit = 300

// writeTimeout bounds a save sent to the server from a page. A save that
// takes longer is queued and replayed, so the page never waits for a dead
// connection to time out.
const writeTimeout = 5 * time.Second

// readTimeout bounds a page's read from the server. A read that takes
// longer is answered from the client's own copy: when the Wi-Fi drops
// without a word the server neither answers nor refuses, and a page would
// otherwise wait out the connection's own, longer, limits. It is a variable
// so tests can shorten it.
var readTimeout = 4 * time.Second

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

// unreachable answers 502, tells the syncer, and keeps the transport
// detail in the log.
func (h *handler) unreachable(w http.ResponseWriter, err error) {
	log.Printf("remote server: %v", err)
	h.sync.NoteFailure(err)
	httpx.WriteError(w, http.StatusBadGateway, "Remote server unreachable")
}

// online reports whether a call to the server is worth making right now.
func (h *handler) online() bool { return h.sync.Online() }

// inStep reports whether the data routes should go through the server right
// now: it answers and nothing saved here is still waiting to reach it (see
// Syncer.InStep).
func (h *handler) inStep() bool { return h.sync.InStep() }

// observe feeds what a server call returned to the syncer and reports
// whether the answer is usable: a 2xx, or a 4xx that describes the data
// rather than the connection. Transport failures, 5xx and a refused key
// are not usable; the caller falls back to locally entered rows or the outbox.
func (h *handler) observe(err error, res *remote.Response) bool {
	switch {
	case err != nil:
		h.sync.NoteFailure(err)
		return false
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
		h.sync.NoteUnauthorized()
		return false
	case res.Retryable():
		h.sync.NoteFailure(fmt.Errorf("server answered %d", res.Status))
		return false
	default:
		h.sync.NoteSuccess()
		return true
	}
}

// listOr answers with a list. In remote mode it comes from the server while
// the client is in step with it (see inStep); otherwise it comes from the
// client's own entries. Only shared prefix configuration supplies cache;
// reading tickets, baskets and reports never stores another client's rows.
func listOr[T any](h *handler, w http.ResponseWriter, rc *remote.Client, remotePath string, cache func([]T) error, local func() ([]T, error)) {
	if rc != nil && h.inStep() {
		res, err := rc.WithTimeout(readTimeout).Get(remotePath)
		if h.observe(err, res) && res.OK() {
			out := []T{}
			if res.JSON(&out) == nil {
				if out == nil {
					out = []T{}
				}
				if cache != nil {
					if err := cache(out); err != nil {
						log.Printf("prefix configuration cache: %v", err)
					}
				}
				httpx.WriteJSON(w, http.StatusOK, out)
				return
			}
		}
	}
	out, err := local()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// singleOr answers with one row, or a placeholder when it does not exist.
// Headers identify the source and distinguish a saved blank row from a
// placeholder without changing the original response JSON.
func singleOr[T any](h *handler, w http.ResponseWriter, rc *remote.Client, remotePath string, placeholder T, local func() (*T, error)) {
	if rc == nil {
		w.Header().Set("X-TAM-Mode", "standalone")
	} else {
		w.Header().Set("X-TAM-Mode", "remote")
	}
	if rc != nil && h.inStep() {
		res, err := rc.WithTimeout(readTimeout).Get(remotePath)
		if h.observe(err, res) && res.OK() {
			var rows []T
			if res.JSON(&rows) == nil {
				w.Header().Set("X-TAM-Source", "server")
				if len(rows) == 0 {
					w.Header().Set("X-TAM-Found", "0")
					httpx.WriteJSON(w, http.StatusOK, placeholder)
					return
				}
				w.Header().Set("X-TAM-Found", "1")
				httpx.WriteJSON(w, http.StatusOK, rows[0])
				return
			}
		}
	}
	row, err := local()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	w.Header().Set("X-TAM-Source", "local")
	if row == nil {
		w.Header().Set("X-TAM-Found", "0")
		httpx.WriteJSON(w, http.StatusOK, placeholder)
		return
	}
	w.Header().Set("X-TAM-Found", "1")
	httpx.WriteJSON(w, http.StatusOK, *row)
}

// rangeOr answers with one entry per id in [from, to]: existing rows where
// they exist, placeholders elsewhere.
func rangeOr[T any](h *handler, w http.ResponseWriter, r *http.Request, rc *remote.Client, remotePath func(from, to int) string,
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
	// A range wider than half of the ints makes to-from wrap below zero;
	// it is too wide as well.
	if to-from > rangeLimit || to-from < 0 {
		to = from + rangeLimit
	}
	var rows []T
	fromServer := false
	if rc != nil && h.inStep() {
		res, err := rc.WithTimeout(readTimeout).Get(remotePath(from, to))
		if h.observe(err, res) && res.OK() && res.JSON(&rows) == nil {
			fromServer = true
		}
	}
	if !fromServer {
		rows, err = local(from, to)
		if err != nil {
			httpx.WriteInternal(w, err)
			return
		}
	}
	byID := make(map[int]T, len(rows))
	for _, row := range rows {
		byID[idOf(row)] = row
	}
	// Counting the ids rather than comparing each with to ends the loop when
	// to is the largest int, where id++ would wrap around.
	out := make([]T, 0, to-from+1)
	for n := 0; n <= to-from; n++ {
		id := from + n
		if row, ok := byID[id]; ok {
			out = append(out, row)
		} else {
			out = append(out, placeholder(id))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// sendNumbered sends a numbered save to the server now. When the server
// answers that the save is older than the last one it applied from this
// client (its data folder was put back from a copy, see store.InOrder), the
// save is numbered past the server's count and sent again; order follows,
// so a save that ends up queued keeps its latest number. The caller holds
// the client's saves in line and the way to the server (Syncer.Numbering,
// Syncer.Sending).
func (h *handler) sendNumbered(rc *remote.Client, method, path string, order *store.Order, body any) (*remote.Response, error) {
	for attempt := 1; ; attempt++ {
		res, err := rc.WithTimeout(writeTimeout).Do(method, path, tamsync.OrderHeaders(*order), body)
		last, behind := tamsync.LastSave(res)
		if err != nil || !behind || attempt == 3 {
			return res, err
		}
		next, nerr := h.st.NextSaveAfter(h.host, last)
		if nerr != nil {
			log.Printf("save order: %v", nerr)
			return res, err
		}
		log.Printf("server: this client's save %d is older than its save %d there (was its data folder put back from a copy?); sending it again as save %d", order.Save, last, next.Save)
		*order = next
	}
}

// writeThrough decodes and validates a list and saves it. In remote mode
// the server is asked first while it answers and nothing saved here waits
// for it; otherwise the rows are kept locally and queued behind the
// saves already waiting, in one transaction, and the answer carries
// X-TAM-Queued so a page can tell. A server that rejects the data answers
// with its error and nothing is written anywhere. save writes the entries
// to this client's store and, when needed, the durable queue.
func writeThrough[T any](h *handler, w http.ResponseWriter, r *http.Request, remotePath string,
	validate func([]T) error, save func(*store.Store, []T) error) {
	var items []T
	if err := httpx.DecodeJSON(w, r, &items); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	if items == nil {
		items = []T{}
	}
	if err := validate(items); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer h.sync.Numbering()()
	// Decode may have waited while Settings changed the connection. Resolve
	// it inside the save/configuration lock, never send to the previous host.
	rc := h.remote(h.settings())
	waiting, err := h.st.OutboxWaiting()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if rc == nil && !waiting {
		if err := save(h.st, items); err != nil {
			httpx.WriteInternal(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, items)
		return
	}
	body, err := json.Marshal(items)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	// The save is numbered before it leaves: sent now or queued, it is the
	// same save to the server (see store.InOrder). Until it is answered or
	// queued, this client's other saves wait, so they reach the server, and
	// the queue, in the order of their numbers.
	order, err := h.st.NextSave(h.host)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if rc != nil && h.inStep() {
		done := h.sync.Sending()
		res, err := h.sendNumbered(rc, http.MethodPost, remotePath, &order, json.RawMessage(body))
		done()
		if h.observe(err, res) {
			if !res.OK() {
				forward(w, res)
				return
			}
			if err := save(h.st, items); err != nil {
				httpx.WriteInternal(w, err)
				return
			}
			httpx.WriteJSON(w, http.StatusOK, items)
			return
		}
	}
	if err := h.sync.SaveQueued(http.MethodPost, remotePath, body, order, func(st *store.Store) error { return save(st, items) }); err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	w.Header().Set("X-TAM-Queued", "1")
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
	if h.online() {
		res, err := rc.WithTimeout(3 * time.Second).Get("/api")
		if h.observe(err, res) && res.OK() {
			var doc map[string]any
			if res.JSON(&doc) == nil {
				httpx.WriteJSON(w, http.StatusOK, doc)
				return
			}
		}
	}
	// A server that refuses the key is healthy; one that does not answer is not.
	healthy := h.sync.State() == tamsync.Unauthenticated
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"whoami": "TAM Server", "authenticated": false, "healthy": healthy})
}

func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, h.settings())
}

// rejected marks a settings patch the user has to fix (a 400), as opposed
// to a failure writing the file (a 500).
type rejected struct{ error }

func (h *handler) postSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &patch); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	var merged config.Settings
	err := h.sync.Reconfigure(func() error {
		var err error
		merged, err = h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
			next, err := config.Merge(cur, patch)
			if err != nil {
				return cur, rejected{err}
			}
			next = config.Normalize(next)
			if err := config.Validate(next); err != nil {
				return cur, rejected{err}
			}
			if next.RemoteURL() != cur.RemoteURL() {
				// The key can still be valid on a replacement; its old display
				// name and certificate pin cannot describe a newly typed address.
				next.RemoteName, next.RemoteFingerprint = "", ""
			}
			return next, nil
		})
		return err
	})
	var bad rejected
	if errors.As(err, &bad) {
		httpx.WriteError(w, http.StatusBadRequest, bad.Error())
		return
	}
	if err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	// The server may have changed: start over with it.
	h.sync.Kick()
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
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			httpx.WriteDecodeError(w, err)
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
		h.unreachable(w, err)
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
	listOr(h, w, h.remote(h.settings()), "/api/prefixes", h.st.UpsertPrefixes, h.st.ListPrefixes)
}

func (h *handler) postPrefixes(w http.ResponseWriter, r *http.Request) {
	existing, err := h.st.ListPrefixes()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	validate := func(ps []store.Prefix) error { return store.ValidatePrefixChanges(ps, existing) }
	writeThrough(h, w, r, "/api/prefixes", validate, (*store.Store).UpsertPrefixes)
}

// deletePrefix removes a prefix on the server (in remote mode) and in the
// local prefix configuration. A prefix the server no longer has is still removed locally;
// 404 only when neither side had it. While the server is away the delete
// is queued like a save.
func (h *handler) deletePrefix(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("p")
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "p is required")
		return
	}
	remotePath := "/api/prefixes?p=" + url.QueryEscape(name)
	defer h.sync.Numbering()()
	rc := h.remote(h.settings())
	remoteHadIt, queue := false, false
	var order store.Order
	waiting, err := h.st.OutboxWaiting()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if rc != nil || waiting {
		var err error
		if order, err = h.st.NextSave(h.host); err != nil {
			httpx.WriteInternal(w, err)
			return
		}
		queue = true
		if rc != nil && h.inStep() {
			done := h.sync.Sending()
			res, err := h.sendNumbered(rc, http.MethodDelete, remotePath, &order, nil)
			done()
			if h.observe(err, res) {
				queue = false
				if res.OK() {
					remoteHadIt = true
				} else if res.Status != http.StatusNotFound {
					forward(w, res)
					return
				}
			}
		}
	}
	// Offline, the delete here and its place in the queue are one
	// transaction; a prefix neither side has is a 404 and queues nothing.
	var gone *store.Prefix
	remove := func(st *store.Store) error {
		var err error
		gone, err = st.DeletePrefix(name)
		if err == nil && gone == nil && !remoteHadIt {
			err = errNoPrefix
		}
		return err
	}
	if queue {
		err = h.sync.SaveQueued(http.MethodDelete, remotePath, nil, order, remove)
	} else {
		err = remove(h.st)
	}
	switch {
	case errors.Is(err, errNoPrefix):
		httpx.WriteError(w, http.StatusNotFound, "Prefix not found")
		return
	case err != nil:
		httpx.WriteInternal(w, err)
		return
	}
	if gone == nil {
		gone = &store.Prefix{Prefix: name}
	}
	if queue {
		w.Header().Set("X-TAM-Queued", "1")
	}
	httpx.WriteJSON(w, http.StatusOK, gone)
}

// errNoPrefix is a delete of a prefix neither the server nor the client has.
var errNoPrefix = errors.New("prefix not found")

// --- tickets ---

func ticketPlaceholder(prefix string, pref string) func(id int) store.Ticket {
	return func(id int) store.Ticket { return store.Ticket{Prefix: prefix, TID: id, Pref: pref} }
}

func (h *handler) allTickets(w http.ResponseWriter, r *http.Request) {
	listOr(h, w, h.remote(h.settings()), "/api/tickets", nil, h.st.AllTickets)
}

func (h *handler) ticketsByPrefix(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(h, w, h.remote(h.settings()), "/api/tickets/"+url.PathEscape(prefix), nil, func() ([]store.Ticket, error) {
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
	singleOr(h, w, h.remote(s), fmt.Sprintf("/api/tickets/%s/%d", url.PathEscape(prefix), id),
		ticketPlaceholder(prefix, s.DefaultPref)(id), func() (*store.Ticket, error) { return h.st.Ticket(prefix, id) })
}

func (h *handler) ticketRange(w http.ResponseWriter, r *http.Request) {
	s := h.settings()
	prefix := r.PathValue("prefix")
	rangeOr(h, w, r, h.remote(s),
		func(from, to int) string {
			return fmt.Sprintf("/api/tickets/%s/%d/%d", url.PathEscape(prefix), from, to)
		},
		ticketPlaceholder(prefix, s.DefaultPref),
		func(t store.Ticket) int { return t.TID },
		func(from, to int) ([]store.Ticket, error) { return h.st.TicketRange(prefix, from, to) })
}

func (h *handler) postTickets(w http.ResponseWriter, r *http.Request) {
	writeThrough(h, w, r, "/api/tickets", store.ValidateTickets, (*store.Store).UpsertTickets)
}

func (h *handler) searchTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := url.Values{}
	for _, k := range []string{"first_name", "last_name", "phone_number"} {
		params.Set(k, q.Get(k))
	}
	listOr(h, w, h.remote(h.settings()), "/api/search/tickets?"+params.Encode(), nil, func() ([]store.Ticket, error) {
		return h.st.SearchTickets(q.Get("first_name"), q.Get("last_name"), q.Get("phone_number"))
	})
}

func (h *handler) postSearch(w http.ResponseWriter, r *http.Request) {
	writeThrough(h, w, r, "/api/search/tickets", store.ValidateTickets, (*store.Store).UpsertTickets)
}

// --- baskets ---

func basketPlaceholder(prefix string) func(id int) store.Basket {
	return func(id int) store.Basket { return store.Basket{Prefix: prefix, BID: id} }
}

func (h *handler) allBaskets(w http.ResponseWriter, r *http.Request) {
	listOr(h, w, h.remote(h.settings()), "/api/baskets", nil, h.st.AllBaskets)
}

func (h *handler) basketsByPrefix(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(h, w, h.remote(h.settings()), "/api/baskets/"+url.PathEscape(prefix), nil, func() ([]store.Basket, error) {
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
	singleOr(h, w, h.remote(h.settings()), fmt.Sprintf("/api/baskets/%s/%d", url.PathEscape(prefix), id),
		basketPlaceholder(prefix)(id), func() (*store.Basket, error) { return h.st.Basket(prefix, id) })
}

func (h *handler) basketRange(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	rangeOr(h, w, r, h.remote(h.settings()),
		func(from, to int) string {
			return fmt.Sprintf("/api/baskets/%s/%d/%d", url.PathEscape(prefix), from, to)
		},
		basketPlaceholder(prefix),
		func(b store.Basket) int { return b.BID },
		func(from, to int) ([]store.Basket, error) { return h.st.BasketRange(prefix, from, to) })
}

func (h *handler) postBaskets(w http.ResponseWriter, r *http.Request) {
	writeThrough(h, w, r, "/api/baskets", store.ValidateBaskets, (*store.Store).UpsertBaskets)
}

// --- drawing ---

func drawingPlaceholder(prefix string) func(id int) store.DrawingLine {
	return func(id int) store.DrawingLine { return store.DrawingLine{Prefix: prefix, BID: id} }
}

func (h *handler) allDrawing(w http.ResponseWriter, r *http.Request) {
	listOr(h, w, h.remote(h.settings()), "/api/drawing", nil, h.st.AllDrawing)
}

func (h *handler) drawingByPrefix(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(h, w, h.remote(h.settings()), "/api/drawing/"+url.PathEscape(prefix), nil, func() ([]store.DrawingLine, error) {
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
	singleOr(h, w, h.remote(h.settings()), fmt.Sprintf("/api/drawing/%s/%d", url.PathEscape(prefix), id),
		drawingPlaceholder(prefix)(id), func() (*store.DrawingLine, error) { return h.st.DrawingLine(prefix, id) })
}

func (h *handler) drawingRange(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	rangeOr(h, w, r, h.remote(h.settings()),
		func(from, to int) string {
			return fmt.Sprintf("/api/drawing/%s/%d/%d", url.PathEscape(prefix), from, to)
		},
		drawingPlaceholder(prefix),
		func(d store.DrawingLine) int { return d.BID },
		func(from, to int) ([]store.DrawingLine, error) { return h.st.DrawingRange(prefix, from, to) })
}

func (h *handler) postDrawing(w http.ResponseWriter, r *http.Request) {
	writeThrough(h, w, r, "/api/drawing", store.ValidateBaskets, (*store.Store).UpsertWinning)
}

// --- reports ---

func (h *handler) reportByName(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(h, w, h.remote(h.settings()), "/api/reports/byname/"+url.PathEscape(prefix), nil, func() ([]store.ReportByNameLine, error) {
		return h.st.ReportByName(prefix)
	})
}

func (h *handler) reportByBasket(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	listOr(h, w, h.remote(h.settings()), "/api/reports/bybasket/"+url.PathEscape(prefix), nil, func() ([]store.ReportByBasketLine, error) {
		return h.st.ReportByBasket(prefix)
	})
}

func (h *handler) reportCounts(w http.ResponseWriter, r *http.Request) {
	listOr(h, w, h.remote(h.settings()), "/api/reports/counts", nil, h.st.ReportCounts)
}

// --- backup and restore ---

func (h *handler) exportLocal(w http.ResponseWriter, r *http.Request) {
	bf, err := h.st.Export()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bf)
}

func decodeBackup(w http.ResponseWriter, r *http.Request) (store.BackupFile, bool) {
	var bf store.BackupFile
	if err := httpx.DecodeJSON(w, r, &bf); err != nil {
		httpx.WriteDecodeError(w, err)
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
		httpx.WriteInternal(w, err)
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
		h.unreachable(w, err)
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
	bf, ok := decodeBackup(w, r)
	if !ok {
		return
	}
	// A connection change can finish while the body arrives. Select its
	// destination only once the complete restore can hold that connection.
	defer h.sync.Numbering()()
	rc := h.remote(h.settings())
	if rc == nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Server not set.")
		return
	}
	defer h.sync.Sending()()
	res, err := restoreInto(rc, bf)
	if err != nil {
		h.unreachable(w, err)
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Backup file imported successfully."})
}

// restoreInto sends a backup to the server, then the winning tickets a
// second time through the drawing route. The original server's restore
// leaves the winning ticket of a basket it already has untouched, and the
// drawing route is what sets it on every server, so the restore comes out
// complete on both.
func restoreInto(rc *remote.Client, bf store.BackupFile) (*remote.Response, error) {
	res, err := rc.Post("/api/backuprestore", bf)
	if err != nil || !res.OK() || len(bf.Baskets) == 0 {
		return res, err
	}
	return rc.Post("/api/drawing", bf.Baskets)
}

// push sends one local table to the server. The page sends an empty JSON
// object as the body; requiring it keeps the Content-Type barrier that
// stops cross-site form posts.
func (h *handler) push(w http.ResponseWriter, r *http.Request) {
	var ignored json.RawMessage
	if err := httpx.DecodeJSON(w, r, &ignored); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	// Keep the local snapshot and both remote restore requests together,
	// before a settings change or another page save can pass them.
	defer h.sync.Numbering()()
	target := r.PathValue("target")
	bf := store.NewBackupFile()
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
		httpx.WriteInternal(w, err)
		return
	}
	rc := h.remote(h.settings())
	if rc == nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Server not set.")
		return
	}
	defer h.sync.Sending()()
	res, err := restoreInto(rc, bf)
	if err != nil {
		h.unreachable(w, err)
		return
	}
	if !res.OK() {
		forward(w, res)
		return
	}
	label := strings.ToUpper(target[:1]) + target[1:]
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": label + " pushed successfully."})
}
