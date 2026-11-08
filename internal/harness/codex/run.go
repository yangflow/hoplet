package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// writeTimeout bounds the writes the read loop makes by itself, so a stuck
// transport cannot stall it.
const writeTimeout = 10 * time.Second

// maxOutput bounds the command output kept for one tool call.
const maxOutput = 1 << 20

// refusedNotice tells the user Codex asked for something this adapter
// cannot put to them.
const refusedNotice = "Codex asked for something Hoplet cannot show, so it was refused"

// methodSettings changes a thread's settings.
const methodSettings = "thread/settings/update"

// requestKind says what a response answers.
type requestKind int

const (
	reqInitialize requestKind = iota
	reqThread
	reqTurn
	reqTurnsList
	reqChecked // nothing to do with the result, but a refusal is shown
	reqOther
)

type run struct {
	io       harness.Stdio
	events   chan harness.Event
	done     chan struct{} // closed by Close
	finished chan struct{} // closed when loop has returned
	ready    chan struct{} // closed once the thread is known, or never will be
	located  chan struct{} // closed once a reattached run knows its turn
	once     sync.Once
	spec     harness.Spec
	params   threadParams
	attached bool // connected to a running server (the daemon restarted)
	prefix   string

	// Owned by loop.
	inTurn  bool
	outputs map[string][]byte // command output so far, per item
	last    tokenCounts       // the session's totals last reported, zero until then

	mu          sync.Mutex
	native      string
	turnID      string
	seq         int
	pending     map[string]requestKind
	startErr    error
	err         error
	plan        bool
	model       string
	effort      proto.Effort
	baseEffort  string                     // the effort Codex chose for the thread
	closed      map[string]bool            // server requests answered or withdrawn, by raw ID
	permissions map[string]requestedAccess // permission approvals open, by approval ID
	questions   map[string]*questionSet    // questions open, by question ID
}

// requestedAccess is a permission approval waiting for its answer.
type requestedAccess struct {
	rpc     json.RawMessage
	profile json.RawMessage
}

// questionSet is one requestUserInput call: Codex takes all its answers at
// once, in a single reply.
type questionSet struct {
	rpc     json.RawMessage
	native  map[string]string // question ID to Codex's ID, for those not yet answered
	answers map[string]userInputAnswer
}

func newRun(io harness.Stdio, spec harness.Spec) (*run, error) {
	m, err := nativeMode(spec.Mode)
	if err != nil {
		return nil, err
	}
	var id [4]byte
	_, _ = rand.Read(id[:]) // never fails
	return &run{
		io: io, events: make(chan harness.Event, 64), done: make(chan struct{}),
		finished: make(chan struct{}), ready: make(chan struct{}), located: make(chan struct{}), spec: spec,
		params: threadParams{
			Cwd: spec.WorkDir, Model: spec.Model,
			ApprovalPolicy: m.approval, Sandbox: m.sandbox, ApprovalsReviewer: m.reviewer,
		},
		attached: spec.Resume == "" && spec.State.Native != "",
		prefix:   "hoplet-" + hex.EncodeToString(id[:]),
		inTurn:   spec.State.InTurn, native: spec.State.Native,
		outputs: map[string][]byte{}, pending: map[string]requestKind{},
		plan: spec.Plan, model: spec.Model, effort: spec.Effort,
		closed: map[string]bool{}, permissions: map[string]requestedAccess{}, questions: map[string]*questionSet{},
	}, nil
}

// start opens the conversation. A running server is already initialized and
// on its thread; all it may need to tell is which turn is under way, since
// the turn's start went by with the previous daemon.
func (r *run) start(ctx context.Context) error {
	if !r.attached {
		close(r.located)
		return r.request(ctx, "initialize", initializeParams{
			ClientInfo:   clientInfo{Name: "hoplet", Title: "Hoplet", Version: "1"},
			Capabilities: clientCapabilities{ExperimentalAPI: true},
		}, reqInitialize)
	}
	close(r.ready)
	if !r.inTurn {
		close(r.located)
		return nil
	}
	return r.request(ctx, "thread/turns/list", turnsListParams{ThreadID: r.spec.State.Native, Limit: 1}, reqTurnsList)
}

