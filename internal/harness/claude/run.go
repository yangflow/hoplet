package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// Approval option IDs. "always" is offered when claude suggests permission
// updates; answering with it applies them all, as claude proposed them.
const (
	optionAllow  = "allow"
	optionAlways = "always"
	optionDeny   = "deny"
)

// denyMessage is what claude is told when the user denies a tool.
// writeTimeout bounds the writes the read loop makes by itself, so a stuck
// transport cannot stall it.
const writeTimeout = 10 * time.Second

const denyMessage = "The user denied this in Hoplet."

// pending is an approval request the adapter has seen and not answered.
type pending struct {
	input       json.RawMessage
	suggestions json.RawMessage
}

// tool is a tool call the adapter has seen start.
type tool struct {
	name  string
	kind  proto.ToolKind
	input json.RawMessage
}

type run struct {
	io       harness.Stdio
	events   chan harness.Event
	done     chan struct{} // closed by Close
	finished chan struct{} // closed when loop has returned
	once     sync.Once

	// Owned by loop.
	inTurn bool
	blocks map[string]int // content blocks seen per assistant message ID
	msgID  string         // the message being streamed
	tools  map[string]tool

	prefix string // makes this run's control request IDs unique
	seq    sync.Mutex
	n      int

	mu        sync.Mutex
	native    string // claude's session ID
	err       error
	approvals map[string]pending
	// The session's permission mode and plan toggle as Hoplet sees them, and
	// the claude mode they currently mean. A mode claude reports that differs
	// from want is a change claude made itself.
	base proto.PermissionMode
	plan bool
	want string
	// observed is the last native mode claude reported.
	observed string
}

func newRun(io harness.Stdio, spec harness.Spec) *run {
	r := &run{
		io: io, events: make(chan harness.Event, 64), done: make(chan struct{}), finished: make(chan struct{}),
		inTurn: spec.State.InTurn, native: spec.State.Native, blocks: map[string]int{}, tools: map[string]tool{},
		prefix: "hoplet-" + rand.Text()[:8], approvals: map[string]pending{},
		base: spec.Mode, plan: spec.Plan,
	}
	r.want = r.wanted()
	go r.loop()
	return r
}

// emit delivers ev unless Close has been called.
func (r *run) emit(ev harness.Event) bool {
	select {
	case r.events <- ev:
		return true
	case <-r.done:
		return false
	}
}

func (r *run) loop() {
	defer close(r.finished)
	defer close(r.events)
	lines := r.io.Lines()
	for {
		var l harness.Line
		select {
		case got, ok := <-lines:
			if !ok {
				select {
				case <-r.done:
					return // the caller closed the run; that is not a detach
				default:
				}
				r.mu.Lock()
				r.err = harness.ErrDetached
				r.mu.Unlock()
				return
			}
			l = got
		case <-r.done:
			return
		}
		evs, final := r.handle(l)
		for _, ev := range evs {
			if !r.emit(ev) {
				return
			}
		}
		if final {
			return
		}
	}
}

// handle turns one line into one or more events, all carrying the line's
// offset; final reports the harness has exited.
func (r *run) handle(l harness.Line) (evs []harness.Event, final bool) {
	base := harness.Event{Raw: l.Data, Offset: l.Offset}
	switch l.Kind {
	case harness.Stderr:
		base.Stderr = true
	case harness.Stdin:
	case harness.Position:
		base.Raw = nil
	case harness.StdinError:
		base.Payload = proto.Error{Message: "harness stdin write failed: " + string(l.Data)}
		base.StdinError = true
	case harness.Exit:
		base.Raw = nil
		base.Exit = &harness.ExitStatus{Code: l.Code, Signal: l.Signal}
		if r.inTurn {
			r.inTurn = false
			base.Payload = proto.TurnEnded{Reason: proto.TurnAborted, Message: exitMessage(l)}
		}
		return []harness.Event{base}, true
	case harness.Stdout:
		payloads, native := r.translate(l.Data)
		if native != "" {
			base.Native = native
			r.mu.Lock()
			r.native = native
			r.mu.Unlock()
		}
		if len(payloads) == 0 {
			return []harness.Event{base}, false
		}
		for i, p := range payloads {
			ev := base
			if i > 0 {
				ev.Native = "" // announced once
			}
			ev.Payload = p
			evs = append(evs, ev)
		}
		return evs, false
	}
	return []harness.Event{base}, false
}

