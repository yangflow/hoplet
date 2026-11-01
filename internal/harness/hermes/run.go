package hermes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
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

// receiptRetries is how often a receipt is asked for again while Hermes
// still reports the turn as queued or started, receiptRetryDelay apart.
const (
	receiptRetries    = 3
	receiptRetryDelay = time.Second
)

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
	seq     int  // its place in the order of writes
	resent  bool // a message written before a drop, to be written again
}

// pendingEnd is a turn whose message.complete arrived and whose receipt is
// awaited.
type pendingEnd struct {
	admission string
	complete  *completePayload
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
	inTurn   bool
	turn     string          // the admission of the running turn
	segment  int             // the assistant message being streamed in this turn
	text     strings.Builder // its text so far
	tools    map[string]tool
	lastTool string
	ending   *pendingEnd     // a turn whose message.complete arrived, awaiting its receipt
	late     map[string]bool // turns ended by a newer turn whose receipt is still due
	retries  map[string]int  // receipt retries per admission
	recover  string          // the admission whose receipt a reconnecting run asked for
	// deferred is a turn that started while Hoplet was disconnected, found
	// running by a snapshot while the turn before it still awaits its end:
	// its events wait in deferredEvents until that end is known, so the
	// turns come out in order. The buffer is bounded; past the bound only
	// text deltas are dropped (the final message carries the whole text).
	deferred       string
	deferredEvents []event
	toolsSeen      bool

	prefix string
	n      int // requests written, under mu

	// receiptDelay is the wait before a receipt is asked for again.
	receiptDelay time.Duration
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
	writes     int            // counts requests written
	stale      map[string]int // requests lost with a connection (and its epoch): their answers are dropped
	timers     map[*time.Timer]struct{}
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
		late: map[string]bool{}, retries: map[string]int{}, stale: map[string]int{}, timers: map[*time.Timer]struct{}{}, receiptDelay: receiptRetryDelay,
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
			r.ending, r.turn, r.recover = nil, "", ""
			r.deferred, r.deferredEvents = "", nil
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
		// A new connection: what was in flight on the old one is lost.
		r.dropInFlight()
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
		return r.onError(req, e), ""
	}
	method := ""
	if req != nil {
		method = req.method
	}
	switch method {
	case "session.create", "session.resume":
		return r.onSnapshot(f.Result, method == "session.create")
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

// dropInFlight forgets the requests written on a connection that is gone:
// the bridge never delivered them, or their answers will not come. Settings
// go back to their queue and messages, approval answers and config changes
// wait for the next snapshot; a request that targets the lost turn (an
// interrupt, a steer, a receipt) is dropped, as the snapshot says where the
// turn stands. Answers to them are ignored.
func (r *run) dropInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attached, r.mutating = false, false
	type lost struct {
		id  string
		req *request
	}
	var dropped []lost
	for id, req := range r.requests {
		if req.method == "session.create" || req.method == "session.resume" {
			delete(r.requests, id) // attachSession asks again on connecting
			continue
		}
		dropped = append(dropped, lost{id, req})
	}
	slices.SortFunc(dropped, func(a, b lost) int { return a.req.seq - b.req.seq })
	for _, l := range dropped {
		delete(r.requests, l.id)
		r.stale[l.id] = r.epoch
		switch l.req.method {
		case "session.mutate":
			operation, _ := l.req.params["operation"].(string)
			payload, _ := l.req.params["payload"].(map[string]any)
			if !slices.ContainsFunc(r.mutations, func(m mutation) bool { return m.operation == operation }) {
				r.mutations = slices.Insert(r.mutations, 0, mutation{operation: operation, payload: payload, retried: l.req.retried})
			}
		case "prompt.submit", "approval.respond", "config.set":
			r.waiting = append(r.waiting, &request{method: l.req.method, params: l.req.params, retried: l.req.retried, resent: true})
		}
	}
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
	}
	return nil, ""
}

