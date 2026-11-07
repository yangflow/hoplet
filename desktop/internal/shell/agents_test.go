package shell

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestTaskTray(t *testing.T) {
	a, _, tt, _ := newApp(t)
	selectRunning(t, a, tt, proto.TodoList{Items: []proto.TodoItem{
		{ID: "1", Text: "Read the bridge", Status: proto.TodoCompleted},
		{ID: "2", Text: "Run tests", Active: "正在运行测试", Status: proto.TodoInProgress},
		{ID: "3", Text: "Write docs", Status: proto.TodoPending},
		{ID: "4", Text: "Old idea", Status: proto.TodoCancelled},
	}})
	for _, want := range []string{"任务 1 / 3", "Read the bridge", "正在运行测试", "Write docs", "Old idea"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Run tests") {
		t.Error("the task in progress is not written in its -ing wording")
	}
	if err := tt.Click("任务 1 / 3"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if tt.HasText("Write docs") || !tt.HasText("· 正在运行测试") {
		t.Fatalf("the folded tray is not one line: %q", tt.Texts())
	}
	all := proto.TodoList{Items: []proto.TodoItem{
		{ID: "1", Text: "Read the bridge", Status: proto.TodoCompleted},
		{ID: "2", Text: "Run tests", Status: proto.TodoCompleted},
	}}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 4, 1, all)})
	tt.Frame()
	if !tt.HasText("全部完成") {
		t.Fatalf("an all-done tray does not say so: %q", tt.Texts())
	}
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 5, 1, proto.TurnEnded{Reason: proto.TurnCompleted})})
	tt.Advance(time.Second)
	if tt.HasText("全部完成") {
		t.Fatalf("the tray stays after the turn: %q", tt.Texts())
	}
}

func TestSubagents(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1600, 900)
	g := proto.Subagent{ID: "a1", CallID: "a1", Type: "Explore", Title: "Look around", Status: proto.SubagentRunning}
	selectRunning(t, a, tt, g)
	for _, want := range []string{"派出 1 个子代理", "子代理运行中 · 1 个", "Explore", "Look around"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	task, err := proto.NewEvent("s1", 4, 1, tt.Now(), proto.Message{ID: "a1/task", Role: proto.RoleUser, Text: "Find the bridge"})
	if err != nil {
		t.Fatal(err)
	}
	task.Agent = "a1"
	h.records = map[string]proto.EventsPage{"a1": {Events: []proto.Event{task}}}
	if err := tt.Click("Explore Look around"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !slices.Contains(h.calls, any("older s1 a1 4")) {
		t.Fatalf("the record is not read: %v", h.calls)
	}
	for _, want := range []string{"任务", "Find the bridge", "运行中"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in the record: %q", want, tt.Texts())
		}
	}
	// What the subagent does next reaches its open record, not the
	// conversation.
	step := events(t, tt.Now(), 5, 1, proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolDone})
	step[0].Agent = "a1"
	done := g
	done.Status, done.Summary, done.DurationMS = proto.SubagentDone, "It is in bridge.go", 48000
	a.ApplySession("s1", proto.SessionItem{Events: append(step, events(t, tt.Now(), 6, 1, done, proto.TurnEnded{Reason: proto.TurnCompleted})...)})
	tt.Advance(time.Second)
	for _, want := range []string{"读取 1 个文件", "It is in bridge.go", "48 秒"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("会话详情 ⌘I"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("已结束 1 个") {
		t.Fatalf("no ended subagents in the details: %q", tt.Texts())
	}
}

func TestBackgroundSubagent(t *testing.T) {
	a, _, tt, _ := newApp(t)
	g := proto.Subagent{ID: "a1", Title: "Watch CI", Status: proto.SubagentRunning, Background: true}
	selectRunning(t, a, tt, g)
	a.ApplySession("s1", proto.SessionItem{Events: events(t, tt.Now(), 4, 1,
		proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "Started it."},
		proto.TurnEnded{Reason: proto.TurnCompleted})})
	tt.Frame()
	got := strings.Join(tt.Texts(), "|")
	if !strings.Contains(got, "派出 1 个子代理 · 1 个在后台运行") {
		t.Fatalf("the group does not say one runs in the background: %q", got)
	}
}
