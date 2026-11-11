package shell

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

func savedEvent(t *testing.T, a *App, tt *ui.Tester, id proto.SessionID) {
	t.Helper()
	a.Select(id)
	a.ApplySession(id, proto.SessionItem{Events: events(t, tt.Now(), 1, 1,
		proto.MemorySaved{Entry: "g1", Version: 1, Action: proto.MemoryCreated, Actor: proto.MemoryByCompanion},
	)})
	settle(tt)
}

func TestMemoryLineBothModes(t *testing.T) {
	for _, id := range []proto.SessionID{"s1", "p1"} {
		t.Run(string(id), func(t *testing.T) {
			a, h, tt, _ := newApp(t)
			e := memEntry("g1", "先给结论", 1, scopeGlobal)
			h.mem.data = Memory{Entries: []proto.ProfileEntry{e}}
			h.mem.details = map[string]proto.ProfileDetail{"g1": {Entry: e}}
			savedEvent(t, a, tt, id)
			clickAll(t, tt, "记住了 先给结论")
			if !a.m.open || a.m.entry != "g1" || !tt.HasText("先给结论的正文") {
				t.Fatalf("open %v, entry %q, texts %q", a.m.open, a.m.entry, tt.Texts())
			}
		})
	}
}

func TestMemoryLineWithdrawn(t *testing.T) {
	a, h, tt, _ := newApp(t)
	e := memEntry("g1", "先给结论", 1, scopeGlobal)
	e.Status = "withdrawn"
	h.mem.details = map[string]proto.ProfileDetail{"g1": {Entry: e}}
	savedEvent(t, a, tt, "p1")
	clickAll(t, tt, "记住了 已撤回")
	if a.m.open || tt.HasText("先给结论") {
		t.Fatalf("open %v, texts %q", a.m.open, tt.Texts())
	}
}

func TestMemoryLineUnreadable(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.mem.entryErr = errors.New("gone")
	savedEvent(t, a, tt, "p1")
	if tt.HasText("记住了") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestMemoryLineAfterWithdrawing(t *testing.T) {
	a, h, tt, _ := newApp(t)
	e := memEntry("g1", "先给结论", 1, scopeGlobal)
	h.mem.data = Memory{Entries: []proto.ProfileEntry{e}}
	h.mem.details = map[string]proto.ProfileDetail{"g1": {Entry: e}}
	savedEvent(t, a, tt, "p1")
	clickAll(t, tt, "记住了 先给结论")
	gone := e
	gone.Status = "withdrawn"
	h.mem.details["g1"] = proto.ProfileDetail{Entry: gone}
	clickAll(t, tt, "撤回", "确认撤回")
	a.Select("p1")
	settle(tt)
	if _, ok := tt.Find("记住了 已撤回"); !ok {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestMemoryBlock(t *testing.T) {
	used := proto.ProfileUsage{Count: 12, Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)}}
	for _, tc := range []struct {
		name  string
		usage proto.ProfileUsage
		err   error
		shows []string
		hides []string
		mark  bool
	}{
		{name: "used", usage: used, shows: []string{"记忆", "先给结论", "本次用了 12 条"}},
		{name: "hidden", usage: proto.ProfileUsage{Hidden: true}, hides: []string{"本次"}},
		{name: "failed", usage: proto.ProfileUsage{Failed: true}, shows: []string{"本次没有用上记忆"}, mark: true},
		{name: "none", usage: proto.ProfileUsage{}, hides: []string{"本次"}},
		{name: "error", err: errors.New("boom"), hides: []string{"本次"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, h, tt, _ := newApp(t)
			tt.SetSize(1700, 900)
			h.mem.usage = map[proto.SessionID]proto.ProfileUsage{"s1": tc.usage}
			h.mem.usageErr = tc.err
			a.Select("s1")
			a.s.detailOpen = true
			settle(tt)
			for _, want := range tc.shows {
				if !tt.HasText(want) {
					t.Errorf("no %q in %q", want, tt.Texts())
				}
			}
			for _, gone := range tc.hides {
				if tt.HasText(gone) {
					t.Errorf("%q in %q", gone, tt.Texts())
				}
			}
			if _, ok := tt.Find("出错"); ok != tc.mark {
				t.Errorf("error mark %v, want %v", ok, tc.mark)
			}
		})
	}
}

func TestMemoryBlockOpensEntry(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	e := memEntry("g1", "先给结论", 1, scopeGlobal)
	h.mem.data = Memory{Entries: []proto.ProfileEntry{e}}
	h.mem.details = map[string]proto.ProfileDetail{"g1": {Entry: e}}
	h.mem.usage = map[proto.SessionID]proto.ProfileUsage{"s1": {Count: 1, Entries: []proto.ProfileEntry{e}}}
	a.Select("s1")
	a.s.detailOpen = true
	settle(tt)
	clickAll(t, tt, "先给结论")
	if !a.m.open || a.m.entry != "g1" {
		t.Fatalf("open %v, entry %q", a.m.open, a.m.entry)
	}
}
