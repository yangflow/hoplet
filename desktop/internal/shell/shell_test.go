package shell

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/icons"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// host records what the shell asks for; calls holds the 9b requests as
// the parameters they were given.
type host struct {
	sendErr    error // what Send answers
	diffs      map[proto.SessionID]proto.SessionDiff
	patched    []proto.SessionID            // the sessions DiffPatch was asked for
	ranged     map[string]proto.SessionDiff // what DiffPatch answers for a range, by "range" or "commit:sha"
	diffAsked  []proto.DiffParams           // what DiffPatch was asked
	commits    proto.SessionCommits         // what Commits answers
	commitsFor []proto.CommitsParams        // what Commits was asked
	held       bool                         // DiffPatch answers only when pending runs
	pending    func()
	lists      map[string]proto.FileList    // what Files answers, by directory
	listed     []proto.FilesListParams      // what Files was asked
	contents   map[string]proto.FileContent // what ReadFile answers, by path ("base:" for the baseline)
	read       []proto.FilesReadParams      // what ReadFile was asked
	reconnects int
	updates    []proto.UpdateParams
	stopped    []proto.SessionID
	archived   []proto.SessionID
	revealed   []string
	calls      []any
	followed   []proto.SessionID
	created    func(proto.Session) // the pending Create's done
	outputs    map[string]proto.ToolOutput
	outputErrs map[string]error
	models     []proto.HarnessInfo
	states     map[proto.SessionID]proto.SessionState // what Session answers
	asked      []proto.SessionID                      // the sessions Session was asked for
	folder     string
	records    map[string]proto.EventsPage // what Older answers for a subagent's record
}

