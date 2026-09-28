package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSweepFindsServersWithoutMulticast(t *testing.T) {
	goServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"whoami":"TAM Server","authenticated":false,"healthy":true,"name":"front-desk","version":"0.0.1"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer goServer.Close()
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"whoami":"TAM Server","authenticated":false,"healthy":true}`))
	}))
	defer original.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>hello</html>`))
	}))
	defer other.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadPort := port(t, dead.URL)
	dead.Close()

	ports := []portSpec{{port(t, goServer.URL), false}, {port(t, original.URL), false}, {port(t, other.URL), false}, {deadPort, false}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := sweepHosts(ctx, []string{"127.0.0.1"}, ports)
	if len(got) != 2 {
		t.Fatalf("found %+v, want the two TAM servers", got)
	}
	// Sorted by name: the original has no name and takes its host.
	if got[0].Name != "127.0.0.1" || got[0].Port != port(t, original.URL) || got[0].Version != "" {
		t.Fatalf("original = %+v", got[0])
	}
	if got[1].Name != "front-desk" || got[1].Host != "127.0.0.1" || got[1].Port != port(t, goServer.URL) || got[1].TLS || got[1].Version != "0.0.1" {
		t.Fatalf("go server = %+v", got[1])
	}
}

// addrNet is what an interface reports: its own address with the mask.
func addrNet(ip string, bits int) *net.IPNet {
	return &net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(bits, 32)}
}

func TestSweepTargetsAreTheLocal24(t *testing.T) {
	wide := addrNet("10.20.30.40", 16)
	small := addrNet("192.168.1.9", 24)
	dup := addrNet("192.168.1.200", 24)
	hosts := sweepTargets([]*net.IPNet{wide, small, dup})
	if len(hosts) != 2*254 {
		t.Fatalf("%d hosts, want two /24s worth", len(hosts))
	}
	if hosts[0] != "10.20.30.1" || hosts[253] != "10.20.30.254" || hosts[254] != "192.168.1.1" {
		t.Fatalf("unexpected order: %s ... %s, %s", hosts[0], hosts[253], hosts[254])
	}
}

func TestDedupeCollapsesOneServerOnSeveralNetworks(t *testing.T) {
	got := dedupe([]Server{
		{Name: "front-desk", Host: "192.168.1.10", Port: "8000"},
		{Name: "front-desk", Host: "10.0.0.10", Port: "8000"},
		{Name: "front-desk", Host: "192.168.1.10", Port: "8443", TLS: true},
		{Name: "10.0.0.20", Host: "10.0.0.20", Port: "8000"},
		{Name: "10.0.0.21", Host: "10.0.0.21", Port: "8000"},
	})
	if len(got) != 4 {
		t.Fatalf("got %+v, want the named server twice (plain and TLS) and both unnamed ones", got)
	}
	if got[2].Name != "front-desk" || got[2].Host != "10.0.0.10" || got[2].TLS {
		t.Fatalf("the plain entry keeps the lowest address: %+v", got[2])
	}
	if got[3].Name != "front-desk" || !got[3].TLS {
		t.Fatalf("the TLS entry stays separate: %+v", got[3])
	}
}

func TestSweepStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	got := sweepHosts(ctx, sweepTargets([]*net.IPNet{mustCIDR("10.99.0.1/24")}), standardPorts)
	if len(got) != 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("a cancelled sweep must return at once and empty: %v after %s", got, time.Since(start))
	}
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func port(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

// A Docker or VM host has a bridge per container network; none of them
// reaches a client, so they are neither listed as reachable nor swept.
func TestSkipInterfaceLeavesOutContainerAndVMBridges(t *testing.T) {
	for _, name := range []string{"docker0", "br-53de69a58e2a", "virbr0", "veth1a2b3c", "lxcbr0", "cni0", "flannel.1", "podman1", "vEthernet (WSL (Hyper-V firewall))", "vEthernet (Default Switch)"} {
		if !skipInterface(name) {
			t.Errorf("%q must be skipped", name)
		}
	}
	for _, name := range []string{"eth0", "br0", "enp4s0f1", "wlan0", "Wi-Fi", "Ethernet 2", "vEthernet (External)", "tailscale0"} {
		if skipInterface(name) {
			t.Errorf("%q must be kept", name)
		}
	}
}

// One server announced on two networks is one entry, at the address the
// client shares a network with; without such an address the lowest wins.
func TestCollapsePrefersAnAddressOnTheClientsNetwork(t *testing.T) {
	both := []Server{
		{Name: "front-desk", Host: "192.168.50.10", Port: "8000"},
		{Name: "front-desk", Host: "10.1.2.112", Port: "8000"},
		{Name: "10.1.2.119", Host: "10.1.2.119", Port: "8000"},
	}
	got := Collapse(both, []*net.IPNet{addrNet("10.1.2.50", 24)})
	if len(got) != 2 || got[1].Name != "front-desk" || got[1].Host != "10.1.2.112" {
		t.Fatalf("with the client on 10.1.2.0/24: %+v", got)
	}
	got = Collapse(both, nil)
	if len(got) != 2 || got[1].Host != "10.1.2.112" {
		t.Fatalf("without a shared network the lowest address wins: %+v", got)
	}
	got = Collapse(both, []*net.IPNet{addrNet("192.168.50.7", 24)})
	if len(got) != 2 || got[1].Host != "192.168.50.10" {
		t.Fatalf("with the client on 192.168.50.0/24: %+v", got)
	}
}
