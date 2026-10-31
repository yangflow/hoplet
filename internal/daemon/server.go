package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// limits are the protocol's thresholds.
type limits struct {
	// gapEvents and gapBytes bound a gap that a session subscription
	// replays; a larger gap gets a snapshot of the last snapshotTurns turns.
	gapEvents     int
	gapBytes      int64
	snapshotTurns int
	// page is the most events one session item carries.
	page int
	// outbound is how many bytes may wait to be written to one connection
	// before it is closed as too slow; the client catches up by cursor.
	outbound int64
	// ping is the heartbeat interval; a connection that does not answer a
	// ping within pingTimeout is closed.
	ping, pingTimeout time.Duration
	// readLimit is the largest frame a client may send.
	readLimit int64
}

func (l limits) withDefaults() limits {
	def := limits{
		gapEvents: 500, gapBytes: 1 << 20, snapshotTurns: 3, page: 500,
		outbound: 8 << 20, ping: 15 * time.Second, pingTimeout: 10 * time.Second, readLimit: 1 << 20,
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
	return l
}

// server serves the client protocol.
type server struct {
	ctx     context.Context // ends every connection when canceled
	store   *store.Store
	mgr     *session.Manager
	queue   *queuer
	token   string
	host    string
	version string
	log     *slog.Logger
	lim     limits

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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ws.CloseNow()
		return
	}
	s.conns.Add(1)
	s.mu.Unlock()
	defer s.conns.Done()
	s.active.Add(1)
	defer s.active.Add(-1)
	newConn(s, ws).serve()
}

// wait refuses new connections and waits for every connection to end;
// cancel s.ctx first.
func (s *server) wait() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.conns.Wait()
}