func exitMessage(l harness.Line) string {
	if l.Signal != "" {
		return "claude killed by SIG" + strings.TrimPrefix(l.Signal, "SIG")
	}
	return fmt.Sprintf("claude exited with code %d", l.Code)
}

// translate maps one stdout line to payloads; the second result is the
// native session ID when the line carries it.
func (r *run) translate(data []byte) ([]proto.Payload, string) {
	var ln line
	if err := json.Unmarshal(data, &ln); err != nil {
		return []proto.Payload{proto.Error{Message: "unparseable claude output"}}, ""
	}
	switch ln.Type {
	case "system":
		var out []proto.Payload
		if ln.Subtype == "init" && !r.inTurn {
			r.inTurn = true
			out = append(out, proto.TurnStarted{})
		}
		if ln.PermissionMode != "" {
			out = append(out, r.observeMode(ln.PermissionMode)...)
		}
		if ln.Subtype == "init" {
			return out, ln.SessionID // claude announces its session on every turn
		}
		return out, ""
	case "stream_event":
		return r.stream(ln.Event), ""
	case "assistant":
		return r.assistant(ln.Message), ""
	case "user":
		return r.toolResults(ln.Message), ""
	case "control_request":
		return r.controlRequest(ln.RequestID, ln.Request), ""
	case "control_response":
		if ln.Response != nil && ln.Response.Subtype == "error" {
			return []proto.Payload{proto.Error{Message: "claude refused a request: " + ln.Response.Error}}, ""
		}
		return nil, ""
	case "control_cancel_request":
		r.mu.Lock()
		_, ok := r.approvals[ln.RequestID]
		delete(r.approvals, ln.RequestID)
		r.mu.Unlock()
		if ok {
			return []proto.Payload{proto.ApprovalResolved{ID: ln.RequestID, Outcome: proto.OutcomeExpired}}, ""
		}
		return nil, ""
	case "result":
		return r.result(ln), ""
	}
	// rate_limit_event and kinds added in later versions carry nothing
	// Hoplet shows; the raw line is still recorded.
	return nil, ""
}

func (r *run) stream(e *streamEvent) []proto.Payload {
	if e == nil {
		return nil
	}
	switch e.Type {
	case "message_start":
		if e.Message != nil {
			r.msgID = e.Message.ID
		}
	case "content_block_delta":
		if e.Delta != nil && e.Delta.Type == "text_delta" && e.Delta.Text != "" {
			return []proto.Payload{proto.MessageDelta{MessageID: blockID(r.msgID, e.Index), Text: e.Delta.Text}}
		}
	}
	return nil
}

func blockID(msg string, i int) string { return msg + "/" + strconv.Itoa(i) }

// assistant maps a message's blocks. claude writes one line per content
// block, in block order, so a block's index is the count seen so far.
func (r *run) assistant(m *message) []proto.Payload {
	if m == nil {
		return nil
	}
	var out []proto.Payload
	for _, b := range m.Content {
		i := r.blocks[m.ID]
		r.blocks[m.ID] = i + 1
		switch b.Type {
		case "text":
			out = append(out, proto.Message{ID: blockID(m.ID, i), Role: proto.RoleAssistant, Text: b.Text})
		case "thinking":
			if b.Thinking != "" {
				out = append(out, proto.Thinking{Text: b.Thinking})
			}
		case "tool_use":
			kind := toolKind(b.Name)
			r.tools[b.ID] = tool{name: b.Name, kind: kind, input: b.Input}
			out = append(out, proto.ToolCall{ID: b.ID, Kind: kind, Name: b.Name, Summary: summary(b.Name, b.Input), Status: proto.ToolRunning})
		}
	}
	return out
}