func (h *host) Update(p proto.UpdateParams) { h.updates = append(h.updates, p) }
func (h *host) Stop(id proto.SessionID)     { h.stopped = append(h.stopped, id) }
func (h *host) Archive(id proto.SessionID)  { h.archived = append(h.archived, id) }
func (h *host) Reveal(dir string)           { h.revealed = append(h.revealed, dir) }
func (h *host) Create(p proto.CreateParams, done func(proto.Session)) {
	h.calls = append(h.calls, p)
	h.created = done
}
func (h *host) Send(p proto.SendParams, done func(error)) {
	p.Key = "" // random
	h.calls = append(h.calls, p)
	if done != nil {
		done(h.sendErr)
	}
}
func (h *host) Steer(p proto.SteerParams)                 { h.calls = append(h.calls, p) }
func (h *host) Interrupt(id proto.SessionID)              { h.calls = append(h.calls, "interrupt "+string(id)) }
func (h *host) Respond(p proto.RespondParams)             { p.Key = ""; h.calls = append(h.calls, p) }
func (h *host) Answer(p proto.AnswerParams)               { h.calls = append(h.calls, p) }
func (h *host) SetPermission(p proto.SetPermissionParams) { h.calls = append(h.calls, p) }
func (h *host) SetPlan(p proto.SetPlanParams)             { h.calls = append(h.calls, p) }
func (h *host) SetEffort(p proto.SetEffortParams)         { h.calls = append(h.calls, p) }
func (h *host) SetFast(p proto.SetFastParams)             { h.calls = append(h.calls, p) }
func (h *host) SetUltracode(p proto.SetUltracodeParams)   { h.calls = append(h.calls, p) }
func (h *host) Session(id proto.SessionID, done func(proto.SessionState)) {
	h.asked = append(h.asked, id)
	if st, ok := h.states[id]; ok {
		done(st)
	}
}
func (h *host) SetModel(p proto.SetModelParams)     { h.calls = append(h.calls, p) }
func (h *host) EditQueued(p proto.QueueEditParams)  { h.calls = append(h.calls, p) }
func (h *host) DeleteQueued(p proto.QueueKeyParams) { h.calls = append(h.calls, p) }
func (h *host) MoveQueued(p proto.QueueMoveParams)  { h.calls = append(h.calls, p) }
func (h *host) Output(p proto.OutputParams, done func(proto.ToolOutput, error)) {
	h.calls = append(h.calls, p)
	if err, ok := h.outputErrs[p.ToolCall]; ok {
		done(proto.ToolOutput{}, err)
	} else if o, ok := h.outputs[p.ToolCall]; ok {
		done(o, nil)
	}
}
func (h *host) Older(p proto.EventsParams, done func(proto.EventsPage)) {
	if p.Agent != "" {
		h.calls = append(h.calls, fmt.Sprintf("older %s %s %d", p.Session, p.Agent, p.BeforeSeq))
		if page, ok := h.records[p.Agent]; ok {
			done(page)
		}
		return
	}
	h.calls = append(h.calls, fmt.Sprintf("older %s %d", p.Session, p.BeforeSeq))
}
func (h *host) Diff(id proto.SessionID, done func(proto.SessionDiff)) {
	if d, ok := h.diffs[id]; ok {
		done(d)
	}
}
func (h *host) DiffPatch(p proto.DiffParams, done func(proto.SessionDiff, error)) {
	h.patched = append(h.patched, p.Session)
	h.diffAsked = append(h.diffAsked, p)
	if h.held {
		h.pending = func() { h.DiffPatch(p, done) }
		return
	}
	if p.Range != "" && p.Range != proto.DiffRangeSession {
		key := string(p.Range)
		if p.Range == proto.DiffRangeCommit {
			key = "commit:" + p.Commit
		}
		if d, ok := h.ranged[key]; ok {
			done(d, nil)
		}
		return
	}
	if d, ok := h.diffs[p.Session]; ok {
		done(d, nil)
	}
}
func (h *host) Commits(p proto.CommitsParams, done func(proto.SessionCommits, error)) {
	h.commitsFor = append(h.commitsFor, p)
	done(h.commits, nil)
}
func (h *host) Files(p proto.FilesListParams, done func(proto.FileList, error)) {
	h.listed = append(h.listed, p)
	key := p.Path
	if p.All {
		key = "*" // every file, as the filter and the palette read them
	}
	if fl, ok := h.lists[key]; ok {
		done(fl, nil)
	}
}
func (h *host) ReadFile(p proto.FilesReadParams, done func(proto.FileContent, error)) {
	h.read = append(h.read, p)
	key := p.Path
	if p.Baseline {
		key = "base:" + key
	}
	if fc, ok := h.contents[key]; ok {
		done(fc, nil)
	}
}
func (h *host) Open(path string)                         { h.calls = append(h.calls, "open "+path) }
func (h *host) CopyPath(path string)                     { h.calls = append(h.calls, "copy "+path) }
func (h *host) Reconnect()                               { h.reconnects++ }
func (h *host) Harnesses(done func([]proto.HarnessInfo)) { done(h.models) }
func (h *host) Follow(id proto.SessionID, _ uint64)      { h.followed = append(h.followed, id) }
func (h *host) ChooseFolder(done func(string)) {
	if h.folder != "" {
		done(h.folder)
	}
}

