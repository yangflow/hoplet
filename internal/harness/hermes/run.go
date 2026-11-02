package hermes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	queue     []*turnRec      // the turns in admission order; the first is shown
	segment   int             // the assistant message being streamed in this turn
	text      strings.Builder // its text so far
	tools     map[string]tool
	model     string // the model Hermes last reported
	lastTool  string
	late      map[string]bool // turns ended by a newer turn whose receipt is still due
	retries   map[string]int  // receipt retries per admission
	toolsSeen bool

	prefix string
	n      int // requests written, under mu; a request's seq

	// receiptDelay is the wait before a receipt is asked for again.
	receiptDelay time.Duration
	// reattach asks for a snapshot as the loop starts: the run attached to
	// a running bridge (the daemon restarted), and the snapshot says where
	// the store's open turn stands, or finds a turn the store missed.
	reattach bool
	// holding is true until that snapshot answers: receipts the old daemon
	// asked for that arrive while its open turn is not known yet are held
	// in held, and that snapshot alone settles the open turn.
	holding bool
	held    []admitted
	// orderMu orders user messages: it is held from the decision to queue
	// or write a message through the write, and while queued ones are
	// released. Lock order: orderMu before mu; orderMu is never taken while
	// holding mu.
	orderMu sync.Mutex

	mu         sync.Mutex
	native     string
	err        error
	generation int64
	revision   int64
	approvals  map[string]*approval
	requests   map[string]*request
	epoch      int            // counts the bridge's connections
	stale      map[string]int // requests lost with a connection (and its epoch): their answers are dropped
	timers     map[*time.Timer]struct{}
	// attached is true once this connection has a snapshot of the session:
	// the gateway refuses anything else before that, so requests wait.
	attached bool
	waiting  []*request
	policy   string // the edit policy Hermes has
	// attachErr is why Hermes refused to attach the session on this
	// connection; while it stands, commands fail with it.
	attachErr error
	// Session settings go one at a time (each changes the revision the
	// next must name) and never mid-turn (Hermes refuses them): they queue,
	// the latest per operation.
	mutations []mutation
	mutating  bool
	// afterSettings are messages sent while settings wait: they go once
	// the settings are applied, in the order the user gave them.
	afterSettings []*request
	// busy is true while turns are queued, for the command methods.
	busy bool
}

