// Package relay forwards Noise streams between hosts and the devices that
// reach them. A host keeps a control connection to the relay; for each
// device that asks for the host, the relay hands the host a one-time
// stream token over that connection, the host dials back with the token,
// and the relay joins the two connections, forwarding each message as it
// comes. The relay sees only ciphertext: the device and the host run their
// own Noise handshake through it.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/proto"
)

// Limits and timings of a relay.
const (
	// MaxStreams is how many streams, joined or waiting, a host may have.
	MaxStreams = 64
	// MaxWaiting is how many connections one client IP may have waiting
	// for their host to dial back.
	MaxWaiting = 8
	// AcceptWait is how long a client waits for its host to dial back.
	AcceptWait = 10 * time.Second
	// HandshakeWait bounds a control connection's handshake.
	HandshakeWait = 10 * time.Second
	// PingEvery is how often the relay pings each connection.
	PingEvery = 15 * time.Second
	// PingWait is how long a ping may go unanswered.
	PingWait = 10 * time.Second
	// MaxMessage bounds a forwarded message: one Noise message fits.
	MaxMessage = 65536
)

// controlMaxFrame bounds a frame on a control connection.
const controlMaxFrame = 4096

// Message types on a control connection, relay to host.
const (
	// TypeRegistered is the first message: the host is registered.
	TypeRegistered = "registered"
	// TypeStream announces a client; the host dials back with Stream.
	TypeStream = "stream"
)

// Message is one frame on a control connection.
type Message struct {
	Type   string `json:"type"`
	Stream string `json:"stream,omitempty"`
}

// Server is a relay. Its directory holds the allowlist, which is read
// again on every registration and every client connection.
type Server struct {
	// Dir is the relay's data directory.
	Dir string
	// Logger receives one line per registration and per stream; never a
	// payload. Nil discards.
	Logger *slog.Logger

	lim limits // zero fields take the defaults; tests shrink them

	mu      sync.Mutex
	hosts   map[noise.HostID]*host
	pending map[string]*pending // by stream token
	streams map[noise.HostID]int
	waiting map[string]int // by client IP
}

type limits struct {
	maxStreams, maxWaiting                         int
	acceptWait, handshakeWait, pingEvery, pingWait time.Duration
}

func (l limits) withDefaults() limits {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	if l.maxStreams == 0 {
		l.maxStreams = MaxStreams
	}
	if l.maxWaiting == 0 {
		l.maxWaiting = MaxWaiting
	}
	def(&l.acceptWait, AcceptWait)
	def(&l.handshakeWait, HandshakeWait)
	def(&l.pingEvery, PingEvery)
	def(&l.pingWait, PingWait)
	return l
}

// host is a registered control connection.
type host struct {
	id   noise.HostID
	ch   *noise.Channel
	done chan struct{} // closed when the control connection ends
}

// pending is a client waiting for its host to dial back.
type pending struct {
	got  chan *websocket.Conn // the host's connection, or nil; buffered
	done chan struct{}        // closed when the stream ends
}

// Serve accepts connections on ln until ctx ends, then closes every
// connection and returns once all have ended. With tlsConf set the
// connections are TLS; without it they are plain, for tests.
func (s *Server) Serve(ctx context.Context, ln net.Listener, tlsConf *tls.Config) error {
	s.lim = s.lim.withDefaults()
	s.mu.Lock()
	s.hosts = map[noise.HostID]*host{}
	s.pending = map[string]*pending{}
	s.streams = map[noise.HostID]int{}
	s.waiting = map[string]int{}
	s.mu.Unlock()
	if tlsConf != nil {
		ln = tls.NewListener(ln, tlsConf)
	}
	var wg sync.WaitGroup
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wg.Add(1)
			defer wg.Done()
			s.ServeHTTP(w, r)
		}),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.logger().Handler(), slog.LevelDebug),
	}
	stop := context.AfterFunc(ctx, func() { _ = srv.Close() })
	defer stop()
	err := srv.Serve(ln)
	// Hijacked connections outlive Close; their handlers end with ctx.
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("relay: serve: %w", err)
}

func (s *Server) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Logger
}

// ServeHTTP serves the relay's three endpoints.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "cross-origin requests are refused", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case proto.RelayHostPath:
		s.serveHost(w, r)
	case proto.RelayConnectPath:
		s.serveConnect(w, r)
	case proto.RelayAcceptPath:
		s.serveAccept(w, r)
	default:
		http.NotFound(w, r)
	}
}

var errNotAllowed = errors.New("the host is not in the relay's allowlist")

// serveHost runs a host's control connection.
func (s *Server) serveHost(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx := r.Context()
	log := s.logger()
	hctx, cancel := context.WithTimeout(ctx, s.lim.handshakeWait)
	var id noise.HostID
	ch, _, err := noise.Accept(hctx, noise.NewWSConn(ws), noise.AcceptConfig{
		Kinds:    []noise.Kind{noise.KindRelay},
		MaxFrame: controlMaxFrame,
		Allow: func(_ noise.Kind, peer, _ []byte) error {
			if id, err = noise.IDOf(peer); err != nil {
				return err
			}
			ok, err := allowed(s.Dir, id)
			if err != nil {
				log.Error("relay: read the allowlist", "err", err)
				return err
			}
			if !ok {
				return errNotAllowed
			}
			return nil
		},
	})
	cancel()
	if err != nil {
		if id != "" {
			log.Info("registration refused", "host", id, "err", err)
		}
		return
	}
	h := &host{id: id, ch: ch, done: make(chan struct{})}
	s.mu.Lock()
	old := s.hosts[id]
	s.hosts[id] = h
	s.mu.Unlock()
	if old != nil {
		_ = old.ch.Close()
	}
	defer func() {
		_ = ch.Close()
		s.mu.Lock()
		if s.hosts[id] == h {
			delete(s.hosts, id)
		}
		s.mu.Unlock()
		close(h.done)
		log.Info("host left", "host", id)
	}()
	if err := writeMessage(ctx, ch, Message{Type: TypeRegistered}); err != nil {
		return
	}
	log.Info("host registered", "host", id, "addr", r.RemoteAddr)
	ctx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go s.heartbeat(ctx, ch.Ping, func() { _ = ch.Close() })
	// A host sends only pings and pongs, which ReadFrame answers on its
	// own; any frame ends the connection.
	_, _ = ch.ReadFrame(ctx)
}

