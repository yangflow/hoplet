package hermes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// writeTimeout bounds the requests the read loop sends by itself, so a
// stuck transport cannot stall it.
const writeTimeout = 10 * time.Second

// Hermes's edit approval policies.
const (
	editAsk       = "ask"
	editWorkspace = "workspace_session"
	editAll       = "session"
)

// editPolicy and yolo are what a permission mode means to Hermes: edits
// ask, are allowed inside the working directory, or are all allowed; yolo
// also skips Hermes's command approvals. Commands Hermes judges safe never
// ask, whatever the mode.
func editPolicy(m proto.PermissionMode) (policy string, yolo bool) {
	switch m {
	case proto.ModeAutoEdit:
		return editWorkspace, false
	case proto.ModeFullAuto:
		return editAll, true
	}
	return editAsk, false
}

// mutation is a session setting waiting to be sent.
type mutation struct {
	operation string
	payload   map[string]any
	retried   bool // refused once for stale values already
}

// request is a request the adapter sent and has no answer to yet.
type request struct {
	method  string
	params  map[string]any
	retried bool
}

type approval struct {
	generation int64
	answered   bool
}

type tool struct {
	name    string
	kind    proto.ToolKind
	path    string // the file an edit tool writes, absolute
	existed bool   // that file existed when the tool started
}

type run struct {
	io       harness.Stdio
	spec     harness.Spec
	events   chan harness.Event
	done     chan struct{}
	finished chan struct{}
	once     sync.Once

	// Owned by loop.
	inTurn    bool
	segment   int             // the assistant message being streamed in this turn
	text      strings.Builder // its text so far
	tools     map[string]tool
	lastTool  string
	ending    *completePayload // a turn whose message.complete arrived, awaiting its receipt
	toolsSeen bool

	prefix string
	seqMu  sync.Mutex
	n      int

	mu         sync.Mutex
	native     string
	err        error
	generation int64
	revision   int64
	admission  string // the current or last turn's admission
	approvals  map[string]*approval
	requests   map[string]*request
	// attached is true once this connection has a snapshot of the session:
	// the gateway refuses anything else before that, so requests wait.
	attached bool
	waiting  []*request
	mode     proto.PermissionMode
	policy   string // the edit policy Hermes has
	effort   proto.Effort
	model    string
	// Session settings go one at a time (each changes the revision the
	// next must name) and never mid-turn (Hermes refuses them): they queue,
	// the latest per operation.
	mutations []mutation
	mutating  bool
	// afterSettings are messages sent while settings wait: they go once
	// the settings are applied, in the order the user gave them.
	afterSettings []*request
	// busy mirrors inTurn for the command methods.
	busy bool
}

func newRun(io harness.Stdio, spec harness.Spec) *run {
	native := spec.Resume
	if native == "" {
		native = spec.State.Native
	}
	r := &run{
		io: io, spec: spec, events: make(chan harness.Event, 64), done: make(chan struct{}), finished: make(chan struct{}),
		inTurn: spec.State.InTurn, busy: spec.State.InTurn, tools: map[string]tool{},
		prefix: "hoplet-" + rand.Text()[:8], native: native,
		approvals: map[string]*approval{}, requests: map[string]*request{},
		mode: spec.Mode, policy: editAsk, effort: spec.Effort, model: spec.Model,
	}
	if native != "" {
		// An attached session already has the mode Hoplet set.
		r.policy, _ = editPolicy(spec.Mode)
	}
	// Attaching to a bridge that is already running (the daemon restarted)
	// finds its connection attached to the session; a new bridge attaches
	// when it connects.
	r.attached = native != "" && spec.Resume == ""
	go r.loop()
	return r
}

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
					return
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
			r.setInTurn(false)
			base.Payload = proto.TurnEnded{Reason: proto.TurnAborted, Message: "the Hermes bridge exited"}
		}
		return []harness.Event{base}, true
	case harness.Stdout:
		payloads, native := r.translate(l.Data)
		if native != "" {
			base.Native = native
		}
		if len(payloads) == 0 {
			return []harness.Event{base}, false
		}
		for i, p := range payloads {
			ev := base
			if i > 0 {
				ev.Native = ""
			}
			ev.Payload = p
			evs = append(evs, ev)
		}
		return evs, false
	}
	return []harness.Event{base}, false
}

