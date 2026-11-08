package codex

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

func mcpSpec(token string) harness.Spec {
	return harness.Spec{
		WorkDir: "/w", Dir: "/sessions/s1", Env: []string{"A=1"},
		MCP: []harness.MCPServer{{Name: "hoplet", Command: []string{"/bin/hoplet", "mcp", "--home", "/h"}, Env: []string{tools.TokenEnv + "=" + token}}},
	}
}

func TestLaunchInjectsToolsWithoutTheToken(t *testing.T) {
	h := &Harness{Binary: "/bin/codex"}
	l, err := h.Launch(mcpSpec("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/sessions/s1", "mcp-hoplet.token")
	want := []string{
		"/bin/codex", "app-server",
		"-c", `mcp_servers.hoplet.command="/bin/hoplet"`,
		"-c", `mcp_servers.hoplet.args=["mcp","--home","/h","--token-file","` + path + `"]`,
		"-c", `mcp_servers.hoplet.default_tools_approval_mode="approve"`,
	}
	if !slices.Equal(l.Command, want) {
		t.Fatalf("command:\n%q\nwant\n%q", l.Command, want)
	}
	if got := string(l.Files["mcp-hoplet.token"]); got != "secret-1" {
		t.Fatalf("token file: %q", got)
	}
	for _, s := range append(slices.Clone(l.Command), l.Env...) {
		if strings.Contains(s, "secret-1") || strings.Contains(s, tools.TokenEnv) {
			t.Fatalf("the token reached the process: %q", s)
		}
	}
	next, err := h.Launch(mcpSpec("secret-2"))
	if err != nil || string(next.Files["mcp-hoplet.token"]) != "secret-2" {
		t.Fatalf("a new launch keeps the old token: %q, %v", next.Files["mcp-hoplet.token"], err)
	}
}

func TestLaunchRefusesWhatItCannotInject(t *testing.T) {
	cases := map[string]func(*harness.Spec){
		"no session dir": func(s *harness.Spec) { s.Dir = "" },
		"a bad name":     func(s *harness.Spec) { s.MCP[0].Name = `a.b="c"` },
		"no command":     func(s *harness.Spec) { s.MCP[0].Command = nil },
		"no token":       func(s *harness.Spec) { s.MCP[0].Env = nil },
		"another var":    func(s *harness.Spec) { s.MCP[0].Env = append(s.MCP[0].Env, "API_KEY=x") },
	}
	for name, change := range cases {
		spec := mcpSpec("t")
		change(&spec)
		if _, err := (&Harness{Binary: "/bin/codex"}).Launch(spec); err == nil {
			t.Errorf("%s: launched", name)
		}
	}
}

func TestPersonaIsForNewThreadsOnly(t *testing.T) {
	cases := []struct {
		name   string
		spec   harness.Spec
		method string
		want   string
	}{
		{"new", harness.Spec{Persona: "be brief", Profile: "lives in Oslo"}, "thread/start", `"developerInstructions":"be brief\n\nlives in Oslo"`},
		{"persona alone", harness.Spec{Persona: "be brief"}, "thread/start", `"developerInstructions":"be brief"`},
		{"resumed", harness.Spec{Persona: "be brief", Resume: "t1"}, "thread/resume", ""},
		{"none", harness.Spec{}, "thread/start", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &replayer{t: t, io: newFakeStdio()}
			c.spec.WorkDir, c.spec.Mode = "/w", proto.ModeFullAuto
			p.attach(c.spec)
			defer p.run.Close()
			p.out(`{"id":"hoplet-x-1","result":{}}`)
			got := p.lastWrite()
			if !strings.Contains(got, c.method) {
				t.Fatalf("sent %s", got)
			}
			if c.want == "" && strings.Contains(got, "developerInstructions") || !strings.Contains(got, c.want) {
				t.Fatalf("sent %s, want %s", got, c.want)
			}
		})
	}
}

func TestKeepsPersona(t *testing.T) {
	var h harness.Harness = &Harness{}
	if k, ok := h.(harness.PersonaKeeper); !ok || !k.KeepsPersona() {
		t.Fatal("a resumed thread keeps its instructions")
	}
	if _, ok := h.(harness.MCPPersister); ok {
		t.Fatal("the token must be issued anew for every launch")
	}
	if !caps().MCPInject {
		t.Fatal("tools are injected")
	}
}
