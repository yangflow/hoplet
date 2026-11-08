package memory

import (
	"context"
	"strings"
	"time"
)

// The codes of a rejected write.
const (
	CodeCredential      = "credential"
	CodeBadEvidence     = "bad_evidence"
	CodeStaleEvidence   = "stale_evidence"
	CodeWithdrawnReplay = "withdrawn_replay"
)

// pastVersion is one committed version, decoded.
type pastVersion struct {
	entry Entry
	actor Actor
	at    time.Time
}

func (v pastVersion) byUser() bool { return v.actor == ActorUser || v.actor == ActorFile }

func normalized(body string) string { return strings.Join(strings.Fields(body), " ") }

// pastVersions returns every entry's committed versions, oldest first.
func (s *Service) pastVersions(ctx context.Context) (map[string][]pastVersion, error) {
	ops, err := s.store.AllCommittedMemoryOps(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]pastVersion)
	for _, op := range ops {
		e, err := Decode([]byte(op.Content))
		if err != nil {
			s.log.WarnContext(ctx, "memory: skip undecodable version", "op", op.ID, "err", err)
			continue
		}
		out[e.ID] = append(out[e.ID], pastVersion{entry: e, actor: Actor(op.Actor), at: op.CreatedAt})
	}
	return out, nil
}

func allBefore(times []time.Time, limit time.Time) bool {
	for _, t := range times {
		if !t.Before(limit) {
			return false
		}
	}
	return true
}

// userStatedAfter reports whether a user_stated piece of evidence is later
// than limit.
func userStatedAfter(w Write, times []time.Time, limit time.Time) bool {
	for i, ev := range w.Evidence {
		if ev.Kind == EvidenceUserStated && times[i].After(limit) {
			return true
		}
	}
	return false
}

// checkReplay refuses a write that brings back what the user has already
// corrected or withdrawn. It returns the refusal's code and detail, or an
// empty code.
func (s *Service) checkReplay(ctx context.Context, w Write, times []time.Time) (code, detail string, err error) {
	all, err := s.pastVersions(ctx)
	if err != nil {
		return "", "", err
	}
	body := normalized(w.Body)
	if w.Action != ActionWithdraw {
		for id, versions := range all {
			head := versions[len(versions)-1]
			if head.entry.Status == StatusWithdrawn && normalized(head.entry.Body) == body && !userStatedAfter(w, times, head.at) {
				return CodeWithdrawnReplay, "this content was withdrawn in " + id, nil
			}
		}
	}
	if versions := all[w.Target]; w.Target != "" {
		for i := len(versions) - 1; i >= 0; i-- {
			if !versions[i].byUser() {
				continue
			}
			if allBefore(times, versions[i].at) {
				return CodeStaleEvidence, "the user changed " + w.Target + " after this evidence", nil
			}
			break
		}
	}
	if w.Action == ActionWithdraw {
		return "", "", nil
	}
	for id, versions := range all {
		for i := 1; i < len(versions); i++ {
			replaced := normalized(versions[i-1].entry.Body) == body && normalized(versions[i].entry.Body) != body
			if replaced && versions[i].byUser() && allBefore(times, versions[i].at) {
				return CodeStaleEvidence, "the user replaced this content in " + id, nil
			}
		}
	}
	return "", "", nil
}