// translate turns one bridge line into payloads; native is set when the
// line names the Hermes session.
func (r *run) translate(line []byte) (out []proto.Payload, native string) {
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		return []proto.Payload{proto.Error{Message: "unreadable line from the Hermes bridge: " + err.Error()}}, ""
	}
	switch {
	case f.Bridge == BridgeConnected:
		r.attachSession()
		return nil, ""
	case f.Bridge == BridgeDisconnected:
		r.mu.Lock()
		r.attached = false
		r.mu.Unlock()
		return nil, ""
	case f.Bridge != "":
		return nil, ""
	case f.Method == "event":
		var ev event
		if json.Unmarshal(f.Params, &ev) != nil {
			return nil, ""
		}
		return r.onEvent(ev), ""
	case f.Method != "":
		return nil, "" // a request from the gateway: none is enabled for these sessions
	}
	return r.onResponse(f)
}

// attachSession creates the session (idempotently: its request ID is the
// Hoplet session ID) or resumes it on a new connection.
func (r *run) attachSession() {
	r.mu.Lock()
	native := r.native
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if native == "" {
		params := map[string]any{
			"request_id": string(r.spec.Session), "source": "acp", "cwd": r.spec.WorkDir,
			"editor": map[string]any{"edit_approval_policy": editAsk},
		}
		if r.spec.Model != "" {
			params["model"] = r.spec.Model
		}
		if r.spec.Effort != "" {
			params["reasoning"] = string(r.spec.Effort)
		}
		if _, yolo := editPolicy(r.spec.Mode); yolo {
			params["yolo"] = true
		}
		if r.spec.Fast {
			params["service_tier"] = "fast"
		}
		_ = r.call(ctx, "session.create", params)
		return
	}
	_ = r.call(ctx, "session.resume", map[string]any{"session_id": native, "omit_messages": true})
}

func (r *run) onResponse(f frame) (out []proto.Payload, native string) {
	id := strings.Trim(string(f.ID), `"`)
	r.mu.Lock()
	req := r.requests[id]
	delete(r.requests, id)
	r.mu.Unlock()
	if f.Error != nil {
		return r.onError(req, f.Error), ""
	}
	method := ""
	if req != nil {
		method = req.method
	}
	switch method {
	case "session.create", "session.resume":
		return r.onSnapshot(f.Result, method == "session.create")
	case "prompt.submit":
		var a admitted
		if json.Unmarshal(f.Result, &a) == nil && a.Admission != "" {
			r.mu.Lock()
			r.admission = a.Admission
			r.mu.Unlock()
		}
		return nil, ""
	case "prompt.receipt":
		var a admitted
		if json.Unmarshal(f.Result, &a) != nil {
			return nil, ""
		}
		return r.onReceipt(a), ""
	case "session.mutate":
		return r.onMutated(req, f.Result), ""
	case "":
		// An answer to a request an earlier daemon sent: only what it
		// says about itself counts.
		return r.onOrphan(f.Result)
	}
	return nil, ""
}

// onOrphan reads a response whose request this run did not send: after a
// daemon restart the shim replays answers to its predecessor's requests.
func (r *run) onOrphan(result json.RawMessage) (out []proto.Payload, native string) {
	var probe struct {
		Stored    *string     `json:"stored_session_id"`
		Admission string      `json:"admission_id"`
		Status    string      `json:"status"`
		Result    *turnResult `json:"result"`
	}
	if json.Unmarshal(result, &probe) != nil {
		return nil, ""
	}
	switch {
	case probe.Stored != nil:
		return r.onSnapshot(result, false)
	case probe.Admission != "" && probe.Status == "terminal" && probe.Result != nil:
		var a admitted
		_ = json.Unmarshal(result, &a)
		return r.onReceipt(a), ""
	case probe.Admission != "":
		r.mu.Lock()
		r.admission = probe.Admission
		r.mu.Unlock()
	}
	return nil, ""
}

