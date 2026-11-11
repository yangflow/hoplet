package shell

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

var (
	scopeGlobal = proto.ProfileScope{Kind: "global"}
	scopeRepo   = proto.ProfileScope{Kind: "repo", Value: "github.com/x/hoplet"}
)

func memEntry(id, title string, version int, scope proto.ProfileScope) proto.ProfileEntry {
	return proto.ProfileEntry{ID: id, Version: version, Status: "active", Scope: scope, Title: title, Body: title + "的正文"}
}

// openMemoryOn shows the page over the given memory, each entry with a
// detail to read.
func openMemoryOn(t *testing.T, data Memory) (*App, *host, *ui.Tester) {
	t.Helper()
	a, h, tt, _ := newApp(t)
	h.mem.data = data
	h.mem.details = map[string]proto.ProfileDetail{}
	for _, e := range data.Entries {
		h.mem.details[e.ID] = proto.ProfileDetail{Entry: e, Scopes: []proto.ProfileScope{scopeGlobal, scopeRepo}}
	}
	a.OpenMemory("")
	tt.Frame()
	return a, h, tt
}

func clickAll(t *testing.T, tt *ui.Tester, labels ...string) {
	t.Helper()
	for _, l := range labels {
		if err := tt.Click(l); err != nil {
			t.Fatalf("click %q in %q: %v", l, tt.Texts(), err)
		}
		tt.Frame()
	}
}

func TestMemoryGroups(t *testing.T) {
	_, _, tt := openMemoryOn(t, Memory{
		Entries: []proto.ProfileEntry{
			memEntry("r1", "用 make check", 1, scopeRepo),
			memEntry("g1", "先给结论", 2, scopeGlobal),
			{ID: "w1", Version: 1, Status: "withdrawn", Scope: scopeGlobal, Title: "已经撤回的"},
		},
		Candidates: []proto.ProfileCandidate{{Entry: proto.ProfileEntry{ID: "c1", Title: "周末不早起", Scope: scopeGlobal}, Action: "create"}},
	})
	texts := tt.Texts()
	var order []int
	for _, want := range []string{"待确认", "周末不早起", "全局", "先给结论", "hoplet", "用 make check"} {
		i := slices.Index(texts, want)
		if i < 0 {
			t.Fatalf("no %q in %q", want, texts)
		}
		order = append(order, i)
	}
	if !slices.IsSorted(order) {
		t.Fatalf("order %v of %q", order, texts)
	}
	if tt.HasText("已经撤回的") {
		t.Fatal("a withdrawn entry is listed")
	}
	if !tt.HasText("v2") {
		t.Fatalf("no version in %q", texts)
	}
}

func TestMemoryColumns(t *testing.T) {
	entries := func(n int) []proto.ProfileEntry {
		var out []proto.ProfileEntry
		for i := range n {
			scope := scopeGlobal
			if i%2 == 1 {
				scope = scopeRepo
			}
			out = append(out, memEntry(fmt.Sprintf("e%d", i), fmt.Sprintf("条目%d", i), 1, scope))
		}
		return out
	}
	for _, tc := range []struct {
		name string
		n    int
		two  bool
	}{{"few", memoryFew, false}, {"many", memoryFew + 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, tt := openMemoryOn(t, Memory{Entries: entries(tc.n)})
			g, ok := tt.Find("全局")
			r, ok2 := tt.Find("hoplet")
			if !ok || !ok2 {
				t.Fatalf("groups missing in %q", tt.Texts())
			}
			if two := r.X > g.X; two != tc.two {
				t.Fatalf("global at %v, repo at %v, want two columns %v", g, r, tc.two)
			}
		})
	}
}

func TestMemoryOneScopeOneColumn(t *testing.T) {
	var entries []proto.ProfileEntry
	for i := range memoryFew + 1 {
		entries = append(entries, memEntry(fmt.Sprintf("e%d", i), fmt.Sprintf("条目%d", i), 1, scopeGlobal))
	}
	a, _, tt := openMemoryOn(t, Memory{Entries: entries})
	first, ok := tt.Find("条目0")
	last, ok2 := tt.Find(fmt.Sprintf("条目%d", memoryFew))
	if !ok || !ok2 || first.X != last.X || len(a.memoryGroups()) != 1 {
		t.Fatalf("first %v, last %v, texts %q", first, last, tt.Texts())
	}
}

func TestSplitGroups(t *testing.T) {
	group := func(n int) memoryGroup { return memoryGroup{entries: make([]proto.ProfileEntry, n)} }
	for _, tc := range []struct {
		sizes []int
		want  int
	}{{[]int{3, 3}, 1}, {[]int{1, 1, 4}, 2}, {[]int{6}, 1}} {
		var gs []memoryGroup
		for _, n := range tc.sizes {
			gs = append(gs, group(n))
		}
		if got := splitGroups(gs); got != tc.want {
			t.Errorf("split %v = %d, want %d", tc.sizes, got, tc.want)
		}
	}
}

