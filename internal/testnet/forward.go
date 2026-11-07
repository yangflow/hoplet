// Package testnet holds network helpers for tests.
package testnet

import (
	"io"
	"net"
	"sync"
	"testing"
)

// Forwarder relays loopback TCP connections to a target and can cut every
// relayed connection at once, as a network drop would.
type Forwarder struct {
	ln net.Listener

	mu     sync.Mutex
	target string
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

// NewForwarder listens on a loopback port; it stops when the test ends.
// Set the target before the first connection.
func NewForwarder(t testing.TB) *Forwarder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &Forwarder{ln: ln, conns: map[net.Conn]struct{}{}}
	f.wg.Go(f.accept)
	t.Cleanup(func() {
		_ = ln.Close()
		f.Cut()
		f.wg.Wait()
	})
	return f
}

// Addr is the forwarder's host:port.
func (f *Forwarder) Addr() string { return f.ln.Addr().String() }

// URL is the forwarder as a route.
func (f *Forwarder) URL() string { return "ws://" + f.Addr() }

// SetTarget sets the host:port new connections are relayed to.
func (f *Forwarder) SetTarget(addr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.target = addr
}

// Cut closes every relayed connection; new ones are still relayed.
func (f *Forwarder) Cut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		_ = c.Close()
		delete(f.conns, c)
	}
}

func (f *Forwarder) accept() {
	for {
		in, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		target := f.target
		f.mu.Unlock()
		out, err := net.Dial("tcp", target)
		if err != nil {
			_ = in.Close()
			continue
		}
		f.mu.Lock()
		f.conns[in], f.conns[out] = struct{}{}, struct{}{}
		f.mu.Unlock()
		pipe := func(dst, src net.Conn) {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
			_ = src.Close()
		}
		f.wg.Go(func() { pipe(out, in) })
		f.wg.Go(func() { pipe(in, out) })
	}
}
