package main

import (
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// relay stands between one client and the server, like that client's
// Wi-Fi. While the link is up it passes bytes both ways. While it is down
// nothing passes and nothing is refused: requests hang, as they do when a
// client walks out of range. What was sent meanwhile is held, as TCP holds
// it for retransmission, and delivered after the link is back, up to late
// after it: a request its client gave up on can still reach the server,
// even after newer ones.
type relay struct {
	ln     net.Listener
	target string // the server, host:port
	late   time.Duration

	mu   sync.Mutex
	rng  *rand.Rand
	down bool
	back chan struct{} // closed when the link is up again
}

func newRelay(target string, late time.Duration, seed uint64) (*relay, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &relay{ln: ln, target: target, late: late, rng: rand.New(rand.NewPCG(seed, 99)), back: make(chan struct{})}
	close(r.back)
	go r.serve()
	return r, nil
}

// port is where the client connects instead of the server.
func (r *relay) port() string {
	_, port, _ := net.SplitHostPort(r.ln.Addr().String())
	return port
}

// setDown takes the link down or brings it back.
func (r *relay) setDown(down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if down == r.down {
		return
	}
	r.down = down
	if down {
		r.back = make(chan struct{})
	} else {
		close(r.back)
	}
}

// hold waits while the link is down. Data held through an outage arrives a
// random moment up to late after the link is back, as retransmissions do.
func (r *relay) hold() {
	r.mu.Lock()
	down, back := r.down, r.back
	r.mu.Unlock()
	if !down {
		return
	}
	<-back
	r.mu.Lock()
	wait := time.Duration(r.rng.Int64N(int64(r.late) + 1))
	r.mu.Unlock()
	time.Sleep(wait)
}

func (r *relay) serve() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		go r.handle(c)
	}
}

// handle carries one connection. A connection opened while the link is
// down reaches the server only once it is back, with what the client sent
// in the meantime.
func (r *relay) handle(client net.Conn) {
	defer client.Close()
	r.hold()
	server, err := net.DialTimeout("tcp", r.target, 10*time.Second)
	if err != nil {
		return
	}
	defer server.Close()
	done := make(chan struct{}, 2)
	go r.pump(server, client, done)
	go r.pump(client, server, done)
	<-done
	<-done
}

// pump copies src to dst, holding each piece while the link is down. When
// src has no more to send, dst is told so, and the far side still gets
// everything src sent before.
func (r *relay) pump(dst, src net.Conn, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			r.hold()
			if _, werr := dst.Write(buf[:n]); werr != nil {
				src.Close()
				return
			}
		}
		if err != nil {
			break
		}
	}
	if tcp, ok := dst.(*net.TCPConn); ok {
		tcp.CloseWrite()
	} else {
		dst.Close()
	}
}

func (r *relay) close() { r.ln.Close() }
