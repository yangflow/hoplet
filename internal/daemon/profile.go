package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// profileUsageShown is how many entries a session's details list.
const profileUsageShown = 3

func profileEntry(e memory.Entry) proto.ProfileEntry {
	out := proto.ProfileEntry{
		ID: e.ID, Version: e.Version, Category: string(e.Category), Status: string(e.Status),
		Scope:    proto.ProfileScope{Kind: string(e.Scope.Kind), Value: e.Scope.Value},
		Evidence: make([]proto.ProfileEvidence, len(e.Evidence)), ValidUntil: e.ValidUntil, ReplacedBy: e.ReplacedBy,
		UpdatedAt: e.UpdatedAt, Title: e.Title(), Body: e.Body,
	}
	for i, ev := range e.Evidence {
		out.Evidence[i] = proto.ProfileEvidence{Kind: string(ev.Kind), Ref: ev.Ref}
	}
	return out
}

// profileFacts are what an entry's own file does not say.
type profileFacts struct {
	links map[string][]string
	uses  map[string]int
}

func (s *server) profileFacts(ctx context.Context) (profileFacts, error) {
	links, err := s.memory.Links(ctx)
	if err != nil {
		return profileFacts{}, err
	}
	uses, err := s.store.MemoryUseCounts(ctx)
	if err != nil {
		return profileFacts{}, err
	}
	return profileFacts{links, uses}, nil
}

func (f profileFacts) entry(e memory.Entry) proto.ProfileEntry {
	out := profileEntry(e)
	out.Links, out.Uses = f.links[e.ID], f.uses[e.ID]
	return out
}

func profileScope(p *proto.ProfileScope) *memory.Scope {
	if p == nil {
		return nil
	}
	return &memory.Scope{Kind: memory.ScopeKind(p.Kind), Value: p.Value}
}

func profileResult(res memory.Result, err error) (proto.ProfileWriteResult, error) {
	if err != nil {
		return proto.ProfileWriteResult{}, err
	}
	out := proto.ProfileWriteResult{Outcome: string(res.Outcome), Code: res.Code, Reason: res.Reason}
	if res.Outcome != memory.OutcomeRejected {
		e := profileEntry(res.Entry)
		out.Entry = &e
	}
	return out, nil
}

func (s *server) profileList(ctx context.Context, p proto.ProfileListParams) (proto.ProfileList, error) {
	q := store.MemoryQuery{Category: p.Category, Status: p.Status, Text: p.Text, Limit: p.Limit}
	if p.All {
		q.Status = ""
	} else if q.Status == "" {
		q.Status = string(memory.StatusActive)
	}
	if p.Scope != nil {
		q.ScopeKind, q.ScopeValue = p.Scope.Kind, p.Scope.Value
	}
	entries, err := s.memory.Search(ctx, q)
	if err != nil {
		return proto.ProfileList{}, err
	}
	facts, err := s.profileFacts(ctx)
	if err != nil {
		return proto.ProfileList{}, err
	}
	out := proto.ProfileList{Entries: make([]proto.ProfileEntry, len(entries))}
	for i, e := range entries {
		out.Entries[i] = facts.entry(e)
	}
	return out, nil
}

func (s *server) profileGet(ctx context.Context, id string) (proto.ProfileDetail, error) {
	e, err := s.memory.Get(ctx, id)
	if err != nil {
		return proto.ProfileDetail{}, err
	}
	versions, err := s.memory.History(ctx, id)
	if err != nil {
		return proto.ProfileDetail{}, err
	}
	facts, err := s.profileFacts(ctx)
	if err != nil {
		return proto.ProfileDetail{}, err
	}
	out := proto.ProfileDetail{Entry: facts.entry(e), History: make([]proto.ProfileVersion, len(versions))}
	for i, v := range versions {
		said, err := memory.Decode([]byte(v.Content))
		if err != nil {
			return proto.ProfileDetail{}, fmt.Errorf("daemon: version %d of %s: %w", v.Version, id, err)
		}
		out.History[i] = proto.ProfileVersion{
			Version: v.Version, Actor: string(v.Actor), Reason: v.Reason, Content: v.Content, At: v.At,
			Body: said.Body, Evidence: profileEntry(said).Evidence,
		}
	}
	return out, nil
}

