package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	gosync "sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

func TestMain(m *testing.M) {
	code := m.Run()
	runtimetest.Cleanup()
	os.Exit(code)
}

type sink struct {
	mu   gosync.Mutex
	msgs []any
}

func (s *sink) send(msg any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
	return nil
}

func (s *sink) states(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.msgs {
		if st, ok := m.(sync.RuntimeState); ok && st.SessionID == id {
			out = append(out, st.State)
		}
	}
	return out
}

func (s *sink) waitState(t *testing.T, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.states(id); len(st) > 0 && st[len(st)-1] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("states of %s = %v, want %s", id, s.states(id), want)
}

func newTestBridge(t *testing.T) (*bridge, *sink, string, string) {
	t.Helper()
	bin := runtimetest.Build(t)
	projects := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("FAKE_CLAUDE_PROJECTS", projects)
	file := runtimetest.Session(t, projects, workdir, "s1")
	cfg := &config.Config{
		ClaudeBin:               bin,
		ClaudeProjects:          projects,
		HopletHome:              t.TempDir(),
		RuntimeIdleMinutes:      30,
		RuntimeMaxIdleProcesses: 3,
	}
	s := &sink{}
	ownGrace = 50 * time.Millisecond
	t.Cleanup(func() { ownGrace = 0 })
	b := newBridge(cfg, s.send, func() {}, func(string) {}, nil)
	t.Cleanup(b.close)
	return b, s, file, workdir
}

func TestBridgeRelaysAPromptAsRuntimeMessages(t *testing.T) {
	b, s, _, _ := newTestBridge(t)
	h := b.hooks()
	if err := h.Prompt("s1", "say hello", nil); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "ready")

	s.mu.Lock()
	defer s.mu.Unlock()
	var states []string
	var text string
	for _, m := range s.msgs {
		switch m := m.(type) {
		case sync.RuntimeState:
			states = append(states, m.State)
			if m.V != sync.ProtocolVersion || m.Type != sync.TypeRuntimeState || !m.Capabilities.Permissions ||
				!m.Capabilities.Interrupt || len(m.Capabilities.Config) != 4 {
				t.Fatalf("state message = %+v", m)
			}
		case sync.RuntimeOutput:
			if m.Type != sync.TypeRuntimeOutput {
				t.Fatalf("output message = %+v", m)
			}
			if m.Kind == "text" {
				text += m.Text
			}
		}
	}
	if len(states) != 3 || states[0] != "starting" || states[1] != "running" || states[2] != "ready" {
		t.Fatalf("states = %v", states)
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
}

func TestBridgeRefusesAndAnswers(t *testing.T) {
	b, s, _, _ := newTestBridge(t)
	h := b.hooks()
	if err := h.Prompt("nope", "say x", nil); err == nil {
		t.Fatal("a session that is not on this device was accepted")
	}
	if err := h.Prompt("s1", "tool echo hi", nil); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "waiting-permission")
	if err := h.Prompt("s1", "say again", nil); err == nil || err.Error() != "busy" {
		t.Fatalf("second prompt: %v", err)
	}
	var req sync.RuntimePermission
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, m := range s.msgs {
			if p, ok := m.(sync.RuntimePermission); ok {
				req = p
			}
		}
		s.mu.Unlock()
		if req.RequestID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if req.RequestID == "" || req.Tool != "Bash" {
		t.Fatalf("permission = %+v", req)
	}
	var in map[string]any
	if err := json.Unmarshal(req.Input, &in); err != nil || in["command"] != "echo hi" {
		t.Fatalf("input = %s", req.Input)
	}

	// A reconnect reports the open request again.
	before := len(b.mgr.Pending())
	b.report()
	if before != 1 {
		t.Fatalf("pending = %d", before)
	}
	if err := h.Answer("s1", req.RequestID, true, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.Answer("s1", req.RequestID, true, "", ""); !errors.Is(err, runtime.ErrUnknownRequest) {
		t.Fatalf("second answer: %v", err)
	}
	s.waitState(t, "s1", "ready")
}

func TestBridgeForwardsAPlanExitWithItsFreshContext(t *testing.T) {
	b, s, _, _ := newTestBridge(t)
	if err := b.hooks().Prompt("s1", "exitplan 1. do it", nil); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "waiting-permission")
	var req sync.RuntimePermission
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && req.RequestID == "" {
		s.mu.Lock()
		for _, m := range s.msgs {
			if p, ok := m.(sync.RuntimePermission); ok {
				req = p
			}
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	if pe := req.PlanExit; pe == nil || pe.Plan != "1. do it" || !pe.FreshContext {
		t.Fatalf("plan exit = %+v", pe)
	}
}

func TestBridgeReportsRunningProcessesAfterAReconnect(t *testing.T) {
	b, s, _, _ := newTestBridge(t)
	h := b.hooks()
	if err := h.Prompt("s1", "say hi", nil); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "ready")
	s.mu.Lock()
	s.msgs = nil
	s.mu.Unlock()
	b.report()
	s.waitState(t, "s1", "ready")
	if got := s.states("s1"); len(got) != 1 {
		t.Fatalf("report = %v", got)
	}
}

func TestBridgeNoticesForeignWritesFromScannedSizes(t *testing.T) {
	b, s, file, _ := newTestBridge(t)
	h := b.hooks()
	if err := h.Prompt("s1", "say hi", nil); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "ready")
	src := sizedSource{id: "s1", path: file}
	b.observe(context.Background(), []provider.Source{src})
	if got := b.mgr.State("s1"); got != runtime.StateReady {
		t.Fatalf("own writes: state = %s", got)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond) // past the grace after the turn result
	_, _ = f.WriteString("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"terminal\"}}\n")
	_ = f.Close()
	b.observe(context.Background(), []provider.Source{src})
	s.waitState(t, "s1", "external")
	if err := h.Prompt("s1", "say no", nil); !errors.Is(err, runtime.ErrExternal) {
		t.Fatalf("prompt: %v", err)
	}
}

func TestBridgeStopsAProcessBeforeHandoff(t *testing.T) {
	b, s, _, _ := newTestBridge(t)
	if err := b.hooks().Prompt("s1", "say hi", nil); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "ready")
	if err := b.stopSession("s1"); err != nil {
		t.Fatal(err)
	}
	s.waitState(t, "s1", "idle")
}

type sizedSource struct{ id, path string }

func (s sizedSource) ID() string            { return s.id }
func (s sizedSource) Dir() string           { return "" }
func (s sizedSource) Cwd() string           { return "" }
func (s sizedSource) InProject(string) bool { return false }
func (s sizedSource) Read(context.Context, int64, int64) ([]byte, error) {
	return nil, nil
}
func (s sizedSource) Size(context.Context) (int64, error) {
	st, err := os.Stat(s.path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
