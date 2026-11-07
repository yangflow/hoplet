package claude

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

// tagged is a payload of a subagent's record rather than of the session's
// own conversation; handle unwraps it into Event.Agent.
type tagged struct {
	proto.Payload
	agent string
}

// isAgentTool reports whether name is claude's tool that sends off a
// subagent ("Task" before it was renamed).
func isAgentTool(name string) bool { return name == "Agent" || name == "Task" }

// isTodoTool reports whether name is one of claude's task list tools: the
// task tools, or the older TodoWrite.
func isTodoTool(name string) bool {
	switch name {
	case "TodoWrite", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet":
		return true
	}
	return false
}

// owner is the subagent a line belongs to: the outermost one, since a
// subagent's own subagents are flattened into its record; empty for the
// session's own lines.
func (r *run) owner(parent *string) string {
	if parent == nil || *parent == "" {
		return ""
	}
	if outer, ok := r.nested[*parent]; ok {
		return outer
	}
	return *parent
}

// tag marks payloads as agent's; it leaves them alone for the session's
// own conversation.
func tag(ps []proto.Payload, agent string) []proto.Payload {
	if agent == "" {
		return ps
	}
	for i, p := range ps {
		ps[i] = tagged{Payload: p, agent: agent}
	}
	return ps
}

// spawn starts tracking a subagent sent off by the session's own call id.
// Its record begins with the task the main agent handed it.
func (r *run) spawn(id string, input json.RawMessage) []proto.Payload {
	var in struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
		Background   bool   `json:"run_in_background"`
	}
	_ = json.Unmarshal(input, &in)
	if in.SubagentType == "general-purpose" {
		in.SubagentType = "" // claude's default kind says nothing
	}
	a := proto.Subagent{ID: id, CallID: id, Type: in.SubagentType, Title: in.Description, Status: proto.SubagentRunning, Background: in.Background}
	r.subs[id] = a
	out := []proto.Payload{a}
	if in.Prompt != "" {
		out = append(out, tagged{Payload: proto.Message{ID: id + "/task", Role: proto.RoleUser, Text: in.Prompt}, agent: id})
	}
	return out
}

// usageBlock is the usage claude appends to a subagent's result text.
var usageBlock = regexp.MustCompile(`(?s)<usage>(.*?)</usage>`)

// agentTrailer is the line claude appends naming the subagent for resuming.
var agentTrailer = regexp.MustCompile(`(?m)^agentId: .*$`)

// handBack is the frame claude puts around a subagent's report in the
// result text: a list of the tools it used and a notice, then the report
// with every line indented by two spaces.
var handBack = regexp.MustCompile(`(?s)^\s*<tools-used-before-summary>.*?</tools-used-before-summary>\s*(?:\[Subagent hand-back\].*?The report follows:\n)?`)

// ended applies the notification claude sends when a subagent ends. It is
// the only word on a background subagent; for one the turn waits for it
// comes before or after the call's result, and whichever comes first
// ends it.
func (r *run) ended(n taskNotification) []proto.Payload {
	a, ok := r.subs[n.ToolUseID]
	if !ok {
		return nil
	}
	delete(r.subs, n.ToolUseID)
	switch n.Status {
	case "completed":
		a.Status = proto.SubagentDone
	case "failed":
		a.Status = proto.SubagentFailed
	default: // killed, stopped
		a.Status = proto.SubagentStopped
	}
	a.Summary = strings.TrimSpace(n.Summary)
	a.Tokens = max(a.Tokens, n.Usage.TotalTokens)
	a.ToolUses = max(a.ToolUses, n.Usage.ToolUses)
	a.DurationMS = max(a.DurationMS, n.Usage.DurationMS)
	if !a.Background {
		r.hidden[n.ToolUseID] = true // its call's result says no more
	}
	return []proto.Payload{a}
}

// finish records the result of a subagent's call when no notification
// ended it first: the report it handed back and what it used. A
// background subagent's call returns as soon as it is launched, so that
// does not end it.
func (r *run) finish(id string, b block, extra *toolUseResult) []proto.Payload {
	a := r.subs[id]
	if a.Background && !b.IsError {
		return nil // launched; the notification ends it
	}
	var parts []string
	if extra != nil {
		for _, c := range extra.Content {
			if t := strings.TrimSpace(handBack.ReplaceAllString(c.Text, "")); t != "" {
				parts = append(parts, t)
			}
		}
	}
	if len(parts) == 0 {
		for _, t := range resultParts(b.Content) {
			if m := usageBlock.FindStringSubmatch(t); m != nil {
				readUsage(&a, m[1])
				t = strings.Replace(t, m[0], "", 1)
			}
			t = agentTrailer.ReplaceAllString(t, "")
			if loc := handBack.FindStringIndex(t); loc != nil {
				t = dedent(t[loc[1]:])
			}
			if t = strings.TrimSpace(t); t != "" {
				parts = append(parts, t)
			}
		}
	}
	if extra != nil {
		a.Tokens = max(a.Tokens, extra.TotalTokens)
		a.ToolUses = max(a.ToolUses, extra.TotalToolUseCount)
		a.DurationMS = max(a.DurationMS, extra.TotalDurationMS)
	}
	a.Status = proto.SubagentDone
	if b.IsError {
		a.Status = proto.SubagentFailed
	}
	a.Summary = strings.Join(parts, "\n\n")
	delete(r.subs, id)
	return []proto.Payload{a}
}

