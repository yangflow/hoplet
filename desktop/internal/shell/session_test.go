package shell

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

// events builds a session's events from seq first, a second apart, ending
// before the tester's clock.
func events(t *testing.T, now time.Time, first uint64, turn uint32, ps ...proto.Payload) []proto.Event {
	t.Helper()
	var out []proto.Event
	for i, p := range ps {
		seq := first + uint64(i)
		ev, err := proto.NewEvent("s1", seq, turn, now.Add(-time.Hour+time.Duration(seq)*time.Second), p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func claudeHarness() proto.HarnessInfo {
	return proto.HarnessInfo{Kind: "claude", Version: "2.1.312", Supported: true,
		Capabilities: proto.Capabilities{SetMode: true, SetModel: true, PlanMode: true,
			Modes:   []proto.PermissionMode{proto.ModeConfirm, proto.ModeAutoEdit, proto.ModeAutoReview, proto.ModeFullAuto},
			Efforts: []proto.Effort{proto.EffortLow, proto.EffortMedium, proto.EffortHigh}},
		Models: []proto.ModelInfo{
			{ID: "default", Model: "claude-opus-5-5", Name: "Default (recommended)", Default: true},
			{ID: "haiku", Model: "claude-haiku-5-5", Name: "Haiku 5.5", Description: "Fastest"},
		}}
}

func TestCodeConversation(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1200, 900)
	h.outputs = map[string]proto.ToolOutput{"t2": {Text: "FAIL bridge_test.go:12\nexit status 1\n", Size: 34}}
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	if err := tt.Click("Fix sync"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.followed, []proto.SessionID{"s1"}) {
		t.Fatalf("followed %v", h.followed)
	}
	out := &proto.BlobRef{SHA256: "x", Size: 34}
	now := tt.Now()
	a.ApplySession("s1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: events(t, now, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "fix the heartbeat"},
		proto.TurnStarted{},
		proto.Thinking{Text: "the pump never closes"},
		proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolDone},
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "go test ./...", Status: proto.ToolFailed, Output: out},
		proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test"},
		proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "ok", DecidedBy: "device:mac"},
		proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "Fixed: the pump **closes** now."},
		proto.FileChange{Path: "/w/hoplet/bridge.go", Op: proto.FileModified},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	)}})
	tt.Frame()
	// The finished turn's steps fold into one line over its reply.
	if tt.HasText("go test ./...") || !tt.HasText("思考 1 次 · 读取 1 个文件 · 运行 1 条命令") {
		t.Fatalf("the group stays open: %q", tt.Texts())
	}
	if err := tt.Click("思考 1 次 · 读取 1 个文件 · 运行 1 条命令"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second) // the group opens
	if err := tt.Click("go test ./..."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame() // the failed call's output arrives
	for _, want := range []string{"fix the heartbeat", "思考过程", "✕ 1 个失败", "go test ./...",
		"FAIL bridge_test.go:12\nexit status 1", "已允许", "Run go test", "改了 1 个文件", "bridge.go"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "closes") {
		t.Errorf("markdown text missing: %q", tt.Texts())
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.OutputParams{Session: "s1", ToolCall: "t2"}) {
		t.Fatalf("last call %#v", got)
	}
}

