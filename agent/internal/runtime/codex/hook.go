package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// RuleSource builds the engine that decides one process's tool calls, from the start spec with the session's
// ID and directory. The hook asks for it on every call, so a rule pushed while a process runs applies at once.
type RuleSource func(runtime.StartSpec) (*rules.Engine, error)

// hookMatcher matches every tool: Codex matches the regular expression against the tool name.
const hookMatcher = ".*"

// hookAskPrefix starts the request ID of an approval the hook holds open, apart from the IDs of Codex's own
// approval requests.
const hookAskPrefix = "hook:"

// hookConfig is the `config` of thread/start and thread/resume that registers the rule hook. Hoplet's own hook
// is trusted without the user's review (`bypass_hook_trust`, ADR 0029); the command is the absolute path of the
// agent binary.
func hookConfig(command string) map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": hookMatcher,
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": command,
					"timeout": hookTimeoutSeconds,
				}},
			}},
		},
		"bypass_hook_trust": true,
	}
}

// HookCommand is the command line of the hook for the agent binary at exe: the quoted absolute path and
// `hook codex`.
func HookCommand(exe string) string { return shellQuote(exe) + " hook codex" }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// hookInput is the part of a PreToolUse hook input the agent reads. The session ID, the working directory and the
// permission mode in it are not read: the daemon knows them (ADR 0032).
type hookInput struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolUseID     string          `json:"tool_use_id"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// hookAsk is an approval the hook holds open: the channel gets the user's verdict, or is closed when the turn
// ends the wait.
type hookAsk struct {
	mu     sync.Mutex
	closed bool
	ch     chan runtime.Verdict
}

// decideHook decides one tool call of this process. Nothing that fails lets a call through: input it cannot
// read, rules it cannot build, or a panic in the engine is a deny. The log names the failure and never the input.
func (p *process) decideHook(ctx context.Context, raw json.RawMessage) (out hookReply) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] the permission rules failed on a Codex tool call: %T", r)
			out = denyReply("Hoplet could not check this call against the permission rules")
		}
	}()
	unreadable := denyReply("Hoplet could not read this call to check it against the permission rules")
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil || in.ToolName == "" ||
		(in.HookEventName != "" && in.HookEventName != "PreToolUse") {
		log.Printf("[agent] a Codex tool call came with a hook input the permission rules cannot read")
		return unreadable
	}
	if t := bytes.TrimSpace(in.ToolInput); len(t) > 0 && !bytes.Equal(t, []byte("null")) && t[0] != '{' {
		log.Printf("[agent] a Codex tool call came with a tool input the permission rules cannot read")
		return unreadable
	}

	p.mu.Lock()
	p.hookCalls++
	plan := p.cfg[optCollaboration] == collabPlan
	src, dir, thread := p.rules, p.dir, p.threadID
	p.mu.Unlock()
	if src == nil {
		return denyReply("Hoplet has no permission rules for this Codex process")
	}
	engine, err := src(runtime.StartSpec{SessionID: thread, Dir: dir})
	if err != nil {
		log.Printf("[agent] the permission rules cannot be built: %v", err)
		return denyReply("Hoplet could not build the permission rules")
	}
	call := rules.FromCodexHook(in.ToolName, in.ToolInput, dir, dir)
	d := engine.Decide(call)
	decided := func() {
		p.emit(runtime.RuleDecided{
			ToolUseID: in.ToolUseID, Decision: string(d.Action), RuleID: d.RuleID, Scope: string(d.Scope),
			Revision: engine.Revision(),
		})
	}
	switch d.Action {
	case rules.Deny:
		decided()
		return denyReply(d.Reason)
	case rules.Ask:
		decided()
		return p.askUser(ctx, in, call)
	case rules.Allow:
		// A plan does not run commands or change files, so a rule never lifts that: Codex decides.
		if plan {
			return hookReply{Decision: hookPass}
		}
		decided()
		p.rememberHookOK(in.ToolUseID)
		return hookReply{Decision: hookAllow, Reason: d.Reason}
	}
	return denyReply("Hoplet could not decide this call against the permission rules")
}

// askUser raises the call as an approval in the web UI through the process's own permission path and holds the
// hook open until the user answers, the turn ends or the hook times out. Only allow or deny comes back.
func (p *process) askUser(ctx context.Context, in hookInput, call rules.Call) hookReply {
	ask := &hookAsk{ch: make(chan runtime.Verdict, 1)}
	p.mu.Lock()
	p.hookSeq++
	id := fmt.Sprintf("%s%d", hookAskPrefix, p.hookSeq)
	p.hookAsks[id] = ask
	p.mu.Unlock()

	tool, input := in.ToolName, in.ToolInput
	switch call.Kind {
	case rules.KindRun:
		tool, input = "commandExecution", mustJSON(map[string]any{"command": call.Command, "cwd": call.Cwd})
	case rules.KindWrite:
		tool, input = "fileChange", mustJSON(map[string]any{"paths": append([]string{}, call.Paths...)})
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	p.emit(runtime.PermissionRequest{RequestID: id, Tool: tool, ToolUseID: in.ToolUseID, Input: input})

	var v runtime.Verdict
	answered := false
	select {
	case v, answered = <-ask.ch:
	case <-ctx.Done():
	case <-p.gone:
	case <-p.quit:
	}
	p.mu.Lock()
	delete(p.hookAsks, id)
	idle := len(p.requests) == 0 && len(p.hookAsks) == 0 && p.turnPending
	p.mu.Unlock()
	if !answered {
		p.emit(runtime.PermissionCancelled{RequestID: id})
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return denyReply("no answer in time")
		}
		return denyReply("The request was not answered in Hoplet; the call was denied")
	}
	if idle {
		p.emit(runtime.StateChanged{State: runtime.StateRunning})
	}
	if v.Allow {
		p.rememberHookOK(in.ToolUseID)
		return hookReply{Decision: hookAllow}
	}
	msg := strings.TrimSpace(v.Message)
	if msg == "" {
		msg = "Denied by the user through Hoplet"
	}
	return denyReply(msg)
}

// answerHook delivers the user's verdict to the hook waiting on the request.
func (p *process) answerHook(requestID string, v runtime.Verdict) error {
	p.mu.Lock()
	ask := p.hookAsks[requestID]
	p.mu.Unlock()
	if ask == nil {
		return fmt.Errorf("codex: no request %q is waiting", requestID)
	}
	ask.mu.Lock()
	defer ask.mu.Unlock()
	if ask.closed {
		return fmt.Errorf("codex: request %q is no longer waiting", requestID)
	}
	select {
	case ask.ch <- v:
		return nil
	default:
		return fmt.Errorf("codex: request %q was already answered", requestID)
	}
}

// endHookAsks ends every wait of the hook: the turn was interrupted, so the calls waiting for a person are denied.
func (p *process) endHookAsks() {
	p.mu.Lock()
	asks := p.hookAsks
	p.hookAsks = map[string]*hookAsk{}
	p.mu.Unlock()
	for _, a := range asks {
		a.mu.Lock()
		if !a.closed {
			a.closed = true
			close(a.ch)
		}
		a.mu.Unlock()
	}
}

// checkHookRan logs and reports once when a command or a file change finished without the daemon having been asked about
// any call: the hook does not run, so the rules are not enforced on this process (ADR 0029, ADR 0032).
func (p *process) checkHookRan(toolUseID string) {
	p.mu.Lock()
	warn := p.rules != nil && p.hookCalls == 0 && !p.hookWarned
	if warn {
		p.hookWarned = true
	}
	p.mu.Unlock()
	if warn {
		log.Printf("[agent] a Codex tool call ran without the rule hook being asked: the permission rules are not enforced on this process")
		p.emit(runtime.RulesUnenforced{ToolUseID: toolUseID})
	}
}

// hookOKTTL is how long the daemon remembers a call the hook let through: Codex asks for its own approval right
// after the hook returns.
const hookOKTTL = 10 * time.Minute

// rememberHookOK records that the user or a rule approved the call, so Codex's own approval of the same item is
// accepted without asking again.
func (p *process) rememberHookOK(toolUseID string) {
	if toolUseID == "" {
		return
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, t := range p.hookOK {
		if now.After(t) {
			delete(p.hookOK, id)
		}
	}
	p.hookOK[toolUseID] = now.Add(hookOKTTL)
}

// takeHookOK reports whether the call of this item was approved at the hook, and forgets it.
func (p *process) takeHookOK(itemID string) bool {
	if itemID == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.hookOK[itemID]
	delete(p.hookOK, itemID)
	return ok && time.Now().Before(t)
}