// last returns the last 9b request.
func (h *host) last() any {
	if len(h.calls) == 0 {
		return nil
	}
	return h.calls[len(h.calls)-1]
}

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
	for _, want := range []string{"hoplet", "site", "Fix sync", "Add tray", "新会话", "未读", "3 小时", "studio", "全部机器"} {
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
	if err := tt.ChooseMenuItem("在 Finder 中显示目录"); err != nil {
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
	if err := tt.ChooseMenuItem("显示", "需要我处理的  1"); err != nil {
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
	a.Fail(errors.New("session not found"), nil)
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

func TestBackForward(t *testing.T) {
	a, _, tt, states := newApp(t)
	for _, title := range []string{"Fix sync", "Add tray", "Old idea"} {
		if err := tt.Click(title); err != nil {
			t.Fatal(err)
		}
	}
	if st := (*states)[len(*states)-1]; !st.CanBack || st.CanForward {
		t.Fatalf("after three selections %+v", st)
	}
	if err := tt.Click("后退 ⌘["); err != nil {
		t.Fatal(err)
	}
	if a.selected != "s2" {
		t.Fatalf("back selected %q", a.selected)
	}
	a.Back()
	if a.Forward(); a.selected != "s2" {
		t.Fatalf("back then forward selected %q", a.selected)
	}
	// Selecting after going back drops what was ahead.
	a.Select("s3")
	tt.Frame()
	if st := (*states)[len(*states)-1]; st.CanForward || !st.CanBack {
		t.Fatalf("after a new selection %+v", st)
	}
	// Back skips sessions that are gone.
	a.cat.Apply(proto.CatalogItem{Snapshot: true, Version: 9, Sessions: []proto.Session{
		{ID: "s1", Mode: proto.ModeCode, Title: "Fix sync"}, {ID: "s3", Mode: proto.ModeCode},
	}})
	if a.Back(); a.selected != "s1" {
		t.Fatalf("back over a missing session selected %q", a.selected)
	}
}

func TestTopRowCentersOnTrafficLights(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 78})
	tt.Frame()
	for _, name := range []string{"隐藏侧栏 ⌃⌘S", "后退 ⌘[", "前进 ⌘]", "Code ⌘2"} {
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		if mid := r.Y + r.H/2; mid < 25.5 || mid > 26.5 {
			t.Errorf("%s centered at %.1f, want 26 (the traffic lights)", name, mid)
		}
		if name == "隐藏侧栏 ⌃⌘S" && r.X < 78 {
			t.Errorf("%s at x %.1f under the traffic lights", name, r.X)
		}
	}
	a.ToggleSidebar()
	tt.Frame()
	if r, ok := tt.Find("显示侧栏 ⌃⌘S"); !ok || r.Y+r.H/2 < 25.5 || r.Y+r.H/2 > 26.5 || r.X < 78 {
		t.Fatalf("show-sidebar button at %+v", r)
	}
}

var errDaemon = errors.New("dial: no such file")

func TestSidebarKeys(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if err := tt.Click("Add tray"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	if a.selected != "s1" {
		t.Fatalf("down from Add tray selected %q", a.selected)
	}
	tt.Key(0, ui.KeyUp)
	if a.selected != "s2" {
		t.Fatalf("up selected %q", a.selected)
	}
	s4, _ := a.cat.Get("s4")
	s4.Pinned = true
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{s4}})
	tt.Frame()
	tt.Key(ui.Cmd|ui.Alt, ui.Key1)
	if a.selected != "s4" {
		t.Fatalf("⌘⌥1 selected %q", a.selected)
	}
}

