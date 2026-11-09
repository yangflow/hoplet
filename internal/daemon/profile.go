package daemon

import (
	"context"

	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

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
	out := proto.ProfileList{Entries: make([]proto.ProfileEntry, len(entries))}
	for i, e := range entries {
		out.Entries[i] = profileEntry(e)
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
	out := proto.ProfileDetail{Entry: profileEntry(e), History: make([]proto.ProfileVersion, len(versions))}
	for i, v := range versions {
		out.History[i] = proto.ProfileVersion{Version: v.Version, Actor: string(v.Actor), Reason: v.Reason, Content: v.Content, At: v.At}
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
