package discovery

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Some venue access points drop multicast, which silences mDNS. Sweep is
// the fallback: it asks the standard ports on every address of the local
// /24 networks whether a TAM server lives there. Plain unicast works on
// those networks, so this finds the server when the announcement cannot.

const (
	probeTimeout  = 300 * time.Millisecond
	sweepWorkers  = 256
	maxSweepHosts = 1024
)

type portSpec struct {
	port string
	tls  bool
}

// standardPorts are the defaults of tam-server: 8000 plain, 8443 with -tls.
var standardPorts = []portSpec{{"8000", false}, {"8443", true}}

// LocalNetworks returns this machine's IPv4 addresses with their networks,
// leaving out loopback and interfaces that are down.
func LocalNetworks() []*net.IPNet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, n)
		}
	}
	return out
}

// LocalAddresses returns this machine's IPv4 addresses as strings, for a
// server to print and show so a client can be pointed at it by hand.
func LocalAddresses() []string {
	var out []string
	for _, n := range LocalNetworks() {
		out = append(out, n.IP.To4().String())
	}
	sort.Strings(out)
	return out
}

// sweepTargets lists the addresses to probe: the /24 around each local
// address (a wider network would take too long), capped at maxSweepHosts.
func sweepTargets(nets []*net.IPNet) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range nets {
		ip := n.IP.To4()
		if ip == nil {
			continue
		}
		base := ip.Mask(net.CIDRMask(24, 32))
		for i := 1; i < 255 && len(out) < maxSweepHosts; i++ {
			h := net.IPv4(base[0], base[1], base[2], byte(i)).String()
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	return out
}

// Sweep probes the standard ports on every address of the local /24
// networks and returns the TAM servers that answered, sorted by name. It
// takes about a second and a half on a quiet network.
func Sweep(ctx context.Context) []Server {
	return sweepHosts(ctx, sweepTargets(LocalNetworks()), standardPorts)
}

func sweepHosts(ctx context.Context, hosts []string, ports []portSpec) []Server {
	client := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{
			DialContext:       (&net.Dialer{Timeout: probeTimeout}).DialContext,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // only asking who is there
			DisableKeepAlives: true,
		},
	}
	type job struct {
		host string
		spec portSpec
	}
	jobs := make(chan job)
	found := make(chan Server, len(hosts)*len(ports)+1)
	var wg sync.WaitGroup
	for i := 0; i < sweepWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if s, ok := probe(ctx, client, j.host, j.spec); ok {
					found <- s
				}
			}
		}()
	}
	for _, h := range hosts {
		for _, p := range ports {
			select {
			case <-ctx.Done():
				h = ""
			case jobs <- job{h, p}:
			}
			if h == "" {
				break
			}
		}
	}
	close(jobs)
	wg.Wait()
	close(found)
	var all []Server
	for s := range found {
		all = append(all, s)
	}
	return dedupe(all)
}

// dedupe keeps one entry per server. A server that listens on every
// interface of a machine with several networks answers on each of them,
// so entries with the same name, port and TLS setting collapse into the
// one with the lowest address; a server that gave no name (the original)
// is only known by its address and stays as it is.
func dedupe(all []Server) []Server {
	seen := map[string]Server{}
	for _, s := range all {
		key := net.JoinHostPort(s.Host, s.Port)
		if s.Name != s.Host {
			key = s.Name + "|" + s.Port + "|" + map[bool]string{true: "tls", false: "plain"}[s.TLS]
		}
		if prev, ok := seen[key]; !ok || s.Host < prev.Host {
			seen[key] = s
		}
	}
	return sorted(seen)
}

// probe asks one address and port whether a TAM server answers there.
func probe(ctx context.Context, client *http.Client, host string, spec portSpec) (Server, bool) {
	scheme := "http"
	if spec.tls {
		scheme = "https"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+net.JoinHostPort(host, spec.port)+"/api", nil)
	if err != nil {
		return Server{}, false
	}
	res, err := client.Do(req)
	if err != nil {
		return Server{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Server{}, false
	}
	var doc struct {
		Whoami  string `json:"whoami"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&doc) != nil || doc.Whoami != "TAM Server" {
		return Server{}, false
	}
	name := doc.Name
	if name == "" {
		name = host
	}
	return Server{Name: name, Host: host, Port: spec.port, TLS: spec.tls, Version: doc.Version}, true
}