func TestCompanionMenuAndDetails(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if err := tt.Click("Personal ⌘1"); err != nil {
		t.Fatal(err)
	}
	a.Select("p1")
	tt.Frame()
	if err := tt.RightClick("小助手"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶"); err == nil {
		t.Fatal("a companion's menu pins")
	}
	r, ok := tt.Find("小助手")
	if !ok {
		t.Fatal("no row")
	}
	tt.Move(r.X+2, r.Y+2)
	tt.Frame()
	if tt.HasText("置顶 ⇧⌘P") || tt.HasText("归档 ⌘⌫") {
		t.Fatal("a companion's row offers pin or archive")
	}
	if err := tt.Click("伙伴详情 ⌘I"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	for _, want := range []string{"Hermes", "studio · 在线"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestToasts(t *testing.T) {
	a, _, tt, _ := newApp(t)
	a.Select("s1")
	tt.Frame()
	retried := 0
	a.Fail(errors.New("timeout"), func() { retried++ })
	tt.Frame()
	tt.Advance(10 * time.Second)
	if !tt.HasText("没做成：timeout") {
		t.Fatalf("a failure went away by itself: %q", tt.Texts())
	}
	if err := tt.Click("重试"); err != nil || retried != 1 || tt.HasText("没做成：timeout") {
		t.Fatalf("retry: %v, %d, %q", err, retried, tt.Texts())
	}
	a.Fail(errors.New("gone"), nil)
	tt.Frame()
	if tt.HasText("重试") {
		t.Fatal("a failure that cannot be retried offers to")
	}
	if err := tt.Click("关闭"); err != nil || tt.HasText("没做成：gone") {
		t.Fatalf("close: %v, %q", err, tt.Texts())
	}
	a.hint("再按一次 ⎋ 停止")
	tt.Move(0, 0) // hovering keeps a hint
	tt.Frame()
	if !tt.HasText("再按一次 ⎋ 停止") {
		t.Fatalf("no hint in %q", tt.Texts())
	}
	tt.Advance(5 * time.Second)
	if tt.HasText("再按一次 ⎋ 停止") {
		t.Fatal("the hint stayed")
	}
}

func TestEmptyLists(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if err := tt.Click("排列"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("显示", "运行中  1"); err != nil {
		t.Fatalf("%v", err)
	}
	s3, _ := a.cat.Get("s3")
	s3.Status = proto.StatusIdle
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{s3}})
	tt.Frame()
	if !tt.HasText("没有运行中的会话") {
		t.Fatalf("no empty filter in %q", tt.Texts())
	}
	if err := tt.Click("显示全部"); err != nil || !tt.HasText("Fix sync") {
		t.Fatalf("show all: %v", err)
	}

	b, _, tb, _ := newApp(t)
	b.Apply(proto.CatalogItem{Snapshot: true, Version: 7})
	tb.Frame()
	if !tb.HasText("还没有会话") || tb.HasText("新建会话 ⌘N") {
		t.Fatalf("an empty list says why, with no button: %q", tb.Texts())
	}
	if err := tb.Click("Personal ⌘1"); err != nil {
		t.Fatal(err)
	}
	// 新建伙伴 is the list header's ＋ alone.
	if !tb.HasText("还没有伙伴") || strings.Count(strings.Join(tb.Texts(), "|"), "新建伙伴 ⇧⌘N") != 1 {
		t.Fatalf("no empty companions in %q", tb.Texts())
	}
}

func TestNewSessionElsewhereFlashes(t *testing.T) {
	a, _, tt, _ := newApp(t)
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{{ID: "s9", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Directory: "/w/hoplet", Status: proto.StatusIdle, Title: "From the phone", UpdatedAt: tt.Now()}}})
	tt.Frame()
	if _, ok := a.s.flash["s9"]; !ok {
		t.Fatal("a session made elsewhere does not light")
	}
	tt.Advance(flashTime + 50*time.Millisecond)
	if _, ok := a.s.flash["s9"]; ok {
		t.Fatal("still lit")
	}
}

func TestFlashWithLessMotion(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{{ID: "s9", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Directory: "/w/hoplet", Status: proto.StatusIdle, Title: "From the phone", UpdatedAt: tt.Now()}}})
	tt.Frame()
	tt.Advance(flashTime - 100*time.Millisecond)
	if _, ok := a.s.flash["s9"]; !ok {
		t.Fatal("the light went before its time")
	}
	tt.Advance(200 * time.Millisecond)
	if _, ok := a.s.flash["s9"]; ok {
		t.Fatal("still lit")
	}
}

func TestSidebarResizes(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if a.side != theme.SidebarWidth {
		t.Fatalf("starts at %v", a.side)
	}
	drag := func(dx float32) {
		x := a.side
		tt.Press(x, 400)
		tt.Move(x+dx, 400)
		tt.Release(x+dx, 400)
		tt.Frame()
	}
	drag(-30)
	if a.side != theme.SidebarWidth-30 {
		t.Fatalf("narrowed to %v", a.side)
	}
	drag(-200)
	if a.side != theme.SidebarMin {
		t.Fatalf("narrowed past the least to %v", a.side)
	}
	drag(400)
	if a.side != theme.SidebarMax {
		t.Fatalf("widened past the most to %v", a.side)
	}
	if r, ok := tt.Find("全部机器"); !ok || r.X+r.W > a.side {
		t.Fatalf("the sidebar's header at %+v, width %v", r, a.side)
	}
}

