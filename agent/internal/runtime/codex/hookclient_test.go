package codex

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/rules"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func runClient(t *testing.T, stdin string, e map[string]string, timeout time.Duration) map[string]any {
	t.Helper()
	var out bytes.Buffer
	RunHook(strings.NewReader(stdin), &out, env(e), timeout)
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	return m
}

func decision(m map[string]any) (string, string) {
	h, _ := m["hookSpecificOutput"].(map[string]any)
	if h == nil {
		return "", ""
	}
	if h["hookEventName"] != "PreToolUse" {
		return "bad event", ""
	}
	d, _ := h["permissionDecision"].(string)
	r, _ := h["permissionDecisionReason"].(string)
	return d, r
}

func TestRunHookFailsClosed(t *testing.T) {
	s := startServer(t)
	nonce, _ := newNonce()
	s.register(nonce, &fakeTarget{reply: hookReply{Decision: hookAllow}})
	good := map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}
	cases := map[string]struct {
		in  string
		env map[string]string
	}{
		"no env":         {`{"tool_name":"Bash"}`, nil},
		"no nonce":       {`{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path()}},
		"bad json":       {`nope`, good},
		"empty":          {``, good},
		"no daemon":      {`{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path() + ".gone", HookNonceEnv: nonce}},
		"unknown nonce":  {`{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path(), HookNonceEnv: "x"}},
		"oversize input": {`{"x":"` + strings.Repeat("a", MaxHookRequest) + `"}`, good},
	}
	for name, c := range cases {
		d, r := decision(runClient(t, c.in, c.env, 5*time.Second))
		if d != "deny" || r == "" {
			t.Errorf("%s: %q %q", name, d, r)
		}
	}
}

func TestRunHookAllowsDeniesAndPasses(t *testing.T) {
	s := startServer(t)
	nonce, _ := newNonce()
	s.register(nonce, &fakeTarget{reply: hookReply{Decision: hookDeny, Reason: "why"}})
	if d, r := decision(runClient(t, `{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}, 5*time.Second)); d != "deny" || r != "why" {
		t.Errorf("deny: %q %q", d, r)
	}
	// Codex does not support an allow from a hook, so an allow prints nothing at all.
	nonce, _ = newNonce()
	s.register(nonce, &fakeTarget{reply: hookReply{Decision: hookAllow}})
	var out bytes.Buffer
	RunHook(strings.NewReader(`{"tool_name":"Bash"}`), &out, env(map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}), 5*time.Second)
	if out.Len() != 0 {
		t.Errorf("allow printed %q", out.String())
	}
	nonce, _ = newNonce()
	s.register(nonce, &fakeTarget{reply: hookReply{Decision: hookPass}})
	m := runClient(t, `{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}, 5*time.Second)
	if len(m) != 0 {
		t.Errorf("pass printed %v", m)
	}
}

func TestRunHookTimeoutDenies(t *testing.T) {
	s := startServer(t)
	tg := &fakeTarget{block: make(chan struct{}), got: make(chan json.RawMessage, 1)}
	defer close(tg.block)
	nonce, _ := newNonce()
	s.register(nonce, tg)
	start := time.Now()
	d, r := decision(runClient(t, `{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}, 300*time.Millisecond))
	if d != "deny" || r == "" || time.Since(start) > 5*time.Second {
		t.Errorf("%q %q after %v", d, r, time.Since(start))
	}
}

func TestHookCommandQuotesThePath(t *testing.T) {
	if got := HookCommand("/a b/it's/hoplet-agent"); got != `'/a b/it'\''s/hoplet-agent' hook codex` {
		t.Error(got)
	}
}

func TestHookConfigShape(t *testing.T) {
	b, _ := json.Marshal(hookConfig("/x/hoplet-agent hook codex"))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["bypass_hook_trust"] != true {
		t.Error("trust not bypassed")
	}
	h := m["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	cmd := h["hooks"].([]any)[0].(map[string]any)
	if h["matcher"] != ".*" || cmd["type"] != "command" || cmd["command"] != "/x/hoplet-agent hook codex" || cmd["timeout"] != float64(900) {
		t.Errorf("%v", h)
	}
}

func TestTheDeadlinesLeaveRoomBeforeCodexTimesOut(t *testing.T) {
	codex := time.Duration(hookTimeoutSeconds) * time.Second
	if !(hookAskTimeout < HookClientTimeout && HookClientTimeout < codex) {
		t.Errorf("ask %v, client %v, codex %v", hookAskTimeout, HookClientTimeout, codex)
	}
}

// A daemon that never answers still gets a deny from the hook before Codex's own timeout, which would let the
// call through.
func TestRunHookPrintsADenyBeforeItsTimeoutWhenTheDaemonNeverAnswers(t *testing.T) {
	s := startServer(t)
	s.askTimeout = time.Hour
	tg := &fakeTarget{block: make(chan struct{}), got: make(chan json.RawMessage, 1)}
	defer close(tg.block)
	nonce, _ := newNonce()
	s.register(nonce, tg)
	const timeout = 300 * time.Millisecond
	start := time.Now()
	d, r := decision(runClient(t, `{"tool_name":"Bash"}`, map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}, timeout))
	if d != "deny" || r == "" {
		t.Errorf("%q %q", d, r)
	}
	if took := time.Since(start); took > timeout+2*time.Second {
		t.Errorf("the deny came after %v", took)
	}
}

// An ask nobody answers is resolved by the daemon, before the client gives up.
func TestAnUnansweredAskBecomesADenyInTime(t *testing.T) {
	s := startServer(t)
	s.askTimeout = 200 * time.Millisecond
	p := hookProcess(t, rule("ask-git", "run", rules.Ask, rules.Match{Command: "git push"}))
	nonce, _ := newNonce()
	s.register(nonce, p)
	d, r := decision(runClient(t, string(bashInput("git push")), map[string]string{HookSockEnv: s.Path(), HookNonceEnv: nonce}, 10*time.Second))
	if d != "deny" || r != "no answer in time" {
		t.Errorf("%q %q", d, r)
	}
}
