package runtime_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/runtime/claude"
	"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"
)

// A process of a driver with the mcp capability starts with the MCP spec of its session: the config file is on the
// command line (the nonce is not), and the file and the registration end with the process.
func TestManagerStartsAProcessWithItsMCPSpec(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	var asked []string
	var released atomic.Int32
	e := newEnv(t, func(c *runtime.Config) {
		c.Driver = claude.New(claude.Options{Bin: runtimetest.Build(t), HopletHome: home})
		c.MCP = func(id string) *runtime.MCPSpec {
			asked = append(asked, id)
			return &runtime.MCPSpec{
				Command: "/bin/hoplet-agent", Args: []string{"mcp"}, Nonce: "nonce-" + id,
				Env: map[string]string{"HOPLET_MCP_SOCK": "/s", "HOPLET_MCP_NONCE": "nonce-" + id}, ReadAllowed: true,
				Release: func() { released.Add(1) },
			}
		}
	})
	e.session("s1")
	if err := e.m.Prompt("s1", "say hello there"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	if len(asked) != 1 || asked[0] != "s1" {
		t.Fatalf("asked = %v", asked)
	}
	starts := e.starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	args := strings.Join(starts[0].Args, " ")
	cfgPath := filepath.Join(home, "mcp", "nonce-s1.json")
	if !strings.Contains(args, "--mcp-config "+cfgPath) || strings.Contains(args, "--strict-mcp-config") {
		t.Fatalf("args = %s", args)
	}
	if strings.Contains(args, "nonce-s1 ") || strings.HasSuffix(args, "nonce-s1") {
		t.Fatalf("the nonce is on the command line: %s", args)
	}
	st, err := os.Stat(cfgPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v %v", st, err)
	}
	dir, err := os.Stat(filepath.Dir(cfgPath))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("config dir: %v %v", dir, err)
	}

	ctx, cancel := contextTimeout(5 * time.Second)
	defer cancel()
	e.m.Shutdown(ctx)
	e.rec.waitFor(t, "the registration is released", func() bool { return released.Load() == 1 })
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("the config file outlives the process: %v", err)
	}
}

// Without Config.MCP a process starts without the flag.
func TestManagerWithoutMCPStartsWithoutTheFlag(t *testing.T) {
	e := newEnv(t, nil)
	e.session("s1")
	if err := e.m.Prompt("s1", "say hello there"); err != nil {
		t.Fatal(err)
	}
	e.rec.waitState(t, "s1", runtime.StateReady)
	if args := strings.Join(e.starts()[0].Args, " "); strings.Contains(args, "--mcp-config") {
		t.Fatalf("args = %s", args)
	}
}
