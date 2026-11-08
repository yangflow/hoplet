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
// cannot put to them yet.
const refusedNotice = "Codex asked for an approval or an answer Hoplet cannot show yet, so it was refused"

// requestKind says what a response answers.
type requestKind int

const (
	reqInitialize requestKind = iota
	reqThread
	reqTurn
	reqTurnsList
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

	mu       sync.Mutex
	native   string
	turnID   string
	seq      int
	pending  map[string]requestKind
	startErr error
	err      error
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

// serverRequest refuses what the server asks of the user: an approval is
// declined, anything else answered with an error, so a turn never hangs on
// a question nobody can see.
func (r *run) serverRequest(m message) []proto.Payload {
	reply := message{ID: m.ID, Error: &rpcError{Code: -32601, Message: "not supported by this client"}}
	if strings.HasSuffix(m.Method, "/requestApproval") {
		reply = message{ID: m.ID, Result: json.RawMessage(`{"decision":"decline"}`)}
	}
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
	r.mu.Unlock()
	close(r.ready)
	if res.Model == "" {
		return nil, res.Thread.ID
	}
	return []proto.Payload{proto.ModelChanged{Model: res.Model}}, res.Thread.ID
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
	case "item/agentMessage/delta":
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
	case itemAgentMessage:
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

// Interrupt stops the turn under way; the turn's end arrives as an event.
func (r *run) Interrupt(ctx context.Context) error {
	id, err := r.thread(ctx)
	if err != nil {
		return err
	}
	select {
	case <-r.located:
	case <-ctx.Done():
		return fmt.Errorf("codex: %w", ctx.Err())
	case <-r.finished:
		return errors.New("codex: the run has ended")
	}
	r.mu.Lock()
	t := r.turnID
	r.mu.Unlock()
	if t == "" {
		return errors.New("codex: no turn to interrupt")
	}
	return r.request(ctx, "turn/interrupt", turnInterruptParams{ThreadID: id, TurnID: t}, reqOther)
}

// Steer is not supported yet.
func (r *run) Steer(context.Context, string) error { return harness.ErrUnsupported }

// Respond is not supported yet.
func (r *run) Respond(context.Context, string, string) error { return harness.ErrUnsupported }

// Answer is not supported yet.
func (r *run) Answer(context.Context, string, string) error { return harness.ErrUnsupported }

// SetMode is not supported yet.
func (r *run) SetMode(context.Context, proto.PermissionMode) error { return harness.ErrUnsupported }

// SetPlan is not supported yet.
func (r *run) SetPlan(context.Context, bool) error { return harness.ErrUnsupported }

// SetEffort is not supported yet.
func (r *run) SetEffort(context.Context, proto.Effort) error { return harness.ErrUnsupported }

// SetFast is not supported.
func (r *run) SetFast(context.Context, bool) error { return harness.ErrUnsupported }

// SetUltracode is not supported.
func (r *run) SetUltracode(context.Context, bool) error { return harness.ErrUnsupported }

// SetModel is not supported yet.
func (r *run) SetModel(context.Context, string, string) error { return harness.ErrUnsupported }

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
