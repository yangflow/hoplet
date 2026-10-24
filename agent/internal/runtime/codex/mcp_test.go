package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// callLog records what the driver did with a spec, safely across the process's goroutines.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *callLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

func (l *callLog) waitFor(t *testing.T, n int) []string {
	t.Helper()
	for i := 0; i < 500 && len(l.get()) < n; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	got := l.get()
	if len(got) < n {
		t.Fatalf("calls = %v", got)
	}
	return got
}

func mcpSpec() (*runtime.MCPSpec, *callLog) {
	l := &callLog{}
	return &runtime.MCPSpec{
		Command: "/usr/local/bin/hoplet-agent", Args: []string{"mcp"}, Nonce: "n0nce",
		Env:         map[string]string{"HOPLET_MCP_SOCK": "/h/mcp.sock", "HOPLET_MCP_NONCE": "n0nce"},
		ReadAllowed: true,
		Bind:        func(id string) { l.add("bind:" + id) },
		Release:     func() { l.add("release") },
	}, l
}

func checkMCPServer(t *testing.T, params map[string]any) {
	t.Helper()
	cfg, _ := params["config"].(map[string]any)
	servers, _ := cfg["mcp_servers"].(map[string]any)
	srv, _ := servers["hoplet"].(map[string]any)
	if srv == nil {
		t.Fatalf("config = %v", params["config"])
	}
	if srv["command"] != "/usr/local/bin/hoplet-agent" || srv["args"].([]any)[0] != "mcp" {
		t.Errorf("server = %v", srv)
	}
	env := srv["env"].(map[string]any)
	if len(env) != 2 || env["HOPLET_MCP_SOCK"] != "/h/mcp.sock" || env["HOPLET_MCP_NONCE"] != "n0nce" {
		t.Errorf("env = %v", env)
	}
	b, _ := json.Marshal(srv)
	if strings.Contains(string(b), "hpt_") {
		t.Error("the server entry carries a token")
	}
}

func TestDriverDeclaresTheMCPCapability(t *testing.T) {
	for _, c := range New(Options{}).Capabilities() {
		if c == runtime.CapMCP {
			return
		}
	}
	t.Fatal("no mcp capability")
}

func TestMCPServerIsInTheConfigOfNewAndResumedThreads(t *testing.T) {
	h := newHarness(t)
	spec, calls := mcpSpec()
	_, p, err := h.d.StartNew(ctx(t), runtime.StartSpec{Dir: t.TempDir(), New: true, MCP: spec})
	if err != nil {
		t.Fatal(err)
	}
	expect[runtime.Ready](t, p)
	checkMCPServer(t, h.received("thread/start")[0])
	if got := calls.get(); len(got) != 1 || got[0] != "bind:thr-new" {
		t.Fatalf("calls = %v", got)
	}
	_ = p.Kill()

	spec2, calls2 := mcpSpec()
	p2, err := h.d.Start(ctx(t), runtime.StartSpec{SessionID: "abc", Dir: t.TempDir(), MCP: spec2})
	if err != nil {
		t.Fatal(err)
	}
	expect[runtime.Ready](t, p2)
	checkMCPServer(t, h.received("thread/resume")[0])
	if got := calls2.get(); len(got) != 1 || got[0] != "bind:abc" {
		t.Fatalf("calls = %v", got)
	}
	_ = p2.Kill()
	if got := calls2.waitFor(t, 2); got[1] != "release" {
		t.Fatalf("calls = %v", got)
	}
}

func TestNoMCPServerWithoutASpec(t *testing.T) {
	h := newHarness(t)
	h.startNew(nil)
	if _, ok := h.received("thread/start")[0]["config"]; ok {
		t.Error("a config was sent without a hook or an MCP spec")
	}
}

func TestMCPServerSitsBesideTheHook(t *testing.T) {
	h, _ := hookHarness(t)
	spec, _ := mcpSpec()
	_, p, err := h.d.StartNew(ctx(t), runtime.StartSpec{Dir: t.TempDir(), New: true, MCP: spec})
	if err != nil {
		t.Fatal(err)
	}
	expect[runtime.Ready](t, p)
	params := h.received("thread/start")[0]
	checkHookInjected(t, params)
	checkMCPServer(t, params)
	_ = p.Kill()
}

func TestMCPSpecIsReleasedWhenTheStartFails(t *testing.T) {
	h := newHarness(t)
	spec, calls := mcpSpec()
	if _, err := h.d.Start(ctx(t), runtime.StartSpec{SessionID: "missing", Dir: t.TempDir(), MCP: spec}); err == nil {
		t.Fatal("a missing thread started")
	}
	if got := calls.waitFor(t, 1); got[0] != "release" {
		t.Fatalf("calls = %v", got)
	}
}

func TestHookAllowsTheReadToolsOfTheHopletServerOnlyWithoutARule(t *testing.T) {
	call := func(tool string) json.RawMessage {
		return json.RawMessage(`{"hook_event_name":"PreToolUse","tool_name":"mcp__hoplet__` + tool + `","tool_use_id":"tu1","tool_input":{}}`)
	}
	p := hookProcess(t)
	p.mcp, _ = mcpSpec()
	if r := p.decideHook(context.Background(), call("list_sessions")); r.Decision != hookAllow {
		t.Errorf("read tool: %+v", r)
	}
	if r := p.decideHook(context.Background(), call("send_prompt")); r.Decision == hookAllow {
		t.Errorf("a tool that is not a read tool was allowed: %+v", r)
	}
	if r := p.decideHook(context.Background(), call("propose_skill_change")); r.Decision == hookAllow {
		t.Errorf("a write tool was allowed: %+v", r)
	}
	q := hookProcess(t)
	if r := q.decideHook(context.Background(), call("list_sessions")); r.Decision == hookAllow {
		t.Errorf("a process without the server was allowed: %+v", r)
	}
	d := hookProcess(t, rule("deny-hoplet", "mcp:hoplet/*", rules.Deny, rules.Match{}))
	d.mcp, _ = mcpSpec()
	if r := d.decideHook(context.Background(), call("list_sessions")); r.Decision != hookDeny {
		t.Errorf("a deny rule lost: %+v", r)
	}
}