// toolResults maps the results of tool calls; a successful file tool also
// reports the file it changed.
func (r *run) toolResults(m *message) []proto.Payload {
	if m == nil {
		return nil
	}
	var out []proto.Payload
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			continue
		}
		t, ok := r.tools[b.ToolUseID]
		delete(r.tools, b.ToolUseID)
		status := proto.ToolDone
		if b.IsError {
			status = proto.ToolFailed
		}
		out = append(out, proto.ToolCall{ID: b.ToolUseID, Kind: t.kind, Name: t.name, Summary: summary(t.name, t.input), Status: status})
		if !ok || b.IsError {
			continue
		}
		if path := filePath(t.name, t.input); path != "" {
			op := proto.FileModified
			if t.name == "Write" && strings.HasPrefix(resultText(b.Content), "File created") {
				op = proto.FileCreated
			}
			out = append(out, proto.FileChange{Path: path, Op: op})
		}
	}
	if len(m.Content) > 0 && len(out) == 0 {
		return nil // an interrupt marker or an echoed prompt
	}
	return out
}

func (r *run) controlRequest(id string, req *controlRequest) []proto.Payload {
	if req == nil {
		return nil
	}
	if req.Subtype != "can_use_tool" {
		// Nothing else is registered with claude, so nothing else should
		// ask; refuse it rather than leave claude waiting.
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		_ = r.write(ctx, responseLine{Type: "control_response", Response: responseInner{
			Subtype: "error", RequestID: id, Error: "Hoplet does not handle " + req.Subtype,
		}})
		return []proto.Payload{proto.Error{Message: "claude sent an unsupported control request: " + req.Subtype}}
	}
	r.mu.Lock()
	r.approvals[id] = pending{input: req.Input, suggestions: req.Suggestions}
	r.mu.Unlock()
	return []proto.Payload{approval(id, req)}
}

// exitPlanTool is the tool claude calls to ask for its plan to be approved.
const exitPlanTool = "ExitPlanMode"

func approval(id string, req *controlRequest) proto.ApprovalRequested {
	if req.ToolName == exitPlanTool {
		return proto.ApprovalRequested{
			ID: id, Kind: proto.ApprovalTool, Title: "Start with this plan", Detail: inputField(req.Input, "plan"),
			ToolCallID: req.ToolUseID, Options: []proto.ApprovalOption{
				{ID: optionAllow, Label: "Start", Scope: proto.ScopeOnce},
				{ID: optionDeny, Label: "Keep planning", Scope: proto.ScopeDeny},
			},
		}
	}
	kind := toolKind(req.ToolName)
	a := proto.ApprovalRequested{ID: id, Title: title(req), Detail: req.Description, ToolCallID: req.ToolUseID}
	switch kind {
	case proto.ToolExec:
		a.Kind = proto.ApprovalCommand
	case proto.ToolEdit:
		a.Kind = proto.ApprovalFileChange
	default:
		a.Kind = proto.ApprovalTool
	}
	if a.Detail == title(req) || strings.HasSuffix(a.Title, " "+a.Detail) {
		a.Detail = ""
	}
	a.Options = []proto.ApprovalOption{{ID: optionAllow, Label: "Allow once", Scope: proto.ScopeOnce}}
	if scope, ok := suggestionScope(req.Suggestions); ok {
		label := "Allow for this session"
		if scope == proto.ScopeAlways {
			label = "Always allow"
		}
		a.Options = append(a.Options, proto.ApprovalOption{ID: optionAlways, Label: label, Scope: scope})
	}
	a.Options = append(a.Options, proto.ApprovalOption{ID: optionDeny, Label: "Deny", Scope: proto.ScopeDeny})
	return a
}