// request writes a request whose response the loop will handle.
func (r *run) request(ctx context.Context, method string, params any, kind requestKind) error {
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("codex: %s: %w", method, err)
	}
	r.mu.Lock()
	r.seq++
	id := r.prefix + "-" + strconv.Itoa(r.seq)
	r.pending[id] = kind
	r.mu.Unlock()
	raw, _ := json.Marshal(id) // a string always marshals
	return r.write(ctx, message{ID: raw, Method: method, Params: p})
}

func (r *run) write(ctx context.Context, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("codex: encode %s: %w", m.Method, err)
	}
	if err := r.io.WriteLine(ctx, b); err != nil {
		return fmt.Errorf("codex: write %s: %w", m.Method, err)
	}
	return nil
}

// inLoop runs a write the read loop makes by itself.
func (r *run) inLoop(f func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return f(ctx)
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
			select {
			case r.events <- ev:
			case <-r.done:
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
		r.failStart(errors.New("codex: the app-server exited"))
		return []harness.Event{base}, true
	case harness.Stdout:
		payloads, nativeID := r.translate(l.Data)
		base.Native = nativeID
		if len(payloads) == 0 {
			return []harness.Event{base}, false
		}
		for i, p := range payloads {
			ev := base
			if i > 0 {
				ev.Native = "" // announced once
			}
			if o, ok := p.(withOutput); ok {
				ev.Output, p = o.output, o.ToolCall
			}
			ev.Payload = p
			evs = append(evs, ev)
		}
		return evs, false
	}
	return []harness.Event{base}, false
}

// withOutput is a ToolCall with the output that rides on its event.
type withOutput struct {
	proto.ToolCall
	output []byte
}

func exitMessage(l harness.Line) string {
	if l.Signal != "" {
		return "Codex exited on signal " + l.Signal
	}
	return "Codex exited with code " + strconv.Itoa(l.Code)
}

// translate maps one stdout line. A line that is not JSON, or that this
// adapter does not know, carries nothing: its raw form is kept.
func (r *run) translate(line []byte) (out []proto.Payload, nativeID string) {
	var m message
	if json.Unmarshal(line, &m) != nil {
		return nil, ""
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		return r.serverRequest(m), ""
	case m.Method != "":
		return r.notification(m), ""
	case len(m.ID) > 0:
		return r.response(m)
	}
	return nil, ""
}

// serverRequest puts what the server asks to the user. Anything this adapter
// cannot show is answered with an error at once, so a turn never hangs on a
// question nobody can see.
func (r *run) serverRequest(m message) []proto.Payload {
	var out []proto.Payload
	switch m.Method {
	case methodCommandApproval, methodFileApproval, methodPermissionApproval:
		out = r.approval(m)
	case methodUserInput:
		out = r.userInput(m)
	}
	if out != nil {
		return out
	}
	reply := message{ID: m.ID, Error: &rpcError{Code: -32601, Message: "not supported by this client"}}
	if err := r.inLoop(func(ctx context.Context) error { return r.write(ctx, reply) }); err != nil {
		return []proto.Payload{proto.Error{Message: err.Error()}}
	}
	return []proto.Payload{proto.Error{Message: refusedNotice}}
}

// response handles the answer to one of this run's requests. One from
// before a daemon restart matches nothing and is ignored.
func (r *run) response(m message) ([]proto.Payload, string) {
	var id string
	if json.Unmarshal(m.ID, &id) != nil {
		return nil, ""
	}
	r.mu.Lock()
	kind, ok := r.pending[id]
	delete(r.pending, id)
	r.mu.Unlock()
	if !ok {
		return nil, ""
	}
	switch kind {
	case reqInitialize:
		return r.initialized(m), ""
	case reqThread:
		return r.threadOpened(m)
	case reqTurn:
		return r.turnAccepted(m), ""
	case reqTurnsList:
		r.turnFound(m)
	case reqChecked:
		if m.Error != nil {
			return []proto.Payload{proto.Error{Message: m.Error.Message}}, ""
		}
	}
	return nil, ""
}