// selectRunning selects s1 with a running turn.
func selectRunning(t *testing.T, a *App, tt *ui.Tester, extra ...proto.Payload) {
	t.Helper()
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	ps := append([]proto.Payload{proto.Message{ID: "k1", Role: proto.RoleUser, Text: "go"}, proto.TurnStarted{}}, extra...)
	a.ApplySession("s1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: events(t, tt.Now(), 1, 1, ps...)}})
	tt.Frame()
}

func TestComposerSendsQueuesAndSteers(t *testing.T) {
	a, h, tt, _ := newApp(t)
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	tt.Frame()
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hello")
	tt.Key(0, ui.KeyEnter)
	if got := h.last(); !reflect.DeepEqual(got, proto.SendParams{Session: "s1", Text: "hello"}) {
		t.Fatalf("send %#v", got)
	}
	selectRunning(t, a, tt)
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("also lint")
	tt.Key(ui.Cmd, ui.KeyEnter)
	if got := h.last(); !reflect.DeepEqual(got, proto.SteerParams{Session: "s1", Text: "also lint"}) {
		t.Fatalf("steer %#v", got)
	}
	tt.Type("then docs")
	tt.Key(0, ui.KeyEnter) // a turn runs: the daemon queues it
	if got := h.last(); !reflect.DeepEqual(got, proto.SendParams{Session: "s1", Text: "then docs"}) {
		t.Fatalf("queued send %#v", got)
	}
	a.ApplySession("s1", proto.SessionItem{Queue: &proto.Queue{Messages: []proto.QueuedMessage{{Key: "q1", Text: "then docs"}}}})
	tt.Frame()
	if !tt.HasText("排队 1") || !tt.HasText("then docs") {
		t.Fatalf("queue tray %q", tt.Texts())
	}
	// A queued message's steer, edit and delete show under the pointer.
	if tt.HasText("删除") {
		t.Fatalf("delete shows without the pointer: %q", tt.Texts())
	}
	r, _ := tt.Find("then docs")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.QueueKeyParams{Session: "s1", Key: "q1"}) {
		t.Fatalf("delete %#v", got)
	}
	tt.Advance(time.Second) // the composer settles at its new height
	if err := tt.Click("停止 ⎋"); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); got != "interrupt s1" {
		t.Fatalf("stop %#v", got)
	}
}

func TestApprovalInComposer(t *testing.T) {
	a, h, tt, _ := newApp(t)
	selectRunning(t, a, tt, proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test", Detail: "go test -race ./...",
		Options: []proto.ApprovalOption{{ID: "once", Label: "Allow once", Scope: proto.ScopeOnce}, {ID: "always", Label: "Always allow", Scope: proto.ScopeAlways}, {ID: "no", Label: "Deny", Scope: proto.ScopeDeny}}})
	for _, want := range []string{"运行命令", "1 / 1", "go test -race ./...", "允许一次", "总是允许", "拒绝"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if got := h.last(); !reflect.DeepEqual(got, proto.RespondParams{Session: "s1", Approval: "a1", Option: "always"}) {
		t.Fatalf("respond %#v", got)
	}
	tt.Type("use make check instead")
	tt.Key(0, ui.KeyEnter)
	n := len(h.calls)
	if !reflect.DeepEqual(h.calls[n-2], proto.RespondParams{Session: "s1", Approval: "a1", Option: "no"}) ||
		!reflect.DeepEqual(h.calls[n-1], proto.SendParams{Session: "s1", Text: "use make check instead"}) {
		t.Fatalf("deny with a reason: %#v", h.calls[n-2:])
	}
	if err := tt.Click("收起 ⎋"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Always allow") {
		t.Fatal("the approval did not go away")
	}
}

func TestOfflineOutbox(t *testing.T) {
	a, h, tt, _ := newApp(t)
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	a.Lost(errDaemon)
	tt.Frame()
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("later")
	tt.Key(0, ui.KeyEnter)
	if len(h.calls) != 0 || !tt.HasText("重连后发送") || !tt.HasText("· 断开 0 秒") {
		t.Fatalf("calls %#v texts %q", h.calls, tt.Texts())
	}
	if err := tt.Click("立即重连"); err != nil || h.reconnects != 1 {
		t.Fatalf("reconnect: %v, %d", err, h.reconnects)
	}
	a.Connected("studio")
	if got := h.last(); !reflect.DeepEqual(got, proto.SendParams{Session: "s1", Text: "later"}) {
		t.Fatalf("after reconnecting %#v", got)
	}
}

func TestEffortModelAndPermission(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.SetHarnesses(h.models)
	a.Select("s1")
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Haiku 5.5"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetModelParams{Session: "s1", Model: "haiku"}) {
		t.Fatalf("set model %#v", got)
	}
	if err := tt.Click("推理强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("High"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetEffortParams{Session: "s1", Effort: proto.EffortHigh}) {
		t.Fatalf("set effort %#v", got)
	}
	s1, _ := a.cat.Get("s1")
	s1.Effort = proto.EffortHigh // as the daemon reports it
	a.Apply(proto.CatalogItem{Version: a.cat.Version() + 1, Sessions: []proto.Session{s1}})
	tt.Frame()
	if err := tt.Click("默认"); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetEffortParams{Session: "s1"}) {
		t.Fatalf("back to the default effort %#v", got)
	}
	if err := tt.Click("计划模式"); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetPlanParams{Session: "s1", Plan: true}) {
		t.Fatalf("plan %#v", got)
	}
	if tt.HasText("Ultracode") || tt.HasText("快速模式") {
		t.Fatalf("switches the harness lacks: %q", tt.Texts())
	}
	// ⎋ closes the submenu, then the menu.
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.s.runOpen {
		t.Fatal("⎋ leaves the run menu open")
	}
	if err := tt.Click("审批：需确认"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	if err := tt.Click("全自动"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetPermissionParams{Session: "s1", Permission: proto.ModeFullAuto}) {
		t.Fatalf("set permission %#v", got)
	}
}