func TestMemoryEditSaves(t *testing.T) {
	e := memEntry("g1", "先给结论", 3, scopeGlobal)
	_, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{e}})
	clickAll(t, tt, "先给结论", "编辑")
	if !tt.HasText("保存后，新启动的会话才会用上") {
		t.Fatalf("no dialog in %q", tt.Texts())
	}
	tt.Type("再解释")
	tt.Frame()
	reads := h.mem.reads
	clickAll(t, tt, "保存")
	if len(h.mem.writes) != 1 {
		t.Fatalf("writes %+v", h.mem.writes)
	}
	p := h.mem.writes[0].(proto.ProfileUpdateParams)
	if p.ID != "g1" || p.ExpectedVersion != 3 || !strings.Contains(p.Body, "再解释") || p.Scope != nil || p.OpID == "" {
		t.Fatalf("params %+v", p)
	}
	if h.mem.reads != reads+1 || tt.HasText("保存后，新启动的会话才会用上") {
		t.Fatalf("reads %d, texts %q", h.mem.reads, tt.Texts())
	}
}

func TestMemoryEditScope(t *testing.T) {
	_, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "编辑", "范围：全局", "hoplet", "保存")
	if len(h.mem.writes) != 1 {
		t.Fatalf("writes %+v", h.mem.writes)
	}
	p := h.mem.writes[0].(proto.ProfileUpdateParams)
	if p.Scope == nil || *p.Scope != scopeRepo {
		t.Fatalf("scope %+v", p.Scope)
	}
}

func TestMemoryEditUnchangedWritesNothing(t *testing.T) {
	_, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "编辑", "保存")
	if len(h.mem.writes) != 0 {
		t.Fatalf("writes %+v", h.mem.writes)
	}
}

func TestMemoryEditConflict(t *testing.T) {
	a, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "编辑")
	tt.Type("x")
	tt.Frame()
	h.mem.writeErr = &proto.APIError{Code: proto.CodeConflict, Message: "changed"}
	reads := h.mem.reads
	clickAll(t, tt, "保存")
	if a.m.edit.open || h.mem.reads != reads+1 || !tt.HasText("这条记忆刚被改过") {
		t.Fatalf("open %v, reads %d, texts %q", a.m.edit.open, h.mem.reads, tt.Texts())
	}
}

func TestMemoryRetryKeepsOp(t *testing.T) {
	_, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "编辑")
	tt.Type("x")
	tt.Frame()
	h.mem.writeErr = errors.New("offline")
	clickAll(t, tt, "保存")
	h.mem.writeErr = nil
	clickAll(t, tt, "保存")
	if len(h.mem.writes) != 2 {
		t.Fatalf("writes %+v", h.mem.writes)
	}
	first, second := h.mem.writes[0].(proto.ProfileUpdateParams), h.mem.writes[1].(proto.ProfileUpdateParams)
	if first.OpID != second.OpID {
		t.Fatalf("op IDs %q and %q", first.OpID, second.OpID)
	}
}

func TestMemoryWithdrawConfirms(t *testing.T) {
	a, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 2, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "撤回")
	if len(h.mem.writes) != 0 || !tt.HasText("新启动的会话不再用这条记忆") {
		t.Fatalf("writes %+v, texts %q", h.mem.writes, tt.Texts())
	}
	clickAll(t, tt, "取消")
	if len(h.mem.writes) != 0 {
		t.Fatalf("cancel wrote %+v", h.mem.writes)
	}
	clickAll(t, tt, "撤回", "确认撤回")
	if len(h.mem.writes) != 1 {
		t.Fatalf("writes %+v", h.mem.writes)
	}
	if p := h.mem.writes[0].(proto.ProfileWithdrawParams); p.ID != "g1" || p.ExpectedVersion != 2 {
		t.Fatalf("params %+v", p)
	}
	if a.m.entry != "" {
		t.Fatalf("entry %q still open", a.m.entry)
	}
}

func TestMemoryRejectedWrite(t *testing.T) {
	a, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "编辑")
	tt.Type("x")
	tt.Frame()
	h.mem.result = proto.ProfileWriteResult{Outcome: "rejected", Reason: "像是密钥"}
	clickAll(t, tt, "保存")
	if !a.m.edit.open || !tt.HasText("像是密钥") {
		t.Fatalf("open %v, texts %q", a.m.edit.open, tt.Texts())
	}
}

func TestMemoryUnlink(t *testing.T) {
	e := memEntry("g1", "先给结论", 1, scopeGlobal)
	e.Links = []string{"g2"}
	_, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{e, memEntry("g2", "不用敬语", 1, scopeGlobal)}})
	clickAll(t, tt, "先给结论", "移除关联")
	if len(h.mem.writes) != 1 {
		t.Fatalf("writes %+v", h.mem.writes)
	}
	if p := h.mem.writes[0].(proto.ProfileUnlinkParams); p.ID != "g1" || p.Other != "g2" {
		t.Fatalf("params %+v", p)
	}
}

