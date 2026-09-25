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

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
	"ticket-auction-manager/tam-go/internal/store"
)

type handler struct {
	st           *store.Store
	settingsPath string
}

// NewHandler returns the client handler. dist is the built web app, served
// under /web with index.html as the fallback for client-side routes.
func NewHandler(st *store.Store, settingsPath string, dist fs.FS) http.Handler {
	h := &handler{st: st, settingsPath: settingsPath}
	mux := http.NewServeMux()

	mux.Handle("GET /{$}", http.RedirectHandler("/web/", http.StatusFound))
	mux.Handle("GET /web/", newSPA(dist))

	mux.HandleFunc("GET /api", h.root)
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

	return mux
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

// settings loads the settings file for this request. A broken file is
// logged and defaults are used, so the app keeps working.
func (h *handler) settings() config.Settings {
	s, err := config.Load(h.settingsPath)
	if err != nil {
		log.Printf("%v (using defaults)", err)
	}
	return s
}

// remote returns a client for the configured server, or nil in standalone
// mode.
func (h *handler) remote(s config.Settings) *remote.Client {
	base := s.RemoteURL()
	if base == "" {
		return nil
	}
	return remote.New(base, s.RemoteKey, s.RemoteTLS)
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