// Ultracode shows for a harness that has it, on a model with xhigh, and
// marks the button while on.
func TestUltracodeSwitch(t *testing.T) {
	a, h, tt, _ := newApp(t)
	ch := claudeHarness()
	ch.Capabilities.Ultracode = true
	ch.Models[1].Efforts = []proto.Effort{proto.EffortLow, proto.EffortHigh}
	ch.Models[0].Efforts = []proto.Effort{proto.EffortLow, proto.EffortHigh, proto.EffortXHigh}
	h.models = []proto.HarnessInfo{ch}
	a.SetHarnesses(h.models)
	a.Select("s1")
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Ultracode"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetUltracodeParams{Session: "s1", Ultracode: true}) {
		t.Fatalf("ultracode %#v", got)
	}
	s1, _ := a.cat.Get("s1")
	s1.Model, s1.Ultracode = "haiku", true
	a.Apply(proto.CatalogItem{Version: a.cat.Version() + 1, Sessions: []proto.Session{s1}})
	tt.Frame()
	if tt.HasText("Ultracode") {
		t.Fatal("Ultracode offered on a model without xhigh")
	}
}

func TestNewSession(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models, h.folder = []proto.HarnessInfo{claudeHarness()}, "/w/site"
	if err := tt.Click("新建"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("在 Hoplet 上做什么？") || !tt.HasText("选择工作目录") {
		t.Fatalf("new page %q", tt.Texts())
	}
	if err := tt.Click("工作目录"); err != nil { // the folder picker opens at once
		t.Fatal(err)
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("add a tray")
	tt.Key(0, ui.KeyEnter)
	if got := h.last(); !reflect.DeepEqual(got, proto.CreateParams{Mode: proto.ModeCode, Harness: "claude", Directory: "/w/site", Permission: proto.ModeAutoReview}) {
		t.Fatalf("create %#v", got)
	}
	h.created(proto.Session{ID: "n1", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/w/site", Status: proto.StatusIdle})
	if a.selected != "n1" || !reflect.DeepEqual(h.last(), proto.SendParams{Session: "n1", Text: "add a tray"}) {
		t.Fatalf("selected %q, last %#v", a.selected, h.last())
	}
}

func TestPersonalChat(t *testing.T) {
	a, _, tt, _ := newApp(t)
	a.Select("p1")
	// The events sit an hour back: run the clock past the day's first two
	// hours, so that they fall on today whenever the test runs.
	now := tt.Now()
	y, m, d := now.Date()
	if since := now.Sub(time.Date(y, m, d, 0, 0, 0, 0, now.Location())); since < 2*time.Hour {
		tt.Advance(2*time.Hour - since)
		now = tt.Now()
	}
	evs := events(t, now, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "下个月去东京"},
		proto.TurnStarted{},
		proto.ToolCall{ID: "t1", Kind: proto.ToolWeb, Name: "web_search", Summary: "tokyo", Status: proto.ToolDone},
	)
	for i := range evs {
		evs[i].Session = "p1"
	}
	a.ApplySession("p1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: evs}})
	tt.Frame()
	for _, want := range []string{"今天", "下个月去东京", "抓取 1 个网页 · 运行中", "···"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}

// An empty composer is two lines high, growing to eight.
func TestComposerIsTwoLines(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if r, ok := tt.Find("消息"); !ok || r.H < 28 || r.H > 40 {
		t.Fatalf("an empty composer is %v high", r.H)
	}
	a.Lost(errDaemon)
	a.NewSession()
	tt.Frame()
	if !tt.HasText("· 未连接") {
		t.Fatalf("offline new page %q", tt.Texts())
	}
}

func TestNewSessionPicksHarnessByModel(t *testing.T) {
	a, h, tt, _ := newApp(t)
	hermes := proto.HarnessInfo{Kind: "hermes", Version: "0.22.5", Supported: true, Capabilities: proto.Capabilities{SetModel: true},
		Models: []proto.ModelInfo{{ID: "codex/m1", Provider: "local", Name: "codex/m1", Description: "local", Default: true}}}
	h.models = []proto.HarnessInfo{claudeHarness(), hermes}
	a.NewSession()
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Claude Code", "Hermes", "Default (recommended)", "搜索模型"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("codex/m1") {
		t.Fatal("another harness's models show before its tab is chosen")
	}
	if err := tt.Click("Hermes"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("codex/m1"); err != nil {
		t.Fatal(err)
	}
	// Choosing keeps the menu up; ⎋ closes the submenu, then the menu.
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	a.s.newPick.dir = "/w/site"
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hi")
	tt.Key(0, ui.KeyEnter)
	want := proto.CreateParams{Mode: proto.ModeCode, Harness: "hermes", Directory: "/w/site", Permission: proto.ModeAutoReview, Model: "codex/m1", ModelProvider: "local"}
	if got := h.last(); !reflect.DeepEqual(got, want) {
		t.Fatalf("create %#v", got)
	}
}

func TestContextLine(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": {Session: "s1", Repos: []proto.RepoDiff{
		{Path: ".", Branch: "main", State: proto.RepoOK},
		{Path: "vendor/x", Branch: "dev", State: proto.RepoOK},
	}}}
	a.Select("s1")
	tt.Frame()
	tt.Frame()
	for _, want := range []string{"/w/hoplet", "main", "2"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestSessionDetails(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": {Session: "s1", Repos: []proto.RepoDiff{{Path: ".", Branch: "main", State: proto.RepoOK}}}}
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	tt.Frame()
	if err := tt.Click("会话详情 ⌘I"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Fix sync", "空闲 · 建于", "studio", "/w/hoplet", "main", "Claude Code", "s1"} {
		if !hasPrefix(tt.Texts(), want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	tt.Advance(time.Second) // the details column opens
	if err := tt.Click("在 Finder 中显示"); err != nil || len(h.revealed) != 1 {
		t.Fatalf("reveal: %v %v", err, h.revealed)
	}
}

func hasPrefix(texts []string, want string) bool {
	for _, s := range texts {
		if strings.HasPrefix(s, want) {
			return true
		}
	}
	return false
}

func TestOutputFolds(t *testing.T) {
	a, h, tt, _ := newApp(t)
	long := strings.Repeat("line\n", 30)
	h.outputs = map[string]proto.ToolOutput{"t1": {Text: long}, "t2": {Text: "ok\n", Truncated: true}}
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	out := &proto.BlobRef{SHA256: "x", Size: 1}
	a.ApplySession("s1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: events(t, tt.Now(), 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "go"},
		proto.TurnStarted{},
		proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "make a", Status: proto.ToolFailed, Output: out},
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "make b", Status: proto.ToolFailed, Output: out},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	)}})
	tt.Frame()
	for _, open := range []string{"运行 2 条命令", "make a", "make b"} {
		if err := tt.Click(open); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	tt.Frame()
	for _, want := range []string{"还有 18 行", "输出超过 64 KB，只显示开头"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("展开全部"); err != nil || tt.HasText("还有 18 行") {
		t.Fatalf("show all: %v", err)
	}
	if err := tt.Click("折叠"); err != nil || !tt.HasText("还有 18 行") {
		t.Fatalf("fold: %v", err)
	}
}

func TestNewPageStates(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models, h.folder = []proto.HarnessInfo{claudeHarness()}, "/w/site"
	a.NewSession()
	tt.Frame()
	if !tt.HasText("选择工作目录") || tt.HasText("最近：") {
		t.Fatalf("no directory yet: %q", tt.Texts())
	}
	if err := tt.Click("工作目录"); err != nil || a.s.newPick.dir != "/w/site" {
		t.Fatalf("pick: %v, %q", err, a.s.newPick.dir)
	}
	tt.Frame()
	if tt.HasText("选择工作目录") || !tt.HasText("/w/site") {
		t.Fatalf("a directory chosen: %q", tt.Texts())
	}
	a.Lost(errDaemon)
	tt.Frame()
	if !tt.HasText("· 未连接") || tt.HasText("/w/site") {
		t.Fatalf("offline: %q", tt.Texts())
	}
	a.Connected("studio")
	tt.Frame()
	if !tt.HasText("/w/site") {
		t.Fatalf("the directory did not come back: %q", tt.Texts())
	}
}

func TestModelSearch(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hai")
	tt.Frame()
	if n := strings.Count(strings.Join(tt.Texts(), "|"), "Default (recommended)"); n != 2 || !tt.HasText("Haiku 5.5") { // the button and the 模型 row keep theirs
		t.Fatalf("search: %q", tt.Texts())
	}
	tt.Type("x")
	tt.Frame()
	if !tt.HasText("没有匹配“haix”的模型") {
		t.Fatalf("no match: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.s.runSub != "" || !a.s.runOpen {
		t.Fatalf("⎋ closes only the models: submenu %q, menu %v", a.s.runSub, a.s.runOpen)
	}
}

// A new session leaves the effort to the harness until a level is chosen,
// and 默认 goes back to that.
func TestEffortDefault(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if !tt.HasText("默认") || a.s.newPick.run.effort != "" {
		t.Fatalf("a new session starts at %q: %q", a.s.newPick.run.effort, tt.Texts())
	}
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("推理强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("High"); err != nil {
		t.Fatal(err)
	}
	if a.s.newPick.run.effort != proto.EffortHigh {
		t.Fatalf("chose %q", a.s.newPick.run.effort)
	}
	if err := tt.Click("默认"); err != nil {
		t.Fatal(err)
	}
	if a.s.newPick.run.effort != "" {
		t.Fatalf("默认 chose %q", a.s.newPick.run.effort)
	}
}

func TestWorkingRow(t *testing.T) {
	a, _, tt, _ := newApp(t)
	selectRunning(t, a, tt)
	if !tt.HasText("思考中") {
		t.Fatalf("no working row while the turn runs: %q", tt.Texts())
	}
	if tt.HasText("用时") {
		t.Fatalf("turns are not marked: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 3, 1,
		proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "go test ./...", Status: proto.ToolRunning})})
	tt.Frame()
	if !tt.HasText("运行命令") {
		t.Fatalf("the working row does not say a command runs: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 4, 1,
		proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test"})})
	tt.Frame()
	if !tt.HasText("等你审批") {
		t.Fatalf("the working row does not say it waits on me: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 5, 1,
		proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "ok"},
		proto.TurnEnded{Reason: proto.TurnCompleted})})
	tt.Frame()
	if tt.HasText("思考中") || tt.HasText("运行命令") || tt.HasText("等你审批") {
		t.Fatalf("the working row stays after the turn: %q", tt.Texts())
	}
}

func TestToolRowStates(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1200, 900)
	h.outputErrs = map[string]error{"t2": errors.New("连接断了")}
	out := &proto.BlobRef{SHA256: "x", Size: 34}
	long := "go test ./internal/session/... -run TestHeartbeat -count=1 2>&1 | tail -20"
	selectRunning(t, a, tt,
		proto.ToolCall{ID: "t1", Kind: proto.ToolWeb, Name: "WebFetch", Summary: "https://example.com", Status: proto.ToolFailed},
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: long, Status: proto.ToolFailed, Output: out},
		proto.ToolCall{ID: "t3", Kind: proto.ToolWeb, Name: "WebFetch", Summary: "https://example.com/slow", Status: proto.ToolRunning},
	)
	tt.Frame()
	// Only the newest step opens by itself; the failed ones open on a click.
	if tt.HasText("失败，harness 没给出原因") {
		t.Fatalf("an earlier step opened by itself: %q", tt.Texts())
	}
	for _, open := range []string{"https://example.com", long} {
		if err := tt.Click(open); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	for _, want := range []string{"失败，harness 没给出原因", "没取到输出：连接断了", long} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	delete(h.outputErrs, "t2")
	h.outputs = map[string]proto.ToolOutput{"t2": {Text: "FAIL\n", Size: 5}}
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("FAIL") {
		t.Fatalf("retry shows no output: %q", tt.Texts())
	}
	if tt.HasText("未完成") {
		t.Fatalf("a running tool is unfinished while its turn runs: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 6, 1, proto.TurnEnded{Reason: proto.TurnInterrupted})})
	tt.Frame()
	if err := tt.Click("抓取 2 个网页 · 运行 1 条命令"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("未完成") {
		t.Fatalf("no 未完成 after the turn ended: %q", tt.Texts())
	}
}

func TestModelProviders(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{{Kind: "hermes", Supported: true, Capabilities: proto.Capabilities{SetModel: true},
		Models: []proto.ModelInfo{
			{ID: "codex/a", Provider: "openai-codex", ProviderName: "OpenAI Codex", Name: "codex/a", Default: true},
			{ID: "codex/b", Provider: "openai-codex", ProviderName: "OpenAI Codex", Name: "codex/b"},
			{ID: "h4", Provider: "nous", ProviderName: "Nous Portal", Name: "Hermes 4 70B"},
		}}}
	a.NewSession()
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("OpenAI Codex") || !tt.HasText("Nous Portal") {
		t.Fatalf("no provider headers: %q", tt.Texts())
	}
	tt.Type("hermes")
	tt.Frame()
	if tt.HasText("OpenAI Codex") || !tt.HasText("Nous Portal") {
		t.Fatalf("search keeps only the matching provider: %q", tt.Texts())
	}
}

func TestDetailsColumn(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1300, 820)
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	tt.Frame()
	if strings.Contains(strings.Join(tt.Texts(), "|"), "建于") {
		t.Fatalf("details show before ⌘I: %q", tt.Texts())
	}
	if err := tt.Click("会话详情 ⌘I"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"建于", "studio", "/w/hoplet", "s1", "复制会话 ID"} {
		if !tt.HasText(want) && !strings.Contains(strings.Join(tt.Texts(), "|"), want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if !tt.HasText("消息") && !strings.Contains(strings.Join(tt.Texts(), "|"), "回复") {
		t.Errorf("the composer stays beside the details: %q", tt.Texts())
	}
	if tt.HasText("复制 Claude Code 会话 ID") {
		t.Fatal("a harness session ID before the harness started one")
	}
	s1, _ := a.cat.Get("s1")
	s1.Harness.NativeID = "5447e5d2-1317-413a-aa1c-151a5ec48a03"
	a.Apply(proto.CatalogItem{Version: a.cat.Version() + 1, Sessions: []proto.Session{s1}})
	tt.Frame()
	if !tt.HasText(s1.Harness.NativeID) || !tt.HasText("复制 Claude Code 会话 ID") {
		t.Fatalf("no harness session ID in %q", tt.Texts())
	}
	tt.Advance(time.Second) // the details column opens
	if err := tt.Click("复制 Claude Code 会话 ID"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != s1.Harness.NativeID {
		t.Fatalf("copied %q", got)
	}
}

func TestCodeBlockCopy(t *testing.T) {
	a, _, tt, _ := newApp(t)
	selectRunning(t, a, tt, proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "看：\n\n```go\nfmt.Println(1)\n```\n"})
	tt.Frame()
	if err := tt.Click("复制"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "fmt.Println(1)\n" {
		t.Fatalf("clipboard %q", got)
	}
	if !tt.HasText("已复制") {
		t.Fatalf("the copy button does not say it copied: %q", tt.Texts())
	}
}

func TestQueueFolds(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1200, 900)
	selectRunning(t, a, tt)
	var ms []proto.QueuedMessage
	for i := range 5 {
		ms = append(ms, proto.QueuedMessage{Key: fmt.Sprint(i), Text: fmt.Sprintf("message %d", i)})
	}
	a.ApplySession("s1", proto.SessionItem{Queue: &proto.Queue{Messages: ms}})
	tt.Frame()
	if !tt.HasText("排队 5") || !tt.HasText("message 2") || tt.HasText("message 3") || !tt.HasText("还有 2 条") {
		t.Fatalf("the queue does not fold past three: %q", tt.Texts())
	}
	tt.Advance(time.Second) // the composer settles at its new height
	if err := tt.Click("还有 2 条"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("message 4") {
		t.Fatalf("还有 N 条 does not show the rest: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 3, 1, proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test"})})
	tt.Frame()
	if !tt.HasText("排队 5 条") || !strings.Contains(strings.Join(tt.Texts(), "|"), "下一条：message 0") || tt.HasText("message 1") {
		t.Fatalf("the queue is not one line during an approval: %q", tt.Texts())
	}
}

func TestContextRow(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": {Session: "s1", Repos: []proto.RepoDiff{{Path: ".", Branch: "main", State: proto.RepoOK}}}}
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	tt.Frame()
	tt.Frame()
	for _, want := range []string{"studio", "本机", "/w/hoplet", "main"} {
		if !tt.HasText(want) {
			t.Errorf("no %q above the composer: %q", want, tt.Texts())
		}
	}
}

// A harness that has not listed its models says so in the model submenu,
// rather than offering another harness's under its button.
func TestModelMenuWithoutModels(t *testing.T) {
	a, h, tt, _ := newApp(t)
	claude := claudeHarness()
	claude.Models = nil
	hermes := proto.HarnessInfo{Kind: "hermes", Supported: true, Capabilities: proto.Capabilities{SetModel: true},
		Models: []proto.ModelInfo{{ID: "codex/m1", Provider: "local", Name: "codex/m1", Default: true}}}
	h.models = []proto.HarnessInfo{claude, hermes}
	a.NewSession()
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Claude Code 还没有给出模型列表") || tt.HasText("codex/m1") {
		t.Fatalf("models of the wrong harness: %q", tt.Texts())
	}
}

// A harness that cannot run shows, dimmed, on the new-session page, and
// says how to install it.
func TestMissingHarness(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness(), {Kind: "hermes", Missing: "hermes: Hermes Agent is not installed"}}
	a.NewSession()
	tt.Frame()
	if len(a.s.harnesses) != 1 || len(a.s.missing) != 1 {
		t.Fatalf("installed %v, missing %v", a.s.harnesses, a.s.missing)
	}
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Hermes"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if !tt.HasText("Hermes 未安装") {
		t.Fatalf("no install hint in %q", tt.Texts())
	}
}

// A model that takes no effort has no effort row, nor one on its button.
func TestModelWithoutEffort(t *testing.T) {
	a, h, tt, _ := newApp(t)
	ch := claudeHarness()
	ch.Models[1].NoEffort = true
	h.models = []proto.HarnessInfo{ch}
	a.SetHarnesses(h.models)
	a.Select("s1")
	tt.Frame()
	s1, _ := a.cat.Get("s1")
	s1.Model = "haiku"
	a.Apply(proto.CatalogItem{Version: a.cat.Version() + 1, Sessions: []proto.Session{s1}})
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("推理强度") || tt.HasText("默认") {
		t.Fatalf("effort offered for a model without it: %q", tt.Texts())
	}
}

// The context ring shows once the harness said the window's size, and its
// tip says the numbers.
func TestContextRing(t *testing.T) {
	a, _, tt, _ := newApp(t)
	a.Select("s1")
	now := tt.Now()
	a.ApplySession("s1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: events(t, now, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "hi"},
		proto.ContextUsage{Used: 84000},
	)}})
	tt.Frame()
	if _, ok := tt.Find("上下文已用 42% · 84K / 200K tokens"); ok {
		t.Fatal("a ring before the window is known")
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, now, 3, 1, proto.ContextUsage{Used: 84000, Window: 200000})})
	tt.Frame()
	if _, ok := tt.Find("上下文已用 42% · 84K / 200K tokens"); !ok {
		t.Fatalf("no ring in %q", tt.Texts())
	}
}