func (r *run) initialized(m message) []proto.Payload {
	if m.Error != nil {
		return r.startFailed("initialize: " + m.Error.Message)
	}
	method, params := "thread/start", r.params
	params.Ephemeral = r.spec.Ephemeral
	if r.spec.Resume != "" {
		method = "thread/resume"
		params.ThreadID, params.ExcludeTurns = r.spec.Resume, true
		params.Ephemeral = false
	}
	err := r.inLoop(func(ctx context.Context) error {
		if err := r.write(ctx, message{Method: "initialized"}); err != nil {
			return err
		}
		return r.request(ctx, method, params, reqThread)
	})
	if err != nil {
		return r.startFailed(err.Error())
	}
	return nil
}

func (r *run) threadOpened(m message) ([]proto.Payload, string) {
	if m.Error != nil {
		return r.startFailed("open thread: " + m.Error.Message), ""
	}
	var res threadResult
	if err := json.Unmarshal(m.Result, &res); err != nil || res.Thread.ID == "" {
		return r.startFailed("open thread: unexpected response"), ""
	}
	r.mu.Lock()
	r.native = res.Thread.ID
	if res.Model != "" {
		r.model = res.Model
	}
	r.baseEffort = res.ReasoningEffort
	settings := r.startSettings()
	r.mu.Unlock()
	var out []proto.Payload
	if settings != nil { // written before Send can be, so the first turn has them
		err := r.inLoop(func(ctx context.Context) error {
			return r.request(ctx, methodSettings, settings, reqChecked)
		})
		if err != nil {
			out = append(out, proto.Error{Message: err.Error()})
		}
	}
	close(r.ready)
	if res.Model != "" {
		out = append(out, proto.ModelChanged{Model: res.Model})
	}
	return out, res.Thread.ID
}

// startSettings are the session's settings thread/start and thread/resume
// cannot carry; nil when there are none. A resumed thread is told its plan
// state either way, so one left in plan mode cannot stay there. Call with
// r.mu held.
func (r *run) startSettings() *threadSettingsParams {
	p := threadSettingsParams{ThreadID: r.native, Effort: string(r.effort)}
	if r.plan || r.spec.Resume != "" {
		p.CollaborationMode = r.collaboration()
	}
	if p.Effort == "" && p.CollaborationMode == nil {
		return nil
	}
	return &p
}

// collaboration is the collaboration mode the settings mean. Call with r.mu
// held.
func (r *run) collaboration() *collaborationMode {
	mode := collabDefault
	if r.plan {
		mode = collabPlan
	}
	c := &collaborationMode{Mode: mode, Settings: collaborationSettings{Model: r.model}}
	if e := r.currentEffort(); e != "" {
		c.Settings.ReasoningEffort = &e
	}
	return c
}

// currentEffort is the effort in force: the session's, or Codex's own. Call
// with r.mu held.
func (r *run) currentEffort() string {
	if r.effort != "" {
		return string(r.effort)
	}
	return r.baseEffort
}

// startFailed makes every command fail with why the thread could not open.
func (r *run) startFailed(why string) []proto.Payload {
	r.failStart(errors.New("codex: " + why))
	return []proto.Payload{proto.Error{Message: "Codex could not start: " + why}}
}

func (r *run) failStart(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.native != "" || r.startErr != nil {
		return
	}
	r.startErr = err
	close(r.ready)
}

// turnAccepted handles turn/start's response. A refusal has no turn/started
// or turn/completed coming, so it is the turn's whole life.
func (r *run) turnAccepted(m message) []proto.Payload {
	if m.Error == nil {
		return nil
	}
	if r.inTurn {
		return []proto.Payload{proto.Error{Message: m.Error.Message}}
	}
	return []proto.Payload{proto.TurnStarted{}, proto.TurnEnded{Reason: proto.TurnFailed, Message: m.Error.Message}}
}

