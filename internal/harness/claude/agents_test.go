package claude

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
)

// TestSubagents follows a subagent from its call to its result: the call
// becomes a subagent event, its own lines go to its record, and a nested
// subagent is flattened into the outer one's record.
func TestSubagents(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{})
	for _, l := range []string{
		`{"type":"system","subtype":"init","session_id":"n1"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Agent","input":{"description":"Find the pump","prompt":"Look for the pump loop.","subagent_type":"Explore"}}]}}`,
		`{"type":"stream_event","parent_tool_use_id":"t1","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}}`,
		`{"type":"user","parent_tool_use_id":"t1","message":{"content":[{"type":"text","text":"Look for the pump loop."}]}}`,
		`{"type":"assistant","parent_tool_use_id":"t1","message":{"id":"s1","content":[{"type":"tool_use","id":"t2","name":"Grep","input":{"pattern":"pump"}}],"usage":{"input_tokens":9}}}`,
		`{"type":"user","parent_tool_use_id":"t1","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"bridge.go"}]}}`,
		`{"type":"assistant","parent_tool_use_id":"t1","message":{"id":"s2","content":[{"type":"tool_use","id":"t3","name":"Task","input":{"description":"inner"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"t3","message":{"id":"s3","content":[{"type":"thinking","thinking":"deeper"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"It is in bridge.go."},{"type":"text","text":"agentId: abc (for resuming)\n<usage>total_tokens: 1200\ntool_uses: 4\nduration_ms: 48000</usage>"}]}]}}`,
		`{"type":"result","subtype":"success"}`,
	} {
		p.push(harness.Stdout, []byte(l))
	}
	want := []string{
		`native n1 proto.TurnStarted {}`,
		`proto.Subagent {"id":"t1","call_id":"t1","type":"Explore","title":"Find the pump","status":"running"}`,
		`agent t1 proto.Message {"id":"t1/task","role":"user","text":"Look for the pump loop."}`,
		`agent t1 proto.ToolCall {"id":"t2","kind":"read","name":"Grep","summary":"pump","status":"running"}`,
		`agent t1 proto.ToolCall {"id":"t2","kind":"read","name":"Grep","summary":"pump","status":"done"}`,
		`agent t1 proto.ToolCall {"id":"t3","kind":"other","name":"Task","summary":"inner","status":"running"}`,
		`agent t1 proto.Thinking {"text":"deeper"}`,
		`proto.Subagent {"id":"t1","call_id":"t1","type":"Explore","title":"Find the pump","status":"done","tokens":1200,"tool_uses":4,"duration_ms":48000,"summary":"It is in bridge.go."}`,
		`proto.TurnEnded {"reason":"completed"}`,
	}
	if got := events(p); !slices.Equal(got, want) {
		t.Fatalf("events\n%q\nwant\n%q", got, want)
	}
}

// TestSubagentStoppedAndBackground: stopping the turn stops the subagents
// it waited for; a background one is only launched by its call's result
// and ends with its notification, after the turn.
func TestSubagentStoppedAndBackground(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{})
	for _, l := range []string{
		`{"type":"system","subtype":"init","session_id":"n1"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Agent","input":{"description":"wait","subagent_type":"general-purpose"}},{"type":"tool_use","id":"t2","name":"Agent","input":{"description":"bg","run_in_background":true}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"Async agent launched"}]}}`,
		`{"type":"result","subtype":"error_during_execution","terminal_reason":"aborted_tools"}`,
		`{"type":"system","subtype":"task_notification","tool_use_id":"t2","status":"completed","summary":"Counted 2.","usage":{"total_tokens":900,"tool_uses":1,"duration_ms":2000}}`,
		`{"type":"system","subtype":"task_notification","tool_use_id":"t1","status":"killed","summary":""}`,
	} {
		p.push(harness.Stdout, []byte(l))
	}
	want := []string{
		`native n1 proto.TurnStarted {}`,
		`proto.Subagent {"id":"t1","call_id":"t1","title":"wait","status":"running"}`,
		`proto.Subagent {"id":"t2","call_id":"t2","title":"bg","status":"running","background":true}`,
		`proto.Subagent {"id":"t1","call_id":"t1","title":"wait","status":"stopped"}`,
		`proto.TurnEnded {"reason":"interrupted"}`,
		`proto.Subagent {"id":"t2","call_id":"t2","title":"bg","status":"done","background":true,"tokens":900,"tool_uses":1,"duration_ms":2000,"summary":"Counted 2."}`,
	}
	if got := events(p); !slices.Equal(got, want) {
		t.Fatalf("events\n%q\nwant\n%q", got, want)
	}
}