func TestTokens(t *testing.T) {
	for n, want := range map[int64]string{999: "999", 84000: "84K", 92500: "92.5K", 1_000_000: "1M", 1_234_567: "1.2M"} {
		if got := tokens(n); got != want {
			t.Errorf("tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

// A long model name gives way in the run menu; the row's name keeps its
// one line.
func TestRunMenuLongModel(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if err := tt.Click("模型与强度"); err != nil {
		t.Fatal(err)
	}
	r, ok := tt.Find("模型")
	if !ok {
		t.Fatalf("no 模型 row in %q", tt.Texts())
	}
	label, _ := tt.Find("推理强度")
	if r.H > label.H+1 {
		t.Fatalf("模型 wraps: %v high, 推理强度 %v", r.H, label.H)
	}
}

// A decided approval stays on one line however long its title, and its
// hover shows only when it was decided.
func TestApprovalRecordLine(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(900, 700)
	a.Select("s1")
	now := tt.Now()
	long := "Bash " + strings.Repeat("ls -A && which cargo rustc; ", 8)
	a.ApplySession("s1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: events(t, now, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "go"},
		proto.TurnStarted{},
		proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: long},
		proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, DecidedBy: "desktop@host"},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	)}})
	tt.Frame()
	if err := tt.Click("审批 1 次"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	label, ok := tt.Find("已允许")
	title, ok2 := tt.Find(long)
	if !ok || !ok2 || label.H > 20 || title.H > 20 {
		t.Fatalf("the record wraps: label %+v, title %+v", label, title)
	}
	tt.Move(title.X+10, title.Y+title.H/2)
	tt.Frame()
	if tt.HasText("desktop@host") || strings.Contains(strings.Join(tt.Texts(), "|"), "desktop@") {
		t.Fatalf("the hover names the device: %q", tt.Texts())
	}
}

