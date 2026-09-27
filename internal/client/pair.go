package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
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

// sweepEvery is how long a subnet sweep's result is reused before the
// network is asked again; the Settings page polls more often than that.
const sweepEvery = 10 * time.Second

// servers lists the tam-servers on this network: the ones announcing
// themselves (mDNS) and, for networks that drop multicast, the ones found by
// asking the standard ports on every address of the local /24 networks.
// The sweep runs in the background and takes a few seconds; its result
// shows up on the Settings page's next poll. A server seen at several
// addresses is listed once, at the address this laptop shares a network
// with.
func (h *handler) servers(w http.ResponseWriter, r *http.Request) {
	announced, err := discovery.Browse(r.Context(), browseWait)
	if err != nil {
		log.Printf("discovery: %v", err)
	}
	out := discovery.Collapse(append(announced, h.sweep()...), discovery.LocalNetworks())
	if out == nil {
		out = []discovery.Server{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// sweep returns the last subnet sweep and starts a fresh one in the
// background when the last is older than sweepEvery.
func (h *handler) sweep() []discovery.Server {
	h.sweepMu.Lock()
	defer h.sweepMu.Unlock()
	if !h.sweeping && time.Since(h.sweptAt) >= sweepEvery {
		h.sweeping = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			found := discovery.Sweep(ctx)
			h.sweepMu.Lock()
			h.swept, h.sweptAt, h.sweeping = found, time.Now(), false
			h.sweepMu.Unlock()
		}()
	}
	return h.swept
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
//
// Saves still queued stay queued when the laptop pairs with the server it
// was paired with (at the same address, or by the same name at a new one),
// which is how a laptop whose key was refused gets going again. Pairing with
// another server moves them to the failed list instead: they are not sent
// anywhere by themselves, and Settings offers Retry and Discard.
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

	res, err = rc.Do(http.MethodPost, "/api/auth", map[string]string{"TAM-PW": req.Password}, map[string]string{"description": h.host})
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

	prev := h.settings()
	same := prev.RemoteURL() != "" &&
		(prev.RemoteServer == req.Host && prev.RemotePort == req.Port || prev.RemoteName != "" && prev.RemoteName == name)
	if _, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
		cur.RemoteServer, cur.RemotePort, cur.RemoteTLS = req.Host, req.Port, req.TLS
		cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = key.AuthKey, name, fingerprint
		return config.Normalize(cur), nil
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	msg := "Paired with " + name
	if !same {
		from := "before this pairing"
		if prev.RemoteURL() != "" {
			from = "for " + serverLabel(prev)
		}
		moved, err := h.st.FailAllOutbox("queued " + from + "; retry to send it to " + name)
		switch {
		case err != nil:
			log.Printf("outbox: %v", err)
		case moved > 0:
			log.Printf("paired with %s: %s queued %s set aside in the failed list", name, plural(moved, "save"), from)
			msg += fmt.Sprintf(". %s queued %s %s set aside: Settings lists them under could not be sent, to retry here or discard.",
				plural(moved, "save"), from, wasWere(moved))
		}
	}
	h.sync.Reset()
	h.sync.Kick()
	log.Printf("paired with %s (%s)", name, hostPort)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": msg, "server": hostPort})
}

// serverLabel names the server of the settings as the pages do.
func serverLabel(s config.Settings) string {
	if s.RemoteName != "" {
		return s.RemoteName
	}
	return net.JoinHostPort(s.RemoteServer, s.RemotePort)
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
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
	// Saves that had not reached the server are kept in the failed list,
	// where Settings offers Retry and Discard once the laptop is paired again.
	kept, err := h.st.FailAllOutbox("not sent before unpairing from " + serverLabel(s))
	if err != nil {
		log.Printf("outbox: %v", err)
	}
	h.sync.Reset()
	msg := "Standalone again."
	if kept > 0 {
		msg += fmt.Sprintf(" %s that had not reached the server %s kept: after pairing again, Settings lists them under could not be sent, to retry or discard.",
			plural(kept, "save"), wasWere(kept))
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
	n, err := h.st.RetryFailed(h.host)
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