func (r *run) onSnapshot(result json.RawMessage, created bool) (out []proto.Payload, native string) {
	var s snapshot
	if json.Unmarshal(result, &s) != nil || s.Session == "" {
		return []proto.Payload{proto.Error{Message: "Hermes answered without a session"}}, ""
	}
	r.mu.Lock()
	known := r.native
	r.native = s.Session
	r.revision, r.generation = s.Revision, s.Generation
	r.attached = true
	waiting := r.waiting
	r.waiting = nil
	admission := r.admission
	for _, p := range s.Pending {
		if p.Status == "started" {
			admission = p.Admission
		}
	}
	r.admission = admission
	r.mu.Unlock()
	if known != s.Session {
		native = s.Session
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if created {
		// Hermes takes only "ask" at creation; anything looser is a change.
		policy, _ := editPolicy(r.spec.Mode)
		r.setPolicy(ctx, policy)
	}
	for _, req := range waiting {
		r.fillSession(req, s.Session)
		if req.method == "prompt.submit" {
			_ = r.submitRequest(ctx, req)
			continue
		}
		_ = r.send(ctx, req)
	}
	// Approvals still open in Hermes are known again (their answers need
	// their generation).
	r.mu.Lock()
	for _, p := range s.Prompts {
		if p.Kind == "approval" {
			if _, ok := r.approvals[p.ID]; !ok {
				r.approvals[p.ID] = &approval{generation: p.Generation}
			}
		}
	}
	r.mu.Unlock()
	r.pumpMutations(ctx)
	if r.inTurn && !s.Running {
		if admission == "" {
			// Nothing names the turn that ended: say so rather than keep
			// it open.
			r.setInTurn(false)
			return []proto.Payload{proto.TurnEnded{Reason: proto.TurnAborted, Message: "the turn ended while Hoplet was not connected"}}, native
		}
		// The turn ended while the connection was down: its receipt says
		// how, and carries its final answer.
		_ = r.call(ctx, "prompt.receipt", map[string]any{"session_id": s.Session, "admission_id": admission, "include_result": true})
	}
	return nil, native
}

func (r *run) onReceipt(a admitted) []proto.Payload {
	if a.Status != "terminal" || !r.inTurn {
		return nil
	}
	var out []proto.Payload
	ending := r.ending
	r.ending = nil
	if ending == nil && a.Result != nil && a.Result.FinalResponse != "" {
		// Recovered after a lost connection: the answer was never streamed
		// here.
		out = append(out, proto.Message{ID: r.messageID(a.Admission), Role: proto.RoleAssistant, Text: a.Result.FinalResponse})
	}
	if a.Usage != nil {
		out = append(out, proto.Usage{InputTokens: a.Usage.Input, OutputTokens: a.Usage.Output})
	}
	r.setInTurn(false)
	r.segment, r.toolsSeen = 0, false
	r.text.Reset()
	return append(out, turnEnd(ending, a))
}

// turnEnd classifies a finished turn from its message.complete, when seen,
// else from its receipt.
func turnEnd(c *completePayload, a admitted) proto.TurnEnded {
	status, failure := "", ""
	if c != nil {
		status, failure = c.Status, c.Failure
		if failure == "" && c.Surface != nil {
			failure = c.Surface.Code
		}
	}
	if status == "" {
		switch {
		case a.Result != nil && a.Result.Interrupted, a.Outcome == "cancelled":
			status = "interrupted"
		case a.Result != nil && a.Result.Failed, a.Outcome == "failed":
			status = "error"
		default:
			status = "complete"
		}
		if a.Result != nil {
			failure = a.Result.FailureReason
			if failure == "" {
				failure = a.Result.Error
			}
		}
	}
	switch status {
	case "complete":
		return proto.TurnEnded{Reason: proto.TurnCompleted}
	case "interrupted":
		return proto.TurnEnded{Reason: proto.TurnInterrupted}
	}
	if failure == "" {
		failure = "Hermes reported an error"
	}
	return proto.TurnEnded{Reason: proto.TurnFailed, Message: failure}
}

func (r *run) onMutated(req *request, result json.RawMessage) []proto.Payload {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	defer r.pumpMutations(ctx)
	var m mutated
	_ = json.Unmarshal(result, &m)
	r.mu.Lock()
	r.mutating = false
	if m.Revision != nil {
		r.revision = *m.Revision
	}
	if m.Generation != nil {
		r.generation = *m.Generation
	}
	r.mu.Unlock()
	if m.Status == "confirmation_required" && req != nil && m.Confirm != "" {
		// A guarded model: the user picked it, so confirm it.
		payload, _ := req.params["payload"].(map[string]any)
		next := map[string]any{}
		for k, v := range payload {
			next[k] = v
		}
		next["confirm"] = m.Confirm
		r.queueMutation(req.params["operation"].(string), next, true)
	}
	if req != nil && req.params["operation"] == "edit_policy" {
		if p, ok := req.params["payload"].(map[string]any); ok {
			r.mu.Lock()
			r.policy, _ = p["policy"].(string)
			r.mu.Unlock()
		}
	}
	return nil
}

// onError retries a request refused for a stale generation or revision
// once with the fresh values the refusal carries; other refusals become
// errors.
func (r *run) onError(req *request, e *rpcError) []proto.Payload {
	reason := e.Data.Reason
	if reason == "" {
		reason = e.Message
	}
	if req != nil && req.method == "session.mutate" {
		return r.onMutateRefused(req, reason, e)
	}
	if req != nil && !req.retried && (reason == "stale_generation" || reason == "revision_conflict") {
		r.mu.Lock()
		if e.Data.Generation != nil {
			r.generation = *e.Data.Generation
		}
		if e.Data.Revision != nil {
			r.revision = *e.Data.Revision
		}
		gen, rev := r.generation, r.revision
		r.mu.Unlock()
		params := req.params
		if _, ok := params["execution_generation"]; ok && req.method != "approval.respond" {
			params["execution_generation"] = gen
		}
		if req.method == "approval.respond" && e.Data.Generation != nil {
			params["execution_generation"] = gen
		}
		_ = rev
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.send(ctx, &request{method: req.method, params: params, retried: true}); err == nil {
			return nil
		}
	}
	method := "a request"
	if req != nil {
		method = req.method
	}
	msg := fmt.Sprintf("Hermes refused %s: %s", method, reason)
	if e.Data.Detail != "" {
		msg += " (" + e.Data.Detail + ")"
	}
	return []proto.Payload{proto.Error{Message: msg}}
}

func (r *run) onEvent(ev event) []proto.Payload {
	r.mu.Lock()
	native := r.native
	if ev.Generation != nil {
		r.generation = *ev.Generation
	}
	if ev.Admission != "" && (ev.Session == "" || ev.Session == native) {
		r.admission = ev.Admission
	}
	r.mu.Unlock()
	if ev.Session != "" && native != "" && ev.Session != native {
		return nil
	}
	switch ev.Type {
	case "message.start":
		var out []proto.Payload
		r.setInTurn(true)
		r.ending = nil
		r.segment, r.toolsSeen = 0, false
		r.text.Reset()
		if ev.Admission != "" {
			r.mu.Lock()
			r.admission = ev.Admission
			r.mu.Unlock()
		}
		return append(out, proto.TurnStarted{})
	case "message.delta":
		var p deltaPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return nil
		}
		if p.Text == nil {
			return r.closeSegment()
		}
		if *p.Text == "" {
			return nil
		}
		r.text.WriteString(*p.Text)
		return []proto.Payload{proto.MessageDelta{MessageID: r.currentID(), Text: *p.Text}}
	case "message.interim":
		var p interimPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return nil
		}
		if p.AlreadyStreamed {
			return r.closeSegment()
		}
		out := r.closeSegment()
		out = append(out, proto.Message{ID: r.currentID(), Role: proto.RoleAssistant, Text: p.Text})
		r.segment++
		return out
	case "tool.start":
		var p toolPayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.ID == "" {
			return nil
		}
		out := r.closeSegment()
		t := tool{name: p.Name, kind: toolKind(p.Name)}
		if path := editPath(p.Name, p.Args); path != "" {
			t.path = r.absolute(path)
			_, err := os.Stat(t.path)
			t.existed = err == nil
		}
		r.tools[p.ID] = t
		r.lastTool, r.toolsSeen = p.ID, true
		return append(out, proto.ToolCall{ID: p.ID, Kind: t.kind, Name: p.Name, Summary: oneLine(p.Context), Status: proto.ToolRunning})
	case "tool.complete":
		var p toolPayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.ID == "" {
			return nil
		}
		t, ok := r.tools[p.ID]
		if !ok {
			t = tool{name: p.Name, kind: toolKind(p.Name)}
		}
		delete(r.tools, p.ID)
		status := proto.ToolDone
		if p.IsError {
			status = proto.ToolFailed
		}
		out := []proto.Payload{proto.ToolCall{ID: p.ID, Kind: t.kind, Name: p.Name, Summary: oneLine(p.Context), Status: status}}
		if !p.IsError && t.path != "" {
			op := proto.FileModified
			if !t.existed {
				op = proto.FileCreated
			}
			out = append(out, proto.FileChange{Path: t.path, Op: op})
		}
		return out
	case "approval.request":
		var p prompt
		if json.Unmarshal(ev.Payload, &p) != nil || p.ID == "" {
			return nil
		}
		r.mu.Lock()
		r.approvals[p.ID] = &approval{generation: p.Generation}
		r.mu.Unlock()
		return []proto.Payload{r.approvalRequested(p)}
	case "approval.settled":
		var p settledPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return nil
		}
		r.mu.Lock()
		a := r.approvals[p.ID]
		delete(r.approvals, p.ID)
		r.mu.Unlock()
		if a == nil || a.answered {
			return nil
		}
		// Settled without an answer from Hoplet: in another Hermes client,
		// or by Hermes's own timeout.
		return []proto.Payload{proto.ApprovalResolved{ID: p.ID, Outcome: proto.OutcomeExpired, DecidedBy: "hermes"}}
	case "message.complete":
		var p completePayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return nil
		}
		if !r.inTurn {
			return nil
		}
		var out []proto.Payload
		if p.Text != "" {
			out = append(out, proto.Message{ID: r.currentID(), Role: proto.RoleAssistant, Text: p.Text})
		}
		r.ending = &p
		admission := p.Admission
		if admission == "" {
			r.mu.Lock()
			admission = r.admission
			r.mu.Unlock()
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		r.mu.Lock()
		native := r.native
		r.mu.Unlock()
		if err := r.call(ctx, "prompt.receipt", map[string]any{"session_id": native, "admission_id": admission, "include_result": true}); err != nil {
			// No receipt coming: end the turn without its usage.
			r.setInTurn(false)
			r.ending = nil
			out = append(out, turnEnd(&p, admitted{}))
		}
		return out
	case "error":
		var p messagePayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.Message == "" {
			return nil
		}
		return []proto.Payload{proto.Error{Message: p.Message}}
	case "session.info", "session.updated":
		var p infoPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			r.mu.Lock()
			if p.Revision != nil {
				r.revision = *p.Revision
			}
			if p.Generation != nil {
				r.generation = *p.Generation
			}
			r.mu.Unlock()
		}
		return nil
	case "session.replay_gap":
		// The gateway dropped this subscription: attach again.
		r.attachSession()
		return nil
	}
	return nil
}