// A new session shows my first message and that its harness is starting
// at once, before the daemon records the message.
func TestNewSessionStartsAtOnce(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	a.s.newPick.dir = "/w/site"
	tt.Frame()
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("add a tray")
	tt.Key(0, ui.KeyEnter)
	h.created(proto.Session{ID: "n1", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/w/site", Status: proto.StatusIdle})
	tt.Frame()
	if !tt.HasText("add a tray") || !tt.HasText("正在启动 Claude Code") {
		t.Fatalf("the new session waits blank: %q", tt.Texts())
	}
}

// A running turn's group is open with only its newest step open; notes
// between steps join it; the finished turn folds to one line over its
// reply.
func TestTurnGroup(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1200, 900)
	selectRunning(t, a, tt,
		proto.Thinking{Text: "first thought"},
		proto.Message{ID: "n1", Role: proto.RoleAssistant, Text: "Looking at the bridge."},
		proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolDone},
		proto.Thinking{Text: "second thought"},
	)
	tt.Frame()
	for _, want := range []string{"思考 2 次 · 写了 1 段说明 · 读取 1 个文件", "说明", "Looking at the bridge.", "second thought"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("first thought") {
		t.Fatalf("an earlier step is open: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 7, 1,
		proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "Done."},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	)})
	tt.Frame()
	if tt.HasText("second thought") || tt.HasText("Looking at the bridge.") || !tt.HasText("Done.") {
		t.Fatalf("the finished group stays open: %q", tt.Texts())
	}
}