func newRun(io harness.Stdio, spec harness.Spec) *run {
	native := spec.Resume
	if native == "" {
		native = spec.State.Native
	}
	r := &run{
		io: io, spec: spec, events: make(chan harness.Event, 64), done: make(chan struct{}), finished: make(chan struct{}),
		busy: spec.State.InTurn, tools: map[string]tool{},
		prefix: "hoplet-" + rand.Text()[:8], native: native,
		approvals: map[string]*approval{}, requests: map[string]*request{},
		late: map[string]bool{}, retries: map[string]int{}, stale: map[string]int{}, timers: map[*time.Timer]struct{}{}, receiptDelay: receiptRetryDelay,
		policy: editAsk,
	}
	if spec.State.InTurn {
		// A turn is open that this run has not seen start: the first
		// receipt, event or snapshot names it.
		r.queue = []*turnRec{{started: true, placeholder: true}}
	}
	if native != "" {
		// An attached session already has the mode Hoplet set.
		r.policy, _ = editPolicy(spec.Mode)
	}
	// Attaching to a bridge that is already running (the daemon restarted)
	// finds its connection attached to the session; a new bridge attaches
	// when it connects.
	r.attached = native != "" && spec.Resume == ""
	r.reattach = r.attached
	r.holding = r.reattach
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
	if r.reattach {
		// Its answer comes after the lines the shim replays, so the
		// receipts the old daemon asked for are read first.
		r.attachSession()
	}
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
		if len(r.queue) > 0 {
			r.queue = nil
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
		// A new connection: what was in flight on the old one is lost, and
		// the session is attached again.
		r.dropInFlight()
		r.mu.Lock()
		r.attachErr = nil
		r.mu.Unlock()
		// Receipts asked for on the old connection will not be answered.
		clear(r.late)
		r.mu.Lock()
		r.epoch++
		for id, e := range r.stale {
			if e < r.epoch-1 { // lost before the previous connection
				delete(r.stale, id)
			}
		}
		r.mu.Unlock()
		r.attachSession()
		return nil, ""
	case f.Bridge == BridgeDisconnected:
		r.dropInFlight()
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

func (r *run) onResponse(f frame) (out []proto.Payload, native string) {
	id := strings.Trim(string(f.ID), `"`)
	r.mu.Lock()
	if _, lost := r.stale[id]; lost {
		// Written on a connection that is gone: it was sent again or given up.
		delete(r.stale, id)
		r.mu.Unlock()
		return nil, ""
	}
	req := r.requests[id]
	delete(r.requests, id)
	r.mu.Unlock()
	if e := f.rpcErr(); e != nil {
		if req == nil {
			// An earlier daemon's request: what came of it shows in the
			// events and the next snapshot.
			return nil, ""
		}
		out, resent := r.onError(req, e)
		if !resent && (req.method == "session.create" || req.method == "session.resume") {
			out = append(out, r.attachRefused(e)...)
			if r.holding {
				// No snapshot is coming: settle as if nothing ran.
				out = r.settleHeld(out, false)
				out = r.advance(out)
			}
		}
		return out, ""
	}
	method := ""
	if req != nil {
		method = req.method
	}
	switch method {
	case "session.create", "session.resume":
		return r.onSnapshot(f.Result, method == "session.create", true)
	case "prompt.submit":
		// Its admission may be a queued turn's: not the running turn's.
		return nil, ""
	case "prompt.receipt":
		var a admitted
		if json.Unmarshal(f.Result, &a) != nil {
			a = admitted{Status: "unknown"}
		}
		if adm, _ := req.params["admission_id"].(string); adm != "" {
			a.Admission = adm
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
		return r.onSnapshot(result, false, false)
	case probe.Admission != "" && probe.Status == "terminal" && probe.Result != nil:
		var a admitted
		_ = json.Unmarshal(result, &a)
		return r.onOrphanReceipt(a), ""
	}
	return nil, ""
}

func (r *run) onEvent(ev event) []proto.Payload {
	r.mu.Lock()
	native := r.native
	if ev.Generation != nil {
		r.generation = *ev.Generation
	}
	r.mu.Unlock()
	if ev.Session != "" && native != "" && ev.Session != native {
		return nil
	}
	if ev.Type == "message.start" {
		return r.onStart(ev.Admission)
	}
	// Approvals and errors are kept with their turn too, but one of no
	// known turn is still shown: it may wait for an answer.
	loose := false
	switch ev.Type {
	case "message.delta", "message.interim", "message.complete", "tool.start", "tool.complete":
	case "approval.request", "error":
		loose = true
	default:
		return r.translateEvent(ev)
	}
	// An event of a turn: the first turn's is translated, a later one's
	// kept until that turn comes first, any other is dropped.
	adopted := false
	if h := r.unadopted(); h != nil && !h.ended && ev.Admission != "" && r.find(ev.Admission) == nil {
		h.admission = ev.Admission // reattached to a running bridge: the running turn's content
		h.seen, adopted = true, true
	}
	h := r.head()
	switch {
	case h != nil && (ev.Admission == "" || ev.Admission == h.admission):
		out := r.translateEvent(ev)
		if adopted {
			out = r.adoptHeld(out, ev.Admission)
		}
		return out
	case h != nil && r.find(ev.Admission) != nil:
		r.find(ev.Admission).buffer(ev)
		return nil
	case loose:
		return r.translateEvent(ev)
	}
	return nil
}

func (r *run) translateEvent(ev event) []proto.Payload {
	switch ev.Type {
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
		if a != nil && a.answered {
			return nil
		}
		// Settled without an answer from Hoplet: in another Hermes client,
		// or by Hermes's own timeout. (An approval this run does not know
		// was raised before Hoplet reattached; the store ignores one that
		// is not pending.)
		return []proto.Payload{proto.ApprovalResolved{ID: p.ID, Outcome: proto.OutcomeExpired, DecidedBy: "hermes"}}
	case "message.complete":
		var p completePayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return nil
		}
		h := r.head()
		if h == nil || h.complete != nil || (p.Admission != "" && p.Admission != h.admission) {
			return nil // not the running turn's
		}
		var out []proto.Payload
		if p.Text != "" {
			out = append(out, proto.Message{ID: r.currentID(), Role: proto.RoleAssistant, Text: p.Text})
		}
		h.complete = &p
		// The turn ends with its receipt, which advance asks for.
		return r.advance(out)
	case "error":
		var p messagePayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.Message == "" {
			return nil
		}
		return []proto.Payload{proto.Error{Message: p.Message}}
	case "session.usage":
		// Each turn's usage names the model that ran it.
		var p usagePayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.Model == "" || p.Model == r.model {
			return nil
		}
		r.model = p.Model
		return []proto.Payload{proto.ModelChanged{Model: p.Model, Provider: p.Provider}}
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

func (r *run) currentID() string { return r.messageID(r.turnID()) }

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

// Send sends a user message; Hermes queues it if a turn is running.
func (r *run) Send(ctx context.Context, text string) error {
	if err := r.refused(); err != nil {
		return err
	}
	return r.submit(ctx, text)
}

// Steer adds text to the running turn after its current tool calls.
func (r *run) Steer(ctx context.Context, text string) error {
	if err := r.refused(); err != nil {
		return err
	}
	native, gen := r.session()
	return r.call(ctx, "session.steer", map[string]any{"session_id": native, "execution_generation": gen, "text": text})
}

// Interrupt ends the running turn.
func (r *run) Interrupt(ctx context.Context) error {
	if err := r.refused(); err != nil {
		return err
	}
	native, gen := r.session()
	return r.call(ctx, "session.interrupt", map[string]any{"session_id": native, "execution_generation": gen})
}

// Respond answers an approval with one of Hermes's choices.
func (r *run) Respond(ctx context.Context, approvalID, optionID string) error {
	if err := r.refused(); err != nil {
		return err
	}
	native, gen := r.session()
	// Marked before the write, so an event the answer provokes finds it;
	// taken back if the answer is not taken.
	r.mu.Lock()
	a := r.approvals[approvalID]
	if a != nil {
		gen = a.generation
		a.answered = true
	}
	r.mu.Unlock()
	err := r.call(ctx, "approval.respond", map[string]any{
		"session_id": native, "execution_generation": gen, "prompt_id": approvalID, "choice": optionID,
	})
	if err != nil && a != nil {
		r.mu.Lock()
		a.answered = false
		r.mu.Unlock()
	}
	return err
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
	if err := r.refused(); err != nil {
		return err
	}
	native, _ := r.session()
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
	if err := r.refused(); err != nil {
		return err
	}
	return r.mutate(ctx, "reasoning", map[string]any{"effort": string(effort)})
}

// SetFast turns Hermes's fast service tier on or off from the next turn.
func (r *run) SetFast(ctx context.Context, on bool) error {
	if err := r.refused(); err != nil {
		return err
	}
	native, _ := r.session()
	value := "normal"
	if on {
		value = "fast"
	}
	return r.call(ctx, "config.set", map[string]any{"session_id": native, "key": "service_tier", "value": value})
}

// SetModel changes the model; Hermes applies it from the next turn. The
// provider is one of model.options' provider slugs.
func (r *run) SetModel(ctx context.Context, model, provider string) error {
	if err := r.refused(); err != nil {
		return err
	}
	payload := map[string]any{"model": model}
	if provider != "" {
		payload["provider"] = provider
	}
	return r.mutate(ctx, "model", payload)
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
		r.mu.Lock()
		for t := range r.timers {
			t.Stop()
		}
		clear(r.timers)
		r.mu.Unlock()
		err = r.io.Close()
		<-r.finished
	})
	return err
}
