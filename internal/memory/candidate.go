package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/internal/store"
)

// Candidate is a proposed change waiting for the user.
type Candidate struct {
	Entry           Entry
	Action          Action
	Target          string
	ExpectedVersion int
	Actor           Actor
	Reason          string
	CreatedAt       time.Time
}

// Candidates returns the undecided candidates, oldest first. One whose file
// has become unreadable since the last scan is left out.
func (s *Service) Candidates(ctx context.Context) ([]Candidate, error) {
	rows, err := s.store.OpenMemoryCandidates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, c := range rows {
		e, err := s.Get(ctx, c.EntryID)
		if err != nil {
			s.log.WarnContext(ctx, "memory: skip candidate", "id", c.EntryID, "err", err)
			continue
		}
		out = append(out, Candidate{
			Entry: e, Action: Action(c.Action), Target: c.TargetID, ExpectedVersion: c.ExpectedVersion,
			Actor: Actor(c.Actor), Reason: c.Reason, CreatedAt: c.CreatedAt,
		})
	}
	return out, nil
}

// Accept carries out a candidate as the user and returns the entry that took
// effect. A target that has changed since the candidate was made is
// store.ErrConflict and leaves the candidate open. Repeating opID finishes an
// interrupted accept and writes nothing already written.
func (s *Service) Accept(ctx context.Context, id, opID string, scope *Scope) (Entry, error) {
	if scope != nil && !validScope(*scope) {
		return Entry{}, invalid("scope %q with value %q", scope.Kind, scope.Value)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, done, result, err := s.decision(ctx, id, opID)
	if done || err != nil {
		return result, err
	}
	reason := "accepted candidate " + id
	switch Action(c.Action) {
	case ActionCreate:
		result, err = s.step(ctx, opID+"-1", func() (Op, error) {
			e, err := s.Get(ctx, id)
			if err != nil {
				return Op{}, err
			}
			if scope != nil {
				e.Scope = *scope
			}
			e.Status = StatusActive
			return Op{Entry: e, ExpectedVersion: e.Version, Reason: reason}, nil
		})
	case ActionUpdate:
		result, err = s.acceptOnTarget(ctx, c, opID, reason, func(target *Entry, proposal Entry) {
			target.Status, target.Evidence, target.Body, target.Scope = StatusActive, proposal.Evidence, proposal.Body, proposal.Scope
			if scope != nil {
				target.Scope = *scope
			}
		})
	case ActionWithdraw:
		if scope != nil {
			return Entry{}, invalid("a withdrawal has no scope to adjust")
		}
		result, err = s.acceptOnTarget(ctx, c, opID, reason, func(target *Entry, _ Entry) {
			target.Status = StatusWithdrawn
		})
	default:
		err = fmt.Errorf("memory: candidate %s has action %q", id, c.Action)
	}
	if err != nil {
		return Entry{}, err
	}
	return result, s.store.DecideMemoryCandidate(ctx, id, store.MemoryCandidateAccepted)
}

// acceptOnTarget writes a candidate's change to its target, then retires the
// candidate in favor of the target.
func (s *Service) acceptOnTarget(ctx context.Context, c store.MemoryCandidate, opID, reason string, change func(target *Entry, proposal Entry)) (Entry, error) {
	result, err := s.step(ctx, opID+"-1", func() (Op, error) {
		proposal, err := s.Get(ctx, c.EntryID)
		if err != nil {
			return Op{}, err
		}
		target, err := s.Get(ctx, c.TargetID)
		if err != nil {
			return Op{}, err
		}
		change(&target, proposal)
		target.ReplacedBy = ""
		return Op{Entry: target, ExpectedVersion: c.ExpectedVersion, Reason: reason}, nil
	})
	if err != nil {
		return Entry{}, err
	}
	_, err = s.step(ctx, opID+"-2", func() (Op, error) {
		e, err := s.Get(ctx, c.EntryID)
		if err != nil {
			return Op{}, err
		}
		e.Status, e.ReplacedBy = StatusSuperseded, c.TargetID
		return Op{Entry: e, ExpectedVersion: e.Version, Reason: reason}, nil
	})
	if err != nil {
		return Entry{}, err
	}
	return result, nil
}

// Reject withdraws a candidate as the user, so that the same content is
// refused if it comes back. Repeating opID writes nothing already written.
func (s *Service) Reject(ctx context.Context, id, opID string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, done, result, err := s.decision(ctx, id, opID)
	if done || err != nil {
		return result, err
	}
	result, err = s.step(ctx, opID+"-1", func() (Op, error) {
		e, err := s.Get(ctx, id)
		if err != nil {
			return Op{}, err
		}
		e.Status = StatusWithdrawn
		return Op{Entry: e, ExpectedVersion: e.Version, Reason: "rejected candidate " + id}, nil
	})
	if err != nil {
		return Entry{}, err
	}
	return result, s.store.DecideMemoryCandidate(ctx, id, store.MemoryCandidateRejected)
}

// decision loads a candidate. done reports that opID already decided it, and
// result is what that decision returned; a candidate decided otherwise is
// store.ErrConflict.
func (s *Service) decision(ctx context.Context, id, opID string) (c store.MemoryCandidate, done bool, result Entry, err error) {
	if opID == "" {
		return c, false, Entry{}, invalid("a decision needs an operation ID")
	}
	c, err = s.store.MemoryCandidateByEntry(ctx, id)
	if err != nil || c.State == store.MemoryCandidateOpen {
		return c, false, Entry{}, err
	}
	e, found, err := s.stepResult(ctx, opID+"-1")
	if err != nil {
		return c, false, Entry{}, err
	}
	if !found {
		return c, false, Entry{}, fmt.Errorf("%w: memory candidate %s is %s", store.ErrConflict, id, c.State)
	}
	return c, true, e, nil
}

// stepResult returns what the step committed, if it did. A step that failed
// before its file was placed may be tried again under a suffixed ID.
func (s *Service) stepResult(ctx context.Context, stepID string) (Entry, bool, error) {
	id, found, err := s.stepAttempt(ctx, stepID)
	if err != nil || !found {
		return Entry{}, false, err
	}
	op, err := s.store.MemoryOpByID(ctx, id)
	if err != nil {
		return Entry{}, false, err
	}
	e, err := Decode([]byte(op.Content))
	if err != nil {
		return Entry{}, false, fmt.Errorf("memory: op %s: %w", id, err)
	}
	return e, true, nil
}

// stepAttempt finds the step's committed attempt, or the first unused ID.
func (s *Service) stepAttempt(ctx context.Context, stepID string) (string, bool, error) {
	id := stepID
	for attempt := 1; ; attempt++ {
		op, err := s.store.MemoryOpByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return id, false, nil
		}
		if err != nil {
			return "", false, err
		}
		switch op.State {
		case store.MemoryOpCommitted:
			return id, true, nil
		case store.MemoryOpFailed:
			id = fmt.Sprintf("%s.%d", stepID, attempt)
		default:
			return "", false, fmt.Errorf("%w: memory op %s is %s", store.ErrConflict, id, op.State)
		}
	}
}

// step commits one write of a decision unless an earlier try already did.
func (s *Service) step(ctx context.Context, stepID string, build func() (Op, error)) (Entry, error) {
	id, found, err := s.stepAttempt(ctx, stepID)
	if err != nil {
		return Entry{}, err
	}
	if found {
		e, _, err := s.stepResult(ctx, stepID)
		return e, err
	}
	op, err := build()
	if err != nil {
		return Entry{}, err
	}
	op.ID, op.Actor = id, ActorUser
	return s.commit(ctx, op)
}
