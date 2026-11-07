package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

var testMCPCommand = []string{"/opt/hoplet", "mcp", "--home", "/h"}

// mcpHarness is the fake harness taking MCP servers; it keeps the specs
// and launches the manager gave it, and writes its MCP servers into a
// launch file the way a real adapter does.
type mcpHarness struct {
	*fake.Harness
	persist bool

	mu       sync.Mutex
	launches []harness.Spec
	launched []harness.Launch
	attaches []harness.Spec
}

func (h *mcpHarness) Probe(ctx context.Context) (harness.Info, error) {
	info, err := h.Harness.Probe(ctx)
	info.Capabilities.MCPInject = true
	return info, err
}

func (h *mcpHarness) Launch(spec harness.Spec) (harness.Launch, error) {
	l, err := h.Harness.Launch(spec)
	if len(spec.MCP) > 0 {
		l.Files = map[string][]byte{"mcp.json": []byte(strings.Join(spec.MCP[0].Env, "\n"))}
	}
	h.mu.Lock()
	h.launches, h.launched = append(h.launches, spec), append(h.launched, l)
	h.mu.Unlock()
	return l, err
}

func (h *mcpHarness) Attach(ctx context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	h.mu.Lock()
	h.attaches = append(h.attaches, spec)
	h.mu.Unlock()
	return h.Harness.Attach(ctx, spec, io)
}

func (h *mcpHarness) PersistsMCP() bool { return h.persist }

func (h *mcpHarness) specs() (launches, attaches []harness.Spec, launched []harness.Launch) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.launches), slices.Clone(h.attaches), slices.Clone(h.launched)
}

// token is the session token in an injected server.
func token(t *testing.T, servers []harness.MCPServer) string {
	t.Helper()
	if len(servers) != 1 || servers[0].Name != "hoplet" || !slices.Equal(servers[0].Command, testMCPCommand) || len(servers[0].Env) != 1 {
		t.Fatalf("servers = %+v", servers)
	}
	tok, ok := strings.CutPrefix(servers[0].Env[0], tools.TokenEnv+"=")
	if !ok || tok == "" {
		t.Fatalf("env = %v", servers[0].Env)
	}
	return tok
}

func (e *env) mcpManager(h *mcpHarness) *Manager {
	e.t.Helper()
	m := e.managerWith(map[string]harness.Harness{"fake": h})
	m.cfg.MCPCommand = testMCPCommand
	return m
}

func (e *env) tokenOwner(tok string) (proto.SessionID, error) {
	return e.store.SessionByToken(context.Background(), store.TokenHash(tok))
}

func TestToolTokenEveryLaunch(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", ResumeTurn: 1, Turns: []fake.Turn{
		{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}},
	}})
	h := &mcpHarness{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	m := e.mcpManager(h)
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "first turn end", turnEnded)

	launches, attaches, launched := h.specs()
	first := token(t, launches[0].MCP)
	if launches[0].Dir != m.dir(sess.ID) {
		t.Fatalf("Dir = %q", launches[0].Dir)
	}
	if id, err := e.tokenOwner(first); err != nil || id != sess.ID {
		t.Fatalf("token owner %q %v", id, err)
	}
	if len(attaches) != 1 || attaches[0].MCP != nil {
		t.Fatalf("attach specs %+v", attaches)
	}
	for _, s := range append(slices.Clone(launched[0].Env), launched[0].Command...) {
		if strings.Contains(s, first) {
			t.Fatalf("token in the harness's environment or command line: %q", s)
		}
	}
	f := filepath.Join(m.dir(sess.ID), "mcp.json")
	if st, err := os.Stat(f); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mcp.json: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(m.dir(sess.ID), "shim.log")); strings.Contains(string(b), first) {
		t.Fatal("token in the shim log")
	}

	// The next launch replaces the token.
	if err := m.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	waitShimGone(t, m, sess.ID)
	if _, err := m.Send(ctx, sess.ID, "two", "k2"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "second turn end", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnEnded) == 2 })
	launches, _, _ = h.specs()
	second := token(t, launches[1].MCP)
	if second == first {
		t.Fatal("same token twice")
	}
	if _, err := e.tokenOwner(first); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old token: %v", err)
	}
	if id, err := e.tokenOwner(second); err != nil || id != sess.ID {
		t.Fatalf("new token owner %q %v", id, err)
	}
}

func TestToolTokenPersisted(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}})
	h := &mcpHarness{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}, persist: true}
	m := e.mcpManager(h)
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	launches, attaches, _ := h.specs()
	if launches[0].MCP != nil {
		t.Fatalf("launch got %+v", launches[0].MCP)
	}
	// A session about to be created gets a token with its creation.
	if id, err := e.tokenOwner(token(t, attaches[0].MCP)); err != nil || id != sess.ID {
		t.Fatalf("token owner %q %v", id, err)
	}

	info, err := h.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spec := m.specFor(sess, false)
	spec.State.Native = "n1"
	if got, err := m.persistedTools(ctx, h, info, spec); err != nil || got != nil {
		t.Fatalf("a created session with a token: %+v %v", got, err)
	}
	other := e.create(m)
	spec = m.specFor(other, false)
	spec.State.Native = "n2"
	got, err := m.persistedTools(ctx, h, info, spec)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := e.tokenOwner(token(t, got)); err != nil || id != other.ID {
		t.Fatalf("a session created before tools: %q %v", id, err)
	}
	notPersisting := &mcpHarness{Harness: h.Harness}
	if got, err := m.persistedTools(ctx, notPersisting, info, m.specFor(other, false)); err != nil || got != nil {
		t.Fatalf("a harness that forgets: %+v %v", got, err)
	}
}

func TestNoToolsWithout(t *testing.T) {
	e := newEnv(t, fake.Script{})
	h := &mcpHarness{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	m := e.mcpManager(h)
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	info, err := h.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	noInject := info
	noInject.Capabilities.MCPInject = false
	ephemeral := m.specFor(sess, false)
	ephemeral.Ephemeral = true
	for name, c := range map[string]struct {
		info    harness.Info
		spec    harness.Spec
		command []string
	}{
		"ephemeral":         {info, ephemeral, testMCPCommand},
		"harness without":   {noInject, m.specFor(sess, false), testMCPCommand},
		"no server command": {info, m.specFor(sess, false), nil},
	} {
		m.cfg.MCPCommand = c.command
		if got, err := m.issueTools(ctx, c.info, c.spec); err != nil || got != nil {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
	}
	if has, err := e.store.HasSessionToken(ctx, sess.ID); err != nil || has {
		t.Fatalf("token issued: %v %v", has, err)
	}
}
