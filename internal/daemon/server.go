package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yangflow/hoplet/internal/tools"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/artifact"
	"github.com/yangflow/hoplet/internal/frame"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/workspace"
	"github.com/yangflow/hoplet/proto"
)

// limits are the protocol's thresholds.
type limits struct {
	// gapEvents and gapBytes bound a gap that a session subscription
	// replays; a larger gap gets a snapshot of the last snapshotTurns turns.
	gapEvents     int
	gapBytes      int64
	snapshotTurns int
	// page is the most events any session item after the first carries (the
	// first replays a gap of up to gapEvents), and the most a page of older
	// events holds.
	page int
	// outbound is how many bytes may wait to be written to one connection
	// before it is closed as too slow; the client catches up by cursor.
	outbound int64
	// ping is the heartbeat interval; a connection that does not answer a
	// ping within pingTimeout is closed.
	ping, pingTimeout time.Duration
	// readLimit is the largest frame a client may send.
	readLimit int64
	// hello bounds how long a connection may stay without a hello, and
	// how long a relay may take to confirm a registration.
	hello time.Duration
	// relayRetry is the first wait before a relay is dialed again; each
	// later wait doubles up to relayRetryMax.
	relayRetry, relayRetryMax time.Duration
}

func (l limits) withDefaults() limits {
	def := limits{
		gapEvents: 500, gapBytes: 1 << 20, snapshotTurns: 3, page: 500,
		outbound: 8 << 20, ping: 15 * time.Second, pingTimeout: 10 * time.Second, readLimit: 1 << 20, hello: 10 * time.Second,
		relayRetry: time.Second, relayRetryMax: 30 * time.Second,
	}
	if l.gapEvents == 0 {
		l.gapEvents = def.gapEvents
	}
	if l.gapBytes == 0 {
		l.gapBytes = def.gapBytes
	}
	if l.snapshotTurns == 0 {
		l.snapshotTurns = def.snapshotTurns
	}
	if l.page == 0 {
		l.page = def.page
	}
	if l.outbound == 0 {
		l.outbound = def.outbound
	}
	if l.ping == 0 {
		l.ping = def.ping
	}
	if l.pingTimeout == 0 {
		l.pingTimeout = def.pingTimeout
	}
	if l.readLimit == 0 {
		l.readLimit = def.readLimit
	}
	if l.hello == 0 {
		l.hello = def.hello
	}
	if l.relayRetry == 0 {
		l.relayRetry = def.relayRetry
	}
	if l.relayRetryMax == 0 {
		l.relayRetryMax = def.relayRetryMax
	}
	return l
}

// server serves the client protocol.
type server struct {
	ctx     context.Context // ends every connection when canceled
	store   *store.Store
	mgr     *session.Manager
	ws      *workspace.Service
	queue   *queuer
	sys     *system
	arts    *artifact.Service
	tools   *tools.Set
	token   string
	host    string
	version string
	log     *slog.Logger
	lim     limits

	identity noise.Identity
	routes   []string         // written into pairing codes
	now      func() time.Time // the clock pairing codes expire by
	pair     pairing

	peersMu sync.Mutex
	peers   map[string]map[*conn]struct{} // remote connections by device

	mu     sync.Mutex
	closed bool // set by wait; no connection starts after it
	conns  sync.WaitGroup
	active atomic.Int64 // open connections
}

// ServeHTTP upgrades a request to the client protocol. A request carrying
// an Origin header comes from a browser page, which must never reach the
// daemon, so it is refused before the upgrade.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser requests are not accepted", http.StatusForbidden)
		return
	}
	if r.URL.Path != proto.APIPath {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Debug("accept", "err", err)
		return
	}
	ws.SetReadLimit(s.lim.readLimit)
	if !s.enter() {
		_ = ws.CloseNow()
		return
	}
	defer s.leave()
	newConn(s, frame.WebSocket(ws)).serve()
}

// enter counts a new connection, or reports false once wait has begun.
func (s *server) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns.Add(1)
	s.active.Add(1)
	return true
}

// leave uncounts a connection enter counted.
func (s *server) leave() {
	s.active.Add(-1)
	s.conns.Done()
}

// wait refuses new connections and waits for every connection to end;
// cancel s.ctx first.
func (s *server) wait() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.conns.Wait()
}
