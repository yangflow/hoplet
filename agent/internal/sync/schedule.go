package sync

import (
	"context"
	"sort"
	"time"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

const (
	// roundBytesPerSession bounds the raw bytes one session sends in one Reconcile round. A session over the
	// bound continues in the next round, which starts at once; a single line may exceed it.
	roundBytesPerSession = 16 << 20
	// activeWindow is how long after its last growth a session is sent before the others.
	activeWindow = 30 * time.Second
)

// scheduler orders sessions for a round and remembers lines that cannot be synced. It holds no state the
// server cursors do not imply: losing it only forgets which sessions grew recently and what was reported.
type scheduler struct {
	seen    map[string]seenSize
	blocked map[string]int64 // sessionId -> offset of a line above the limit
}

type seenSize struct {
	size   int64
	grewAt time.Time
}

// order puts sessions that grew within activeWindow first, most recent first, and keeps the scan order for
// the rest. A session's first sighting is not growth, so a large import queues behind active sessions.
func (s *scheduler) order(ctx context.Context, sources []provider.Source, now time.Time) []provider.Source {
	if s.seen == nil {
		s.seen = map[string]seenSize{}
	}
	next := make(map[string]seenSize, len(sources))
	for _, src := range sources {
		id := src.ID()
		cur := seenSize{grewAt: s.seen[id].grewAt}
		if size, err := src.Size(ctx); err == nil && size >= 0 {
			cur.size = size
			if prev, ok := s.seen[id]; ok && size > prev.size {
				cur.grewAt = now
			}
		}
		next[id] = cur
	}
	s.seen = next
	for id := range s.blocked {
		if _, ok := next[id]; !ok {
			delete(s.blocked, id)
		}
	}
	out := append([]provider.Source(nil), sources...)
	active := func(src provider.Source) (time.Time, bool) {
		at := next[src.ID()].grewAt
		return at, !at.IsZero() && now.Sub(at) <= activeWindow
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, iok := active(out[i])
		aj, jok := active(out[j])
		if iok != jok {
			return iok
		}
		return iok && ai.After(aj)
	})
	return out
}

// block records the offset of a line above the limit and reports whether it is new.
func (s *scheduler) block(id string, offset int64) bool {
	if s.blocked == nil {
		s.blocked = map[string]int64{}
	}
	if prev, ok := s.blocked[id]; ok && prev == offset {
		return false
	}
	s.blocked[id] = offset
	return true
}

func (s *scheduler) blockedAt(id string, cursor int64) bool {
	off, ok := s.blocked[id]
	return ok && off == cursor
}
