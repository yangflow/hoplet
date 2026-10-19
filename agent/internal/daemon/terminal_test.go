package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/terminal"
)

func terminalRig(t *testing.T) (*terminals, *bridge) {
	t.Helper()
	bin := runtimetest.Build(t)
	projects := t.TempDir()
	t.Setenv("FAKE_CLAUDE_PROJECTS", projects)
	runtimetest.Session(t, projects, t.TempDir(), "s1")
	cfg := &config.Config{
		ClaudeBin: bin,
		CodexBin:  "/nonexistent/codex", ClaudeProjects: projects, HopletHome: t.TempDir(), Terminal: true,
		RuntimeIdleMinutes: 30, RuntimeMaxIdleProcesses: 3,
	}
	var terms *terminals
	b := newBridge(cfg, (&sink{}).send, func() {}, func(string) {}, func(id string) { terms.turnStarted(id) })
	t.Cleanup(b.close)
	moves := &handoffs{cfg: cfg, bridge: b, held: map[string]bool{}, active: map[string]bool{}}
	terms = newTerminals(cfg, "dev-1", b, moves, locator(cfg), func() *sync.Client { return nil })
	moves.onPrepare = terms.handoffStarted
	t.Cleanup(func() { terms.mgr.CloseAll(terminal.ReasonOwnerOffline) })
	return terms, b
}

func reasonOf(err error) string {
	var oe *terminal.OpenError
	if errors.As(err, &oe) {
		return oe.Reason
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func waitActive(t *testing.T, terms *terminals, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for terms.mgr.Active() != n {
		if time.Now().After(deadline) {
			t.Fatalf("active terminals = %d, want %d", terms.mgr.Active(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestATerminalOpensOnAnIdleSessionAndClosesWhenATurnStarts(t *testing.T) {
	terms, b := terminalRig(t)
	if err := terms.open(sync.TerminalOpen{TerminalID: "t1", SessionID: "s1", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	waitActive(t, terms, 1)
	if err := b.hooks().Prompt("s1", "say one", nil); err != nil {
		t.Fatal(err)
	}
	waitActive(t, terms, 0)
}

func TestATerminalIsRefusedForAnUnknownSessionAndForAHandoff(t *testing.T) {
	terms, _ := terminalRig(t)
	if got := reasonOf(terms.open(sync.TerminalOpen{TerminalID: "t1", SessionID: "nope", Cols: 80, Rows: 24})); got != terminal.ReasonNotADirectory {
		t.Fatalf("unknown session: %q", got)
	}
	terms.moves.mu.Lock()
	terms.moves.active["s1"] = true
	terms.moves.mu.Unlock()
	if got := reasonOf(terms.open(sync.TerminalOpen{TerminalID: "t2", SessionID: "s1", Cols: 80, Rows: 24})); got != terminal.ReasonHandoffRunning {
		t.Fatalf("handoff: %q", got)
	}
	if terms.mgr.Active() != 0 {
		t.Fatal("a refused open left a terminal")
	}
}

func TestAHandoffAndADeletionCloseTheTerminal(t *testing.T) {
	terms, _ := terminalRig(t)
	open := func(id string) {
		t.Helper()
		if err := terms.open(sync.TerminalOpen{TerminalID: id, SessionID: "s1", Cols: 80, Rows: 24}); err != nil {
			t.Fatal(err)
		}
		waitActive(t, terms, 1)
	}
	open("t1")
	terms.handoffStarted("s1")
	waitActive(t, terms, 0)

	open("t2")
	closedFirst := false
	terms.wrapDelete(func(_ context.Context, _ string, _, _, _ bool) []sync.DeletionStep {
		closedFirst = terms.mgr.Active() == 0
		return nil
	})(context.Background(), "s1", true, true, false)
	if !closedFirst {
		t.Fatal("the deletion ran with the terminal still open")
	}
}
