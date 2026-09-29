package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientReadinessRejectsServerBehindRelay(t *testing.T) {
	var clientStatus atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			w.Write([]byte(`{"whoami":"TAM Server"}`))
			return
		}
		if r.URL.Path == "/api/status" && clientStatus.Load() {
			w.Write([]byte(`{"state":"connected"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	relay, err := newRelay(server.Listener.Addr().String(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.close()
	base := "http://127.0.0.1:" + relay.port()
	serverProgram := &program{url: base}
	clientProgram := &program{url: base, readyPath: "/api/status"}
	if !serverProgram.answers() {
		t.Fatal("server root did not answer through relay")
	}
	if clientProgram.answers() {
		t.Fatal("relay forwarding server root was mistaken for a listening client")
	}
	clientStatus.Store(true)
	if !clientProgram.answers() {
		t.Fatal("client-specific status was not accepted when root still reports the proxied server")
	}
}

func TestPrepareClientsAllocatesPortsAfterRelays(t *testing.T) {
	run := &test{o: options{clients: 50, seed: 1}, work: t.TempDir(), server: &program{host: "127.0.0.1", port: "1"}}
	if err := run.prepareClients("unused-client-binary"); err != nil {
		t.Fatal(err)
	}
	for _, client := range run.clients {
		defer client.relay.close()
	}
	seen := map[string]bool{}
	for _, client := range run.clients {
		port := client.relay.port()
		if seen[port] {
			t.Fatalf("duplicate relay port %s", port)
		}
		seen[port] = true
	}
	for _, client := range run.clients {
		port := client.prog.port
		if seen[port] {
			t.Fatalf("client %s shares allocated port %s with another client or relay", client.prog.name, port)
		}
		seen[port] = true
		if client.prog.readyPath != "/api/status" {
			t.Fatal("client did not get client-specific readiness")
		}
		listener, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			t.Fatalf("allocated client port is occupied: %v", err)
		}
		defer listener.Close()
	}
}
