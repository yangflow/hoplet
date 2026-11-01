package shell

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/icons"
	"github.com/yangflow/hoplet/proto"
)

// host records what the shell asks for.
type host struct {
	updates  []proto.UpdateParams
	stopped  []proto.SessionID
	archived []proto.SessionID
	revealed []string
}

func (h *host) Update(p proto.UpdateParams) { h.updates = append(h.updates, p) }
func (h *host) Stop(id proto.SessionID)     { h.stopped = append(h.stopped, id) }
func (h *host) Archive(id proto.SessionID)  { h.archived = append(h.archived, id) }
func (h *host) Reveal(dir string)           { h.revealed = append(h.revealed, dir) }

func newApp(t *testing.T) (*App, *host, *ui.Tester, *[]State) {
	t.Helper()
	set, err := icons.Load()
	if err != nil {
		t.Fatal(err)
	}
	h := &host{}
	var states []State
	a := New(set, h, func(s State) { states = append(states, s) })
	tt := ui.NewTester(a.View, 1000, 700)
	tt.SetVibrancy(true)
	now := tt.Now()
	a.Connected("studio")
	a.Apply(proto.CatalogItem{Snapshot: true, Version: 5, Sessions: []proto.Session{
		{ID: "s1", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/w/hoplet", Status: proto.StatusIdle,
			Title: "Fix sync", Unread: true, UpdatedAt: now.Add(-5 * time.Minute)},
		{ID: "s2", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "codex"}, Directory: "/w/hoplet", Status: proto.StatusWaiting,
			Title: "Add tray", UpdatedAt: now.Add(-time.Minute)},
		{ID: "s3", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/w/site", Status: proto.StatusRunning,
			UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "s4", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/w/site", Status: proto.StatusIdle,
			Title: "Old idea", UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "p1", Mode: proto.ModePersonal, Harness: proto.HarnessRef{Kind: "hermes"}, Status: proto.StatusIdle,
			Title: "小助手", Preview: "明天 9 点提醒你", UpdatedAt: now},
	}})
	tt.Frame()
	return a, h, tt, &states
}

func TestSidebarLists(t *testing.T) {
	_, _, tt, states := newApp(t)
	for _, want := range []string{"hoplet", "site", "Fix sync", "Add tray", "新会话", "未读", "3 小时", "studio"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("小助手") {
		t.Error("Code mode lists a companion")
	}
	got := (*states)[len(*states)-1]
	want := State{Connected: true, Counts: got.Counts,
		NeedsYou: []Entry{{"s2", "Add tray"}}, Running: []Entry{{"s3", "新会话"}}}
	if !reflect.DeepEqual(got, want) || got.Counts.NeedsYou != 1 || got.Counts.Running != 1 {
		t.Fatalf("state %+v", got)
	}
}

func TestSelectMarksRead(t *testing.T) {
	a, h, tt, states := newApp(t)
	if err := tt.Click("Fix sync"); err != nil {
		t.Fatal(err)
	}
	off := false
	if len(h.updates) != 1 || !reflect.DeepEqual(h.updates[0], proto.UpdateParams{Session: "s1", Unread: &off}) {
		t.Fatalf("updates %+v", h.updates)
	}
	if (*states)[len(*states)-1].Selected.ID != "s1" || a.selected != "s1" {
		t.Fatalf("selection %+v", (*states)[len(*states)-1].Selected)
	}
	// The title bar shows the selection.
	if r, ok := tt.Find("会话菜单"); !ok || r.W == 0 {
		t.Fatal("no session menu in the title bar")
	}
}

func TestAwayKeepsUnread(t *testing.T) {
	a, h, tt, _ := newApp(t)
	if err := tt.Click("Add tray"); err != nil {
		t.Fatal(err)
	}
	a.SetAway(true)
	s, _ := a.cat.Get("s2")
	s.Unread = true
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{s}})
	if len(h.updates) != 0 {
		t.Fatalf("marked read while away: %+v", h.updates)
	}
	a.SetAway(false)
	off := false
	if len(h.updates) != 1 || !reflect.DeepEqual(h.updates[0], proto.UpdateParams{Session: "s2", Unread: &off}) {
		t.Fatalf("updates after coming back %+v", h.updates)
	}
}

func TestSessionMenu(t *testing.T) {
	_, h, tt, _ := newApp(t)
	if err := tt.RightClick("Add tray"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶"); err != nil {
		t.Fatal(err)
	}
	on := true
	if last := h.updates[len(h.updates)-1]; !reflect.DeepEqual(last, proto.UpdateParams{Session: "s2", Pinned: &on}) {
		t.Fatalf("pin %+v", last)
	}

	if err := tt.RightClick("Add tray"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("在 Finder 中显示"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.revealed, []string{"/w/hoplet"}) {
		t.Fatalf("revealed %v", h.revealed)
	}

	if err := tt.RightClick("Add tray"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("重命名"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Tray icon")
	tt.Key(0, ui.KeyEnter)
	title := "Tray icon"
	if last := h.updates[len(h.updates)-1]; !reflect.DeepEqual(last, proto.UpdateParams{Session: "s2", Title: &title}) {
		t.Fatalf("rename %+v", last)
	}

	if err := tt.RightClick("Add tray"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("归档"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.archived, []proto.SessionID{"s2"}) {
		t.Fatalf("archived %v", h.archived)
	}
}

func TestHoverActions(t *testing.T) {
	_, h, tt, _ := newApp(t)
	r, ok := tt.Find("Add tray")
	if !ok {
		t.Fatal("no row")
	}
	tt.Move(r.X+2, r.Y+2)
	tt.Frame()
	if err := tt.Click("置顶 ⇧⌘P"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	if len(h.updates) == 0 || h.updates[len(h.updates)-1].Pinned == nil {
		t.Fatalf("updates %+v", h.updates)
	}
}

func TestModesAndArrange(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if err := tt.Click("Personal ⌘1"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("小助手") || !tt.HasText("明天 9 点提醒你") || tt.HasText("Fix sync") {
		t.Fatalf("personal texts %q", tt.Texts())
	}
	if err := tt.Click("Code ⌘2"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("排列"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("需要我处理的"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Add tray") || tt.HasText("Fix sync") {
		t.Fatalf("needs-you texts %q", tt.Texts())
	}
	if err := tt.Click("hoplet"); err != nil {
		t.Fatal(err)
	}
	if !a.closed["/w/hoplet"] || tt.HasText("Add tray") {
		t.Fatalf("group not folded: %q", tt.Texts())
	}
}

func TestOffline(t *testing.T) {
	a, _, tt, states := newApp(t)
	a.Lost(errors.New("dial: no such file"))
	tt.Frame()
	if _, ok := tt.Find("断线"); !ok {
		t.Fatalf("texts %q", tt.Texts())
	}
	if (*states)[len(*states)-1].Connected {
		t.Fatal("state still connected")
	}
	a.Fail(errors.New("session not found"))
	tt.Frame()
	if !tt.HasText("没做成：session not found") {
		t.Fatalf("no toast in %q", tt.Texts())
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	for d, want := range map[time.Duration]string{
		10 * time.Second: "刚刚", 5 * time.Minute: "5 分", 3 * time.Hour: "3 小时", 49 * time.Hour: "2 天", 40 * 24 * time.Hour: "9月22日",
	} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("ago %v = %q, want %q", d, got, want)
		}
	}
}
