package claude

import (
	"bytes"
	"encoding/json"
	"log"

	"github.com/yangflow/hoplet/agent/internal/mcp"
	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// The PreToolUse hook (ADR 0029): Hoplet registers one control-channel callback for every tool in `initialize`,
// and Claude Code sends a hook_callback request before each tool call, in every permission mode. The agent
// decides the call against its rules there.
const (
	hookCallbackID = "hoplet-pretooluse"
	// hookMatcher matches every tool: the empty matcher is the form ADR 0029 tested against Claude Code.
	hookMatcher = ""
	// hookTimeoutSeconds bounds Claude Code's wait for the callback. The callback answers at once, but the ADR
	// measured a 900 s timeout holding, and a callback that waits for a person needs at least ten minutes.
	hookTimeoutSeconds = 900
)

// RuleSource builds the engine that decides one process's tool calls. The driver asks for it when a process
// starts, with the start spec, so the rules can be those of that session and its directory.
type RuleSource func(runtime.StartSpec) (*rules.Engine, error)

// DefaultRules is the rule set when no store is wired in: the built-in defaults, all enabled, and the
// compiled-in floor.
func DefaultRules(env rules.Env) RuleSource {
	return func(runtime.StartSpec) (*rules.Engine, error) { return rules.New(env, nil, rules.Defaults()) }
}

// hookRegistration is the `hooks` field of the initialize request.
func hookRegistration() map[string]any {
	return map[string]any{
		"PreToolUse": []map[string]any{{
			"matcher":         hookMatcher,
			"hookCallbackIds": []string{hookCallbackID},
			"timeout":         hookTimeoutSeconds,
		}},
	}
}

// hookInput is the part of a PreToolUse hook input the agent reads.
type hookInput struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolUseID     string          `json:"tool_use_id"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// handleHook answers a hook_callback request. Only the callback Hoplet registered is answered; any other gets
// the error response of an unsupported request.
func (p *process) handleHook(m *message) {
	if p.engine == nil || m.Request.CallbackID != hookCallbackID {
		p.unsupported(m)
		return
	}
	// The decision reads the file system (symlinks), so it does not hold up the output loop.
	go func(id string, raw json.RawMessage) {
		_ = p.send(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": id,
				"response":   p.hookOutput(raw),
			},
		})
	}(m.RequestID, m.Request.Input)
}

// hookOutput decides one call. Nothing that fails lets a call through: a hook input it cannot read, or a panic
// in the engine, is a deny. The log names the failure and never the input.
func (p *process) hookOutput(raw json.RawMessage) (out map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] the permission rules failed on a tool call: %T", r)
			out = hookDecision(rules.Deny, "Hoplet could not check this call against the permission rules")
		}
	}()
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil || in.ToolName == "" ||
		(in.HookEventName != "" && in.HookEventName != "PreToolUse") {
		log.Printf("[agent] a tool call came with a hook input the permission rules cannot read")
		return hookDecision(rules.Deny, "Hoplet could not read this call to check it against the permission rules")
	}
	if t := bytes.TrimSpace(in.ToolInput); len(t) > 0 && !bytes.Equal(t, []byte("null")) && t[0] != '{' {
		log.Printf("[agent] a tool call came with a tool input the permission rules cannot read")
		return hookDecision(rules.Deny, "Hoplet could not read this call to check it against the permission rules")
	}
	engine := p.engine
	if p.rebuild != nil {
		e, err := p.rebuild()
		if err != nil {
			log.Printf("[agent] the permission rules cannot be built: %v", err)
			return hookDecision(rules.Deny, "Hoplet could not build the permission rules")
		}
		engine = e
	}
	call := rules.FromClaude(in.ToolName, in.ToolInput, p.dir, p.dir)
	d := engine.Decide(call)
	// The built-in allow of the session MCP read tools (ADR 0034): it applies only where no rule decided, so a
	// deny or ask rule of the user still wins, and only in a process that has the server.
	if d.Action == "" && p.mcpRead && call.Kind == rules.KindMCP && call.Server == mcp.ServerName && mcp.IsReadTool(call.Tool) {
		d = rules.Decision{Action: rules.Allow, Reason: "a read tool of the Hoplet session MCP server"}
	}
	decided := func() {
		p.emitLate(runtime.RuleDecided{
			ToolUseID: in.ToolUseID, Decision: string(d.Action), RuleID: d.RuleID, Scope: string(d.Scope),
			Revision: engine.Revision(),
		})
	}
	switch d.Action {
	case rules.Deny, rules.Ask:
		decided()
		return hookDecision(d.Action, d.Reason)
	case rules.Allow:
		// The mode comes from the agent's own state. A plan does not run commands or change files, so a rule
		// never lifts that: the mode decides.
		p.mu.Lock()
		mode := p.mode
		p.mu.Unlock()
		if mode == modePlan {
			return map[string]any{}
		}
		decided()
		return hookDecision(rules.Allow, d.Reason)
	}
	return map[string]any{}
}

func hookDecision(a rules.Action, reason string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       string(a),
		"permissionDecisionReason": reason,
	}}
}