// closeSegment ends the streamed message: a Message with its whole text
// supersedes its deltas, and the next text is a new message.
func (r *run) closeSegment() []proto.Payload {
	if r.text.Len() == 0 {
		return nil
	}
	out := []proto.Payload{proto.Message{ID: r.currentID(), Role: proto.RoleAssistant, Text: r.text.String()}}
	r.text.Reset()
	r.segment++
	return out
}

func (r *run) currentID() string {
	r.mu.Lock()
	admission := r.admission
	r.mu.Unlock()
	return r.messageID(admission)
}

func (r *run) messageID(admission string) string {
	return admission + "/" + strconv.Itoa(r.segment)
}

func (r *run) absolute(path string) string {
	if filepath.IsAbs(path) || r.spec.WorkDir == "" {
		return path
	}
	return filepath.Join(r.spec.WorkDir, path)
}

func (r *run) approvalRequested(p prompt) proto.ApprovalRequested {
	a := proto.ApprovalRequested{ID: p.ID, Kind: proto.ApprovalCommand, Title: p.Command, Detail: p.Description, ToolCallID: r.lastTool}
	if p.Edit != nil {
		a.Kind = proto.ApprovalFileChange
		a.Title = "Edit " + p.Edit.Path
		a.Detail = p.Edit.Path
	}
	for _, c := range p.Choices {
		a.Options = append(a.Options, option(c))
	}
	return a
}