func TestMemoryDetailShows(t *testing.T) {
	e := memEntry("g1", "先给结论", 2, scopeGlobal)
	e.Uses = 4
	old := memEntry("o1", "先讲背景", 1, scopeGlobal)
	old.Status, old.ReplacedBy = "superseded", "g1"
	_, h, tt := openMemoryOn(t, Memory{Entries: []proto.ProfileEntry{e, old}})
	h.mem.details["g1"] = proto.ProfileDetail{Entry: e, History: []proto.ProfileVersion{
		{Version: 2, Actor: "user", Body: "第二版"},
		{Version: 1, Actor: "extraction", Body: "第一版", Evidence: []proto.ProfileEvidence{{Kind: "user_stated", Ref: "event:s1/4"}}},
	}}
	clickAll(t, tt, "先给结论")
	for _, want := range []string{"先给结论的正文", "替代了", "先讲背景", "用过 4 次"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("第一版") {
		t.Fatal("versions show before being asked for")
	}
	clickAll(t, tt, "版本")
	for _, want := range []string{"第二版", "你编辑", "第一版", "你说的 ·", "Fix sync"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	clickAll(t, tt, "收起")
	if tt.HasText("先给结论的正文") {
		t.Fatal("the entry did not close")
	}
}

func TestMemoryCandidates(t *testing.T) {
	target := memEntry("g1", "先给结论", 1, scopeGlobal)
	for _, tc := range []struct {
		name    string
		cand    proto.ProfileCandidate
		title   string
		note    string
		button  string
		dropped bool
		shows   []string
	}{
		{"create", proto.ProfileCandidate{Action: "create", Entry: proto.ProfileEntry{ID: "c1", Title: "不用敬语", Body: "新的正文", Scope: scopeGlobal}}, "不用敬语", "提议新增", "确认", false, []string{"新的正文"}},
		{"update", proto.ProfileCandidate{Action: "update", Target: "g1", Entry: proto.ProfileEntry{ID: "c1", Title: "结论优先", Body: "新的正文", Scope: scopeGlobal}}, "结论优先", "提议修改", "确认", false, []string{"现在", "先给结论的正文", "新的正文"}},
		{"withdraw", proto.ProfileCandidate{Action: "withdraw", Target: "g1", Entry: proto.ProfileEntry{ID: "c1", Title: "x", Scope: scopeGlobal}}, "先给结论", "提议撤回", "撤回", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cand.Scopes = []proto.ProfileScope{scopeGlobal, scopeRepo}
			data := Memory{Entries: []proto.ProfileEntry{target}, Candidates: []proto.ProfileCandidate{tc.cand}}

			a, h, tt := openMemoryOn(t, data)
			// The candidate's row comes first, above the entry of the same title.
			a.openEntry("c1")
			tt.Frame()
			for _, want := range append(tc.shows, tc.note) {
				if !tt.HasText(want) {
					t.Errorf("no %q in %q", want, tt.Texts())
				}
			}
			_, menu := tt.Find("范围：全局")
			if menu == tc.dropped {
				t.Errorf("scope dropdown %v, want %v", menu, !tc.dropped)
			}
			if !tc.dropped {
				clickAll(t, tt, "范围：全局", "hoplet")
			}
			clickAll(t, tt, tc.button)
			if len(h.mem.writes) != 1 {
				t.Fatalf("writes %+v", h.mem.writes)
			}
			p := h.mem.writes[0].(proto.ProfileAcceptParams)
			if p.ID != "c1" || tc.dropped != (p.Scope == nil) || (p.Scope != nil && *p.Scope != scopeRepo) {
				t.Fatalf("params %+v", p)
			}
			if a.m.entry != "" {
				t.Fatalf("entry %q still open", a.m.entry)
			}

			a, h, tt = openMemoryOn(t, data)
			a.openEntry("c1")
			tt.Frame()
			clickAll(t, tt, "拒绝")
			if len(h.mem.writes) != 1 {
				t.Fatalf("writes %+v", h.mem.writes)
			}
			if r := h.mem.writes[0].(proto.ProfileRejectParams); r.ID != "c1" {
				t.Fatalf("params %+v", r)
			}
		})
	}
}

func TestMemoryAcceptKeepsScope(t *testing.T) {
	cand := proto.ProfileCandidate{Action: "create", Entry: proto.ProfileEntry{ID: "c1", Title: "不用敬语", Scope: scopeGlobal}, Scopes: []proto.ProfileScope{scopeGlobal, scopeRepo}}
	_, h, tt := openMemoryOn(t, Memory{Candidates: []proto.ProfileCandidate{cand}})
	clickAll(t, tt, "不用敬语", "确认")
	if p := h.mem.writes[0].(proto.ProfileAcceptParams); p.Scope != nil {
		t.Fatalf("scope %+v", p.Scope)
	}
}

func TestMemoryOpenWithdrawn(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.mem.data = Memory{Entries: []proto.ProfileEntry{
		memEntry("g1", "先给结论", 1, scopeGlobal),
		{ID: "w1", Version: 1, Status: "withdrawn", Scope: scopeGlobal, Title: "已经撤回的"},
	}}
	h.mem.details = map[string]proto.ProfileDetail{"w1": {Entry: h.mem.data.Entries[1]}}
	a.OpenMemory("w1")
	tt.Frame()
	if !tt.HasText("这条记忆已撤回") || tt.HasText("先给结论") {
		t.Fatalf("texts %q", tt.Texts())
	}
}
