package client

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/discovery"
	"ticket-auction-manager/tam-go/internal/httpx"
	"ticket-auction-manager/tam-go/internal/remote"
)

// browseWait is how long GET /api/servers listens for announcements.
const browseWait = 1500 * time.Millisecond

// status answers where the client stands with its server.
func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	st := h.sync.Status()
	if st.Mode == "standalone" {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"mode": "standalone"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

// servers lists the tam-servers announcing themselves on this network.
func (h *handler) servers(w http.ResponseWriter, r *http.Request) {
	list, err := discovery.Browse(r.Context(), browseWait)
	if err != nil {
		log.Printf("discovery: %v", err)
	}
	if list == nil {
		list = []discovery.Server{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

type pairRequest struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	TLS      bool   `json:"tls"`
	Password string `json:"password"`
}

// pair connects this client to a server: it checks the address answers as
// a TAM server, creates an access key with the server password, and saves
// the connection. Over TLS the server certificate is pinned from now on.
func (h *handler) pair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	req.Port = strings.TrimSpace(req.Port)
	if req.Port == "" {
		req.Port = "8000"
	}
	probe := config.Defaults()
	probe.RemoteServer, probe.RemotePort, probe.RemoteTLS = req.Host, req.Port, req.TLS
	if req.Host == "" {
		httpx.WriteError(w, http.StatusBadRequest, "host is required")
		return
	}
	if err := config.Validate(probe); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "password is required")
		return
	}

	hostPort := net.JoinHostPort(req.Host, req.Port)
	fingerprint := ""
	var rc *remote.Client
	if req.TLS {
		fp, err := remote.Fingerprint(hostPort)
		if err != nil {
			h.unreachableAt(w, hostPort, err)
			return
		}
		fingerprint = fp
		rc = remote.NewPinned(probe.RemoteURL(), "", fp)
	} else {
		rc = remote.New(probe.RemoteURL(), "", false)
	}
	rc = rc.WithTimeout(10 * time.Second)

	name := req.Host
	res, err := rc.Get("/api")
	if err != nil {
		h.unreachableAt(w, hostPort, err)
		return
	}
	var root struct {
		Whoami string `json:"whoami"`
		Name   string `json:"name"`
	}
	if !res.OK() || res.JSON(&root) != nil || root.Whoami != "TAM Server" {
		httpx.WriteError(w, http.StatusBadGateway, fmt.Sprintf("%s did not answer as a TAM server", hostPort))
		return
	}
	if root.Name != "" {
		name = root.Name
	}

	laptop, _ := os.Hostname()
	if laptop == "" {
		laptop = "laptop"
	}
	res, err = rc.Do(http.MethodPost, "/api/auth", map[string]string{"TAM-PW": req.Password}, map[string]string{"description": laptop})
	if err != nil {
		h.unreachableAt(w, hostPort, err)
		return
	}
	switch {
	case res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden:
		httpx.WriteError(w, http.StatusUnauthorized, "The server rejected the password")
		return
	case res.Status == http.StatusServiceUnavailable:
		httpx.WriteError(w, http.StatusServiceUnavailable, "The server has no password yet; open its admin page first")
		return
	case !res.OK():
		forward(w, res)
		return
	}
	var key struct {
		AuthKey string `json:"auth_key"`
	}
	if res.JSON(&key) != nil || key.AuthKey == "" {
		httpx.WriteError(w, http.StatusBadGateway, "The server did not return an access key")
		return
	}

	if _, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
		cur.RemoteServer, cur.RemotePort, cur.RemoteTLS = req.Host, req.Port, req.TLS
		cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = key.AuthKey, name, fingerprint
		return config.Normalize(cur), nil
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	if dropped, err := h.st.ClearOutbox(); err != nil {
		log.Printf("outbox: %v", err)
	} else if dropped > 0 {
		log.Printf("paired with %s: dropped %s queued for the previous server", name, plural(dropped, "save"))
	}
	h.sync.Reset()
	h.sync.Kick()
	log.Printf("paired with %s (%s)", name, hostPort)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Paired with " + name, "server": hostPort})
}

// unpair returns the client to standalone mode. With the server password
// the laptop's key is also deleted on the server, when it can be reached.
func (h *handler) unpair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	s := h.settings()
	if s.RemoteURL() == "" {
		httpx.WriteError(w, http.StatusBadRequest, "This client is not paired with a server")
		return
	}
	if req.Password != "" && s.RemoteKey != "" {
		if rc := h.remote(s); rc != nil {
			res, err := rc.WithTimeout(5*time.Second).Do(http.MethodDelete, "/api/auth?key_to_del="+url.QueryEscape(s.RemoteKey),
				map[string]string{"TAM-PW": req.Password}, nil)
			switch {
			case err != nil:
				log.Printf("unpair: the key stays on the server: %v", err)
			case !res.OK():
				log.Printf("unpair: the server kept the key (%d)", res.Status)
			}
		}
	}
	if _, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
		cur.RemoteServer, cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = "", "", "", ""
		return cur, nil
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	dropped, err := h.st.ClearOutbox()
	if err != nil {
		log.Printf("outbox: %v", err)
	}
	h.sync.Reset()
	msg := "Standalone again."
	if dropped > 0 {
		msg += fmt.Sprintf(" %s that had not reached the server were dropped; they are still on this laptop.", plural(dropped, "save"))
	}
	log.Print("unpaired: standalone again")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// retryOutbox puts the saves the server refused back in line.
func (h *handler) retryOutbox(w http.ResponseWriter, r *http.Request) {
	var ignored json.RawMessage
	if err := httpx.DecodeJSON(w, r, &ignored); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	n, err := h.st.RetryFailed()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	h.sync.Kick()
	pending, _, err := h.st.OutboxCounts()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": "Retrying " + plural(n, "save"), "pending": pending})
}

// discardOutbox forgets the saves the server refused.
func (h *handler) discardOutbox(w http.ResponseWriter, r *http.Request) {
	var ignored json.RawMessage
	if err := httpx.DecodeJSON(w, r, &ignored); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}
	n, err := h.st.DiscardFailed()
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "Discarded " + plural(n, "save")})
}

func (h *handler) unreachableAt(w http.ResponseWriter, hostPort string, err error) {
	log.Printf("pair %s: %v", hostPort, err)
	httpx.WriteError(w, http.StatusBadGateway, "Remote server unreachable")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