// A message that did not go out stays, faded, over why and 重试; a retry
// that goes through shows it starting again.
func TestSendFails(t *testing.T) {
	a, h, tt, _ := newApp(t)
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	tt.Frame()
	h.sendErr = errors.New("Hoplet 托管的 claude 没有安装")
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("add a tray")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("add a tray") || !tt.HasText("没能启动 Claude Code：Hoplet 托管的 claude 没有安装") || tt.HasText("正在启动 Claude Code") {
		t.Fatalf("no failure bar: %q", tt.Texts())
	}
	h.sendErr = nil
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("没能启动 Claude Code：Hoplet 托管的 claude 没有安装") || !tt.HasText("正在启动 Claude Code") {
		t.Fatalf("the retry did not go: %q", tt.Texts())
	}
	n := 0
	for _, c := range h.calls {
		if p, ok := c.(proto.SendParams); ok && p.Text == "add a tray" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("sent %d times", n)
	}
}

// Outside a git repository a change card lists the files with no line
// counts: no "+0".
func TestChangesOutsideGit(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": {Session: "s1"}}
	a.SetHarnesses([]proto.HarnessInfo{claudeHarness()})
	a.Select("s1")
	a.ApplySession("s1", proto.SessionItem{Snapshot: &proto.SessionSnapshot{Events: events(t, tt.Now(), 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "go"},
		proto.TurnStarted{},
		proto.FileChange{Path: "/w/hoplet/main.rs", Op: proto.FileCreated},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	)}})
	tt.Frame()
	tt.Frame()
	if !tt.HasText("改了 1 个文件") || tt.HasText("+0") {
		t.Fatalf("card %q", tt.Texts())
	}
}
