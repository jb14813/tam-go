// Package server is the HTTP API of tam-server, the shared database that
// several tam-client installations talk to in remote mode.
package server

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/store"
)

// Version is the program version reported by GET /api.
const Version = "0.0.1"

// Password is the server password that protects key management. The admin
// package's Password implements it; FixedPassword is enough for tests.
type Password interface {
	// IsSet reports whether a password exists at all. While it is false the
	// key routes answer 503 so nobody can pair with an unconfigured server.
	IsSet() bool
	// Check reports whether plain is the password. An empty plain never
	// matches.
	Check(plain string) bool
}

type fixedPassword string

func (p fixedPassword) IsSet() bool { return p != "" }

func (p fixedPassword) Check(plain string) bool {
	return plain != "" && subtle.ConstantTimeCompare([]byte(plain), []byte(p)) == 1
}

// FixedPassword returns a Password that is the given string; "" is an unset
// password.
func FixedPassword(s string) Password { return fixedPassword(s) }

// Info describes the server to its clients in the GET /api answer.
type Info struct {
	Name    string // shown to clients when pairing; defaults to the host name
	Version string // defaults to Version
}

// Option configures NewHandler.
type Option func(*handler)

// WithInfo sets the name and version GET /api reports. Empty fields keep
// their defaults.
func WithInfo(info Info) Option {
	return func(h *handler) {
		if info.Name != "" {
			h.info.Name = info.Name
		}
		if info.Version != "" {
			h.info.Version = info.Version
		}
	}
}

// touchEvery is how often at most a key's last_seen is written. It is a
// variable so tests can lower it.
var touchEvery = time.Minute

type handler struct {
	st   *store.Store
	pw   Password
	info Info

	mu      sync.Mutex
	touched map[string]time.Time // key -> last time last_seen was written
}

// NewHandler returns the server API. Data routes require a TAM-KEY header
// that matches a stored access key; key management requires a TAM-PW header
// that pw accepts, and answers 503 while no password is set. Unknown paths
// and wrong methods under /api answer {"detail": ...} like the original.
func NewHandler(st *store.Store, pw Password, opts ...Option) http.Handler {
	hostname, _ := os.Hostname()
	h := &handler{st: st, pw: pw, info: Info{Name: hostname, Version: Version}, touched: map[string]time.Time{}}
	for _, opt := range opts {
		opt(h)
	}
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
		key := keyOf(r)
		ok, err := h.st.KeyExists(key)
		if err != nil {
			httpx.WriteInternal(w, err)
			return
		}
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Key")
			return
		}
		h.touch(key)
		next(w, r)
	})
}

// keyOf returns the request's access key. The original client sends it as
// TAM_KEY on its server-backup download, so that spelling counts too.
func keyOf(r *http.Request) string {
	if key := r.Header.Get("TAM-KEY"); key != "" {
		return key
	}
	return r.Header.Get("TAM_KEY")
}

// touch records the key's last_seen time, at most once per touchEvery so
// the heartbeat of every laptop does not turn into a write every 5 s. A
// failure is logged and never fails the request; last_seen is informational.
func (h *handler) touch(key string) {
	now := time.Now()
	h.mu.Lock()
	last, seen := h.touched[key]
	if seen && now.Sub(last) < touchEvery {
		h.mu.Unlock()
		return
	}
	h.touched[key] = now
	h.mu.Unlock()
	if err := h.st.TouchKey(key); err != nil {
		log.Printf("record last_seen for a key: %v", err)
	}
}

// forget drops the throttle entry of a deleted key.
func (h *handler) forget(key string) {
	h.mu.Lock()
	delete(h.touched, key)
	h.mu.Unlock()
}

func (h *handler) requirePassword(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.pw.IsSet() {
			httpx.WriteError(w, http.StatusServiceUnavailable, "server password not set")
			return
		}
		if !h.pw.Check(r.Header.Get("TAM-PW")) {
			httpx.WriteError(w, http.StatusUnauthorized, "Invalid Password")
			return
		}
		next(w, r)
	})
}

func (h *handler) root(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	authed, err := h.st.KeyExists(key)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if authed {
		h.touch(key) // the client's heartbeat is this route
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"whoami": "TAM Server", "authenticated": authed, "healthy": true,
		"name": h.info.Name, "version": h.info.Version,
	})
}

// errOrder is a save whose name and number (X-TAM-Laptop, X-TAM-Save) do
// not make sense.
var errOrder = errors.New("X-TAM-Laptop must name the laptop, in at most 64 characters, and X-TAM-Save number its save, above 0")

// ordered runs a save in the order the laptop made it when the request
// numbers it, as tam-client does: a save that is not newer than the last
// one applied from that laptop is skipped (see store.InOrder) and the
// answer says so with X-TAM-Stale; the laptop takes it as done. Saves
// without numbers, as the original client sends them, apply as they come.
func (h *handler) ordered(w http.ResponseWriter, r *http.Request, save func(*store.Store) error) (stale bool, err error) {
	laptop, number := r.Header.Get("X-TAM-Laptop"), r.Header.Get("X-TAM-Save")
	if laptop == "" && number == "" {
		return false, save(h.st)
	}
	n, perr := strconv.ParseInt(number, 10, 64)
	if laptop == "" || len(laptop) > 64 || perr != nil || n <= 0 {
		return false, errOrder
	}
	applied, err := h.st.InOrder(laptop, n, save)
	if err != nil {
		return false, err
	}
	if !applied {
		w.Header().Set("X-TAM-Stale", "1")
		log.Printf("skipped save %d of laptop %s: it arrived after a newer one (a late copy or a repeat)", n, laptop)
	}
	return !applied, nil
}

// write is ordered for the save routes: it answers a bad number (400) or a
// failure (500) itself, and reports whether the save was stale and whether
// the route goes on to answer.
func (h *handler) write(w http.ResponseWriter, r *http.Request, save func(*store.Store) error) (stale, ok bool) {
	stale, err := h.ordered(w, r, save)
	switch {
	case errors.Is(err, errOrder):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return false, false
	case err != nil:
		httpx.WriteInternal(w, err)
		return false, false
	}
	return stale, true
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
	h.forget(key)
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
	if _, ok := h.write(w, r, func(st *store.Store) error { return st.UpsertPrefixes(ps) }); !ok {
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
	var gone *store.Prefix
	stale, ok := h.write(w, r, func(st *store.Store) error {
		var err error
		gone, err = st.DeletePrefix(name)
		return err
	})
	switch {
	case !ok:
	case stale:
		// Done before: answered as done, so a laptop replaying it does
		// not file it as refused.
		httpx.WriteJSON(w, http.StatusOK, store.Prefix{Prefix: name})
	case gone == nil:
		httpx.WriteError(w, http.StatusNotFound, "Prefix not found")
	default:
		httpx.WriteJSON(w, http.StatusOK, gone)
	}
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
	if _, ok := h.write(w, r, func(st *store.Store) error { return st.UpsertTickets(ts) }); !ok {
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
	if _, ok := h.write(w, r, func(st *store.Store) error { return st.UpsertBaskets(bs) }); !ok {
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
	if _, ok := h.write(w, r, func(st *store.Store) error { return st.UpsertWinning(bs) }); !ok {
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