// suggestionScope says how far claude's suggested permission updates reach:
// the session only, or beyond it (settings files).
func suggestionScope(raw json.RawMessage) (proto.ApprovalScope, bool) {
	var ss []suggestion
	if len(raw) == 0 || json.Unmarshal(raw, &ss) != nil || len(ss) == 0 {
		return "", false
	}
	for _, s := range ss {
		if s.Destination != "session" {
			return proto.ScopeAlways, true
		}
	}
	return proto.ScopeSession, true
}

func title(req *controlRequest) string {
	name := req.DisplayName
	if name == "" {
		name = req.ToolName
	}
	if s := summary(req.ToolName, req.Input); s != "" {
		return name + " " + s
	}
	return name
}

func (r *run) result(ln line) []proto.Payload {
	var out []proto.Payload
	if u := ln.Usage; u != nil && (u.InputTokens+u.OutputTokens+u.CacheCreationInputTokens+u.CacheReadInputTokens) > 0 {
		out = append(out, proto.Usage{InputTokens: u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens, OutputTokens: u.OutputTokens})
	}
	end := proto.TurnEnded{Reason: proto.TurnCompleted}
	switch {
	case ln.TerminalReason == "aborted_streaming" || ln.TerminalReason == "aborted_tools":
		end.Reason = proto.TurnInterrupted
	case ln.IsError:
		end.Reason = proto.TurnFailed
		end.Message = ln.Result
		if end.Message == "" {
			end.Message = ln.errorsText()
		}
	}
	if !r.inTurn {
		// A result outside a turn (claude never sent init) still ends one;
		// the daemon ignores ends of turns it never opened.
		return append(out, end)
	}
	r.inTurn = false
	r.msgID = ""
	clear(r.blocks)
	clear(r.tools)
	r.mu.Lock()
	clear(r.approvals) // the store expires what is still pending
	r.mu.Unlock()
	return append(out, end)
}

// wanted is the claude mode the session's settings mean; call with mu held
// or before the run is shared.
func (r *run) wanted() string {
	if r.plan {
		return planMode
	}
	m, err := nativeMode(r.base)
	if err != nil {
		return "default"
	}
	return m
}

// observeMode compares a mode claude reports with the one Hoplet asked for
// and reports what claude changed by itself: entering plan mode, leaving
// it (after the user approves a plan), or switching modes (an approval
// answered with a session-wide permission update). When claude leaves plan
// mode it returns to its own default, so the session's mode is restored.
func (r *run) observeMode(mode string) []proto.Payload {
	r.mu.Lock()
	seen := r.observed
	r.observed = mode
	if mode == r.want || mode == seen {
		r.mu.Unlock()
		return nil
	}
	var restore string
	switch {
	case mode == planMode:
		r.plan = true
	case r.plan:
		r.plan = false
		if target := r.wanted(); target != mode {
			restore = target
		}
	default:
		r.base = hopletMode(mode)
	}
	r.want = r.wanted()
	changed := proto.ModeChanged{Permission: r.base, Plan: r.plan}
	r.mu.Unlock()
	if restore != "" {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.control(ctx, map[string]string{"subtype": "set_permission_mode", "mode": restore}); err != nil {
			return []proto.Payload{changed, proto.Error{Message: "could not restore the permission mode after plan mode: " + err.Error()}}
		}
	}
	return []proto.Payload{changed}
}

// setMode sends claude the mode the session's settings mean.
func (r *run) setMode(ctx context.Context) error {
	r.mu.Lock()
	r.want = r.wanted()
	m := r.want
	r.mu.Unlock()
	return r.control(ctx, map[string]string{"subtype": "set_permission_mode", "mode": m})
}

// Commands.

func (r *run) write(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("claude: encode: %w", err)
	}
	return r.io.WriteLine(ctx, b)
}

func (r *run) requestID() string {
	r.seq.Lock()
	defer r.seq.Unlock()
	r.n++
	return r.prefix + "-" + strconv.Itoa(r.n)
}

