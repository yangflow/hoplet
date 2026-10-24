package daemon

import (
	gosync "sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// The events the core must not miss go through the journal (ADR 0039); output and the report after a reconnect go
// straight to the connection, and the report ends with runtime.reported.
func TestBridgeJournalsWhatTheCoreMustNotMiss(t *testing.T) {
	cfg := &config.Config{HopletHome: t.TempDir(), RuntimeIdleMinutes: 30, RuntimeMaxIdleProcesses: 3}
	var mu gosync.Mutex
	var sent, journalled []any
	record := func(into *[]any) func(any) error {
		return func(m any) error {
			mu.Lock()
			defer mu.Unlock()
			*into = append(*into, m)
			return nil
		}
	}
	b := newBridge(cfg, record(&sent), func() {}, func(string) {}, nil)
	t.Cleanup(b.close)
	b.setJournal(record(&journalled))
	b.emit(runtime.RuleDecisionEvent{SessionID: "s1", RuleDecided: runtime.RuleDecided{
		ToolUseID: "toolu_1", Decision: "deny", RuleID: "default.secrets", Scope: "global", Revision: 3}})
	b.emit(runtime.StateEvent{SessionID: "s1", State: runtime.StateRunning})
	b.emit(runtime.PermissionClosedEvent{SessionID: "s1", RequestID: "r1", Reason: "timeout"})
	b.emit(runtime.OutputEvent{SessionID: "s1", Output: runtime.Output{Kind: runtime.OutputText, Text: "hi"}})
	b.reportAll(true)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := len(journalled) == 3 && len(sent) >= 2
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("journalled=%v sent=%v", journalled, sent)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if m, ok := journalled[0].(sync.RuntimePermissionAuto); !ok || m.ToolUseID != "toolu_1" || m.RuleID != "default.secrets" {
		t.Fatalf("journalled[0] = %+v", journalled[0])
	}
	if _, ok := journalled[1].(sync.RuntimeState); !ok {
		t.Fatalf("journalled[1] = %+v", journalled[1])
	}
	if m, ok := journalled[2].(sync.RuntimePermissionClosed); !ok || m.RequestID != "r1" || m.Reason != "timeout" {
		t.Fatalf("journalled[2] = %+v", journalled[2])
	}
	if _, ok := sent[0].(sync.RuntimeOutput); !ok {
		t.Fatalf("sent[0] = %+v", sent[0])
	}
	if m, ok := sent[len(sent)-1].(sync.RuntimeReported); !ok || m.Type != sync.TypeRuntimeReported {
		t.Fatalf("the report does not end with runtime.reported: %+v", sent)
	}
}