func (r *run) onSnapshot(result json.RawMessage, created bool) (out []proto.Payload, native string) {
	var s snapshot
	if json.Unmarshal(result, &s) != nil || s.Session == "" {
		return []proto.Payload{proto.Error{Message: "Hermes answered without a session"}}, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	// Messages that waited go out in order, before any a caller sends now.
	r.orderMu.Lock()
	r.mu.Lock()
	known := r.native
	r.native = s.Session
	r.revision, r.generation = s.Revision, s.Generation
	r.attached = true
	waiting := r.waiting
	r.waiting = nil
	started := ""
	for _, p := range s.Pending {
		if p.Status == "started" {
			started = p.Admission
		}
	}
	// Approvals still open in Hermes are known again (their answers need
	// their generation); those it no longer lists were settled while
	// Hoplet was not connected.
	open := map[string]prompt{}
	for _, p := range s.Prompts {
		if p.Kind == "approval" {
			open[p.ID] = p
			if _, ok := r.approvals[p.ID]; !ok {
				r.approvals[p.ID] = &approval{generation: p.Generation}
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(r.approvals)) {
		if _, ok := open[id]; ok {
			continue
		}
		if !r.approvals[id].answered {
			out = append(out, proto.ApprovalResolved{ID: id, Outcome: proto.OutcomeExpired, DecidedBy: "hermes"})
		}
		delete(r.approvals, id)
	}
	r.mu.Unlock()
	if known != s.Session {
		native = s.Session
	}
	// The receipts of turns that ended in favor of a newer one cannot
	// arrive any more; their usage is lost.
	clear(r.late)
	out = append(out, r.meetRunningTurn(s.Running, started)...)
	if created {
		// Hermes takes only "ask" at creation; anything looser is a change.
		policy, _ := editPolicy(r.spec.Mode)
		r.setPolicyLocked(ctx, policy)
	}
	front := 0 // held messages the resent ones went ahead of
	for _, req := range waiting {
		if req.method == "approval.respond" {
			id, _ := req.params["prompt_id"].(string)
			p, ok := open[id]
			if !ok {
				continue // settled meanwhile
			}
			req.params["execution_generation"] = p.Generation
		}
		r.fillSession(req, s.Session)
		switch {
		case req.method == "prompt.submit" && req.resent:
			// Written before the drop, so before everything held since.
			r.holdFirst(ctx, req, &front)
		case req.method == "prompt.submit":
			_ = r.submitLocked(ctx, req)
		default:
			_ = r.send(ctx, req)
		}
	}
	r.orderMu.Unlock()
	r.pumpMutations(ctx)
	switch {
	case r.ending != nil:
		// The turn's end is still owed: its receipt may have been lost.
		_ = r.requestReceipt(ctx, r.ending.admission)
	case r.inTurn && (!s.Running || r.deferred != ""):
		admission := r.turn
		if admission == "" {
			admission = started
		}
		if admission == "" {
			// Nothing names the turn that ended: say so rather than keep
			// it open.
			r.finishTurn()
			return append(out, proto.TurnEnded{Reason: proto.TurnAborted, Message: "the turn ended while Hoplet was not connected"}), native
		}
		// The turn ended while the connection was down: its receipt says
		// how, and carries its final answer.
		r.recover = admission
		_ = r.requestReceipt(ctx, admission)
	}
	return out, native
}

// holdFirst sends a message written before a drop: ahead of the messages
// held for pending settings (all written later), after the resent ones
// already put there (*front of them).
func (r *run) holdFirst(ctx context.Context, req *request, front *int) {
	r.mu.Lock()
	if r.attached && (r.mutating || len(r.mutations) > 0 || len(r.afterSettings) > 0) {
		r.afterSettings = slices.Insert(r.afterSettings, *front, req)
		*front++
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	_ = r.send(ctx, req)
}

// meetRunningTurn reconciles the turn Hermes says is running with the one
// Hoplet has. A turn that started while Hoplet was away is started here; if
// the turn before it is still open, that one ended while away: its end
// comes first, and the new turn's events wait for it (see deferred).
func (r *run) meetRunningTurn(running bool, started string) []proto.Payload {
	cur := r.turn
	if r.ending != nil {
		cur = r.ending.admission
	}
	if !running || started == "" || started == cur {
		return nil
	}
	switch {
	case !r.inTurn:
		r.turn = started
		r.segment, r.toolsSeen = 0, false
		r.text.Reset()
		r.setInTurn(true)
		return []proto.Payload{proto.TurnStarted{}}
	case cur == "":
		r.turn = started
	case r.deferred != started:
		r.deferred, r.deferredEvents = started, nil
	}
	return nil
}

// deferEvent keeps an event of the deferred turn. Past the bound only text
// deltas are dropped: the final message carries the whole text. Its start
// is not kept either, as the turn is started when its time comes.
func (r *run) deferEvent(ev event) {
	const bound = 4096
	if ev.Type == "message.start" || (ev.Type == "message.delta" && len(r.deferredEvents) >= bound) {
		return
	}
	r.deferredEvents = append(r.deferredEvents, ev)
}

// startDeferred starts the turn that waited for the end of the one before
// it, then plays its events; out holds that end.
func (r *run) startDeferred(out []proto.Payload) []proto.Payload {
	if r.deferred == "" {
		return out
	}
	events := r.deferredEvents
	r.turn, r.deferred, r.deferredEvents = r.deferred, "", nil
	r.segment, r.toolsSeen = 0, false
	r.text.Reset()
	out = append(out, proto.TurnStarted{})
	for _, ev := range events {
		out = append(out, r.onEvent(ev)...)
	}
	return out
}

// requestReceipt asks how a turn ended.
func (r *run) requestReceipt(ctx context.Context, admission string) error {
	native, _ := r.session()
	return r.call(ctx, "prompt.receipt", map[string]any{"session_id": native, "admission_id": admission, "include_result": true})
}

// Kinds of turn a receipt can belong to.
const (
	receiptLate     = "late"     // ended by a newer turn: only its usage is left
	receiptEnding   = "ending"   // its message.complete was seen
	receiptRecovery = "recovery" // it ended while Hoplet was away
)

// classify says which pending end a receipt for admission settles, if any.
func (r *run) classify(admission string) string {
	switch {
	case r.late[admission]:
		return receiptLate
	case r.ending != nil && r.ending.admission == admission:
		return receiptEnding
	case r.inTurn && r.ending == nil && (admission == r.recover || admission == r.turn || r.turn == ""):
		return receiptRecovery
	}
	return ""
}

func (r *run) onReceipt(a admitted) []proto.Payload {
	kind := r.classify(a.Admission)
	if kind == "" {
		return nil
	}
	switch a.Status {
	case "terminal":
	case "queued", "started":
		return r.retryReceipt(a.Admission, kind)
	default:
		return r.giveUp(a.Admission, kind, "Hermes has no record of the turn")
	}
	delete(r.retries, a.Admission)
	var out []proto.Payload
	usage := func() {
		if a.Usage != nil {
			out = append(out, proto.Usage{InputTokens: a.Usage.Input, OutputTokens: a.Usage.Output})
		}
	}
	if kind == receiptLate {
		delete(r.late, a.Admission)
		usage()
		return out
	}
	var complete *completePayload
	if r.ending != nil {
		complete = r.ending.complete
	} else if a.Result != nil && a.Result.FinalResponse != "" {
		// Recovered after a lost connection: the answer was never streamed
		// here.
		out = append(out, proto.Message{ID: r.messageID(a.Admission), Role: proto.RoleAssistant, Text: a.Result.FinalResponse})
	}
	usage()
	return r.startDeferred(append(out, r.endTurn(complete, a)))
}

// retryReceipt asks again for a receipt that says the turn has not ended,
// a few times, then ends it without one.
func (r *run) retryReceipt(admission, kind string) []proto.Payload {
	n := r.retries[admission]
	if n >= receiptRetries {
		return r.giveUp(admission, kind, "Hermes did not report how the turn ended")
	}
	r.retries[admission] = n + 1
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	default:
	}
	var t *time.Timer
	t = time.AfterFunc(r.receiptDelay, func() {
		r.mu.Lock()
		delete(r.timers, t)
		r.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		_ = r.requestReceipt(ctx, admission)
	})
	r.timers[t] = struct{}{}
	return nil
}

// giveUp ends a turn whose receipt will not come: from its message.complete
// when there was one (without usage), else as aborted.
func (r *run) giveUp(admission, kind, reason string) []proto.Payload {
	delete(r.retries, admission)
	switch kind {
	case receiptLate:
		delete(r.late, admission)
		return nil
	case receiptEnding:
		return r.startDeferred([]proto.Payload{r.endTurn(r.ending.complete, admitted{})})
	}
	r.finishTurn()
	return r.startDeferred([]proto.Payload{proto.TurnEnded{Reason: proto.TurnAborted, Message: reason}})
}

// endTurn closes the running turn and says how it ended.
func (r *run) endTurn(c *completePayload, a admitted) proto.TurnEnded {
	end := turnEnd(c, a)
	r.finishTurn()
	return end
}

// finishTurn leaves the running turn; the settings that waited for it go.
func (r *run) finishTurn() {
	r.ending, r.turn, r.recover = nil, "", ""
	if r.deferred == "" {
		r.setInTurn(false)
	}
	r.segment, r.toolsSeen = 0, false
	r.text.Reset()
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
		// A guarded model: the user picked it, so confirm it, unless they
		// have picked again since.
		operation, _ := req.params["operation"].(string)
		payload, _ := req.params["payload"].(map[string]any)
		next := map[string]any{}
		for k, v := range payload {
			next[k] = v
		}
		next["confirm"] = m.Confirm
		r.mu.Lock()
		if !slices.ContainsFunc(r.mutations, func(q mutation) bool { return q.operation == operation }) {
			r.mutations = slices.Insert(r.mutations, 0, mutation{operation: operation, payload: next})
		}
		r.mu.Unlock()
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
	method := "a request"
	if req != nil {
		method = req.method
	}
	switch method {
	case "session.mutate":
		return r.onMutateRefused(req, reason, e)
	case "prompt.receipt":
		adm, _ := req.params["admission_id"].(string)
		if kind := r.classify(adm); kind != "" {
			return r.giveUp(adm, kind, "Hermes has no record of the turn")
		}
		return nil
	case "session.interrupt":
		if reason == "stale_generation" {
			return nil // the turn it targeted is over
		}
	case "session.steer":
		if reason == "stale_generation" {
			return []proto.Payload{proto.Error{Message: "the turn ended before the steer reached Hermes"}}
		}
	}
	if req != nil && !req.retried && (reason == "stale_generation" || reason == "revision_conflict") &&
		(req.method != "approval.respond" || e.Data.Generation != nil) {
		r.mu.Lock()
		if e.Data.Generation != nil {
			r.generation = *e.Data.Generation
		}
		if e.Data.Revision != nil {
			r.revision = *e.Data.Revision
		}
		gen := r.generation
		r.mu.Unlock()
		params := req.params
		if _, ok := params["execution_generation"]; ok {
			params["execution_generation"] = gen
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.send(ctx, &request{method: req.method, params: params, retried: true}); err == nil {
			return nil
		}
	}
	msg := fmt.Sprintf("Hermes refused %s: %s", method, reason)
	if method == "approval.respond" {
		id, _ := req.params["prompt_id"].(string)
		r.mu.Lock()
		if a := r.approvals[id]; a != nil {
			a.answered = false
		}
		r.mu.Unlock()
		msg = "Hermes did not take the approval answer: " + reason
	}
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
	r.mu.Unlock()
	if ev.Session != "" && native != "" && ev.Session != native {
		return nil
	}
	if r.inTurn && r.turn == "" && ev.Admission != "" {
		// Reattached to a running bridge: the first event names the turn.
		r.turn = ev.Admission
	}
	if r.deferred != "" && ev.Admission == r.deferred {
		r.deferEvent(ev)
		return nil
	}
	switch ev.Type {
	case "message.start":
		var out []proto.Payload
		if r.ending != nil && ev.Admission != r.ending.admission {
			// The turn before is still awaiting its receipt: it ends here,
			// and its usage follows when the receipt comes.
			out = append(out, turnEnd(r.ending.complete, admitted{}))
			r.late[r.ending.admission] = true
			r.ending = nil
		}
		r.setInTurn(true)
		r.turn, r.recover = ev.Admission, ""
		r.segment, r.toolsSeen = 0, false
		r.text.Reset()
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
		admission := p.Admission
		if admission == "" {
			admission = ev.Admission
		}
		if !r.inTurn || (admission != "" && r.turn != "" && admission != r.turn) {
			return nil // not the running turn's
		}
		if admission == "" {
			admission = r.turn
		}
		var out []proto.Payload
		if p.Text != "" {
			out = append(out, proto.Message{ID: r.currentID(), Role: proto.RoleAssistant, Text: p.Text})
		}
		r.ending = &pendingEnd{admission: admission, complete: &p}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.requestReceipt(ctx, admission); err != nil {
			// No receipt coming: end the turn without its usage.
			out = append(out, r.endTurn(&p, admitted{}))
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

func (r *run) currentID() string { return r.messageID(r.turn) }

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

// send writes a request, or holds it until the session is attached.
func (r *run) send(ctx context.Context, req *request) error {
	r.mu.Lock()
	if req.method != "session.create" && req.method != "session.resume" && !r.attached {
		r.waiting = append(r.waiting, req)
		r.mu.Unlock()
		return nil
	}
	// Registered before it is written, so its answer finds it.
	r.n++
	id := r.prefix + "-" + strconv.Itoa(r.n)
	r.writes++
	req.seq = r.writes
	r.requests[id] = req
	r.mu.Unlock()
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": req.method, "params": req.params})
	if err == nil {
		err = r.io.WriteLine(ctx, b)
	} else {
		err = fmt.Errorf("hermes: encode: %w", err)
	}
	if err != nil {
		r.mu.Lock()
		delete(r.requests, id)
		r.mu.Unlock()
		return err
	}
	return nil
}

// session returns the Hermes session ID (empty before the session
// exists; a waiting request gets it when it does) and current generation.
func (r *run) session() (string, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.native, r.generation
}

// fillSession names the session in a request written before it existed.
func (r *run) fillSession(req *request, native string) {
	if s, ok := req.params["session_id"].(string); ok && s == "" {
		req.params["session_id"] = native
	}
}

func (r *run) submit(ctx context.Context, text string) error {
	native, _ := r.session()
	return r.submitRequest(ctx, &request{method: "prompt.submit", params: map[string]any{"session_id": native, "text": text, "submission_id": rand.Text()}})
}

// submitRequest sends a message, after the settings still waiting and
// after the messages that are queued or being released.
func (r *run) submitRequest(ctx context.Context, req *request) error {
	r.orderMu.Lock()
	defer r.orderMu.Unlock()
	return r.submitLocked(ctx, req)
}

// submitLocked is submitRequest for a caller that holds orderMu.
func (r *run) submitLocked(ctx context.Context, req *request) error {
	r.mu.Lock()
	if r.attached && (r.mutating || len(r.mutations) > 0 || len(r.afterSettings) > 0) {
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
	r.queueMutation(operation, payload)
	r.pumpMutations(ctx)
	return nil
}

// queueMutation queues a setting, replacing a waiting one of the same
// operation.
func (r *run) queueMutation(operation string, payload map[string]any) {
	r.requeue(mutation{operation: operation, payload: payload}, false)
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
	r.orderMu.Lock()
	defer r.orderMu.Unlock()
	r.pumpLocked(ctx)
}

// pumpLocked is pumpMutations for a caller that holds orderMu.
func (r *run) pumpLocked(ctx context.Context) {
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

// policyChange reports whether the edit policy has to change to policy.
func (r *run) policyChange(policy string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policy != policy || slices.ContainsFunc(r.mutations, func(m mutation) bool { return m.operation == "edit_policy" })
}

// setPolicy changes Hermes's edit approval policy (see mutate).
func (r *run) setPolicy(ctx context.Context, policy string) {
	if r.policyChange(policy) {
		_ = r.mutate(ctx, "edit_policy", map[string]any{"policy": policy})
	}
}

// setPolicyLocked is setPolicy for a caller that holds orderMu.
func (r *run) setPolicyLocked(ctx context.Context, policy string) {
	if r.policyChange(policy) {
		r.queueMutation("edit_policy", map[string]any{"policy": policy})
		r.pumpLocked(ctx)
	}
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
	native, gen := r.session()
	return r.call(ctx, "session.steer", map[string]any{"session_id": native, "execution_generation": gen, "text": text})
}

// Interrupt ends the running turn.
func (r *run) Interrupt(ctx context.Context) error {
	native, gen := r.session()
	return r.call(ctx, "session.interrupt", map[string]any{"session_id": native, "execution_generation": gen})
}

// Respond answers an approval with one of Hermes's choices.
func (r *run) Respond(ctx context.Context, approvalID, optionID string) error {
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
	native, _ := r.session()
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
	native, _ := r.session()
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
