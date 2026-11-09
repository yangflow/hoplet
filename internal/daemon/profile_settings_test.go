package daemon

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func TestMemoryMethodsStayLocal(t *testing.T) {
	for _, m := range []string{proto.MethodProfileSettings, proto.MethodProfileSettingsSet, proto.MethodProfileUnlink, proto.MethodProfileUsage} {
		if !localOnly(m) {
			t.Errorf("%s is open to paired devices", m)
		}
	}
}

func usageRig(t *testing.T) (*Daemon, func(kind memory.ScopeKind, value, body string) memory.Entry) {
	t.Helper()
	dir, script := newHome(t, fake.Script{})
	d := start(t, testConfig(dir, script))
	n := 0
	return d, func(kind memory.ScopeKind, value, body string) memory.Entry {
		t.Helper()
		n++
		res, err := d.srv.memory.Apply(context.Background(), memory.Write{
			OpID: fmt.Sprint("seed", n), Actor: memory.ActorUser, Action: memory.ActionCreate, Category: memory.CategoryFact,
			Scope: memory.Scope{Kind: kind, Value: value}, Body: body,
		})
		if err != nil || res.Outcome != memory.OutcomeApplied {
			t.Fatalf("%+v, %v", res, err)
		}
		return res.Entry
	}
}

func usageSession(t *testing.T, d *Daemon, id proto.SessionID) {
	t.Helper()
	if _, err := d.store.CreateSession(context.Background(), proto.Session{
		ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "fake"},
		Directory: "/work", Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func deliver(t *testing.T, d *Daemon, id proto.SessionID, status string, entries ...memory.Entry) {
	t.Helper()
	in := store.MemoryInjection{Session: id, Status: status}
	for _, e := range entries {
		in.Entries = append(in.Entries, store.MemoryInjectionEntry{ID: e.ID, Version: e.Version})
	}
	if err := d.store.AddMemoryInjection(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestProfileUsage(t *testing.T) {
	ctx := context.Background()
	d, seed := usageRig(t)
	global, host, repo := seed(memory.ScopeGlobal, "", "global"), seed(memory.ScopeHost, "h", "host"), seed(memory.ScopeRepo, "r", "repo")
	extra, gone := seed(memory.ScopeGlobal, "", "extra"), seed(memory.ScopeGlobal, "", "gone")
	usageSession(t, d, "s")
	if got, err := d.srv.profileUsage(ctx, "s"); err != nil || !got.Hidden {
		t.Fatalf("nothing used: %+v, %v", got, err)
	}
	deliver(t, d, "s", store.MemoryInjectionDelivered, gone, global, host, repo, extra)
	if err := d.store.AddMemoryUse(ctx, "s", global.ID, 1, store.MemoryUseRead); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.memory.Apply(ctx, memory.Write{OpID: "w", Actor: memory.ActorUser, Action: memory.ActionWithdraw, Target: gone.ID, ExpectedVersion: 1, Reason: "no"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.srv.profileUsage(ctx, "s")
	if err != nil || got.Hidden || got.Failed || got.Count != 5 || len(got.Entries) != 3 {
		t.Fatalf("%+v, %v", got, err)
	}
	if got.Entries[0].ID != repo.ID || got.Entries[1].ID != host.ID || got.Entries[2].ID != global.ID {
		t.Fatalf("order: %+v", got.Entries)
	}
	if err := d.store.SetMemorySettings(ctx, proto.ProfileSettings{UsePersonal: true, AutoSave: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := d.srv.profileUsage(ctx, "s"); err != nil || !got.Hidden {
		t.Fatalf("use off: %+v, %v", got, err)
	}
}

func TestProfileUsageStates(t *testing.T) {
	ctx := context.Background()
	d, _ := usageRig(t)
	tests := []struct {
		id           proto.SessionID
		status       string
		hidden, fail bool
	}{
		{"failed", store.MemoryInjectionFailed, false, true},
		{"unsupported", store.MemoryInjectionUnsupported, true, false},
	}
	for _, tt := range tests {
		usageSession(t, d, tt.id)
		deliver(t, d, tt.id, tt.status)
		if got, err := d.srv.profileUsage(ctx, tt.id); err != nil || got.Hidden != tt.hidden || got.Failed != tt.fail {
			t.Errorf("%s: %+v, %v", tt.id, got, err)
		}
	}
}

func TestProfileFacts(t *testing.T) {
	ctx := context.Background()
	d, seed := usageRig(t)
	a, b := seed(memory.ScopeGlobal, "", "A"), seed(memory.ScopeGlobal, "", "B")
	res, err := d.srv.memory.Apply(ctx, memory.Write{OpID: "l", Actor: memory.ActorUser, Action: memory.ActionUpdate, Target: a.ID, ExpectedVersion: 1, Body: "A2", Reason: "r", Links: []string{b.ID}})
	if err != nil || res.Outcome != memory.OutcomeApplied {
		t.Fatalf("%+v, %v", res, err)
	}
	for _, id := range []proto.SessionID{"s1", "s2"} {
		if err := d.store.AddMemoryUse(ctx, id, b.ID, 1, store.MemoryUseRead); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.srv.profileGet(ctx, b.ID)
	if err != nil || got.Entry.Uses != 2 || !slices.Equal(got.Entry.Links, []string{a.ID}) {
		t.Fatalf("%+v, %v", got.Entry, err)
	}
	detail, err := d.srv.profileGet(ctx, a.ID)
	if err != nil || len(detail.History) != 2 {
		t.Fatalf("%+v, %v", detail, err)
	}
	bodies := []string{detail.History[0].Body, detail.History[1].Body}
	slices.Sort(bodies)
	if !slices.Equal(bodies, []string{"A", "A2"}) || detail.History[0].Evidence == nil {
		t.Fatalf("%+v", detail.History)
	}
	list, err := d.srv.profileList(ctx, proto.ProfileListParams{})
	if err != nil || len(list.Entries) != 2 {
		t.Fatalf("%+v, %v", list, err)
	}
	for _, e := range list.Entries {
		if len(e.Links) != 1 {
			t.Errorf("%+v", e)
		}
	}
}
