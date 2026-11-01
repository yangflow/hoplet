package hermes

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// receiptRetries is how often a receipt is asked for again while Hermes
// still reports the turn as queued or started, receiptRetryDelay apart.
const (
	receiptRetries    = 3
	receiptRetryDelay = time.Second
)

// onOrphanReceipt reads a receipt the old daemon asked for. While its
// open turn's admission is not known, the receipt may be that turn's or an
// earlier turn's whose receipt was still due: it is held until the
// snapshot this run asked for settles the open turn (see settleHeld).
// Without that snapshot coming, the open turn is the one the receipt
// names. A receipt that does not end the open turn yields nothing, not
// even its usage: the old daemon may have counted it already, and usage
// counted twice is worse than a late one lost.
func (r *run) onOrphanReceipt(a admitted) []proto.Payload {
	if h := r.unadopted(); h != nil && r.find(a.Admission) == nil {
		if r.holding {
			r.held = append(r.held, a)
			return nil
		}
		h.admission = a.Admission
	}
	if h := r.head(); h == nil || !h.placeholder || h.admission != a.Admission {
		return nil
	}
	return r.onReceipt(quiet(a))
}

// quiet drops the final answer from a receipt the old daemon asked for:
// it emitted that answer when it read the turn's message.complete, which
// it does before it asks.
func quiet(a admitted) admitted {
	if a.Result != nil {
		res := *a.Result
		res.FinalResponse = ""
		a.Result = &res
	}
	return a
}

// adoptHeld applies a held receipt of the turn the open one was found to
// be: that turn is over.
func (r *run) adoptHeld(out []proto.Payload, admission string) []proto.Payload {
	for i, a := range r.held {
		if a.Admission == admission {
			r.held = slices.Delete(r.held, i, i+1)
			return append(out, r.onReceipt(quiet(a))...)
		}
	}
	return out
}

// settleHeld settles the store's open turn when the snapshot asked for on
// reattaching answers (running says what it found), or when it will not
// come (running false). If that turn is still unknown and is over (nothing
// runs, or a newer turn started), the latest receipt held is its own; the
// others are earlier turns' and are dropped (see onOrphanReceipt). The
// queue is reconciled before this runs, and advanced after it.
func (r *run) settleHeld(out []proto.Payload, running bool) []proto.Payload {
	held := r.held
	r.held, r.holding = nil, false
	h := r.unadopted()
	if h == nil || len(held) == 0 || (running && !h.ended) {
		if h != nil && !running {
			h.ended = true
		}
		return out
	}
	last := held[len(held)-1]
	h.admission, h.ended = last.Admission, true
	return append(out, r.onReceipt(quiet(last))...)
}

