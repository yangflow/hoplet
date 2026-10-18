package daemon

import (
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"
)

func TestBridgeReportsEveryCompletedTurn(t *testing.T) {
	bin := runtimetest.Build(t)
	projects := t.TempDir()
	t.Setenv("FAKE_CLAUDE_PROJECTS", projects)
	runtimetest.Session(t, projects, t.TempDir(), "s1")
	cfg := &config.Config{
		ClaudeBin: bin, ClaudeProjects: projects, HopletHome: t.TempDir(),
		RuntimeIdleMinutes: 30, RuntimeMaxIdleProcesses: 3,
	}
	s := &sink{}
	done := make(chan string, 4)
	b := newBridge(cfg, s.send, func() {}, func(id string) { done <- id }, nil)
	t.Cleanup(b.close)

	h := b.hooks()
	for _, prompt := range []string{"say one", "fail now"} {
		if err := h.Prompt("s1", prompt, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case id := <-done:
			if id != "s1" {
				t.Fatalf("turn of %q", id)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no turn reported for %q", prompt)
		}
		// Let the state settle before the next prompt: a failed turn leaves the session `failed`, which
		// accepts the next one.
		time.Sleep(100 * time.Millisecond)
	}
}