// profileUsage is what a session's details say about memory.
func (s *server) profileUsage(ctx context.Context, id proto.SessionID) (proto.ProfileUsage, error) {
	sess, err := s.store.Session(ctx, id)
	if err != nil {
		return proto.ProfileUsage{}, err
	}
	if !s.mgr.ProfileUsable(ctx, sess.Mode) {
		return proto.ProfileUsage{Hidden: true}, nil
	}
	injections, err := s.store.MemoryInjections(ctx, id)
	if err != nil {
		return proto.ProfileUsage{}, err
	}
	if n := len(injections); n > 0 && injections[n-1].Status == store.MemoryInjectionFailed {
		return proto.ProfileUsage{Failed: true}, nil
	}
	used, err := s.store.MemoryUsesOf(ctx, id)
	if err != nil {
		return proto.ProfileUsage{}, err
	}
	if len(used) == 0 {
		return proto.ProfileUsage{Hidden: true}, nil
	}
	var shown []memory.Entry
	for _, entry := range used {
		e, err := s.memory.Get(ctx, entry)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return proto.ProfileUsage{}, err
		}
		if e.Status != memory.StatusWithdrawn {
			shown = append(shown, e)
		}
	}
	slices.SortStableFunc(shown, func(a, b memory.Entry) int {
		return cmp.Compare(memory.ScopeRank(a.Scope.Kind), memory.ScopeRank(b.Scope.Kind))
	})
	out := proto.ProfileUsage{Count: len(used)}
	for _, e := range shown[:min(len(shown), profileUsageShown)] {
		out.Entries = append(out.Entries, profileEntry(e))
	}
	return out, nil
}

func (s *server) profileUpdate(ctx context.Context, p proto.ProfileUpdateParams) (proto.ProfileWriteResult, error) {
	w := memory.Write{OpID: p.OpID, Actor: memory.ActorUser, Action: memory.ActionUpdate, Target: p.ID, ExpectedVersion: p.ExpectedVersion, Body: p.Body, Reason: "edited by the user"}
	if sc := profileScope(p.Scope); sc != nil {
		w.Scope = *sc
	}
	return profileResult(s.memory.Apply(ctx, w))
}

func (s *server) profileWithdraw(ctx context.Context, p proto.ProfileWithdrawParams) (proto.ProfileWriteResult, error) {
	return profileResult(s.memory.Apply(ctx, memory.Write{OpID: p.OpID, Actor: memory.ActorUser, Action: memory.ActionWithdraw, Target: p.ID, ExpectedVersion: p.ExpectedVersion, Reason: p.Reason}))
}

func (s *server) profileSettingsSet(ctx context.Context, p proto.ProfileSettings) (proto.ProfileSettings, error) {
	if err := s.extract.CheckHarness(ctx, p.ExtractionHarness); err != nil {
		return proto.ProfileSettings{}, err
	}
	if err := s.store.SetMemorySettings(ctx, p); err != nil {
		return proto.ProfileSettings{}, err
	}
	return p, nil
}

func (s *server) profileCandidates(ctx context.Context) (proto.ProfileCandidates, error) {
	cs, err := s.memory.Candidates(ctx)
	if err != nil {
		return proto.ProfileCandidates{}, err
	}
	out := proto.ProfileCandidates{Candidates: make([]proto.ProfileCandidate, len(cs))}
	for i, c := range cs {
		out.Candidates[i] = proto.ProfileCandidate{
			Entry: profileEntry(c.Entry), Action: string(c.Action), Target: c.Target, ExpectedVersion: c.ExpectedVersion,
			Actor: string(c.Actor), Reason: c.Reason, CreatedAt: c.CreatedAt,
		}
	}
	return out, nil
}
