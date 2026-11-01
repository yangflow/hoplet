package hermes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/yangflow/hoplet/proto"
)

// mutation is a session setting waiting to be sent.
type mutation struct {
	operation string
	payload   map[string]any
	retried   bool // refused once for stale values already
}

// request is a request the adapter sent and has no answer to yet.
type request struct {
	method string
	// params is never changed once the request is handed to send: it may
	// be encoded on another goroutine. A changed request is a copy (with).
	params  map[string]any
	retried bool
	seq     int  // its place in the order of writes
	resent  bool // a message written before a drop, to be written again
}

// with returns a copy of the request with one param set.
func (q *request) with(key string, v any) *request {
	c := *q
	c.params = maps.Clone(q.params)
	c.params[key] = v
	return &c
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

// attachRefused records that Hermes refused to attach the session: the
// requests waiting for it are dropped, and commands fail until a new
// connection attaches.
func (r *run) attachRefused(e *rpcError) []proto.Payload {
	r.mu.Lock()
	r.attachErr = fmt.Errorf("hermes: Hermes refused to attach the session: %s", reason(e))
	dropped := len(r.waiting)
	for _, req := range r.waiting {
		if req.method == "approval.respond" {
			// Not answered after all: a later snapshot may expire it.
			id, _ := req.params["prompt_id"].(string)
			if a := r.approvals[id]; a != nil {
				a.answered = false
			}
		}
	}
	r.waiting = nil
	r.mu.Unlock()
	switch dropped {
	case 0:
		return nil
	case 1:
		return []proto.Payload{proto.Error{Message: "1 request was not sent: Hermes refused to attach the session"}}
	}
	return []proto.Payload{proto.Error{Message: fmt.Sprintf("%d requests were not sent: Hermes refused to attach the session", dropped)}}
}

// refused returns the error commands fail with while the session is not
// attached because Hermes refused it.
func (r *run) refused() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attachErr
}

// reason is why a request was refused.
func reason(e *rpcError) string {
	if e.Data.Reason != "" {
		return e.Data.Reason
	}
	return e.Message
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
// errors. resent reports a retry written.
func (r *run) onError(req *request, e *rpcError) (out []proto.Payload, resent bool) {
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
		return r.onMutateRefused(req, reason, e), false
	case "session.create", "session.resume":
		if req.retried || (reason != "stale_generation" && reason != "revision_conflict") {
			msg := "Hermes refused to attach the session: " + reason
			if e.Data.Detail != "" {
				msg += " (" + e.Data.Detail + ")"
			}
			return []proto.Payload{proto.Error{Message: msg}}, false
		}
	case "prompt.receipt":
		adm, _ := req.params["admission_id"].(string)
		if kind := r.classify(adm); kind != "" {
			return r.giveUp(adm, kind, "Hermes has no record of the turn"), false
		}
		return nil, false
	case "session.interrupt":
		if reason == "stale_generation" {
			return nil, false // the turn it targeted is over
		}
	case "session.steer":
		if reason == "stale_generation" {
			return []proto.Payload{proto.Error{Message: "the turn ended before the steer reached Hermes"}}, false
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
		retry := &request{method: req.method, params: req.params}
		if _, ok := req.params["execution_generation"]; ok {
			retry = retry.with("execution_generation", gen)
		}
		retry.retried = true
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.send(ctx, retry); err == nil {
			return nil, true
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
	return []proto.Payload{proto.Error{Message: msg}}, false
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
	req.seq = r.n
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
func (r *run) fillSession(req *request, native string) *request {
	if s, ok := req.params["session_id"].(string); ok && s == "" {
		return req.with("session_id", native)
	}
	return req
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
			_ = r.send(ctx, r.fillSession(req, native))
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