// heartbeat pings until ctx ends and calls fail when a ping goes
// unanswered.
func (s *Server) heartbeat(ctx context.Context, ping func(context.Context) error, fail func()) {
	t := time.NewTicker(s.lim.pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, s.lim.pingWait)
		err := ping(pctx)
		cancel()
		if err != nil {
			fail()
			return
		}
	}
}

func writeMessage(ctx context.Context, ch *noise.Channel, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := ch.WriteFrame(ctx, b); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	return nil
}

// serveConnect joins a client with the host it names once the host dials
// back.
func (s *Server) serveConnect(w http.ResponseWriter, r *http.Request) {
	id := noise.HostID(r.URL.Query().Get("host"))
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	log := s.logger()
	ok := ValidID(string(id))
	if ok {
		if ok, err = allowed(s.Dir, id); err != nil {
			log.Error("relay: read the allowlist", "err", err)
		}
	}
	s.mu.Lock()
	h := s.hosts[id]
	if !ok || h == nil {
		s.mu.Unlock()
		http.Error(w, "no such host on this relay", http.StatusNotFound)
		return
	}
	if s.streams[id] >= s.lim.maxStreams || s.waiting[ip] >= s.lim.maxWaiting {
		s.mu.Unlock()
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	token := rand.Text()
	p := &pending{got: make(chan *websocket.Conn, 1), done: make(chan struct{})}
	s.pending[token] = p
	s.streams[id]++
	s.waiting[ip]++
	s.mu.Unlock()
	defer close(p.done)
	defer func() {
		s.mu.Lock()
		if s.streams[id]--; s.streams[id] == 0 {
			delete(s.streams, id)
		}
		s.mu.Unlock()
	}()

	ctx := r.Context()
	client, err := websocket.Accept(w, r, nil)
	var back *websocket.Conn
	if err == nil {
		back = s.await(ctx, h, token, p)
	} else {
		s.take(token)
	}
	s.mu.Lock()
	if s.waiting[ip]--; s.waiting[ip] == 0 {
		delete(s.waiting, ip)
	}
	s.mu.Unlock()
	if err != nil {
		return
	}
	if back == nil {
		_ = client.CloseNow()
		log.Info("stream not answered", "host", id, "addr", r.RemoteAddr)
		return
	}
	start := time.Now()
	up, down := s.forward(ctx, client, back)
	log.Info("stream ended", "host", id, "addr", r.RemoteAddr, "up", up, "down", down, "duration", time.Since(start).Round(time.Millisecond))
}

// take removes the stream token and returns its pending stream, or nil.
func (s *Server) take(token string) *pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending[token]
	delete(s.pending, token)
	return p
}

// await tells the host about the stream and returns the connection it
// dials back with, or nil when it does not within the wait.
func (s *Server) await(ctx context.Context, h *host, token string, p *pending) *websocket.Conn {
	ctx, cancel := context.WithTimeout(ctx, s.lim.acceptWait)
	defer cancel()
	if writeMessage(ctx, h.ch, Message{Type: TypeStream, Stream: token}) == nil {
		select {
		case ws := <-p.got:
			return ws
		case <-ctx.Done():
		case <-h.done:
		}
	}
	if s.take(token) != nil {
		return nil
	}
	// The host's connection took the token and is being upgraded.
	return <-p.got
}

// serveAccept hands the host's connection to the client waiting on the
// stream token.
func (s *Server) serveAccept(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("stream")
	p := s.take(token)
	if p == nil {
		http.Error(w, "no such stream", http.StatusNotFound)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		p.got <- nil
		return
	}
	p.got <- ws
	select {
	case <-p.done:
	case <-r.Context().Done():
	}
}

// forward copies messages between a and b, one at a time each way, until
// either side ends, a message is not binary or a ping goes unanswered. It
// closes both and returns the bytes forwarded each way.
func (s *Server) forward(ctx context.Context, a, b *websocket.Conn) (ab, ba int64) {
	ctx, cancel := context.WithCancel(ctx)
	a.SetReadLimit(MaxMessage)
	b.SetReadLimit(MaxMessage)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); defer cancel(); ab = pump(ctx, a, b) }()
	go func() { defer wg.Done(); defer cancel(); ba = pump(ctx, b, a) }()
	go func() { defer wg.Done(); s.heartbeat(ctx, a.Ping, cancel) }()
	go func() { defer wg.Done(); s.heartbeat(ctx, b.Ping, cancel) }()
	<-ctx.Done()
	_ = a.CloseNow()
	_ = b.CloseNow()
	wg.Wait()
	return ab, ba
}

// pump copies binary messages from src to dst and returns the bytes
// copied.
func pump(ctx context.Context, src, dst *websocket.Conn) int64 {
	var n int64
	for {
		typ, p, err := src.Read(ctx)
		if err != nil || typ != websocket.MessageBinary {
			return n
		}
		if dst.Write(ctx, websocket.MessageBinary, p) != nil {
			return n
		}
		n += int64(len(p))
	}
}
