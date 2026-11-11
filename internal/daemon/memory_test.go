package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func TestStartCreatesProfile(t *testing.T) {
	dir, script := newHome(t, fake.Script{})
	d := start(t, testConfig(dir, script))
	if info, err := os.Stat(filepath.Join(home.Profile(dir), "entries")); err != nil || !info.IsDir() {
		t.Fatalf("%v, %v", info, err)
	}
	if p, err := d.srv.memory.Problems(context.Background()); err != nil || len(p) != 0 {
		t.Fatalf("%+v, %v", p, err)
	}
}

func TestStartRecoversPendingMemoryOp(t *testing.T) {
	ctx := context.Background()
	dir, script := newHome(t, fake.Script{})
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginMemoryOp(ctx, store.MemoryOp{ID: "o1", EntryID: "e", NewVersion: 1, Actor: "user", Content: "never written", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	d := start(t, testConfig(dir, script))
	op, err := d.store.MemoryOpByID(ctx, "o1")
	if err != nil || op.State != store.MemoryOpFailed {
		t.Fatalf("%+v, %v", op, err)
	}
}

func seedProfile(t *testing.T, svc *memory.Service, st *store.Store) (entry, candidate memory.Entry) {
	t.Helper()
	ctx := context.Background()
	res, err := svc.Apply(ctx, memory.Write{OpID: "seed", Actor: memory.ActorUser, Action: memory.ActionCreate, Category: memory.CategoryFact, Scope: memory.Scope{Kind: memory.ScopeGlobal}, Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := memory.NewID()
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.Commit(ctx, memory.Op{ID: "seed-c", Actor: memory.ActorCompanion, Reason: "noticed", Entry: memory.Entry{
		ID: id, Category: memory.CategoryFact, Status: memory.StatusCandidate, Scope: memory.Scope{Kind: memory.ScopeRepo, Value: "r"}, Body: "proposed",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMemoryCandidate(ctx, store.MemoryCandidate{EntryID: id, Action: "create", Actor: "companion", Reason: "noticed"}); err != nil {
		t.Fatal(err)
	}
	return res.Entry, c
}

func TestProfileMethods(t *testing.T) {
	dir, script := newHome(t, fake.Script{})
	d := start(t, testConfig(dir, script))
	c := dial(t, dir, "test")
	entry, cand := seedProfile(t, d.srv.memory, d.store)

	list, err := c.ProfileList(timeout(t), proto.ProfileListParams{})
	if err != nil || len(list.Entries) != 1 || list.Entries[0].ID != entry.ID || list.Entries[0].Title != "first" {
		t.Fatalf("list %+v, %v", list, err)
	}
	if all, err := c.ProfileList(timeout(t), proto.ProfileListParams{All: true}); err != nil || len(all.Entries) != 2 {
		t.Fatalf("all %+v, %v", all, err)
	}
	if got, err := c.ProfileList(timeout(t), proto.ProfileListParams{Status: "candidate", Scope: &proto.ProfileScope{Kind: "repo", Value: "r"}}); err != nil || len(got.Entries) != 1 {
		t.Fatalf("filtered %+v, %v", got, err)
	}
	detail, err := c.ProfileGet(timeout(t), entry.ID)
	if err != nil || detail.Entry.Version != 1 || len(detail.History) != 1 || detail.History[0].Actor != "user" {
		t.Fatalf("get %+v, %v", detail, err)
	}
	if want := []proto.ProfileScope{{Kind: "global"}}; !slices.Equal(detail.Scopes, want) {
		t.Fatalf("scopes of the user's own entry %+v", detail.Scopes)
	}
	if _, err := c.ProfileGet(timeout(t), "mem_00000000000000000000000000000000"); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("unknown: %v", err)
	}
	res, err := c.ProfileUpdate(timeout(t), proto.ProfileUpdateParams{ID: entry.ID, ExpectedVersion: 1, Body: "second", OpID: "u1"})
	if err != nil || res.Outcome != "applied" || res.Entry == nil || res.Entry.Version != 2 {
		t.Fatalf("update %+v, %v", res, err)
	}
	if _, err := c.ProfileUpdate(timeout(t), proto.ProfileUpdateParams{ID: entry.ID, ExpectedVersion: 1, Body: "third", OpID: "u2"}); apiCode(err) != proto.CodeConflict {
		t.Fatalf("stale: %v", err)
	}
	refused, err := c.ProfileUpdate(timeout(t), proto.ProfileUpdateParams{ID: entry.ID, ExpectedVersion: 2, Body: "password: hunter22", OpID: "u3"})
	if err != nil || refused.Outcome != "rejected" || refused.Code != memory.CodeCredential || refused.Entry != nil {
		t.Fatalf("credential %+v, %v", refused, err)
	}
	cs, err := c.ProfileCandidates(timeout(t))
	if err != nil || len(cs.Candidates) != 1 || cs.Candidates[0].Entry.ID != cand.ID || cs.Candidates[0].Action != "create" {
		t.Fatalf("candidates %+v, %v", cs, err)
	}
	if want := []proto.ProfileScope{{Kind: "global"}, {Kind: "repo", Value: "r"}}; !slices.Equal(cs.Candidates[0].Scopes, want) {
		t.Fatalf("scopes of a candidate %+v", cs.Candidates[0].Scopes)
	}
	accepted, err := c.ProfileAccept(timeout(t), proto.ProfileAcceptParams{ID: cand.ID, Scope: &proto.ProfileScope{Kind: "repo", Value: "s"}, OpID: "a1"})
	if err != nil || accepted.Status != "active" || accepted.Scope.Value != "s" {
		t.Fatalf("accept %+v, %v", accepted, err)
	}
	if _, err := c.ProfileReject(timeout(t), proto.ProfileRejectParams{ID: cand.ID, OpID: "r1"}); apiCode(err) != proto.CodeConflict {
		t.Fatalf("reject decided: %v", err)
	}
	gone, err := c.ProfileWithdraw(timeout(t), proto.ProfileWithdrawParams{ID: entry.ID, ExpectedVersion: 2, Reason: "wrong", OpID: "w1"})
	if err != nil || gone.Outcome != "applied" || gone.Entry.Status != "withdrawn" {
		t.Fatalf("withdraw %+v, %v", gone, err)
	}
}

func TestProfileRejectMethod(t *testing.T) {
	dir, script := newHome(t, fake.Script{})
	d := start(t, testConfig(dir, script))
	c := dial(t, dir, "test")
	_, cand := seedProfile(t, d.srv.memory, d.store)
	got, err := c.ProfileReject(timeout(t), proto.ProfileRejectParams{ID: cand.ID, OpID: "r1"})
	if err != nil || got.Status != "withdrawn" {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestProfileIsLocalOnly(t *testing.T) {
	b := startB(t, fake.Script{}, limits{})
	c := dialRemote(t, b.pairWith(t, newIdentity(t), "phone"))
	calls := map[string]func() error{
		"list": func() error { _, err := c.ProfileList(timeout(t), proto.ProfileListParams{}); return err },
		"get":  func() error { _, err := c.ProfileGet(timeout(t), "x"); return err },
		"update": func() error {
			_, err := c.ProfileUpdate(timeout(t), proto.ProfileUpdateParams{ID: "x", ExpectedVersion: 1, Body: "b", OpID: "o"})
			return err
		},
		"withdraw": func() error {
			_, err := c.ProfileWithdraw(timeout(t), proto.ProfileWithdrawParams{ID: "x", ExpectedVersion: 1, OpID: "o"})
			return err
		},
		"candidates": func() error { _, err := c.ProfileCandidates(timeout(t)); return err },
		"accept": func() error {
			_, err := c.ProfileAccept(timeout(t), proto.ProfileAcceptParams{ID: "x", OpID: "o"})
			return err
		},
		"reject": func() error {
			_, err := c.ProfileReject(timeout(t), proto.ProfileRejectParams{ID: "x", OpID: "o"})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); apiCode(err) != proto.CodeUnauthorized {
			t.Errorf("%s: %v", name, err)
		}
	}
}
