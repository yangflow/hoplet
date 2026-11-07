package hermes

import (
	"reflect"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

func TestTodoListAndDelegation(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventLine("message.start", `{}`))
	task := func(id, text string) proto.Payload {
		return proto.Message{ID: id + "/task", Role: proto.RoleUser, Text: text}
	}
	for _, tc := range []struct {
		line string
		want []proto.Payload
	}{
		{eventLine("tool.start", `{"tool_id":"t1","name":"todo_list","args":{"todos":[{"id":"a","content":"Read","status":"in_progress"},{"id":"b","content":"Write","status":"pending"}]}}`),
			[]proto.Payload{proto.TodoList{Items: []proto.TodoItem{{ID: "a", Text: "Read", Status: proto.TodoInProgress}, {ID: "b", Text: "Write", Status: proto.TodoPending}}}}},
		{eventLine("tool.complete", `{"tool_id":"t1","name":"todo_list"}`), nil},
		{eventLine("tool.start", `{"tool_id":"t2","name":"todo_list","args":{"merge":true,"todos":[{"id":"a","status":"completed"},{"id":"c","content":"Test","status":"cancelled"}]}}`),
			[]proto.Payload{proto.TodoList{Items: []proto.TodoItem{{ID: "a", Text: "Read", Status: proto.TodoCompleted}, {ID: "b", Text: "Write", Status: proto.TodoPending}, {ID: "c", Text: "Test", Status: proto.TodoCancelled}}}}},
		{eventLine("tool.start", `{"tool_id":"d1","name":"delegate_task","args":{"tasks":[{"goal":"Look around"},{"goal":"Read docs\nmore"}]}}`),
			[]proto.Payload{
				proto.Subagent{ID: "d1/1", CallID: "d1", Title: "Look around", Status: proto.SubagentRunning}, task("d1/1", "Look around"),
				proto.Subagent{ID: "d1/2", CallID: "d1", Title: "Read docs", Status: proto.SubagentRunning}, task("d1/2", "Read docs\nmore"),
			}},
		{eventLine("tool.complete", `{"tool_id":"d1","name":"delegate_task","is_error":true}`),
			[]proto.Payload{
				proto.Subagent{ID: "d1/1", CallID: "d1", Title: "Look around", Status: proto.SubagentFailed},
				proto.Subagent{ID: "d1/2", CallID: "d1", Title: "Read docs", Status: proto.SubagentFailed},
			}},
		// A call that waited for its subagent: it ends with the call.
		{eventLine("tool.start", `{"tool_id":"d2","name":"delegate_task","args":{"goal":"One thing"}}`),
			[]proto.Payload{proto.Subagent{ID: "d2", CallID: "d2", Title: "One thing", Status: proto.SubagentRunning}, task("d2", "One thing")}},
		{eventLine("tool.complete", `{"tool_id":"d2","name":"delegate_task","result":"{\"status\": \"completed\"}"}`),
			[]proto.Payload{proto.Subagent{ID: "d2", CallID: "d2", Title: "One thing", Status: proto.SubagentDone}}},
	} {
		if got := x.push(tc.line); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s\ngot  %#v\nwant %#v", tc.line, got, tc.want)
		}
	}
}

func TestBackgroundDelegation(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventLine("message.start", `{}`))
	a := proto.Subagent{ID: "d1/1", CallID: "d1", Title: "Look", Status: proto.SubagentRunning}
	b := proto.Subagent{ID: "d1/2", CallID: "d1", Title: "Read", Status: proto.SubagentRunning}
	x.push(eventLine("tool.start", `{"tool_id":"d1","name":"delegate_task","args":{"tasks":[{"goal":"Look"},{"goal":"Read"}]}}`))
	// Announced while the call runs: known by task order.
	for _, line := range []string{
		eventLine("subagent.spawn_requested", `{"subagent_id":"sa-1","task_index":1,"task_count":2}`),
		eventLine("subagent.start", `{"subagent_id":"sa-1","task_index":1,"task_count":2,"delegation_id":"g1"}`),
	} {
		if got := x.push(line); got != nil {
			t.Fatalf("%s: %v", line, got)
		}
	}
	a.Background, b.Background = true, true
	if got, want := x.push(eventLine("tool.complete", `{"tool_id":"d1","name":"delegate_task","result":"{\"status\": \"dispatched\", \"mode\": \"background\", \"subagent_ids\": [\"sa-0\", \"sa-1\"]}"}`)), []proto.Payload{a, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatched\ngot  %#v\nwant %#v", got, want)
	}
	read := proto.ToolCall{ID: "d1/2/1", Kind: proto.ToolRead, Name: "read_file", Summary: "notes.md", Status: proto.ToolRunning}
	for _, tc := range []struct {
		line   string
		want   []proto.Payload
		agents []string
	}{
		{eventLine("subagent.tool", `{"subagent_id":"sa-1","tool_name":"read_file","text":"notes.md","tool_count":1}`),
			[]proto.Payload{read}, []string{"d1/2"}},
		// The turn ends; the subagents go on.
		{eventLine("message.complete", `{"admission_id":"a1","text":""}`), nil, nil},
		// Known from the call's result only.
		{eventFor("", "subagent.complete", `{"subagent_id":"sa-0","status":"interrupted"}`),
			[]proto.Payload{proto.Subagent{ID: "d1/1", CallID: "d1", Title: "Look", Status: proto.SubagentStopped, Background: true}}, []string{""}},
		{eventFor("", "subagent.complete", `{"subagent_id":"sa-1","status":"completed","summary":"# Notes","input_tokens":100,"output_tokens":20,"tool_count":1,"duration_seconds":7.5}`),
			[]proto.Payload{
				proto.ToolCall{ID: "d1/2/1", Kind: proto.ToolRead, Name: "read_file", Summary: "notes.md", Status: proto.ToolDone},
				proto.Subagent{ID: "d1/2", CallID: "d1", Title: "Read", Status: proto.SubagentDone, Background: true, Tokens: 120, ToolUses: 1, DurationMS: 7500, Summary: "# Notes"},
			}, []string{"d1/2", ""}},
		// Unknown or already ended: nothing.
		{eventFor("", "subagent.complete", `{"subagent_id":"sa-1","status":"completed"}`), nil, nil},
	} {
		got := x.push(tc.line)
		if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(x.agents, tc.agents) {
			t.Fatalf("%s\ngot  %#v %q\nwant %#v %q", tc.line, got, x.agents, tc.want, tc.agents)
		}
	}
}
