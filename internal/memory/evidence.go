package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// checkEvidence returns when each piece of a write's evidence was recorded.
// A non-empty problem says why the evidence does not hold up.
func (s *Service) checkEvidence(ctx context.Context, w Write) (times []time.Time, problem string, err error) {
	if len(w.Evidence) == 0 {
		return nil, "a write needs at least one piece of evidence", nil
	}
	times = make([]time.Time, 0, len(w.Evidence))
	for _, ev := range w.Evidence {
		at, problem, err := s.checkOne(ctx, w, ev)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ev.Ref + " does not exist", nil
		}
		if err != nil || problem != "" {
			return nil, problem, err
		}
		times = append(times, at)
	}
	return times, "", nil
}

func (s *Service) checkOne(ctx context.Context, w Write, ev Evidence) (time.Time, string, error) {
	kind, rest, _ := strings.Cut(ev.Ref, ":")
	i := strings.LastIndex(rest, "/")
	if i <= 0 {
		return time.Time{}, "bad reference " + ev.Ref, nil
	}
	owner, number := rest[:i], rest[i+1:]
	switch kind {
	case "event":
		seq, err := strconv.ParseUint(number, 10, 64)
		if err != nil || seq == 0 {
			return time.Time{}, "bad reference " + ev.Ref, nil
		}
		return s.checkEvent(ctx, w, ev, proto.SessionID(owner), seq)
	case "artifact":
		version, err := strconv.Atoi(number)
		if err != nil || version < 1 {
			return time.Time{}, "bad reference " + ev.Ref, nil
		}
		if ev.Kind == EvidenceUserStated {
			return time.Time{}, ev.Ref + " is an artifact, not something the user said", nil
		}
		a, err := s.store.ArtifactVersion(ctx, proto.ArtifactID(owner), version)
		if err != nil {
			return time.Time{}, "", err
		}
		if a.Session != w.source() {
			return time.Time{}, ev.Ref + " belongs to another session", nil
		}
		return a.UpdatedAt, "", nil
	}
	return time.Time{}, "bad reference " + ev.Ref, nil
}

func (s *Service) checkEvent(ctx context.Context, w Write, ev Evidence, session proto.SessionID, seq uint64) (time.Time, string, error) {
	if session != w.source() {
		return time.Time{}, ev.Ref + " belongs to another session", nil
	}
	if w.Actor == ActorExtraction && (seq < w.Batch.From || seq > w.Batch.To) {
		return time.Time{}, ev.Ref + " is outside the batch", nil
	}
	e, err := s.store.EventBySeq(ctx, session, seq)
	if err != nil {
		return time.Time{}, "", err
	}
	if ev.Kind != EvidenceUserStated {
		return e.Time, "", nil
	}
	p, err := e.Decode()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("memory: decode %s: %w", ev.Ref, err)
	}
	m, ok := p.(proto.Message)
	if !ok || m.Role != proto.RoleUser || m.From != "" || e.Agent != "" {
		return time.Time{}, ev.Ref + " is not something the user said", nil
	}
	return e.Time, "", nil
}