// onSnapshot reads a snapshot; own is true for the answer to this run's
// own session.create or session.resume.
func (r *run) onSnapshot(result json.RawMessage, created, own bool) (out []proto.Payload, native string) {
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
	var unknown []proto.Payload
	for _, p := range s.Prompts {
		if p.Kind == "approval" {
			open[p.ID] = p
			if _, ok := r.approvals[p.ID]; !ok {
				r.approvals[p.ID] = &approval{generation: p.Generation}
				a := r.approvalRequested(p)
				a.ToolCallID = "" // its tool is not known here
				unknown = append(unknown, a)
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
	settle := own && r.holding
	openTurn := r.unadopted()
	if own || openTurn == nil {
		// A snapshot the old daemon asked for is older than the replay: it
		// does not say what became of the store's open turn.
		r.reconcile(s.Running, started)
	}
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
			req = req.with("execution_generation", p.Generation)
		}
		req = r.fillSession(req, s.Session)
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
	if settle {
		out = r.settleHeld(out, s.Running)
	}
	out = r.advance(out)
	if h := r.head(); own && openTurn != nil && h == openTurn && h.admission != "" && !h.ended && h.complete == nil {
		// The store's open turn runs: its message.complete may have been
		// read by the old daemon already, so ask how it ended.
		h.probe, h.asked = true, true
		if err := r.requestReceipt(ctx, h.admission); err != nil {
			h.probe, h.asked = false, false
		}
	}
	// Approvals this run does not know were raised before it reattached or
	// while it was not connected; the store ignores one it already has.
	out = append(out, unknown...)
	return out, native
}

// turnRec is a turn Hoplet knows of. Turns are kept in the order Hermes
// admitted them; the first is the one Hoplet shows.
type turnRec struct {
	admission   string
	started     bool             // its TurnStarted was emitted
	ended       bool             // known to be over in Hermes; its end comes from its receipt
	asked       bool             // its receipt was asked for on this connection
	placeholder bool             // the store's open turn after a daemon restart
	seen        bool             // a placeholder whose events this run translates
	probe       bool             // a running placeholder's receipt asked for at once
	complete    *completePayload // its message.complete, when seen
	events      []event          // its events, kept until it is the first
}

// maxQueuedEvents bounds the events kept for a turn that is not yet the
// first. Past it only text deltas are dropped: the final message still
// carries the whole text from message.complete. A segment closed before
// that by a tool call carries only the text kept.
const maxQueuedEvents = 4096

func (r *run) head() *turnRec {
	if len(r.queue) == 0 {
		return nil
	}
	return r.queue[0]
}

// turnID is the admission of the turn Hoplet shows.
func (r *run) turnID() string {
	if h := r.head(); h != nil {
		return h.admission
	}
	return ""
}

// unadopted returns the first turn if it stands for the store's open turn
// and its admission is not known yet.
func (r *run) unadopted() *turnRec {
	if h := r.head(); h != nil && h.placeholder && h.admission == "" {
		return h
	}
	return nil
}

func (r *run) find(admission string) *turnRec {
	for _, t := range r.queue {
		if t.admission == admission {
			return t
		}
	}
	return nil
}

// enqueue adds a turn behind those known; while turns are queued, settings
// wait.
func (r *run) enqueue(admission string) {
	r.queue = append(r.queue, &turnRec{admission: admission})
	if len(r.queue) == 1 {
		r.setInTurn(true)
	}
}

// buffer keeps an event of a turn that is not the first yet. Kept events
// are in memory only: their lines are committed, so a daemon restart loses
// them. A kept approval is listed again by the next snapshot (onSnapshot);
// the rest are not recovered.
func (t *turnRec) buffer(ev event) {
	if ev.Type == "message.delta" && len(t.events) >= maxQueuedEvents {
		return
	}
	t.events = append(t.events, ev)
}

// pop removes the first turn, whose end has been reported.
func (r *run) pop() {
	if h := r.head(); h != nil {
		delete(r.retries, h.admission)
		r.queue = r.queue[1:]
	}
	r.segment, r.toolsSeen = 0, false
	r.text.Reset()
}

// reconcile sets the queue to what a snapshot says: a turn other than the
// running one is over, the running one is queued if it is not, and the
// first turn's receipt is asked for again (the old connection took the
// request with it).
func (r *run) reconcile(running bool, started string) {
	for _, t := range r.queue {
		if t.admission == "" && started != "" && r.find(started) == nil {
			// The store's open turn is the last one started: the one running.
			t.admission = started
		}
		if !running || (started != "" && t.admission != started) {
			t.ended = true
		}
	}
	if running && started != "" && r.find(started) == nil {
		r.enqueue(started)
	}
	if h := r.head(); h != nil {
		h.asked = false
	}
}

// advance moves the queue on as far as it can and returns the payloads
// that come out: a turn that is over is ended from its receipt (asked for
// once), and the next is started, its kept events played, and so on.
func (r *run) advance(out []proto.Payload) []proto.Payload {
	for h := r.head(); h != nil; h = r.head() {
		switch {
		case !h.started:
			h.started = true
			r.segment, r.toolsSeen = 0, false
			r.lastTool = "" // an approval names a tool of its own turn
			r.text.Reset()
			out = append(out, proto.TurnStarted{})
			events := h.events
			h.events = nil
			for _, ev := range events {
				if r.head() != h {
					break
				}
				out = append(out, r.translateEvent(ev)...)
			}
			continue
		case h.ended && h.admission == "" && h.placeholder && r.holding:
			// The snapshot asked for on reattaching settles it.
		case h.ended && h.admission == "":
			msg := "the turn ended while Hoplet was not connected"
			if h.placeholder {
				msg = "the turn ended while the Hoplet daemon restarted"
			}
			out = append(out, proto.TurnEnded{Reason: proto.TurnAborted, Message: msg})
			r.pop()
			continue
		case (h.ended || h.complete != nil) && !h.asked:
			h.asked = true
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := r.requestReceipt(ctx, h.admission)
			cancel()
			if err != nil {
				// No receipt coming: end the turn without its usage.
				out = append(out, r.endWithout(h, "Hermes did not report how the turn ended"))
				continue
			}
		}
		break
	}
	if len(r.queue) == 0 && r.isBusy() {
		r.setInTurn(false)
	}
	return out
}

// endWithout ends the first turn without a receipt: from its
// message.complete when there was one, else as aborted.
func (r *run) endWithout(h *turnRec, reason string) proto.Payload {
	end := proto.TurnEnded{Reason: proto.TurnAborted, Message: reason}
	if h.complete != nil {
		end = turnEnd(h.complete, admitted{})
	}
	r.pop()
	return end
}

// onStart handles a message.start: a turn is admitted. The first turn, if
// it is another one, is over by now. A start never names the store's open
// turn after a restart: Hermes runs one turn at a time, so that turn is
// over. It ends when the snapshot asked for on reattaching settles it
// (settleHeld), or as aborted.
func (r *run) onStart(admission string) []proto.Payload {
	if r.find(admission) == nil {
		if admission != "" || len(r.queue) == 0 {
			r.enqueue(admission)
		}
	}
	h := r.head()
	if h == nil || h.admission == admission {
		return r.advance(nil)
	}
	var out []proto.Payload
	if h.complete != nil {
		// Its end is known from its message.complete: it ends here, and
		// its usage follows when the receipt comes.
		out = append(out, turnEnd(h.complete, admitted{}))
		r.late[h.admission] = true
		r.pop()
	} else {
		h.ended = true
	}
	return r.advance(out)
}

// requestReceipt asks how a turn ended.
func (r *run) requestReceipt(ctx context.Context, admission string) error {
	native, _ := r.session()
	return r.call(ctx, "prompt.receipt", map[string]any{"session_id": native, "admission_id": admission, "include_result": true})
}

// Kinds of turn a receipt can belong to.
const (
	receiptHead = "head" // the turn Hoplet shows
	receiptLate = "late" // ended by a newer turn: only its usage is left
)

// classify says which turn a receipt for admission settles, if any: only
// the one it names.
func (r *run) classify(admission string) string {
	switch {
	case r.head() != nil && r.head().admission == admission:
		return receiptHead
	case r.late[admission]:
		return receiptLate
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
	h := r.head()
	if h.complete == nil && (!h.placeholder || h.seen) && a.Result != nil && a.Result.FinalResponse != "" {
		// Recovered after a lost connection: the answer was never streamed
		// here. The store's open turn after a restart that this run has not
		// streamed got its answer from the old daemon, or gets it from a
		// replayed message.complete.
		out = append(out, proto.Message{ID: r.messageID(a.Admission), Role: proto.RoleAssistant, Text: a.Result.FinalResponse})
	}
	usage()
	out = append(out, turnEnd(h.complete, a))
	r.pop()
	return r.advance(out)
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
		select {
		case <-r.done:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		_ = r.requestReceipt(ctx, admission)
	})
	r.timers[t] = struct{}{}
	return nil
}

// giveUp ends a turn whose receipt will not come.
func (r *run) giveUp(admission, kind, reason string) []proto.Payload {
	delete(r.retries, admission)
	if kind == receiptLate {
		delete(r.late, admission)
		return nil
	}
	if h := r.head(); h.probe && h.complete == nil && !h.ended {
		// It runs: its own message.complete ends it.
		h.probe, h.asked = false, false
		return nil
	}
	return r.advance([]proto.Payload{r.endWithout(r.head(), reason)})
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

func (r *run) isBusy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy
}

// setInTurn is the loop's way to change busy; a
// turn's end sends the settings that waited for it.
func (r *run) setInTurn(v bool) {
	r.mu.Lock()
	r.busy = v
	r.mu.Unlock()
	if !v {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		r.pumpMutations(ctx)
	}
}
