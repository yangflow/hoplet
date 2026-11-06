package shell

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// panelShown reports whether the right panel is on screen.
func panelShown(tt *ui.Tester) bool {
	_, ok := tt.Find("收起右栏 ⌥⌘I")
	return ok
}

// viewerShown reports whether the panel's viewer is on screen.
func viewerShown(tt *ui.Tester) bool {
	_, ok := tt.Find("放大到占满正文区")
	return ok
}

// settle lets a panel or sidebar move finish.
func settle(tt *ui.Tester) {
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	tt.Frame()
}

func TestPanelOpensAndCloses(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	if panelShown(tt) {
		t.Fatal("the panel shows before it is opened")
	}
	if err := tt.Click("显示右栏 ⌥⌘I"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !panelShown(tt) || a.hideSide {
		t.Fatalf("open with room for both: texts %q, sidebar hidden %v", tt.Texts(), a.hideSide)
	}
	if _, ok := tt.Find("显示右栏 ⌥⌘I"); ok {
		t.Error("the title bar still offers to show the open panel")
	}
	if err := tt.Click("收起右栏 ⌥⌘I"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if panelShown(tt) {
		t.Error("the panel stays after closing")
	}
}

// TestPanelMakesRoom opens the panel in a window too narrow for the
// sidebar, the conversation and the panel's column: the sidebar goes, and
// comes back when the panel closes. Narrower still, the panel waits for
// room.
func TestPanelMakesRoom(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1000, 700)
	a.Select("s1")
	settle(tt)
	a.TogglePanel()
	settle(tt)
	if !a.hideSide || !panelShown(tt) {
		t.Fatalf("1000 wide: sidebar hidden %v, panel shown %v", a.hideSide, panelShown(tt))
	}
	a.TogglePanel()
	settle(tt)
	if a.hideSide {
		t.Error("the sidebar stays hidden after the panel closed")
	}

	tt.SetSize(700, 700)
	a.TogglePanel()
	settle(tt)
	if panelShown(tt) {
		t.Error("the panel shows with less than content-min left for the conversation")
	}
}

// TestPanelViewerOnOpen checks that the viewer comes with the first tab
// and goes with the last: until then the panel is its column alone.
func TestPanelViewerOnOpen(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(theme.WindowWidth, theme.WindowHeight)
	a.Select("s1")
	a.TogglePanel()
	settle(tt)
	if !panelShown(tt) || viewerShown(tt) {
		t.Fatalf("opened: panel %v, viewer %v", panelShown(tt), viewerShown(tt))
	}
	ps := a.panelFor("s1")
	a.openTab(ps, panelTab{path: "cmd/main.go"})
	settle(tt)
	if !viewerShown(tt) || a.hideSide {
		t.Fatalf("a file open at 1440: viewer %v, sidebar hidden %v", viewerShown(tt), a.hideSide)
	}
	if want := float32(theme.WindowWidth) - a.side - theme.ContentMin; a.panelTarget != want {
		t.Errorf("the panel is %v wide beside the sidebar, want %v", a.panelTarget, want)
	}
	a.closeTab(ps, 0)
	settle(tt)
	if viewerShown(tt) || !panelShown(tt) {
		t.Errorf("last tab closed: viewer %v, panel %v", viewerShown(tt), panelShown(tt))
	}
}

// TestPanelYieldsToSidebar shows the sidebar the panel hid: it stays, and
// the panel that no longer fits beside it closes.
func TestPanelYieldsToSidebar(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1000, 700)
	a.Select("s1")
	settle(tt)
	a.TogglePanel()
	settle(tt)
	if !a.hideSide {
		t.Fatal("the panel did not hide the sidebar")
	}
	a.ToggleSidebar()
	settle(tt)
	settle(tt)
	if a.hideSide || a.panelOpen || panelShown(tt) {
		t.Errorf("sidebar hidden %v, panel open %v, shown %v", a.hideSide, a.panelOpen, panelShown(tt))
	}
}

func TestPanelOnlyForCodeSessions(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	a.Select("p1")
	settle(tt)
	if _, ok := tt.Find("显示右栏 ⌥⌘I"); ok {
		t.Error("a companion offers the right panel")
	}
	a.TogglePanel()
	settle(tt)
	if a.panelOpen || panelShown(tt) {
		t.Error("the panel opened for a companion")
	}
}

// TestPanelKeepsEachSession checks that a session keeps its own column and
// tabs.
func TestPanelKeepsEachSession(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	a.Select("s1")
	a.ShowChanges()
	a.openTab(a.panelFor("s1"), panelTab{path: "cmd/main.go"})
	settle(tt)
	if !a.panelOpen || a.panelFor("s1").column != columnChanges || !tt.HasText("main.go") {
		t.Fatalf("s1: open %v, column %v, texts %q", a.panelOpen, a.panelFor("s1").column, tt.Texts())
	}
	a.Select("s2")
	settle(tt)
	if a.panelFor("s2").column != columnTree || tt.HasText("main.go") || !panelShown(tt) {
		t.Errorf("s2 shows s1's panel: texts %q", tt.Texts())
	}
	a.Select("s1")
	settle(tt)
	if a.panelFor("s1").column != columnChanges || !tt.HasText("main.go") {
		t.Errorf("s1 lost its panel: texts %q", tt.Texts())
	}
}

func TestPanelTabs(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	a.Select("s1")
	a.TogglePanel()
	ps := a.panelFor("s1")
	a.openTab(ps, panelTab{path: "cmd/main.go"})
	a.openTab(ps, panelTab{changes: true})
	a.openTab(ps, panelTab{path: "cmd/main.go"}) // open already: shown, not added
	settle(tt)
	if len(ps.tabs) != 2 || ps.active != 0 {
		t.Fatalf("tabs %+v, active %d", ps.tabs, ps.active)
	}
	if err := tt.Click("变更"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ps.active != 1 {
		t.Fatalf("clicking the changes tab: active %d", ps.active)
	}
	if err := tt.Click("关闭 变更"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(ps.tabs) != 1 || ps.active != 0 {
		t.Fatalf("after closing: tabs %+v, active %d", ps.tabs, ps.active)
	}
	if err := tt.Click("放大到占满正文区"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !ps.zoom || tt.HasText("Fix sync") && tt.HasText("消息") {
		t.Fatalf("zoomed: %v, texts %q", ps.zoom, tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if ps.zoom {
		t.Error("⎋ left the viewer zoomed")
	}
}

func TestPanelRailCountsChanges(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": {Session: "s1", Repos: []proto.RepoDiff{{Path: ".", Files: []proto.FileDiff{{Path: "a.go"}, {Path: "b.go"}}}}}}
	a.Select("s1")
	a.TogglePanel()
	settle(tt)
	settle(tt)
	if !tt.HasText("2") {
		t.Errorf("the changes switch does not count 2 files: %q", tt.Texts())
	}
	if err := tt.Click("变更 ⌥⌘D"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.panelFor("s1").column != columnChanges {
		t.Error("the switch did not move to the changes")
	}
}