// option labels one of Hermes's approval choices.
func option(choice string) proto.ApprovalOption {
	switch choice {
	case "once":
		return proto.ApprovalOption{ID: choice, Label: "Allow once", Scope: proto.ScopeOnce}
	case "session":
		return proto.ApprovalOption{ID: choice, Label: "Allow for this session", Scope: proto.ScopeSession}
	case "always":
		return proto.ApprovalOption{ID: choice, Label: "Always allow", Scope: proto.ScopeAlways}
	case "deny":
		return proto.ApprovalOption{ID: choice, Label: "Deny", Scope: proto.ScopeDeny}
	}
	return proto.ApprovalOption{ID: choice, Label: choice}
}

// call sends a request.
func (r *run) call(ctx context.Context, method string, params map[string]any) error {
	return r.send(ctx, &request{method: method, params: params})
}

func (r *run) send(ctx context.Context, req *request) error {
	if req.method != "session.create" && req.method != "session.resume" {
		r.mu.Lock()
		if !r.attached {
			r.waiting = append(r.waiting, req)
			r.mu.Unlock()
			return nil
		}
		r.mu.Unlock()
	}
	r.seqMu.Lock()
	r.n++
	id := r.prefix + "-" + strconv.Itoa(r.n)
	r.seqMu.Unlock()
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": req.method, "params": req.params})
	if err != nil {
		return fmt.Errorf("hermes: encode: %w", err)
	}
	r.mu.Lock()
	r.requests[id] = req
	r.mu.Unlock()
	if err := r.io.WriteLine(ctx, b); err != nil {
		r.mu.Lock()
		delete(r.requests, id)
		r.mu.Unlock()
		return err
	}
	return nil
}

