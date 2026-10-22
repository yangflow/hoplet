package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func hookProcess(t *testing.T, pushed ...rules.Rule) *process {
	t.Helper()
	env := rules.Env{Home: "/home/u", HopletHome: "/home/u/.hoplet", Tmp: "/tmp"}
	p := newProcess(&Driver{}, nil, nil, nil)
	p.dir = "/ws"
	p.threadID = "thr"
	p.rules = func(runtime.StartSpec) (*rules.Engine, error) { return rules.New(env, nil, pushed) }
	return p
}

func rule(id, tool string, a rules.Action, m rules.Match) rules.Rule {
	return rules.Rule{ID: id, Tool: tool, Match: m, Action: a, Scope: rules.ScopeGlobal, Note: id, Enabled: true}
}

func bashInput(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_use_id": "tu1",
		"session_id": "claimed-session", "tool_input": map[string]any{"command": cmd},
	})
	return b
}

func nextEv(t *testing.T, p *process) runtime.ProcessEvent {
	t.Helper()
	select {
	case ev := <-p.events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return nil
	}
}

func TestDecideHookAllowDenyAndAutoEvent(t *testing.T) {
	p := hookProcess(t,
		rule("allow-ls", "run", rules.Allow, rules.Match{Command: "ls"}),
		rule("deny-rm", "run", rules.Deny, rules.Match{Command: "rm"}),
	)
	if r := p.decideHook(context.Background(), bashInput("ls -l")); r.Decision != hookAllow {
		t.Fatalf("%+v", r)
	}
	ev := nextEv(t, p).(runtime.RuleDecided)
	if ev.Decision != "allow" || ev.RuleID != "allow-ls" || ev.ToolUseID != "tu1" {
		t.Errorf("%+v", ev)
	}
	r := p.decideHook(context.Background(), bashInput("rm -rf x"))
	if r.Decision != hookDeny || r.Reason == "" {
		t.Fatalf("%+v", r)
	}
	if ev := nextEv(t, p).(runtime.RuleDecided); ev.Decision != "deny" || ev.RuleID != "deny-rm" {
		t.Errorf("%+v", ev)
	}
}

func TestDecideHookFloorDeniesWithoutPushedRules(t *testing.T) {
	p := hookProcess(t)
	if r := p.decideHook(context.Background(), bashInput("rm -rf /")); r.Decision != hookDeny {
		t.Errorf("%+v", r)
	}
}

