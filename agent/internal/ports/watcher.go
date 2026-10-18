package ports

import (
	"context"
	"log"
	"reflect"
	"sort"
	"sync"
	"time"
)

// Defaults of ADR 0026: a scan every 3 seconds, a session reported at most once a second.
const (
	DefaultInterval = 3 * time.Second
	DefaultMinGap   = time.Second
	// MaxWatched is the most sessions one watch holds (the wire limit).
	MaxWatched  = 64
	scanTimeout = 10 * time.Second
)

// Config wires a Watcher to the machine and to the core connection.
type Config struct {
	Scanner Scanner
	// Dir is the working directory of a session; an error skips the session.
	Dir func(sessionID string) (string, error)
	// Roots are the process IDs the session's own processes descend from (its Claude Code process, its
	// terminal shell); 0 ones are ignored. They are never reported.
	Roots func(sessionID string) []int
	// Send reports the ports of a session. A message sent while the core is away is lost, and the next
	// watch reports again.
	Send func(sessionID string, ports []Port)

	Interval time.Duration
	MinGap   time.Duration
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

type state struct {
	last     []Port
	reported bool
	at       time.Time
}

// Watcher scans while at least one session is watched and reports the ports of each watched session when they
// change.
type Watcher struct {
	cfg  Config
	kick chan struct{}

	mu      sync.Mutex
	watched map[string]*state
}

// New returns a Watcher with the defaults filled in.
func New(cfg Config) *Watcher {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.MinGap <= 0 {
		cfg.MinGap = DefaultMinGap
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	return &Watcher{cfg: cfg, kick: make(chan struct{}, 1), watched: map[string]*state{}}
}

// Watch replaces the set of watched sessions. A session that stays keeps what was last reported; a new one is
// reported by the next scan, which starts at once. An empty set stops the scan.
func (w *Watcher) Watch(sessionIDs []string) {
	w.mu.Lock()
	next := make(map[string]*state, len(sessionIDs))
	added := false
	for _, id := range sessionIDs {
		if len(next) >= MaxWatched {
			break
		}
		if s := w.watched[id]; s != nil {
			next[id] = s
		} else if id != "" {
			next[id] = &state{}
			added = true
		}
	}
	w.watched = next
	w.mu.Unlock()
	if added {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// Run scans until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
		}
		w.Tick(ctx)
	}
}

// Tick scans once when a session is watched and reports the sessions whose ports changed. A session whose
// ports changed less than MinGap after its last report waits for the next tick.
func (w *Watcher) Tick(ctx context.Context) {
	w.mu.Lock()
	ids := make([]string, 0, len(w.watched))
	for id := range w.watched {
		ids = append(ids, id)
	}
	w.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)

	sctx, cancel := context.WithTimeout(ctx, scanTimeout)
	snap, err := w.cfg.Scanner.Scan(sctx)
	cancel()
	if err != nil {
		w.cfg.Logf("[ports] scan: %v", err)
		return
	}
	for _, id := range ids {
		dir, err := w.cfg.Dir(id)
		if err != nil || dir == "" {
			continue
		}
		var roots []int
		if w.cfg.Roots != nil {
			roots = w.cfg.Roots(id)
		}
		found := Select(snap, dir, roots)
		now := w.cfg.Now()

		w.mu.Lock()
		s := w.watched[id]
		send := s != nil && (!s.reported || (!reflect.DeepEqual(s.last, found) && now.Sub(s.at) >= w.cfg.MinGap))
		if send {
			s.last, s.reported, s.at = found, true, now
		}
		w.mu.Unlock()
		if send {
			w.cfg.Send(id, found)
		}
	}
}
