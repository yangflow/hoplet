package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func mcpSpec(release func()) *runtime.MCPSpec {
	return &runtime.MCPSpec{
		Command: "/usr/local/bin/hoplet-agent", Args: []string{"mcp"}, Nonce: "n0nce",
		Env:         map[string]string{"HOPLET_MCP_SOCK": "/h/mcp.sock", "HOPLET_MCP_NONCE": "n0nce"},
		ReadAllowed: true, Release: release,
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

func TestArgsAddOnlyTheMCPConfigFile(t *testing.T) {
	d := New(Options{HopletHome: "/h"})
	args := d.Args(runtime.StartSpec{SessionID: "abc", MCP: mcpSpec(func() {})})
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--mcp-config /h/mcp/n0nce.json --resume abc") {
		t.Fatalf("args = %q", got)
	}
	if strings.Contains(got, "n0nce ") || strings.Contains(got, "strict-mcp-config") || strings.Contains(got, "hoplet-agent") {
		t.Fatalf("args carry more than the file path: %q", got)
	}
	if strings.Contains(strings.Join(d.Args(runtime.StartSpec{SessionID: "abc"}), " "), "--mcp-config") {
		t.Fatal("no MCP spec, no flag")
	}
}

func TestMCPConfigFileIsPrivateAndNamesTheServer(t *testing.T) {
	home := shortDir(t)
	spec := mcpSpec(func() {})
	if err := writeMCPConfig(home, spec); err != nil {
		t.Fatal(err)
	}
	path := mcpConfigPath(home, spec)
	if st, _ := os.Stat(path); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v", st)
	}
	if st, _ := os.Stat(filepath.Dir(path)); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v", st)
	}
	raw, _ := os.ReadFile(path)
	var cfg struct {
		McpServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	s, ok := cfg.McpServers["hoplet"]
	if !ok || len(cfg.McpServers) != 1 || s.Type != "stdio" || s.Command != spec.Command ||
		len(s.Args) != 1 || s.Args[0] != "mcp" || s.Env["HOPLET_MCP_NONCE"] != "n0nce" || s.Env["HOPLET_MCP_SOCK"] != "/h/mcp.sock" {
		t.Fatalf("config = %s", raw)
	}
	if err := writeMCPConfig("", spec); err == nil {
		t.Fatal("a config without a state directory is refused")
	}
}

func TestStartFailureRemovesTheConfigAndReleases(t *testing.T) {
	home := shortDir(t)
	released := 0
	spec := mcpSpec(func() { released++ })
	_, err := New(Options{Bin: filepath.Join(home, "no-such-claude"), HopletHome: home}).
		Start(context.Background(), runtime.StartSpec{SessionID: "s", Dir: home, MCP: spec})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if _, err := os.Stat(mcpConfigPath(home, spec)); !os.IsNotExist(err) {
		t.Fatalf("config remains: %v", err)
	}
	if released != 1 {
		t.Fatalf("released = %d: a failed start ends the registration", released)
	}
}

func TestProcessExitRemovesTheConfigAndReleases(t *testing.T) {
	home := shortDir(t)
	released := 0
	spec := mcpSpec(func() { released++ })
	opts := Options{HopletHome: home}
	bin, _, _ := hookScript(t, nil)
	opts.Bin = bin
	p, err := New(opts).Start(context.Background(), runtime.StartSpec{SessionID: "s", Dir: home, MCP: spec})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, p)
	if released != 1 {
		t.Fatalf("released = %d", released)
	}
	if _, err := os.Stat(mcpConfigPath(home, spec)); !os.IsNotExist(err) {
		t.Fatalf("config remains: %v", err)
	}
}

func mcpHook(t *testing.T, opts Options, withMCP bool, tool string) (string, string) {
	t.Helper()
	home := shortDir(t)
	opts.HopletHome = home
	spec := runtime.StartSpec{}
	if withMCP {
		spec.MCP = mcpSpec(func() {})
	}
	_, answers := runHooks(t, opts, spec, []string{hookRequest("r1", hookCallbackID, tool, `{}`)})
	return decisionOf(t, answers["r1"])
}

func TestReadToolsOfTheHopletServerAreAllowedWithoutAPrompt(t *testing.T) {
	for _, tool := range []string{"list_machines", "list_sessions", "read_session", "get_config", "list_skills", "read_skill", "read_memory", "get_change"} {
		d, _ := mcpHook(t, Options{}, true, "mcp__hoplet__"+tool)
		if d != "allow" {
			t.Fatalf("%s: decision %q", tool, d)
		}
	}
}

func TestOnlyReadToolsOfThisServerInAProcessWithIt(t *testing.T) {
	cases := []struct {
		name    string
		withMCP bool
		tool    string
	}{
		{"a tool that is not a read tool", true, "mcp__hoplet__send_message"},
		{"a write tool", true, "mcp__hoplet__propose_memory_change"},
		{"another server", true, "mcp__github__read_session"},
		{"a process without the server", false, "mcp__hoplet__read_session"},
	}
	for _, c := range cases {
		if d, _ := mcpHook(t, Options{}, c.withMCP, c.tool); d != "" {
			t.Fatalf("%s: decision %q, want none (the vendor asks)", c.name, d)
		}
	}
}

func TestADenyRuleBeatsTheReadToolAllow(t *testing.T) {
	opts := Options{Rules: func(runtime.StartSpec) (*rules.Engine, error) {
		return rules.New(rules.NewEnv("/home/u", "/home/u/.hoplet"), nil, append(rules.Defaults(), rules.Rule{
			ID: "user.nomcp", Tool: "mcp:hoplet/read_session", Action: rules.Deny,
			Scope: rules.ScopeGlobal, Note: "no reading", Enabled: true,
		}))
	}}
	if d, _ := mcpHook(t, opts, true, "mcp__hoplet__read_session"); d != "deny" {
		t.Fatalf("decision %q", d)
	}
	if d, _ := mcpHook(t, opts, true, "mcp__hoplet__list_sessions"); d != "allow" {
		t.Fatalf("decision %q", d)
	}
}
