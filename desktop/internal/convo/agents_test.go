package convo

import (
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// tag marks events as a subagent's.
func tag(evs []proto.Event, agent string) []proto.Event {
	for i := range evs {
		evs[i].Agent = agent
	}
	return evs
}

func TestSubagentsAndTodos(t *testing.T) {
	g := proto.Subagent{ID: "a1", CallID: "a1", Type: "Explore", Title: "Look around", Status: proto.SubagentRunning}
	evs := log(t, 1, 1,
		proto.Message{ID: "k1", Role: proto.RoleUser, Text: "fix it"}, // 1
		proto.TurnStarted{}, // 2
		proto.TodoList{Items: []proto.TodoItem{{ID: "1", Text: "Read", Status: proto.TodoInProgress}}}, // 3
		g, // 4
	)
	evs = append(evs, tag(log(t, 5, 1,
		proto.Message{ID: "a1/task", Role: proto.RoleUser, Text: "Find the bridge"},                                // 5
		proto.ToolCall{ID: "t1", Kind: proto.ToolRead, Name: "Read", Summary: "bridge.go", Status: proto.ToolDone}, // 6
		proto.Message{ID: "m9", Role: proto.RoleAssistant, Text: "It is in bridge.go"},                             // 7
	), "a1")...)
	done := g
	done.Status, done.Summary, done.DurationMS = proto.SubagentDone, "It is in bridge.go", 4000
	evs = append(evs, log(t, 8, 1,
		done, // 8
		proto.TodoList{Items: []proto.TodoItem{{ID: "1", Text: "Read", Status: proto.TodoCompleted}}}, // 9
		proto.MessageDelta{MessageID: "m1", Text: "Done"},                                             // 10
	)...)
	var c Convo
	c.Apply(proto.SessionItem{Events: evs})
	items := c.Grouped()
	if len(items) != 4 || items[2].Kind != KindWork || items[3].Kind != KindText {
		t.Fatalf("items %+v", kinds(items))
	}
	w := items[2].Work
	if len(w.Entries) != 1 || w.Entries[0].Agent == nil || w.Entries[0].Agent.Status != proto.SubagentDone {
		t.Fatalf("entries %+v", w.Entries)
	}
	if todos := c.Todos(); len(todos) != 1 || todos[0].Status != proto.TodoCompleted {
		t.Fatalf("todos %+v", todos)
	}
	ag, ok := c.Agent("a1")
	if !ok || ag.Took(t0) != 4*time.Second || ag.End.IsZero() {
		t.Fatalf("agent %+v", ag)
	}

	r := Record("a1")
	r.Apply(proto.SessionItem{Events: evs})
	if r.Task() != "Find the bridge" {
		t.Fatalf("task %q", r.Task())
	}
	rec := r.Grouped()
	if len(rec) != 3 || rec[1].Kind != KindWork || rec[2].Text != "It is in bridge.go" {
		t.Fatalf("record %+v", kinds(rec))
	}
	r.Live(true)
	if !r.Running() {
		t.Fatal("a live record does not run")
	}
}
