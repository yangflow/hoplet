package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// Options of a permission approval, whose request lists none.
const (
	grantTurn    = "turn"
	grantSession = "session"
	grantNone    = "deny"
)

// fileDecisions are the answers a file change takes; its request lists none.
var fileDecisions = []string{`"accept"`, `"acceptForSession"`, `"decline"`, `"cancel"`}

// approvalID names an approval by its JSON-RPC ID and item, so an answer can
// still reach Codex from a daemon that restarted after the request.
func approvalID(rpc json.RawMessage, item string) string { return string(rpc) + ":" + item }

func rpcOf(approval string) (json.RawMessage, error) {
	id, _, ok := strings.Cut(approval, ":")
	if !ok || !json.Valid([]byte(id)) {
		return nil, fmt.Errorf("codex: unknown approval %q", approval)
	}
	return json.RawMessage(id), nil
}

// approval turns an approval request into its event; nil if it is malformed.
func (r *run) approval(m message) []proto.Payload {
	var p approvalParams
	if json.Unmarshal(m.Params, &p) != nil || p.ItemID == "" {
		return nil
	}
	a := proto.ApprovalRequested{ID: approvalID(m.ID, p.ItemID), ToolCallID: p.ItemID, Detail: p.Reason}
	switch m.Method {
	case methodCommandApproval:
		a.Kind, a.Title = proto.ApprovalCommand, p.Command
		if len(p.CommandActions) == 1 && p.CommandActions[0].Command != "" {
			a.Title = p.CommandActions[0].Command
		}
		a.Options = decisionOptions(p.AvailableDecisions)
	case methodFileApproval:
		a.Kind, a.Title = proto.ApprovalFileChange, "Edit files"
		raw := make([]json.RawMessage, len(fileDecisions))
		for i, d := range fileDecisions {
			raw[i] = json.RawMessage(d)
		}
		a.Options = decisionOptions(raw)
	default:
		if len(p.Permissions) == 0 {
			return nil
		}
		a.Kind, a.Title = proto.ApprovalTool, "Grant more access"
		a.Options = []proto.ApprovalOption{
			{ID: grantTurn, Label: "Allow for this turn", Scope: proto.ScopeOnce},
			{ID: grantSession, Label: "Allow for this session", Scope: proto.ScopeSession},
			{ID: grantNone, Label: "Deny", Scope: proto.ScopeDeny},
		}
		r.mu.Lock()
		r.permissions[a.ID] = requestedAccess{rpc: m.ID, profile: p.Permissions}
		r.mu.Unlock()
	}
	if len(a.Options) == 0 {
		return nil
	}
	return []proto.Payload{a}
}

// decisionOptions passes Codex's decisions through. A decision's option ID is
// the decision itself: its name, or the JSON of one that carries data.
func decisionOptions(decisions []json.RawMessage) []proto.ApprovalOption {
	out := make([]proto.ApprovalOption, 0, len(decisions))
	for _, d := range decisions {
		var name string
		if json.Unmarshal(d, &name) == nil {
			if label, scope, ok := decisionLabel(name, nil); ok {
				out = append(out, proto.ApprovalOption{ID: name, Label: label, Scope: scope})
			}
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(d, &obj) != nil || len(obj) != 1 {
			continue
		}
		for name, body := range obj {
			if label, scope, ok := decisionLabel(name, body); ok {
				var c bytes.Buffer
				if json.Compact(&c, d) == nil {
					out = append(out, proto.ApprovalOption{ID: c.String(), Label: label, Scope: scope})
				}
			}
		}
	}
	return out
}

func decisionLabel(name string, body json.RawMessage) (string, proto.ApprovalScope, bool) {
	switch name {
	case "accept":
		return "Allow once", proto.ScopeOnce, true
	case "acceptForSession":
		return "Allow for this session", proto.ScopeSession, true
	case "acceptWithExecpolicyAmendment":
		return "Always allow this command", proto.ScopeAlways, true
	case "applyNetworkPolicyAmendment":
		var a struct {
			Amendment struct {
				Action string `json:"action"`
			} `json:"network_policy_amendment"`
		}
		if json.Unmarshal(body, &a) != nil {
			return "", "", false
		}
		if a.Amendment.Action == "deny" {
			return "Always deny this host", proto.ScopeDeny, true
		}
		return "Always allow this host", proto.ScopeAlways, true
	case "decline":
		return "Deny", proto.ScopeDeny, true
	case "cancel":
		return "Deny and stop", proto.ScopeDeny, true
	}
	return "", "", false
}

// decisionJSON is the wire form of an option ID.
func decisionJSON(option string) (json.RawMessage, error) {
	if strings.HasPrefix(option, "{") {
		if !json.Valid([]byte(option)) {
			return nil, fmt.Errorf("codex: unknown approval option %q", option)
		}
		return json.RawMessage(option), nil
	}
	if _, _, ok := decisionLabel(option, nil); !ok || option == "applyNetworkPolicyAmendment" {
		return nil, fmt.Errorf("codex: unknown approval option %q", option)
	}
	b, _ := json.Marshal(option) // a string always marshals
	return b, nil
}

// claim marks a server request answered; false if it already was.
func (r *run) claim(rpc json.RawMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed[string(rpc)] {
		return false
	}
	r.closed[string(rpc)] = true
	return true
}

func (r *run) unclaim(rpc json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.closed, string(rpc))
}

