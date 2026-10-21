package daemon

import (
	"fmt"
	gosync "sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// A rule decision made while the core is away is sent after the reconnect, once, and the queue is bounded.
func TestBridgeHoldsRuleDecisionsWhileTheCoreIsAway(t *testing.T) {
	cfg := &config.Config{HopletHome: t.TempDir(), RuntimeIdleMinutes: 30, RuntimeMaxIdleProcesses: 3}
	var mu gosync.Mutex
	connected := false
	var sent []any
	send := func(m any) error {
		mu.Lock()
		defer mu.Unlock()
		if !connected {
			return sync.ErrNotConnected
		}
		sent = append(sent, m)
		return nil
	}
	b := newBridge(cfg, send, func() {}, func(string) {}, nil)
	t.Cleanup(b.close)
	for i := 0; i < maxHeldDecisions+5; i++ {
		b.emit(runtime.RuleDecisionEvent{SessionID: "s1", RuleDecided: runtime.RuleDecided{
			ToolUseID: fmt.Sprintf("toolu_%d", i), Decision: "deny", RuleID: "default.secrets", Scope: "global", Revision: 3}})
	}
	wait := func(what string, n func() int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for n() != maxHeldDecisions {
			if time.Now().After(deadline) {
				t.Fatalf("%s = %d, want %d", what, n(), maxHeldDecisions)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait("held", func() int {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.held)
	})
	mu.Lock()
	connected = true
	mu.Unlock()
	b.report()
	wait("sent", func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(sent)
	})
	mu.Lock()
	defer mu.Unlock()
	m, ok := sent[0].(sync.RuntimePermissionAuto)
	if !ok || m.Type != sync.TypeRuntimePermissionAuto || m.SessionID != "s1" || m.Decision != "deny" ||
		m.RuleID != "default.secrets" || m.Scope != "global" || m.Revision != 3 || m.ToolUseID != "toolu_5" {
		t.Fatalf("message = %+v (the oldest five are dropped)", sent[0])
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.held) != 0 {
		t.Fatalf("held after the report: %d", len(b.held))
	}
}