// turnFound remembers the turn under way found after reattaching, unless the
// turn has moved on since the question was asked.
func (r *run) turnFound(m message) {
	defer close(r.located)
	var res turnsListResult
	if m.Error != nil || json.Unmarshal(m.Result, &res) != nil || len(res.Data) == 0 || res.Data[0].Status != statusInProgress {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnID == "" && r.inTurn {
		r.turnID = res.Data[0].ID
	}
}

func (r *run) notification(m message) []proto.Payload {
	var n notification
	if json.Unmarshal(m.Params, &n) != nil {
		return nil
	}
	r.mu.Lock()
	own := r.native == "" || n.ThreadID == "" || n.ThreadID == r.native
	r.mu.Unlock()
	if !own {
		return nil // a thread Codex's own subagents run on
	}
	switch m.Method {
	case "turn/started":
		if n.Turn == nil {
			return nil
		}
		r.setTurn(n.Turn.ID, true)
		return []proto.Payload{proto.TurnStarted{}}
	case "turn/completed":
		if n.Turn == nil {
			return nil
		}
		r.setTurn("", false)
		return []proto.Payload{turnEnded(*n.Turn)}
	case "item/started":
		if n.Item != nil {
			return itemStarted(*n.Item)
		}
	case "item/completed":
		if n.Item != nil {
			return r.itemCompleted(*n.Item)
		}
	case "serverRequest/resolved":
		r.requestClosed(n.RequestID)
	case "item/agentMessage/delta", "item/plan/delta":
		return []proto.Payload{proto.MessageDelta{MessageID: n.ItemID, Text: n.Delta}}
	case "item/commandExecution/outputDelta":
		if len(r.outputs[n.ItemID]) < maxOutput {
			r.outputs[n.ItemID] = append(r.outputs[n.ItemID], n.Delta...)
		}
	case "thread/tokenUsage/updated":
		if n.TokenUsage != nil {
			return r.usage(*n.TokenUsage)
		}
	case "error":
		if n.Error != nil && !n.WillRetry {
			return []proto.Payload{proto.Error{Message: n.Error.Message}}
		}
	}
	return nil
}

func (r *run) setTurn(id string, in bool) {
	r.inTurn = in
	r.mu.Lock()
	r.turnID = id
	r.mu.Unlock()
}

func turnEnded(t turn) proto.TurnEnded {
	end := proto.TurnEnded{Reason: proto.TurnFailed}
	switch t.Status {
	case statusCompleted:
		end.Reason = proto.TurnCompleted
	case statusInterrupted:
		end.Reason = proto.TurnInterrupted
	}
	if t.Error != nil {
		end.Message = t.Error.Message
	}
	return end
}

func itemStarted(it item) []proto.Payload {
	if tc, ok := toolCall(it); ok {
		tc.Status = proto.ToolRunning
		return []proto.Payload{tc}
	}
	return nil
}

func (r *run) itemCompleted(it item) []proto.Payload {
	switch it.Type {
	case itemAgentMessage, itemPlan:
		return []proto.Payload{proto.Message{ID: it.ID, Role: proto.RoleAssistant, Text: it.Text}}
	case itemReasoning:
		text := strings.Join(append(append([]string(nil), it.Summary...), it.Content...), "\n\n")
		if text == "" {
			return nil
		}
		return []proto.Payload{proto.Thinking{Text: text}}
	}
	tc, ok := toolCall(it)
	if !ok {
		return nil
	}
	tc.Status = finalStatus(it)
	out := []proto.Payload{withOutput{ToolCall: tc, output: r.takeOutput(it)}}
	if it.Type == itemFileChange && tc.Status == proto.ToolDone {
		for _, c := range it.Changes {
			out = append(out, proto.FileChange{Path: c.Path, Op: fileOp(c.Kind.Type)})
		}
	}
	return out
}

func finalStatus(it item) proto.ToolStatus {
	if it.ExitCode != nil && *it.ExitCode != 0 {
		return proto.ToolFailed
	}
	if it.Status == "" || it.Status == statusCompleted {
		return proto.ToolDone
	}
	return proto.ToolFailed
}

func fileOp(kind string) proto.FileOp {
	switch kind {
	case "add":
		return proto.FileCreated
	case "delete":
		return proto.FileDeleted
	}
	return proto.FileModified
}

// takeOutput is a finished tool's output, if it reported one.
func (r *run) takeOutput(it item) []byte {
	streamed := r.outputs[it.ID]
	delete(r.outputs, it.ID)
	if it.AggregatedOutput != nil {
		return []byte(*it.AggregatedOutput)
	}
	if it.Result != nil {
		var parts []string
		for _, c := range it.Result.Content {
			parts = append(parts, c.Text)
		}
		return []byte(strings.Join(parts, "\n"))
	}
	return streamed
}

// toolCall maps an item that is a tool call; its Status is left to the
// caller. Item types not listed here stay raw lines.
func toolCall(it item) (proto.ToolCall, bool) {
	tc := proto.ToolCall{ID: it.ID, Kind: proto.ToolOther}
	switch it.Type {
	case itemCommand:
		tc.Kind, tc.Name, tc.Summary = proto.ToolExec, "Shell", it.Command
		if len(it.CommandActions) == 1 && it.CommandActions[0].Command != "" {
			tc.Summary = it.CommandActions[0].Command
		}
	case itemFileChange:
		paths := make([]string, 0, len(it.Changes))
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
		}
		tc.Kind, tc.Name, tc.Summary = proto.ToolEdit, "Edit", strings.Join(paths, ", ")
	case itemMCP:
		tc.Name, tc.Summary = it.Tool, it.Server+": "+it.Tool
	case itemDynamic:
		tc.Name, tc.Summary = it.Tool, it.Tool
	case itemCollab:
		tc.Name, tc.Summary = "Agent", it.Tool
	case itemWebSearch:
		tc.Kind, tc.Name, tc.Summary = proto.ToolWeb, "Search", it.Query
	default:
		return proto.ToolCall{}, false
	}
	return tc, true
}

