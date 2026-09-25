// Package client is the HTTP surface of tam-client: it serves the embedded
// web app and an /api that works against the local database or, in remote
// mode, against a tam-server.
package client

import (
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
)

type handler struct {
	st  *store.Store
	cfg *config.File

	// One remote client (one connection pool) per server address and TLS
	// setting; the access key is applied per request.
	rcMu   sync.Mutex
	rc     *remote.Client
	rcBase string
	rcTLS  bool
}

// NewHandler returns the client handler. dist is the built web app, served
// under /web with index.html as the fallback for client-side routes.
func NewHandler(st *store.Store, settingsPath string, dist fs.FS) http.Handler {
	h := &handler{st: st, cfg: config.Open(settingsPath)}
	mux := http.NewServeMux()

	mux.Handle("GET /{$}", http.RedirectHandler("/web/", http.StatusFound))
	mux.Handle("GET /web/", newSPA(dist))

	mux.HandleFunc("GET /api", h.root)
	mux.HandleFunc("GET /api/{$}", h.root)
	mux.HandleFunc("GET /api/settings", h.getSettings)
	mux.HandleFunc("POST /api/settings", guard(h.postSettings))

	mux.HandleFunc("GET /api/auth", h.proxyAuth)
	mux.HandleFunc("POST /api/auth", guard(h.proxyAuth))
	mux.HandleFunc("DELETE /api/auth", guard(h.proxyAuth))

	mux.HandleFunc("GET /api/prefixes", h.listPrefixes)
	mux.HandleFunc("POST /api/prefixes", guard(h.postPrefixes))
	mux.HandleFunc("DELETE /api/prefixes", guard(h.deletePrefix))

	mux.HandleFunc("GET /api/tickets", h.allTickets)
	mux.HandleFunc("GET /api/tickets/{prefix}", h.ticketsByPrefix)
	mux.HandleFunc("GET /api/tickets/{prefix}/{id}", h.singleTicket)
	mux.HandleFunc("GET /api/tickets/{prefix}/{from}/{to}", h.ticketRange)
	mux.HandleFunc("POST /api/tickets", guard(h.postTickets))

	mux.HandleFunc("GET /api/baskets", h.allBaskets)
	mux.HandleFunc("GET /api/baskets/{prefix}", h.basketsByPrefix)
	mux.HandleFunc("GET /api/baskets/{prefix}/{id}", h.singleBasket)
	mux.HandleFunc("GET /api/baskets/{prefix}/{from}/{to}", h.basketRange)
	mux.HandleFunc("POST /api/baskets", guard(h.postBaskets))

	mux.HandleFunc("GET /api/drawing", h.allDrawing)
	mux.HandleFunc("GET /api/drawing/{prefix}", h.drawingByPrefix)
	mux.HandleFunc("GET /api/drawing/{prefix}/{id}", h.singleDrawing)
	mux.HandleFunc("GET /api/drawing/{prefix}/{from}/{to}", h.drawingRange)
	mux.HandleFunc("POST /api/drawing", guard(h.postDrawing))

	mux.HandleFunc("GET /api/reports/byname/{prefix}", h.reportByName)
	mux.HandleFunc("GET /api/reports/bybasket/{prefix}", h.reportByBasket)
	mux.HandleFunc("GET /api/reports/counts", h.reportCounts)

	mux.HandleFunc("GET /api/search/tickets", h.searchTickets)
	mux.HandleFunc("POST /api/search/tickets", guard(h.postSearch))

	mux.HandleFunc("GET /api/backuprestore/local", h.exportLocal)
	mux.HandleFunc("POST /api/backuprestore/local", guard(h.importLocal))
	mux.HandleFunc("GET /api/backuprestore/remote", h.exportRemote)
	mux.HandleFunc("POST /api/backuprestore/remote", guard(h.importRemote))
	mux.HandleFunc("POST /api/backuprestore/push/{target}", guard(h.push))

	return httpx.JSONErrors(mux, "/api")
}

// guard refuses writes that a browser reports as coming from another site.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !httpx.SameSite(r) {
			httpx.WriteError(w, http.StatusForbidden, "Cross-site request refused")
			return
		}
		next(w, r)
	}
}

// settings returns the current settings; a hand edit of the file is picked
// up without a restart and a broken file keeps the last good values.
func (h *handler) settings() config.Settings {
	return h.cfg.Get()
}

// remote returns a client for the configured server, or nil in standalone
// mode. The connection pool is kept across requests and rebuilt only when
// the server address or TLS setting changes.
func (h *handler) remote(s config.Settings) *remote.Client {
	base := s.RemoteURL()
	if base == "" {
		return nil
	}
	h.rcMu.Lock()
	defer h.rcMu.Unlock()
	if h.rc == nil || h.rcBase != base || h.rcTLS != s.RemoteTLS {
		h.rc = remote.New(base, "", s.RemoteTLS)
		h.rcBase, h.rcTLS = base, s.RemoteTLS
	}
	return h.rc.WithKey(s.RemoteKey)
}

// --- single page app ---

type spa struct {
	dist  fs.FS
	files http.Handler
	index []byte
}

func newSPA(dist fs.FS) http.Handler {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		log.Printf("web app: index.html is missing from the embedded build: %v", err)
		index = []byte("<!doctype html><title>TAM</title><p>The web app was not built. Run <code>pnpm build</code> in frontend/ before building tam-client.")
	}
	return &spa{dist: dist, files: http.FileServer(http.FS(dist)), index: index}
}

func (s *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/web/")
	if rel != "" {
		if f, err := s.dist.Open(rel); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && !info.IsDir() {
				// Hashed bundle files never change; everything else may.
				if strings.HasPrefix(rel, "_app/immutable/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.StripPrefix("/web/", s.files).ServeHTTP(w, r)
				return
			}
		}
		// A missing asset is a 404; a missing page is the app itself.
		if strings.Contains(path.Base(rel), ".") {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(s.index)
}
