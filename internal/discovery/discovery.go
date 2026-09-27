// Package discovery lets a tam-client find tam-servers on the venue
// network. The server announces itself over mDNS as _tam._tcp; the client
// browses for a moment and lists what answered. Some access points block
// multicast, so typing the address by hand always remains possible.
package discovery

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

const service = "_tam._tcp"

// Server is one announced tam-server.
type Server struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    string `json:"port"`
	TLS     bool   `json:"tls"`
	Version string `json:"version"`
}

// Announce registers this server on the local network until ctx ends.
func Announce(ctx context.Context, name string, port int, useTLS bool, version string) error {
	txt := []string{
		"port=" + strconv.Itoa(port),
		"tls=" + strconv.FormatBool(useTLS),
		"name=" + name,
		"v=" + version,
	}
	srv, err := zeroconf.Register(name, service, "local.", port, txt, nil)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()
	return nil
}

// Browse listens for announcements for wait and returns the servers that
// answered, sorted by name. It returns an empty list, not an error, when
// nothing answered.
func Browse(ctx context.Context, wait time.Duration) ([]Server, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 32)
	if err := zeroconf.Browse(ctx, service, "local.", entries); err != nil {
		return nil, err
	}
	seen := map[string]Server{}
	for {
		select {
		case <-ctx.Done():
			return sorted(seen), nil
		case e, ok := <-entries:
			if !ok {
				return sorted(seen), nil
			}
			s := fromEntry(e)
			if s.Host == "" {
				continue
			}
			seen[net.JoinHostPort(s.Host, s.Port)] = s
		}
	}
}

func fromEntry(e *zeroconf.ServiceEntry) Server {
	s := Server{Name: e.Instance, Port: strconv.Itoa(e.Port)}
	for _, kv := range e.Text {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "port":
			s.Port = v
		case "tls":
			s.TLS = v == "true"
		case "name":
			s.Name = v
		case "v":
			s.Version = v
		}
	}
	switch {
	case len(e.AddrIPv4) > 0:
		s.Host = e.AddrIPv4[0].String()
	case len(e.AddrIPv6) > 0:
		s.Host = e.AddrIPv6[0].String()
	default:
		s.Host = strings.TrimSuffix(e.HostName, ".")
	}
	return s
}

func sorted(seen map[string]Server) []Server {
	out := make([]Server, 0, len(seen))
	for _, s := range seen {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Host < out[j].Host
	})
	return out
}