func (r *run) control(ctx context.Context, req any) error {
	return r.write(ctx, requestLine{Type: "control_request", RequestID: r.requestID(), Request: req})
}

// Send sends a user message.
func (r *run) Send(ctx context.Context, text string) error {
	return r.write(ctx, userLine{Type: "user", Message: userMessage{Role: "user", Content: text}})
}

// Steer is not verified for claude yet.
func (r *run) Steer(context.Context, string) error { return harness.ErrUnsupported }

// Interrupt ends the running turn.
func (r *run) Interrupt(ctx context.Context) error {
	return r.control(ctx, map[string]string{"subtype": "interrupt"})
}

// Respond answers a permission request. An approval the adapter has not
// seen (a daemon that reattached after the request) is answered without
// the original input or suggestions, which claude accepts; keeping a
// permission needs claude's suggestions, so optionAlways without any is an
// error.
func (r *run) Respond(ctx context.Context, approvalID, optionID string) error {
	r.mu.Lock()
	p := r.approvals[approvalID]
	r.mu.Unlock()
	var perm permission
	switch optionID {
	case optionAllow:
		perm = permission{Behavior: "allow", UpdatedInput: p.input}
	case optionAlways:
		if len(p.suggestions) == 0 {
			return fmt.Errorf("claude: approval %q has no permission to keep; answer allow or deny", approvalID)
		}
		perm = permission{Behavior: "allow", UpdatedInput: p.input, UpdatedPermissions: p.suggestions}
	case optionDeny:
		perm = permission{Behavior: "deny", Message: denyMessage}
	default:
		return fmt.Errorf("claude: unknown approval option %q", optionID)
	}
	err := r.write(ctx, responseLine{Type: "control_response", Response: responseInner{Subtype: "success", RequestID: approvalID, Response: perm}})
	if err == nil {
		r.mu.Lock()
		delete(r.approvals, approvalID)
		r.mu.Unlock()
	}
	return err
}

// Answer is not supported: claude's questions are not verified yet.
func (r *run) Answer(context.Context, string, string) error { return harness.ErrUnsupported }

// SetMode changes the permission mode. While plan mode is on it only
// changes the mode restored when plan mode ends.
func (r *run) SetMode(ctx context.Context, mode proto.PermissionMode) error {
	if _, err := nativeMode(mode); err != nil {
		return err
	}
	r.mu.Lock()
	r.base = mode
	inPlan := r.plan
	r.mu.Unlock()
	if inPlan {
		return nil
	}
	return r.setMode(ctx)
}

// SetPlan turns claude's plan mode on or off; off restores the session's
// permission mode.
func (r *run) SetPlan(ctx context.Context, on bool) error {
	r.mu.Lock()
	r.plan = on
	r.mu.Unlock()
	return r.setMode(ctx)
}

// SetEffort changes claude's thinking effort for the following turns.
func (r *run) SetEffort(ctx context.Context, effort proto.Effort) error {
	if !caps().HasEffort(effort) {
		return harness.ErrUnsupported
	}
	return r.control(ctx, map[string]any{"subtype": "apply_flag_settings", "settings": map[string]string{"effortLevel": string(effort)}})
}

// SetFast is not supported: claude 2.1.312 keeps fast mode off when asked
// through its stdio protocol.
func (r *run) SetFast(context.Context, bool) error { return harness.ErrUnsupported }

// SetModel changes the model for the following turns.
func (r *run) SetModel(ctx context.Context, model string) error {
	return r.control(ctx, map[string]string{"subtype": "set_model", "model": model})
}

// Events is the normalized event stream.
func (r *run) Events() <-chan harness.Event { return r.events }

// NativeSession is claude's session ID.
func (r *run) NativeSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.native
}

// Err says why Events closed.
func (r *run) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close stops translating, releases the transport and waits for the loop.
func (r *run) Close() error {
	var err error
	r.once.Do(func() {
		close(r.done)
		err = r.io.Close()
	})
	<-r.finished
	return err
}