// dedent removes the two spaces the hand-back frame indents a report's
// lines with.
func dedent(s string) string {
	var b strings.Builder
	for l := range strings.Lines(s) {
		b.WriteString(strings.TrimPrefix(l, "  "))
	}
	return b.String()
}

// resultParts is a tool result's text, block by block.
func resultParts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	out := make([]string, 0, len(blocks))
	for _, x := range blocks {
		out = append(out, x.Text)
	}
	return out
}

// readUsage reads "total_tokens: N" (or "subagent_tokens: N"),
// "tool_uses: N" and "duration_ms: N" lines.
func readUsage(a *proto.Subagent, s string) {
	for l := range strings.Lines(s) {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(k) {
		case "total_tokens", "subagent_tokens":
			a.Tokens = n
		case "tool_uses":
			a.ToolUses = int(n)
		case "duration_ms":
			a.DurationMS = n
		}
	}
}

// stopSubagents ends the subagents the turn waited for and that are still
// running, as when the turn is stopped; background ones go on.
func (r *run) stopSubagents() []proto.Payload {
	var out []proto.Payload
	for _, id := range slices.Sorted(maps.Keys(r.subs)) {
		a := r.subs[id]
		if a.Background {
			continue
		}
		a.Status = proto.SubagentStopped
		delete(r.subs, id)
		out = append(out, a)
	}
	return out
}

// todo applies a call of a task list tool to the list and reports the
// list when the call changed it. A created task joins the list with its
// call's result, which gives its number.
func (r *run) todo(id, name string, input json.RawMessage) []proto.Payload {
	switch name {
	case "TodoWrite":
		var in struct {
			Todos []struct {
				Content    string `json:"content"`
				Status     string `json:"status"`
				ActiveForm string `json:"activeForm"`
			} `json:"todos"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		r.todos = r.todos[:0]
		for i, t := range in.Todos {
			r.todos = append(r.todos, proto.TodoItem{ID: strconv.Itoa(i + 1), Text: t.Content, Active: t.ActiveForm, Status: todoStatus(t.Status)})
		}
	case "TaskCreate":
		var in struct {
			Subject    string `json:"subject"`
			ActiveForm string `json:"activeForm"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		r.creating[id] = proto.TodoItem{Text: in.Subject, Active: in.ActiveForm, Status: proto.TodoPending}
		return nil
	case "TaskUpdate":
		var in struct {
			TaskID     string `json:"taskId"`
			Status     string `json:"status"`
			Subject    string `json:"subject"`
			ActiveForm string `json:"activeForm"`
		}
		if json.Unmarshal(input, &in) != nil {
			return nil
		}
		i := slices.IndexFunc(r.todos, func(t proto.TodoItem) bool { return t.ID == in.TaskID })
		if i < 0 {
			return nil
		}
		if in.Status == "deleted" {
			r.todos = slices.Delete(r.todos, i, i+1)
			break
		}
		if in.Status != "" {
			r.todos[i].Status = todoStatus(in.Status)
		}
		if in.Subject != "" {
			r.todos[i].Text = in.Subject
		}
		if in.ActiveForm != "" {
			r.todos[i].Active = in.ActiveForm
		}
	default:
		return nil // reading the list changes nothing
	}
	return []proto.Payload{proto.TodoList{Items: slices.Clone(r.todos)}}
}

// createdTask is the result of a successful TaskCreate call.
var createdTask = regexp.MustCompile(`^Task #(\S+) created`)

// created adds the task a TaskCreate call made, numbered as its result
// says; a failed call adds nothing.
func (r *run) created(b block) []proto.Payload {
	t, ok := r.creating[b.ToolUseID]
	if !ok {
		return nil
	}
	delete(r.creating, b.ToolUseID)
	m := createdTask.FindStringSubmatch(strings.Join(resultParts(b.Content), ""))
	if b.IsError || m == nil {
		return nil
	}
	t.ID = m[1]
	r.todos = append(r.todos, t)
	return []proto.Payload{proto.TodoList{Items: slices.Clone(r.todos)}}
}

// todoStatus maps claude's task states; anything unknown counts as not
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
