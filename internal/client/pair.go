package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
		out := map[string]string{"mode": "standalone"}
		if st.SettingsError != "" {
			out["settings_error"] = st.SettingsError
		}
		httpx.WriteJSON(w, http.StatusOK, out)
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
// addresses is listed once, at the address this client shares a network
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
// Saves still queued belong to this event and follow the new connection,
// including a replacement server, a renewed key or a new certificate.
//
// Pairing with another server also saves a backup of this client's local
// data in its data folder (see keepLocalData). Reading shared server rows
// does not replace the entries kept on this client.
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
	client, err := h.st.ClientName(h.host)
	if err != nil {
		httpx.WriteInternal(w, err)
		return
	}
	if res, err := rc.WithKey(key.AuthKey).Handshake(client); err != nil || !res.OK() {
		if err != nil {
			h.unreachableAt(w, hostPort, err)
		} else {
			forward(w, res)
		}
		return
	}

	kept := ""
	// The event queue follows the connection; a different address or server
	// name does not make the volunteer's pending edits a different dataset.
	if err := h.sync.Reconfigure(func() error {
		// Reconfigure holds Numbering and Sending: finish any preceding save
		// before taking the backup and keep that snapshot with this switch.
		prev := h.settings()
		same := prev.RemoteURL() != "" &&
			(prev.RemoteServer == req.Host && prev.RemotePort == req.Port || prev.RemoteName != "" && prev.RemoteName == name)
		if !same {
			kept = h.keepLocalData(name)
		}
		_, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
			cur.RemoteServer, cur.RemotePort, cur.RemoteTLS = req.Host, req.Port, req.TLS
			cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = key.AuthKey, name, fingerprint
			return config.Normalize(cur), nil
		})
		return err
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	msg := "Paired with " + name + "." + kept
	if waiting, _, err := h.st.OutboxCounts(); err == nil && waiting > 0 {
		msg += " " + plural(waiting, "save") + " waiting for this event will be sent to the server."
	}
	h.sync.Kick()
	log.Printf("paired with %s (%s)", name, hostPort)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": msg, "server": hostPort})
}

// keepLocalData saves this client's entries to a backup in its data folder
// before pairing with another server. The entries also remain in its local
// database. Backup and Restore can load the file or send it to the server.
// It returns the sentence the pairing's message adds, or "" for no data.
// The caller holds the client's Numbering lock.
func (h *handler) keepLocalData(server string) string {
	if err := h.st.MaterializeIntents(); err != nil {
		log.Printf("pair: retaining this client's pending data: %v", err)
		return fmt.Sprintf(" This client's data could not be backed up before pairing (%v); its saved entries and pending requests remain in its data folder.", err)
	}
	bf, err := h.st.ExportClientBackup()
	if err != nil {
		log.Printf("pair: keeping this client's data: %v", err)
		return fmt.Sprintf(" This client's data could not be backed up before pairing (%v); it remains in this client's local database.", err)
	}
	if len(bf.Prefixes)+len(bf.Tickets)+len(bf.Baskets)+len(bf.DeletedPrefixes)+len(bf.Conflicts)+len(bf.Revisions) == 0 {
		return ""
	}
	name := "before-pairing-" + time.Now().Format("20060102-150405") + ".json"
	// Keep conflict payloads in their native encoding, as API downloads do.
	data, err := json.Marshal(bf)
	if err == nil {
		err = os.WriteFile(filepath.Join(h.dataDir, name), data, 0o600)
	}
	what := fmt.Sprintf("%s, %s and %s", count(len(bf.Prefixes), "prefix", "prefixes"), plural(len(bf.Tickets), "ticket"), plural(len(bf.Baskets), "basket"))
	if err != nil {
		log.Printf("pair: keeping this client's data (%s): %v", what, err)
		return fmt.Sprintf(" This client's own data (%s) could not be backed up before pairing with %s (%v); it remains in this client's local database.", what, server, err)
	}
	log.Printf("pair: this client's own data (%s) saved to %s before pairing with %s", what, name, server)
	return fmt.Sprintf(" This client's own data (%s) was saved to %s in its data folder first; Backup and Restore can load it again or send it to the server.", what, name)
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
// the client's key is also deleted on the server, when it can be reached.
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
	if err := h.sync.Reconfigure(func() error {
		_, err := h.cfg.Update(func(cur config.Settings) (config.Settings, error) {
			cur.RemoteServer, cur.RemoteKey, cur.RemoteName, cur.RemoteFingerprint = "", "", "", ""
			return cur, nil
		})
		return err
	}); err != nil {
		httpx.WriteInternal(w, fmt.Errorf("save settings: %w", err))
		return
	}
	// Pause delivery without classifying valid event edits as failures.
	kept, _, err := h.st.OutboxCounts()
	if err != nil {
		log.Printf("outbox: %v", err)
	}
	msg := "Standalone again."
	if kept > 0 {
		msg += " " + plural(kept, "save") + " waiting for this event will resume when a server is configured again."
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
	done := h.sync.Numbering() // renumbered in line with the saves being made
	n, err := h.st.RetryFailed(h.host)
	done()
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

// count is plural for a noun whose plural is not noun+"s".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
