package shell

import (
	"errors"
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
	tt.Frame() // the failed call's output arrives
	for _, want := range []string{"fix the heartbeat", "思考 · 读取 1 个文件 · 运行 1 条命令", "✕ 1 个失败", "go test ./...",
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
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.QueueKeyParams{Session: "s1", Key: "q1"}) {
		t.Fatalf("delete %#v", got)
	}
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
	for _, want := range []string{"运行命令", "1 / 1", "go test -race ./...", "Allow once", "Always allow", "Deny"} {
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
	if len(h.calls) != 0 || !tt.HasText("重连后发送") || !tt.HasText("studio · 0 秒") {
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
	if err := tt.Click("强度与模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("换模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Haiku 5.5"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetModelParams{Session: "s1", Model: "haiku"}) {
		t.Fatalf("set model %#v", got)
	}
	if err := tt.Click("强度与模型"); err != nil {
		t.Fatal(err)
	}
	// The slider rests on the middle level for the default; one step
	// right is 高.
	if err := tt.Click("推理强度"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	if got := h.last(); !reflect.DeepEqual(got, proto.SetEffortParams{Session: "s1", Effort: proto.EffortHigh}) {
		t.Fatalf("set effort %#v", got)
	}
	if err := tt.Click("审批：需确认"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if err := tt.Click("全自动"); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); !reflect.DeepEqual(got, proto.SetPermissionParams{Session: "s1", Permission: proto.ModeFullAuto}) {
		t.Fatalf("set permission %#v", got)
	}
}

func TestNewSession(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	if err := tt.Click("新建"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("在 Hoplet 上做什么？") || !tt.HasText("选择工作目录") {
		t.Fatalf("new page %q", tt.Texts())
	}
	if err := tt.Click("工作目录"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("/w/site"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("add a tray")
	tt.Key(0, ui.KeyEnter)
	if got := h.last(); !reflect.DeepEqual(got, proto.CreateParams{Mode: proto.ModeCode, Harness: "claude", Directory: "/w/site", Permission: proto.ModeAutoReview, Effort: proto.EffortMedium}) {
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
	now := tt.Now()
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

func TestComposerIsOneLine(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if r, ok := tt.Find("消息"); !ok || r.H > 30 {
		t.Fatalf("an empty composer is %v high", r.H)
	}
	a.Lost(errDaemon)
	a.NewSession()
	tt.Frame()
	if !tt.HasText("studio · 未连接") {
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
	if err := tt.Click("强度与模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("换模型"); err != nil {
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
		proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "a", Status: proto.ToolFailed, Output: out},
		proto.ToolCall{ID: "t2", Kind: proto.ToolExec, Name: "Bash", Summary: "b", Status: proto.ToolFailed, Output: out},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	)}})
	tt.Frame()
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
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if !tt.HasText("选择工作目录") || !tt.HasText("最近：") {
		t.Fatalf("no directory yet: %q", tt.Texts())
	}
	if err := tt.Click("/w/site"); err != nil || a.s.newPick.dir != "/w/site" {
		t.Fatalf("recent: %v, %q", err, a.s.newPick.dir)
	}
	if tt.HasText("最近：") || tt.HasText("选择工作目录") {
		t.Fatalf("a directory chosen: %q", tt.Texts())
	}
	a.Lost(errDaemon)
	tt.Frame()
	if !tt.HasText("studio · 未连接") || tt.HasText("/w/site") {
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
	if err := tt.Click("强度与模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("换模型"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hai")
	tt.Frame()
	if n := strings.Count(strings.Join(tt.Texts(), "|"), "Default (recommended)"); n != 1 || !tt.HasText("Haiku 5.5") { // the pill keeps its own
		t.Fatalf("search: %q", tt.Texts())
	}
	tt.Type("x")
	tt.Frame()
	if !tt.HasText("没有匹配“haix”的模型") {
		t.Fatalf("no match: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.s.modelOpen || !a.s.effortOpen {
		t.Fatalf("⎋ goes back to the effort: model %v, effort %v", a.s.modelOpen, a.s.effortOpen)
	}
}

func TestEffortDefault(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.models = []proto.HarnessInfo{claudeHarness()}
	a.NewSession()
	tt.Frame()
	if err := tt.Click("强度与模型"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("强度 中") {
		t.Fatalf("a new session does not start at medium: %q", tt.Texts())
	}
	if err := tt.Click("推理强度"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.s.newPick.run.effort != proto.EffortHigh {
		t.Fatalf("→ from medium chose %q", a.s.newPick.run.effort)
	}
	if err := tt.Click("恢复默认"); err != nil {
		t.Fatal(err)
	}
	if a.s.newPick.run.effort != proto.EffortMedium {
		t.Fatalf("reset chose %q", a.s.newPick.run.effort)
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
	if err := tt.Click("强度与模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("换模型"); err != nil {
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