// usage reports the context's fill and the tokens added since the last
// report. The first report after attaching has only its last request to go
// by.
func (r *run) usage(u tokenUsage) []proto.Payload {
	out := []proto.Payload{proto.ContextUsage{Used: u.Last.TotalTokens, Window: u.ModelContextWindow}}
	in, ou := u.Last.InputTokens, u.Last.OutputTokens
	if r.last.TotalTokens > 0 {
		in, ou = u.Total.InputTokens-r.last.InputTokens, u.Total.OutputTokens-r.last.OutputTokens
	}
	if u.Total.TotalTokens != r.last.TotalTokens && (in > 0 || ou > 0) {
		out = append(out, proto.Usage{InputTokens: in, OutputTokens: ou})
	}
	r.last = u.Total
	return out
}

// thread waits until the thread is known and returns its ID.
func (r *run) thread(ctx context.Context) (string, error) {
	select {
	case <-r.ready:
	case <-ctx.Done():
		return "", fmt.Errorf("codex: %w", ctx.Err())
	case <-r.finished:
		return "", errors.New("codex: the run has ended")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return "", r.startErr
	}
	return r.native, nil
}

// Send starts a turn.
func (r *run) Send(ctx context.Context, text string) error {
	id, err := r.thread(ctx)
	if err != nil {
		return err
	}
	return r.request(ctx, "turn/start", turnStartParams{ThreadID: id, Input: []textInput{{Type: "text", Text: text}}}, reqTurn)
}