// The sidebar stops narrowing where its top row would no longer fit
// beside the traffic lights.
func TestSidebarFitsItsTopRow(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 76})
	tt.Frame()
	x := a.side
	tt.Press(x, 400)
	tt.Move(x-200, 400)
	tt.Release(x-200, 400)
	tt.Frame()
	least := sideMin(ui.TitleBar{Left: 76})
	if a.side != least || least <= theme.SidebarMin {
		t.Fatalf("narrowed to %v, least %v", a.side, least)
	}
	r, ok := tt.Find("Code ⌘2")
	if !ok || r.X+r.W > a.side {
		t.Fatalf("the mode switch at %+v in a sidebar %v wide", r, a.side)
	}
}

// With no session selected (at start, after switching modes or archiving
// the selected one) the new-session page shows.
func TestOpensOnNewSession(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if !a.s.newPage {
		t.Fatal("the app opens without the new-session page")
	}
	a.Select("s1")
	tt.Frame()
	a.Archive()
	tt.Frame()
	if !a.s.newPage {
		t.Fatal("archiving the selected session leaves no page")
	}
	a.SetMode(proto.ModePersonal)
	tt.Frame()
	if !a.s.newPage || !tt.HasText("新伙伴") {
		t.Fatalf("Personal shows %q", tt.Texts())
	}
}

// Back and Forward go through sessions, the inbox and the new-session
// page, skipping sessions that are gone.
func TestBackForwardPages(t *testing.T) {
	a, _, tt, _ := newApp(t)
	at := func(want string) {
		t.Helper()
		tt.Frame()
		var got string
		switch {
		case a.inbox:
			got = "inbox"
		case a.s.newPage:
			got = "new"
		default:
			got = a.selected
		}
		if got != want {
			t.Fatalf("at %q, want %q", got, want)
		}
	}
	a.Select("s1")
	a.OpenInbox()
	a.NewSession()
	at("new")
	a.Back()
	at("inbox")
	if !tt.HasText("收件箱") {
		t.Fatalf("no inbox title in %q", tt.Texts())
	}
	a.Back()
	at("s1")
	a.Forward()
	at("inbox")
	a.Select("s2")
	a.Forward() // nothing after the newest page
	at("s2")

	// A session gone from the catalog is skipped.
	a.Select("s3")
	a.OpenInbox()
	var keep []proto.Session
	for _, s := range a.cat.List(catalog.View{Mode: proto.ModeCode}).Groups {
		for _, x := range s.Sessions {
			if x.ID != "s3" {
				keep = append(keep, x)
			}
		}
	}
	a.Apply(proto.CatalogItem{Snapshot: true, Version: a.cat.Version() + 1, Sessions: keep})
	a.Back()
	at("s2")
}

// The sidebar's inbox entry counts the sessions that need me and opens
// the inbox, as ⇧⌘I does.
func TestInboxEntry(t *testing.T) {
	a, _, tt, _ := newApp(t)
	if !tt.HasText("1") {
		t.Fatalf("no badge for the one waiting session in %q", tt.Texts())
	}
	if err := tt.Click("收件箱"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.inbox {
		t.Fatal("the entry does not open the inbox")
	}
	a.Select("s1")
	a.OpenInbox() // as ⇧⌘I does
	tt.Frame()
	if !a.inbox || a.selected != "" {
		t.Fatalf("inbox %v, selected %q", a.inbox, a.selected)
	}
}

// Renaming ends with Enter or a press outside the title, which keep the
// new title, or Escape, which drops it.
func TestRenameEnds(t *testing.T) {
	a, h, tt, _ := newApp(t)
	a.Select("s1")
	a.Rename()
	tt.Frame()
	if !tt.Focused("标题") {
		t.Fatal("the title is not being edited")
	}
	a.draft = "Fix sync now"
	if err := tt.Click("全部机器"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.renaming != "" {
		t.Fatal("a press outside leaves the title in editing")
	}
	if n := len(h.updates); n == 0 || h.updates[n-1].Title == nil || *h.updates[n-1].Title != "Fix sync now" {
		t.Fatalf("updates %+v", h.updates)
	}
	a.Rename()
	tt.Frame()
	a.draft = "dropped"
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.renaming != "" || *h.updates[len(h.updates)-1].Title == "dropped" {
		t.Fatal("Escape kept the edit")
	}
}