// session returns the Hermes session ID (empty before the session
// exists; a waiting request gets it when it does) and current generation.
func (r *run) session() (string, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.native, r.generation, nil
}

// fillSession names the session in a request written before it existed.
func (r *run) fillSession(req *request, native string) {
	if s, ok := req.params["session_id"].(string); ok && s == "" {
		req.params["session_id"] = native
	}
}

func (r *run) submit(ctx context.Context, text string) error {
	native, _, _ := r.session()
	return r.submitRequest(ctx, &request{method: "prompt.submit", params: map[string]any{"session_id": native, "text": text, "submission_id": rand.Text()}})
}

// submitRequest sends a message, after the settings still waiting.
func (r *run) submitRequest(ctx context.Context, req *request) error {
	r.mu.Lock()
	if r.attached && (r.mutating || len(r.mutations) > 0) {
		r.afterSettings = append(r.afterSettings, req)
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	return r.send(ctx, req)
}

// mutate queues a session setting and sends it when nothing else is in
// flight and no turn runs.
func (r *run) mutate(ctx context.Context, operation string, payload map[string]any) error {
	r.queueMutation(operation, payload, false)
	r.pumpMutations(ctx)
	return nil
}

// queueMutation queues a setting, replacing a waiting one of the same
// operation; first puts it at the front (a retry).
func (r *run) queueMutation(operation string, payload map[string]any, first bool) {
	r.requeue(mutation{operation: operation, payload: payload}, first)
}

func (r *run) requeue(m mutation, first bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mutations = slices.DeleteFunc(r.mutations, func(q mutation) bool { return q.operation == m.operation })
	if first {
		r.mutations = slices.Insert(r.mutations, 0, m)
	} else {
		r.mutations = append(r.mutations, m)
	}
}

// pumpMutations sends the next waiting setting if it can go now.
func (r *run) pumpMutations(ctx context.Context) {
	r.mu.Lock()
	if !r.mutating && len(r.mutations) == 0 && len(r.afterSettings) > 0 {
		// The settings are applied: the messages that waited go now.
		held := r.afterSettings
		r.afterSettings = nil
		native := r.native
		r.mu.Unlock()
		for _, req := range held {
			r.fillSession(req, native)
			_ = r.send(ctx, req)
		}
		return
	}
	if r.mutating || r.busy || !r.attached || r.native == "" || len(r.mutations) == 0 {
		r.mu.Unlock()
		return
	}
	m := r.mutations[0]
	r.mutations = r.mutations[1:]
	r.mutating = true
	native, gen, rev := r.native, r.generation, r.revision
	r.mu.Unlock()
	err := r.send(ctx, &request{method: "session.mutate", retried: m.retried, params: map[string]any{
		"session_id": native, "request_id": rand.Text(), "expected_revision": rev,
		"operation": m.operation, "payload": m.payload, "expected_generation": gen,
	}})
	if err != nil {
		r.mu.Lock()
		r.mutating = false
		r.mu.Unlock()
	}
}

// onMutateRefused handles a refused setting: a stale revision or
// generation, or a turn that started first, puts it back to go again with
// fresh values; anything else is an error.
func (r *run) onMutateRefused(req *request, reason string, e *rpcError) []proto.Payload {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	defer r.pumpMutations(ctx)
	r.mu.Lock()
	r.mutating = false
	if e.Data.Generation != nil {
		r.generation = *e.Data.Generation
	}
	if e.Data.Revision != nil {
		r.revision = *e.Data.Revision
	}
	r.mu.Unlock()
	operation, _ := req.params["operation"].(string)
	payload, _ := req.params["payload"].(map[string]any)
	switch reason {
	case "stale_generation", "revision_conflict", "session_busy":
		switch {
		case r.isBusy():
			// A turn started first: the setting waits for it to end.
			r.requeue(mutation{operation: operation, payload: payload}, true)
			return nil
		case !req.retried:
			r.requeue(mutation{operation: operation, payload: payload, retried: true}, true)
			return nil
		}
	}
	msg := fmt.Sprintf("Hermes refused setting %s: %s", operation, reason)
	if e.Data.Detail != "" {
		msg += " (" + e.Data.Detail + ")"
	}
	return []proto.Payload{proto.Error{Message: msg}}
}

func (r *run) isBusy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy
}

