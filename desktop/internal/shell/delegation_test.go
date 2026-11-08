package shell

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// companionWithWork adds to the catalog a child of p1 and a Code session it
// handed off, and fills p1's chat.
func companionWithWork(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	now := tt.Now()
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{
		{ID: "c1", Mode: proto.ModePersonal, Parent: "p1", Harness: proto.HarnessRef{Kind: "hermes"}, Status: proto.StatusIdle, Title: "查酒店", UpdatedAt: now},
		{ID: "k1", Mode: proto.ModeCode, Parent: "p1", Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/w/hoplet", Status: proto.StatusIdle, Title: "修同步", UpdatedAt: now},
	}})
	a.Select("p1")
	a.ApplySession("p1", proto.SessionItem{Events: events(t, now, 1, 1,
		proto.SessionDelegated{Session: "c1", Kind: proto.DelegatedSession, Title: "旧标题"},
		proto.SessionDelegated{Session: "k1", Kind: proto.HandedOff, Title: "修同步"},
		proto.ArtifactPublished{Artifact: "a1", Name: "行程", Kind: proto.ArtifactMarkdown, Version: 3, By: proto.ArtifactByCLI},
		proto.Message{ID: "r1", Role: proto.RoleUser, Text: "找到三家", From: "c1"},
		proto.Message{ID: "r2", Role: proto.RoleUser, Text: "测试挂了", From: "k1", Report: proto.ReportError},
		proto.Message{ID: "r3", Role: proto.RoleUser, Text: "要装依赖", From: "k1", Report: proto.ReportApproval},
	)})
	settle(tt)
}

func TestEventRowsAndReportCards(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	companionWithWork(t, a, tt)
	for _, label := range []string{"委派 查酒店", "转交 Code 修同步", "发布 行程", "查酒店 完成", "修同步 出错了", "修同步 等你审批"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Errorf("no %q in %q", label, tt.Texts())
			continue
		}
		if r.W > 0.78*theme.PersonalColumn {
			t.Errorf("%q is %v wide", label, r.W)
		}
	}
	for _, text := range []string{"· 完成", "· 出错了", "· 等你审批", "v3", "子会话 · "} {
		if !strings.Contains(strings.Join(tt.Texts(), "\n"), text) {
			t.Errorf("no %q in %q", text, tt.Texts())
		}
	}
	if tt.HasText("找到三家") == false {
		t.Errorf("no report body in %q", tt.Texts())
	}
}

func TestEventsAndReportsOpenTheirTargets(t *testing.T) {
	tests := []struct {
		label string
		want  proto.SessionID
		mode  proto.Mode
	}{
		{"委派 查酒店", "c1", proto.ModePersonal},
		{"转交 Code 修同步", "k1", proto.ModeCode},
		{"查酒店 完成", "c1", proto.ModePersonal},
		{"修同步 出错了", "k1", proto.ModeCode},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			a, _, tt, _ := newApp(t)
			tt.SetSize(1700, 900)
			companionWithWork(t, a, tt)
			if err := tt.Click(tc.label); err != nil {
				t.Fatal(err)
			}
			if a.selected != string(tc.want) || a.mode != tc.mode {
				t.Errorf("selected %q in %v, want %q in %v", a.selected, a.mode, tc.want, tc.mode)
			}
		})
	}
}

func TestPublicationOpensItsTab(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	h.artifacts = map[proto.SessionID][]proto.Artifact{"p1": {{ID: "a1", Session: "p1", Name: "行程", Kind: proto.ArtifactMarkdown, Version: 3, UpdatedAt: tt.Now()}}}
	companionWithWork(t, a, tt)
	if err := tt.Click("发布 行程"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !a.panelOpen || a.selected != "p1" {
		t.Fatalf("panel open %v, selected %q", a.panelOpen, a.selected)
	}
	ps := a.panelFor("p1")
	if len(ps.tabs) != 1 || ps.tabs[0].artifact != "a1" {
		t.Errorf("tabs = %+v", ps.tabs)
	}
}

func TestChildSessionStaysUnderItsCompanion(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	companionWithWork(t, a, tt)
	for _, l := range a.cat.List(catalog.View{Mode: proto.ModePersonal}).Groups {
		for _, s := range l.Sessions {
			if s.ID == "c1" {
				t.Error("the child is listed in the sidebar")
			}
		}
	}
	a.Select("c1")
	settle(tt)
	if p, _ := a.cat.Get("p1"); !a.inChild(p) {
		t.Error("the companion's row is not selected")
	}
	if !tt.HasText("小助手") || !tt.HasText("查酒店") {
		t.Fatalf("no title with its companion: %q", tt.Texts())
	}
	if err := tt.Click("回到 小助手"); err != nil {
		t.Fatal(err)
	}
	if a.selected != "p1" {
		t.Errorf("selected %q", a.selected)
	}
	if _, ok := tt.Find("回到 小助手"); ok {
		t.Error("the main session has a link back")
	}
}

func TestCodeSidebarListsHandedOffSessions(t *testing.T) {
	a, _, tt, _ := newApp(t)
	companionWithWork(t, a, tt)
	var found bool
	for _, g := range a.cat.List(catalog.View{Mode: proto.ModeCode}).Groups {
		for _, s := range g.Sessions {
			found = found || s.ID == "k1"
		}
	}
	if !found {
		t.Error("the handed-off session left the Code sidebar")
	}
}