func TestDecideHookFailsClosed(t *testing.T) {
	p := hookProcess(t)
	for name, in := range map[string]string{
		"not json":     `x`,
		"no tool":      `{"hook_event_name":"PreToolUse"}`,
		"other event":  `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`,
		"array input":  `{"tool_name":"Bash","tool_input":[1]}`,
		"string input": `{"tool_name":"Bash","tool_input":"ls"}`,
	} {
		if r := p.decideHook(context.Background(), json.RawMessage(in)); r.Decision != hookDeny || r.Reason == "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	p.rules = nil
	if r := p.decideHook(context.Background(), bashInput("ls")); r.Decision != hookDeny {
		t.Errorf("no rules: %+v", r)
	}
	p.rules = func(runtime.StartSpec) (*rules.Engine, error) { return nil, context.Canceled }
	if r := p.decideHook(context.Background(), bashInput("ls")); r.Decision != hookDeny {
		t.Errorf("rules error: %+v", r)
	}
	p.rules = func(runtime.StartSpec) (*rules.Engine, error) { panic("boom") }
	if r := p.decideHook(context.Background(), bashInput("ls")); r.Decision != hookDeny {
		t.Errorf("panic: %+v", r)
	}
}

func TestDecideHookAskHoldsTheHookUntilAnswered(t *testing.T) {
	p := hookProcess(t, rule("ask-git", "run", rules.Ask, rules.Match{Command: "git push"}))
	for _, tc := range []struct {
		allow bool
		want  string
	}{{true, hookAllow}, {false, hookDeny}} {
		done := make(chan hookReply, 1)
		go func() { done <- p.decideHook(context.Background(), bashInput("git push")) }()
		if ev := nextEv(t, p).(runtime.RuleDecided); ev.Decision != "ask" {
			t.Fatalf("%+v", ev)
		}
		pr := nextEv(t, p).(runtime.PermissionRequest)
		if pr.Tool != "commandExecution" || pr.ToolUseID != "tu1" || len(pr.RequestID) < 5 || pr.RequestID[:5] != hookAskPrefix {
			t.Fatalf("%+v", pr)
		}
		select {
		case r := <-done:
			t.Fatalf("answered before the user: %+v", r)
		case <-time.After(50 * time.Millisecond):
		}
		if err := p.Answer(pr.RequestID, runtime.Verdict{Allow: tc.allow, Message: "no thanks"}); err != nil {
			t.Fatal(err)
		}
		r := <-done
		if r.Decision != tc.want || (!tc.allow && r.Reason != "no thanks") {
			t.Errorf("allow=%v: %+v", tc.allow, r)
		}
		if err := p.Answer(pr.RequestID, runtime.Verdict{Allow: true}); err == nil {
			t.Error("a second answer was accepted")
		}
	}
}

func TestDecideHookAskTimeoutAndInterruptDeny(t *testing.T) {
	p := hookProcess(t, rule("ask-git", "run", rules.Ask, rules.Match{Command: "git push"}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if r := p.decideHook(ctx, bashInput("git push")); r.Decision != hookDeny {
		t.Errorf("timeout: %+v", r)
	}
	var cancelled bool
	for len(p.events) > 0 {
		if _, ok := (<-p.events).(runtime.PermissionCancelled); ok {
			cancelled = true
		}
	}
	if !cancelled {
		t.Error("the request was not cancelled")
	}

	done := make(chan hookReply, 1)
	go func() { done <- p.decideHook(context.Background(), bashInput("git push")) }()
	for {
		if _, ok := nextEv(t, p).(runtime.PermissionRequest); ok {
			break
		}
	}
	p.endHookAsks()
	if r := <-done; r.Decision != hookDeny {
		t.Errorf("interrupt: %+v", r)
	}
}

func TestApprovalAtTheHookIsRememberedForCodexsOwnRequest(t *testing.T) {
	var sent bytes.Buffer
	p := hookProcess(t, rule("allow-ls", "run", rules.Allow, rules.Match{Command: "ls"}))
	p.stdin = nopWriteCloser{&sent}
	if r := p.decideHook(context.Background(), bashInput("ls")); r.Decision != hookAllow {
		t.Fatalf("%+v", r)
	}
	p.handleRequest(json.RawMessage("7"), "item/commandExecution/requestApproval", json.RawMessage(`{"itemId":"tu1","command":"ls"}`))
	if !strings.Contains(sent.String(), `"decision":"accept"`) || len(p.requests) != 0 {
		t.Errorf("sent %q, requests %v", sent.String(), p.requests)
	}
	for len(p.events) > 0 {
		if _, ok := (<-p.events).(runtime.PermissionRequest); ok {
			t.Error("the user was asked again")
		}
	}
	// The approval is used once, and another item is still asked about.
	p.handleRequest(json.RawMessage("8"), "item/commandExecution/requestApproval", json.RawMessage(`{"itemId":"tu1","command":"ls"}`))
	p.handleRequest(json.RawMessage("9"), "item/commandExecution/requestApproval", json.RawMessage(`{"itemId":"other","command":"ls"}`))
	if len(p.requests) != 2 {
		t.Errorf("requests %v", p.requests)
	}
}

func TestUserApprovalAtTheHookIsRemembered(t *testing.T) {
	p := hookProcess(t, rule("ask-git", "run", rules.Ask, rules.Match{Command: "git push"}))
	done := make(chan hookReply, 1)
	go func() { done <- p.decideHook(context.Background(), bashInput("git push")) }()
	nextEv(t, p)
	pr := nextEv(t, p).(runtime.PermissionRequest)
	if err := p.Answer(pr.RequestID, runtime.Verdict{Allow: true}); err != nil {
		t.Fatal(err)
	}
	<-done
	if !p.takeHookOK("tu1") {
		t.Error("the approval was not remembered")
	}
	if p.takeHookOK("tu1") {
		t.Error("the approval was used twice")
	}
}

func TestDeniedAtTheHookIsNotRemembered(t *testing.T) {
	p := hookProcess(t, rule("deny-rm", "run", rules.Deny, rules.Match{Command: "rm"}))
	p.decideHook(context.Background(), bashInput("rm x"))
	if p.takeHookOK("tu1") {
		t.Error("a denied call was remembered")
	}
	q := hookProcess(t, rule("allow-ls", "run", rules.Allow, rules.Match{Command: "ls"}))
	q.cfg[optCollaboration] = collabPlan
	q.decideHook(context.Background(), bashInput("ls"))
	if q.takeHookOK("tu1") {
		t.Error("a plan-mode pass was remembered")
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestDecideHookPlanLeavesAllowToCodex(t *testing.T) {
	p := hookProcess(t, rule("allow-ls", "run", rules.Allow, rules.Match{Command: "ls"}))
	p.cfg[optCollaboration] = collabPlan
	if r := p.decideHook(context.Background(), bashInput("ls")); r.Decision != hookPass {
		t.Errorf("%+v", r)
	}
}

func TestDecideHookTakesTheSessionFromTheProcess(t *testing.T) {
	var got runtime.StartSpec
	p := hookProcess(t)
	p.rules = func(s runtime.StartSpec) (*rules.Engine, error) {
		got = s
		return hookProcess(t).rules(s)
	}
	p.decideHook(context.Background(), bashInput("ls"))
	if got.SessionID != "thr" || got.Dir != "/ws" {
		t.Errorf("%+v", got)
	}
}

func TestChecksThatTheHookRan(t *testing.T) {
	p := hookProcess(t)
	p.checkHookRan("item1")
	if !p.hookWarned {
		t.Error("no warning without a hook call")
	}
	if ev, ok := nextEv(t, p).(runtime.RulesUnenforced); !ok || ev.ToolUseID != "item1" {
		t.Errorf("no event for the call that ran: %+v", ev)
	}
	p.checkHookRan("item2")
	if len(p.events) != 0 {
		t.Error("told more than once")
	}
	q := hookProcess(t)
	q.decideHook(context.Background(), bashInput("ls"))
	for len(q.events) > 0 {
		<-q.events
	}
	q.checkHookRan("item1")
	if q.hookWarned || len(q.events) != 0 {
		t.Error("warned although the hook ran")
	}
}