// currentTurn waits until the thread and its running turn are known.
func (r *run) currentTurn(ctx context.Context) (thread, turn string, err error) {
	thread, err = r.thread(ctx)
	if err != nil {
		return "", "", err
	}
	select {
	case <-r.located:
	case <-ctx.Done():
		return "", "", fmt.Errorf("codex: %w", ctx.Err())
	case <-r.finished:
		return "", "", errors.New("codex: the run has ended")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnID == "" {
		return "", "", errors.New("codex: no turn under way")
	}
	return thread, r.turnID, nil
}

// Interrupt stops the turn under way; the turn's end arrives as an event.
func (r *run) Interrupt(ctx context.Context) error {
	id, t, err := r.currentTurn(ctx)
	if err != nil {
		return err
	}
	return r.request(ctx, "turn/interrupt", turnInterruptParams{ThreadID: id, TurnID: t}, reqOther)
}

// Steer adds text to the turn under way.
func (r *run) Steer(ctx context.Context, text string) error {
	id, t, err := r.currentTurn(ctx)
	if err != nil {
		return err
	}
	return r.request(ctx, "turn/steer", turnSteerParams{ThreadID: id, ExpectedTurnID: t, Input: []textInput{{Type: "text", Text: text}}}, reqChecked)
}

// update changes the thread's settings; they apply from the next turn.
// change runs with r.mu held and fills in what changes.
func (r *run) update(ctx context.Context, change func(*threadSettingsParams)) error {
	id, err := r.thread(ctx)
	if err != nil {
		return err
	}
	p := threadSettingsParams{ThreadID: id}
	r.mu.Lock()
	change(&p)
	r.mu.Unlock()
	return r.request(ctx, methodSettings, p, reqChecked)
}

// SetMode changes the permission mode. The sandbox is sent only when it
// differs, so a change between the modes that share one keeps what the
// user's configuration says about it.
func (r *run) SetMode(ctx context.Context, mode proto.PermissionMode) error {
	m, err := nativeMode(mode)
	if err != nil {
		return err
	}
	return r.update(ctx, func(p *threadSettingsParams) {
		p.ApprovalPolicy, p.ApprovalsReviewer = m.approval, m.reviewer
		if m.sandbox != r.params.Sandbox {
			p.SandboxPolicy = &sandboxPolicy{Type: nativeSandboxPolicy(m.sandbox)}
			r.params.Sandbox = m.sandbox
		}
	})
}

func nativeSandboxPolicy(sandbox string) string {
	if sandbox == "danger-full-access" {
		return "dangerFullAccess"
	}
	return "workspaceWrite"
}

// SetPlan turns plan mode on or off. It is independent of the permission
// mode, which stays as it is.
func (r *run) SetPlan(ctx context.Context, on bool) error {
	return r.update(ctx, func(p *threadSettingsParams) {
		r.plan = on
		p.CollaborationMode = r.collaboration()
	})
}

// SetEffort changes the thinking effort; empty goes back to the one Codex
// chose for the thread.
func (r *run) SetEffort(ctx context.Context, effort proto.Effort) error {
	if effort != "" && !caps().HasEffort(effort) {
		return harness.ErrUnsupported
	}
	return r.update(ctx, func(p *threadSettingsParams) {
		r.effort = effort
		p.Effort = r.currentEffort()
		if r.plan {
			p.CollaborationMode = r.collaboration()
		}
	})
}

// SetFast is not supported.
func (r *run) SetFast(context.Context, bool) error { return harness.ErrUnsupported }

// SetUltracode is not supported.
func (r *run) SetUltracode(context.Context, bool) error { return harness.ErrUnsupported }

// SetModel changes the model.
func (r *run) SetModel(ctx context.Context, model, _ string) error {
	return r.update(ctx, func(p *threadSettingsParams) {
		r.model = model
		p.Model = model
		if r.plan {
			p.CollaborationMode = r.collaboration()
		}
	})
}

// Events delivers the normalized events.
func (r *run) Events() <-chan harness.Event { return r.events }

// NativeSession is the Codex thread ID, once known.
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