// TestSubagentHandBack: without a notification the call's result ends the
// subagent, its report taken out of the frame claude wraps it in.
func TestSubagentHandBack(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{})
	frame := "  <tools-used-before-summary>Read</tools-used-before-summary>\n  \n[Subagent hand-back] The text below is the final report. The report follows:\n  # Notes\n  \n  done\nagentId: a1 (use SendMessage)\n<usage>subagent_tokens: 170\ntool_uses: 1\nduration_ms: 54</usage>"
	b, _ := json.Marshal(frame)
	for _, l := range []string{
		`{"type":"system","subtype":"init","session_id":"n1"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Agent","input":{"description":"notes"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":` + string(b) + `}]}]}}`,
		`{"type":"system","subtype":"task_notification","tool_use_id":"t1","status":"completed","summary":"late"}`,
	} {
		p.push(harness.Stdout, []byte(l))
	}
	want := []string{
		`native n1 proto.TurnStarted {}`,
		`proto.Subagent {"id":"t1","call_id":"t1","title":"notes","status":"running"}`,
		`proto.Subagent {"id":"t1","call_id":"t1","title":"notes","status":"done","tokens":170,"tool_uses":1,"duration_ms":54,"summary":"# Notes\n\ndone"}`,
	}
	if got := events(p); !slices.Equal(got, want) {
		t.Fatalf("events\n%q\nwant\n%q", got, want)
	}
}

// TestTodoTools keeps the task list from the task tools' input, whole on
// every change, numbered as claude's results say, and shows none of their
// calls.
func TestTodoTools(t *testing.T) {
	p, _ := liveRun(t, harness.Spec{})
	for _, l := range []string{
		`{"type":"system","subtype":"init","session_id":"n1"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"c1","name":"TaskCreate","input":{"subject":"Write the test","activeForm":"Writing the test"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"c1","content":"Task #1 created successfully"}]}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"c2","name":"TaskCreate","input":{"subject":"Run it"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"c2","content":"Task #2 created successfully: Run it"}]}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"c7","name":"TaskCreate","input":{"subject":"Refused"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"c7","is_error":true,"content":"no"}]}}`,
		`{"type":"assistant","message":{"id":"m3","content":[{"type":"tool_use","id":"c3","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}}]}}`,
		`{"type":"assistant","message":{"id":"m4","content":[{"type":"tool_use","id":"c4","name":"TaskUpdate","input":{"taskId":"2","status":"deleted"}}]}}`,
		`{"type":"assistant","message":{"id":"m5","content":[{"type":"tool_use","id":"c5","name":"TaskList","input":{}}]}}`,
		`{"type":"assistant","message":{"id":"m6","content":[{"type":"tool_use","id":"c6","name":"TodoWrite","input":{"todos":[{"content":"A","status":"completed","activeForm":"Doing A"},{"content":"B","status":"pending"}]}}]}}`,
	} {
		p.push(harness.Stdout, []byte(l))
	}
	want := []string{
		`native n1 proto.TurnStarted {}`,
		`proto.TodoList {"items":[{"id":"1","text":"Write the test","active":"Writing the test","status":"pending"}]}`,
		`proto.TodoList {"items":[{"id":"1","text":"Write the test","active":"Writing the test","status":"pending"},{"id":"2","text":"Run it","status":"pending"}]}`,
		`proto.TodoList {"items":[{"id":"1","text":"Write the test","active":"Writing the test","status":"in_progress"},{"id":"2","text":"Run it","status":"pending"}]}`,
		`proto.TodoList {"items":[{"id":"1","text":"Write the test","active":"Writing the test","status":"in_progress"}]}`,
		`proto.TodoList {"items":[{"id":"1","text":"A","active":"Doing A","status":"completed"},{"id":"2","text":"B","status":"pending"}]}`,
	}
	if got := events(p); !slices.Equal(got, want) {
		t.Fatalf("events\n%q\nwant\n%q", got, want)
	}
}