// requestClosed forgets a server request Codex no longer waits for.
func (r *run) requestClosed(rpc json.RawMessage) {
	if len(rpc) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed[string(rpc)] = true
	for id, q := range r.questions {
		if bytes.Equal(q.rpc, rpc) {
			delete(r.questions, id)
		}
	}
}

// reply writes a server request's result.
func (r *run) reply(ctx context.Context, rpc json.RawMessage, result any) error {
	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("codex: encode reply: %w", err)
	}
	return r.write(ctx, message{ID: rpc, Result: b})
}

// Respond answers an approval. The first answer counts; a later one, or one
// to a request Codex has withdrawn, is an error.
func (r *run) Respond(ctx context.Context, approval, option string) error {
	rpc, err := rpcOf(approval)
	if err != nil {
		return err
	}
	var result any
	if option == grantTurn || option == grantSession || option == grantNone {
		r.mu.Lock()
		req, ok := r.permissions[approval]
		r.mu.Unlock()
		if !ok {
			return fmt.Errorf("codex: permission approval %q is no longer open", approval)
		}
		reply := permissionsReply{Permissions: req.profile, Scope: option}
		if option == grantNone {
			reply = permissionsReply{Permissions: json.RawMessage(`{}`), Scope: grantTurn}
		}
		result = reply
	} else {
		d, err := decisionJSON(option)
		if err != nil {
			return err
		}
		result = approvalReply{Decision: d}
	}
	if !r.claim(rpc) {
		return fmt.Errorf("codex: approval %q is no longer open", approval)
	}
	if err := r.reply(ctx, rpc, result); err != nil {
		r.unclaim(rpc)
		return err
	}
	r.mu.Lock()
	delete(r.permissions, approval)
	r.mu.Unlock()
	return nil
}

// userInput turns a requestUserInput call into one question event per
// question. A secret is refused: an answer becomes an event and is stored.
func (r *run) userInput(m message) []proto.Payload {
	var p userInputParams
	if json.Unmarshal(m.Params, &p) != nil || len(p.Questions) == 0 {
		return nil
	}
	set := &questionSet{rpc: m.ID, native: map[string]string{}, answers: map[string]userInputAnswer{}}
	out := make([]proto.Payload, 0, len(p.Questions))
	for _, q := range p.Questions {
		if q.IsSecret {
			return nil
		}
		id := string(m.ID) + ":" + q.ID
		set.native[id] = q.ID
		ev := proto.Question{ID: id, Text: q.Question}
		for _, o := range q.Options {
			ev.Options = append(ev.Options, o.Label)
		}
		out = append(out, ev)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range set.native {
		r.questions[id] = set
	}
	return out
}

// Answer answers a question. The first answer counts; a later one is an
// error, as is one to a question this run did not see (asked before a daemon
// restart). Codex gets the answers once a call's questions are all answered.
func (r *run) Answer(ctx context.Context, question, answer string) error {
	r.mu.Lock()
	set, ok := r.questions[question]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("codex: question %q is no longer open", question)
	}
	delete(r.questions, question)
	nativeID := set.native[question]
	delete(set.native, question)
	set.answers[nativeID] = userInputAnswer{Answers: []string{answer}}
	var complete map[string]userInputAnswer
	if len(set.native) == 0 {
		complete = maps.Clone(set.answers)
	}
	r.mu.Unlock()
	if complete == nil {
		return nil
	}
	err := r.reply(ctx, set.rpc, userInputReply{Answers: complete})
	if err != nil {
		r.mu.Lock()
		r.questions[question], set.native[question] = set, nativeID
		r.mu.Unlock()
	}
	return err
}

var _ harness.Run = (*run)(nil)