// setPolicy changes Hermes's edit approval policy (see mutate).
func (r *run) setPolicy(ctx context.Context, policy string) {
	r.mu.Lock()
	same := r.policy == policy && !slices.ContainsFunc(r.mutations, func(m mutation) bool { return m.operation == "edit_policy" })
	r.mu.Unlock()
	if same {
		return
	}
	_ = r.mutate(ctx, "edit_policy", map[string]any{"policy": policy})
}

// setInTurn is the loop's way to change inTurn, keeping busy in step; a
// turn's end sends the settings that waited for it.
func (r *run) setInTurn(v bool) {
	r.inTurn = v
	r.mu.Lock()
	r.busy = v
	r.mu.Unlock()
	if !v {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		r.pumpMutations(ctx)
	}
}

// Send sends a user message; Hermes queues it if a turn is running.
func (r *run) Send(ctx context.Context, text string) error { return r.submit(ctx, text) }

// Steer adds text to the running turn after its current tool calls.
func (r *run) Steer(ctx context.Context, text string) error {
	native, gen, err := r.session()
	if err != nil {
		return err
	}
	return r.call(ctx, "session.steer", map[string]any{"session_id": native, "execution_generation": gen, "text": text})
}

// Interrupt ends the running turn.
func (r *run) Interrupt(ctx context.Context) error {
	native, gen, err := r.session()
	if err != nil {
		return err
	}
	return r.call(ctx, "session.interrupt", map[string]any{"session_id": native, "execution_generation": gen})
}

