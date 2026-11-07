package hermes

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/yangflow/hoplet/proto"
)

// Hermes's task list and delegation tools.
const (
	todoTool     = "todo_list"
	delegateTool = "delegate_task"
)

// tagged is a payload of a subagent's record rather than of the session's
// own conversation; handle unwraps it into Event.Agent.
type tagged struct {
	proto.Payload
	agent string
}

// agents tracks the subagents of delegate_task calls until they end.
type agents struct {
	subs  map[string]*proto.Subagent // by Hoplet ID
	calls map[string][]string        // a call's subagents in task order
	// byNative maps Hermes's subagent IDs to Hoplet's; delegating is the
	// delegate_task call running now, whose subagents Hermes announces
	// before the call completes.
	byNative   map[string]string
	delegating string
	toolOpen   map[string]proto.ToolCall // a subagent's tool call not yet over
}

func newAgents() agents {
	return agents{
		subs: map[string]*proto.Subagent{}, calls: map[string][]string{},
		byNative: map[string]string{}, toolOpen: map[string]proto.ToolCall{},
	}
}

// todo applies a todo_list call's arguments to the task list: merge false
// replaces it, merge true updates tasks by ID and adds new ones. Subtasks
// are flattened in order.
func (r *run) todo(args json.RawMessage) []proto.Payload {
	var a struct {
		Todos []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
		Merge bool `json:"merge"`
	}
	if json.Unmarshal(args, &a) != nil {
		return nil
	}
	if !a.Merge {
		r.todos = r.todos[:0]
	}
	for _, t := range a.Todos {
		i := slices.IndexFunc(r.todos, func(x proto.TodoItem) bool { return x.ID == t.ID })
		if i < 0 {
			r.todos = append(r.todos, proto.TodoItem{ID: t.ID, Text: t.Content, Status: todoStatus(t.Status)})
			continue
		}
		if t.Content != "" {
			r.todos[i].Text = t.Content
		}
		if t.Status != "" {
			r.todos[i].Status = todoStatus(t.Status)
		}
	}
	return []proto.Payload{proto.TodoList{Items: slices.Clone(r.todos)}}
}

// todoStatus maps Hermes's task states; anything unknown counts as not
// started.
func todoStatus(s string) proto.TodoStatus {
	switch s {
	case "in_progress":
		return proto.TodoInProgress
	case "completed":
		return proto.TodoCompleted
	case "cancelled":
		return proto.TodoCancelled
	}
	return proto.TodoPending
}

// delegate starts tracking the subagents a delegate_task call sends off:
// one per task, or one for a single goal, each record beginning with its
// task. Hermes gives no kind of subagent.
func (r *run) delegate(call string, args json.RawMessage) []proto.Payload {
	var a struct {
		Goal  string `json:"goal"`
		Tasks []struct {
			Goal string `json:"goal"`
		} `json:"tasks"`
	}
	_ = json.Unmarshal(args, &a)
	goals := []string{a.Goal}
	if len(a.Tasks) > 0 {
		goals = goals[:0]
		for _, t := range a.Tasks {
			goals = append(goals, t.Goal)
		}
	}
	r.delegating = call
	var out []proto.Payload
	for i, g := range goals {
		id := call
		if len(goals) > 1 {
			id = call + "/" + strconv.Itoa(i+1)
		}
		s := &proto.Subagent{ID: id, CallID: call, Title: oneLine(g), Status: proto.SubagentRunning}
		r.subs[id] = s
		r.calls[call] = append(r.calls[call], id)
		out = append(out, *s)
		if g != "" {
			out = append(out, tagged{Payload: proto.Message{ID: id + "/task", Role: proto.RoleUser, Text: g}, agent: id})
		}
	}
	return out
}

// delegated applies the end of a delegate_task call. A call that sent its
// subagents off in the background leaves them running, now known by
// Hermes's IDs, until their subagent.complete; any other call's subagents
// end with it.
func (r *run) delegated(call string, failed bool, result json.RawMessage) []proto.Payload {
	if r.delegating == call {
		r.delegating = ""
	}
	ids := r.calls[call]
	delete(r.calls, call)
	var d dispatched
	if text := ""; json.Unmarshal(result, &text) == nil {
		_ = json.Unmarshal([]byte(text), &d)
	}
	var out []proto.Payload
	for i, id := range ids {
		s := r.subs[id]
		if s == nil {
			continue
		}
		if !failed && d.Status == "dispatched" {
			if i < len(d.SubagentIDs) {
				r.byNative[d.SubagentIDs[i]] = id
			}
			s.Background = true
		} else {
			s.Status = proto.SubagentDone
			if failed {
				s.Status = proto.SubagentFailed
			}
			delete(r.subs, id)
		}
		out = append(out, *s)
	}
	return out
}

// subagent applies one of Hermes's events about a subagent: the start
// names it, a tool shows in its record, the completion ends it with its
// summary and usage.
func (r *run) subagent(typ string, p subagentPayload) []proto.Payload {
	id, ok := r.byNative[p.ID]
	if !ok {
		// Announced while its delegate_task call runs: by task order.
		ids := r.calls[r.delegating]
		if p.TaskIndex < 0 || p.TaskIndex >= len(ids) {
			return nil
		}
		id = ids[p.TaskIndex]
		r.byNative[p.ID] = id
	}
	s := r.subs[id]
	if s == nil {
		return nil
	}
	switch typ {
	case "subagent.tool":
		out := r.toolOver(id)
		call := proto.ToolCall{ID: id + "/" + strconv.Itoa(p.ToolCount), Kind: toolKind(p.ToolName), Name: p.ToolName, Summary: oneLine(p.Text), Status: proto.ToolRunning}
		r.toolOpen[id] = call
		return append(out, tagged{Payload: call, agent: id})
	case "subagent.complete":
		out := r.toolOver(id)
		switch p.Status {
		case "completed":
			s.Status = proto.SubagentDone
		case "failed", "error":
			s.Status = proto.SubagentFailed
		default:
			s.Status = proto.SubagentStopped
		}
		s.Summary = p.Summary
		s.Tokens = p.InputTokens + p.OutputTokens
		s.ToolUses = p.ToolCount
		s.DurationMS = int64(p.DurationSeconds * 1000)
		delete(r.subs, id)
		delete(r.byNative, p.ID)
		return append(out, *s)
	}
	return nil
}

// toolOver ends a subagent's last tool call: Hermes says only when the next
// one starts or the subagent ends.
func (r *run) toolOver(id string) []proto.Payload {
	call, ok := r.toolOpen[id]
	if !ok {
		return nil
	}
	delete(r.toolOpen, id)
	call.Status = proto.ToolDone
	return []proto.Payload{tagged{Payload: call, agent: id}}
}
