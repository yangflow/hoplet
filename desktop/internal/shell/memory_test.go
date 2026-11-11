package shell

import (
	"errors"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// memFake is the host's growth memory.
type memFake struct {
	data    Memory // what Memory answers
	err     error  // why Memory fails
	held    bool   // Memory answers only when pending runs
	pending func()
	reads   int // how often Memory was asked

	details  map[string]proto.ProfileDetail // what MemoryEntry answers
	entryErr error                          // why MemoryEntry fails
	usage    map[proto.SessionID]proto.ProfileUsage
	usageErr error

	writes   []any                    // the writes, as the parameters they were given
	result   proto.ProfileWriteResult // what UpdateMemory and WithdrawMemory answer
	writeErr error                    // why a write fails
	saved    proto.ProfileSettings    // what SetMemorySettings answers when it does not fail
}

func (h *host) Memory(done func(Memory, error)) {
	h.mem.reads++
	answer := func() { done(h.mem.data, h.mem.err) }
	if h.mem.held {
		h.mem.pending = answer
		return
	}
	answer()
}

func (h *host) MemoryEntry(id string, done func(proto.ProfileDetail, error)) {
	if h.mem.entryErr != nil {
		done(proto.ProfileDetail{}, h.mem.entryErr)
		return
	}
	d, ok := h.mem.details[id]
	if !ok {
		done(proto.ProfileDetail{}, errors.New("not found"))
		return
	}
	done(d, nil)
}

func (h *host) MemoryUsage(id proto.SessionID, done func(proto.ProfileUsage, error)) {
	done(h.mem.usage[id], h.mem.usageErr)
}

func (h *host) UpdateMemory(p proto.ProfileUpdateParams, done func(proto.ProfileWriteResult, error)) {
	h.mem.writes = append(h.mem.writes, p)
	done(h.mem.result, h.mem.writeErr)
}

func (h *host) WithdrawMemory(p proto.ProfileWithdrawParams, done func(proto.ProfileWriteResult, error)) {
	h.mem.writes = append(h.mem.writes, p)
	done(h.mem.result, h.mem.writeErr)
}

func (h *host) UnlinkMemory(p proto.ProfileUnlinkParams, done func(error)) {
	h.mem.writes = append(h.mem.writes, p)
	done(h.mem.writeErr)
}

func (h *host) AcceptMemory(p proto.ProfileAcceptParams, done func(proto.ProfileEntry, error)) {
	h.mem.writes = append(h.mem.writes, p)
	done(proto.ProfileEntry{ID: p.ID}, h.mem.writeErr)
}

func (h *host) RejectMemory(p proto.ProfileRejectParams, done func(proto.ProfileEntry, error)) {
	h.mem.writes = append(h.mem.writes, p)
	done(proto.ProfileEntry{ID: p.ID}, h.mem.writeErr)
}

func (h *host) SetMemorySettings(s proto.ProfileSettings, done func(proto.ProfileSettings, error)) {
	h.mem.writes = append(h.mem.writes, s)
	if h.mem.writeErr != nil {
		done(proto.ProfileSettings{}, h.mem.writeErr)
		return
	}
	h.mem.saved = s
	done(s, nil)
}

func TestMemoryPageNavigation(t *testing.T) {
	a, h, tt, states := newApp(t)
	if err := tt.Click("Fix sync"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.m.open || a.selected != "" || h.mem.reads != 1 {
		t.Fatalf("settings open %v, selected %q, reads %d", a.m.open, a.selected, h.mem.reads)
	}
	for _, want := range []string{"返回", "成长记忆"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	for _, gone := range []string{"新建", "收件箱", "Fix sync"} {
		if tt.HasText(gone) {
			t.Errorf("%q still in the settings' sidebar", gone)
		}
	}
	if _, ok := tt.Find("会话菜单"); ok {
		t.Error("the title bar still shows a session")
	}
	if got := (*states)[len(*states)-1]; !got.CanBack || got.Selected.ID != "" {
		t.Fatalf("state %+v", got)
	}

	// Back returns to the session, Forward to the settings, which read
	// growth memory afresh.
	a.Back()
	tt.Frame()
	if a.m.open || a.selected != "s1" {
		t.Fatalf("after Back: settings %v, selected %q", a.m.open, a.selected)
	}
	a.Forward()
	tt.Frame()
	if !a.m.open || h.mem.reads != 2 {
		t.Fatalf("after Forward: settings %v, reads %d", a.m.open, h.mem.reads)
	}

	// 返回 goes to the page before the settings, keeping them ahead.
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.m.open || a.selected != "s1" || !tt.HasText("新建") {
		t.Fatalf("after 返回: settings %v, selected %q", a.m.open, a.selected)
	}
	a.Forward()
	if !a.m.open {
		t.Fatal("Forward does not return to the settings")
	}
}

func TestMemoryLeaveWithoutHistory(t *testing.T) {
	a, _, tt, _ := newApp(t)
	a.history, a.at = nil, -1
	a.OpenMemory("")
	tt.Frame()
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	if a.m.open || !a.s.newPage {
		t.Fatalf("settings %v, new page %v", a.m.open, a.s.newPage)
	}
}

func TestMemoryBadge(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.mem.data = Memory{
		Entries: []proto.ProfileEntry{{ID: "e1", Version: 1, Status: "active", Scope: proto.ProfileScope{Kind: "global"}, Title: "先给结论"}},
		Candidates: []proto.ProfileCandidate{
			{Entry: proto.ProfileEntry{ID: "c1", Title: "周末不早起"}, Action: "create"},
			{Entry: proto.ProfileEntry{ID: "c2", Title: "不用敬语"}, Action: "create"},
		},
	}
	a.OpenMemory("")
	tt.Frame()
	if !tt.HasText("2") {
		t.Fatalf("no badge for two candidates in %q", tt.Texts())
	}
	h.mem.data.Candidates = nil
	a.OpenMemory("")
	a.loadMemory()
	tt.Frame()
	if tt.HasText("2") {
		t.Fatal("a badge without candidates")
	}
}

func TestMemoryLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*memFake)
		want string
	}{
		{"loading", func(m *memFake) { m.held = true }, "正在读取…"},
		{"failed", func(m *memFake) { m.err = errors.New("boom") }, "读取失败"},
		{"empty", func(m *memFake) {}, "还没有记忆"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, h, tt, _ := newApp(t)
			tc.set(&h.mem)
			a.OpenMemory("")
			tt.Frame()
			if !tt.HasText(tc.want) {
				t.Fatalf("no %q in %q", tc.want, tt.Texts())
			}
		})
	}
}

func TestMemoryStaleAnswer(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.mem.held = true
	a.OpenMemory("")
	first := h.mem.pending
	h.mem.err = errors.New("old")
	a.loadMemory()
	second := h.mem.pending
	first()
	tt.Frame()
	if a.m.known || !tt.HasText("正在读取…") {
		t.Fatal("an older read's answer was taken")
	}
	h.mem.err = nil
	second()
	tt.Frame()
	if !tt.HasText("还没有记忆") {
		t.Fatalf("texts %q", tt.Texts())
	}
}