// Respond answers an approval with one of Hermes's choices.
func (r *run) Respond(ctx context.Context, approvalID, optionID string) error {
	native, gen, err := r.session()
	if err != nil {
		return err
	}
	r.mu.Lock()
	if a := r.approvals[approvalID]; a != nil {
		gen = a.generation
		a.answered = true
	}
	r.mu.Unlock()
	return r.call(ctx, "approval.respond", map[string]any{
		"session_id": native, "execution_generation": gen, "prompt_id": approvalID, "choice": optionID,
	})
}

// Answer is not supported: Hermes sessions Hoplet creates have no
// question tool.
func (r *run) Answer(context.Context, string, string) error { return harness.ErrUnsupported }

// SetMode changes the permission mode: yolo now, the edit policy now or
// when the running turn ends.
func (r *run) SetMode(ctx context.Context, mode proto.PermissionMode) error {
	policy, yolo := editPolicy(mode)
	if mode != proto.ModeConfirm && policy == editAsk {
		return fmt.Errorf("hermes: no permission mode %q", mode)
	}
	native, _, err := r.session()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.mode = mode
	r.mu.Unlock()
	value := "0"
	if yolo {
		value = "1"
	}
	if err := r.call(ctx, "config.set", map[string]any{"session_id": native, "key": "yolo", "value": value}); err != nil {
		return err
	}
	r.setPolicy(ctx, policy)
	return nil
}

// SetPlan is not supported: Hermes's plan mode is only a prompt.
func (r *run) SetPlan(context.Context, bool) error { return harness.ErrUnsupported }

// SetEffort changes the reasoning effort, now or when the running turn
// ends.
func (r *run) SetEffort(ctx context.Context, effort proto.Effort) error {
	r.mu.Lock()
	r.effort = effort
	r.mu.Unlock()
	return r.mutate(ctx, "reasoning", map[string]any{"effort": string(effort)})
}

// SetFast turns Hermes's fast service tier on or off from the next turn.
func (r *run) SetFast(ctx context.Context, on bool) error {
	native, _, err := r.session()
	if err != nil {
		return err
	}
	value := "normal"
	if on {
		value = "fast"
	}
	return r.call(ctx, "config.set", map[string]any{"session_id": native, "key": "service_tier", "value": value})
}

// SetModel changes the model; Hermes applies it from the next turn.
func (r *run) SetModel(ctx context.Context, model string) error {
	r.mu.Lock()
	r.model = model
	r.mu.Unlock()
	return r.mutate(ctx, "model", map[string]any{"model": model})
}

func (r *run) Events() <-chan harness.Event { return r.events }

func (r *run) NativeSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.native
}

func (r *run) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close stops translating and releases the transport; the bridge keeps
// running behind its shim.
func (r *run) Close() error {
	var err error
	r.once.Do(func() {
		close(r.done)
		err = r.io.Close()
		<-r.finished
	})
	return err
}
