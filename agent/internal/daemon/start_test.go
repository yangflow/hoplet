package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// newStartEnv is a bridge on the fake Claude Code whose store watches one project directory.
func newStartEnv(t *testing.T) (b *bridge, s *sink, st *store, watched string, start func(sync.RuntimeStart) (string, error)) {
	t.Helper()
	bin := runtimetest.Build(t)
	root := t.TempDir()
	watched = t.TempDir()
	t.Setenv("FAKE_CLAUDE_PROJECTS", root)
	home := t.TempDir()
	cfg := &config.Config{
		ClaudeBin:               bin,
		ClaudeProjects:          root,
		HopletHome:               home,
		RuntimeIdleMinutes:      30,
		RuntimeMaxIdleProcesses: 3,
	}
	file := &projects.File{Items: []projects.Item{{Path: watched, Imported: []string{}}}}
	if err := file.Save(home); err != nil {
		t.Fatal(err)
	}
	st = newStore(cfg, file)
	s = &sink{}
	b = newBridge(cfg, s.send, func() {}, func(string) {})
	t.Cleanup(b.close)
	run := b.starter(st)
	start = func(req sync.RuntimeStart) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return run(ctx, req)
	}
	return b, s, st, watched, start
}

func importedIn(t *testing.T, st *store, path string) []string {
	t.Helper()
	loaded, err := projects.Load(st.cfg.HopletHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range loaded.Items {
		if it.Path == projects.Clean(path) {
			return it.Imported
		}
	}
	t.Fatalf("%s is not watched", path)
	return nil
}

func TestStartCreatesAndImportsANewSession(t *testing.T) {
	b, s, st, watched, start := newStartEnv(t)
	on := true
	id, err := start(sync.RuntimeStart{
		ProjectPath: watched,
		Provider:    "claude",
		Options:     sync.RuntimeStartOptions{Model: "sonnet", Thinking: &on},
		Text:        "say first prompt",
		PromptID:    "p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 {
		t.Fatalf("session id = %q", id)
	}

	// The session file exists with the prompt in it, and the session is imported into projects.json.
	files, _ := filepath.Glob(filepath.Join(st.cfg.ClaudeProjects, "*", id+".jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files = %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	if !strings.Contains(string(raw), "say first prompt") {
		t.Fatalf("file = %q", raw)
	}
	if got := importedIn(t, st, watched); len(got) != 1 || got[0] != id {
		t.Fatalf("imported = %v, want [%s]", got, id)
	}

	// The turn finishes and the state is reported with the session's ID, as for any session.
	s.waitState(t, id, "ready")
	if _, err := b.locate(id); err != nil {
		t.Fatalf("the session cannot be located: %v", err)
	}
	// The agent reports the states again after the answer, which is what the core accepts them from.
	s.mu.Lock()
	s.msgs = nil
	s.mu.Unlock()
	b.hooks().Started(id)
	s.waitState(t, id, "ready")
	s.mu.Lock()
	defer s.mu.Unlock()
	st0, ok := s.msgs[0].(sync.RuntimeState)
	if !ok || st0.Capabilities.Models == nil || !st0.Capabilities.Fast {
		t.Fatalf("report = %#v", s.msgs[0])
	}
}

func TestStartRefusesWhatTheAgentCannotDo(t *testing.T) {
	b, _, st, watched, start := newStartEnv(t)
	other := t.TempDir()
	for name, req := range map[string]sync.RuntimeStart{
		"project not watched": {ProjectPath: other, Provider: "claude", Text: "say x"},
		"other provider":      {ProjectPath: watched, Provider: "codex", Text: "say x"},
		"empty prompt":        {ProjectPath: watched, Provider: "claude", Text: "  "},
		"undeclared model": {
			ProjectPath: watched, Provider: "claude", Text: "say x",
			Options: sync.RuntimeStartOptions{Model: "gpt-9"},
		},
	} {
		if id, err := start(req); err == nil || id != "" {
			t.Fatalf("%s: id %q, err %v", name, id, err)
		}
	}
	if got := importedIn(t, st, watched); len(got) != 0 {
		t.Fatalf("imported = %v", got)
	}
	if got := b.mgr.States(); len(got) != 0 {
		t.Fatalf("states = %+v", got)
	}
}

func TestStartFailsWhenClaudeCodeExitsBeforeCreatingASession(t *testing.T) {
	b, _, st, watched, start := newStartEnv(t)
	t.Setenv("FAKE_CLAUDE_EXIT_ON_START", "1")
	id, err := start(sync.RuntimeStart{ProjectPath: watched, Provider: "claude", Text: "say x"})
	if err == nil || id != "" {
		t.Fatalf("id %q, err %v", id, err)
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Fatalf("err = %v", err)
	}
	if got := importedIn(t, st, watched); len(got) != 0 {
		t.Fatalf("imported = %v", got)
	}
	if got := b.mgr.States(); len(got) != 0 {
		t.Fatalf("a failed start is still reported: %+v", got)
	}
}

func TestStartFailsWhenTheFirstTurnCrashesWithoutAFile(t *testing.T) {
	_, _, st, watched, start := newStartEnv(t)
	_, err := start(sync.RuntimeStart{ProjectPath: watched, Provider: "claude", Text: "crash now"})
	if err == nil {
		t.Fatal("a crashed first turn was accepted")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if got := importedIn(t, st, watched); len(got) != 0 {
		t.Fatalf("imported = %v", got)
	}
}

func TestProvidersAnnounceTheDriverCapabilities(t *testing.T) {
	b, _, _, _, _ := newStartEnv(t)
	got := b.providers()
	if len(got) != 1 || got[0].Provider != "claude" {
		t.Fatalf("providers = %+v", got)
	}
	c := got[0].Capabilities
	if !c.Permissions || !c.Interrupt || !c.Thinking || !c.Fast || strings.Join(c.Models, ",") != "fable,opus,sonnet" {
		t.Fatalf("capabilities = %+v", c)
	}
	if got[0].Version != "2.1.289" || got[0].Tested == nil || !*got[0].Tested {
		t.Fatalf("version = %q tested = %v", got[0].Version, got[0].Tested)
	}
}
